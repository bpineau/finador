package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

type accountPageData struct {
	Today    domain.Date
	Accounts []*domain.Account
	Error    string
	Flash    string
}

func (s *Server) accountsPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.renderAccountsPage(w, http.StatusOK, r.URL.Query().Get("flash"), "")
}

// renderAccountsPage is called with the lock (R or W) already held.
func (s *Server) renderAccountsPage(w http.ResponseWriter, status int, flash, errMsg string) {
	s.render(w, status, "accounts.html", accountPageData{
		Today:    domain.Today(),
		Accounts: s.file.Book.Accounts,
		Error:    errMsg,
		Flash:    flash,
	})
}

func (s *Server) accountCreate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := parseAccountForm(r)
	if err != nil {
		s.renderAccountsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	acc := &domain.Account{
		ID:       domain.AccountID(domain.NewID()),
		Name:     f.name,
		Currency: f.ccy,
		Tax:      f.tax,
		Aliases:  f.aliases,
	}
	if err := s.file.Book.AddAccount(acc); err != nil {
		s.renderAccountsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.persist(r.Context(), "web: new account "+acc.Name); err != nil {
		s.renderAccountsPage(w, http.StatusInternalServerError, "", "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/accounts?flash="+url.QueryEscape("created "+acc.Name), http.StatusSeeOther)
}

type accountEditData struct {
	Today      domain.Date
	Account    *domain.Account
	TaxMode    string // "none" | "gains" | "value" - drives the <select>
	TaxRatePct string // rate as a percentage string, e.g. "17.2"
	AliasesCSV string
	Error      string
}

func (s *Server) findAccount(w http.ResponseWriter, r *http.Request) (*domain.Account, bool) {
	acc, err := s.file.Book.Account(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "account not found")
		return nil, false
	}
	return acc, true
}

func (s *Server) accountEditPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.findAccount(w, r)
	if !ok {
		return
	}
	s.renderAccountEdit(w, http.StatusOK, acc, "")
}

// renderAccountEdit is called with the lock already held.
func (s *Server) renderAccountEdit(w http.ResponseWriter, status int, acc *domain.Account, errMsg string) {
	s.render(w, status, "account-edit.html", accountEditData{
		Today:      domain.Today(),
		Account:    acc,
		TaxMode:    taxModeName(acc.Tax.Mode),
		TaxRatePct: taxRatePct(acc.Tax),
		AliasesCSV: strings.Join(acc.Aliases, ", "),
		Error:      errMsg,
	})
}

func (s *Server) accountEditSubmit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.findAccount(w, r)
	if !ok {
		return
	}
	f, err := parseAccountForm(r)
	if err != nil {
		s.renderAccountEdit(w, http.StatusBadRequest, acc, err.Error())
		return
	}
	// acc is a live pointer in the book: apply the edit, but restore the previous
	// state if validation or save fails so the in-memory book stays consistent.
	prev := *acc
	acc.Name, acc.Currency, acc.Tax, acc.Aliases = f.name, f.ccy, f.tax, f.aliases
	if err := s.file.Book.CheckAccountRefs(acc); err != nil {
		*acc = prev
		s.renderAccountEdit(w, http.StatusBadRequest, acc, err.Error())
		return
	}
	if err := s.file.Save(); err != nil {
		*acc = prev
		s.renderAccountEdit(w, http.StatusInternalServerError, acc, "could not save: "+err.Error())
		return
	}
	if err := s.syncSaved(r.Context(), "web: edit account "+acc.Name); err != nil {
		s.renderAccountEdit(w, http.StatusInternalServerError, acc, "saved locally, but could not sync to the remote: "+err.Error())
		return
	}
	http.Redirect(w, r, "/accounts?flash="+url.QueryEscape("updated "+acc.Name), http.StatusSeeOther)
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// RemoveAccount refuses to orphan transactions; surface that as a page error
	// (it's an expected, recoverable case) rather than a hard error page.
	if err := s.file.Book.RemoveAccount(r.PathValue("id")); err != nil {
		s.renderAccountsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.persist(r.Context(), "web: delete account"); err != nil {
		s.renderAccountsPage(w, http.StatusInternalServerError, "", "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/accounts?flash="+url.QueryEscape("account deleted"), http.StatusSeeOther)
}

// accountForm holds the validated fields shared by create and edit.
type accountForm struct {
	name    string
	ccy     domain.Currency
	tax     domain.TaxRule
	aliases []string
}

func parseAccountForm(r *http.Request) (accountForm, error) {
	var f accountForm
	f.name = strings.TrimSpace(r.FormValue("name"))
	if f.name == "" {
		return f, fmt.Errorf("a name is required")
	}
	ccyStr := strings.TrimSpace(r.FormValue("ccy"))
	if ccyStr == "" {
		ccyStr = "EUR"
	}
	ccy, err := domain.ParseCurrency(ccyStr)
	if err != nil {
		return f, err
	}
	f.ccy = ccy
	if f.tax, err = parseTaxRuleForm(r); err != nil {
		return f, err
	}
	f.aliases = parseAliasList(r.FormValue("aliases"))
	return f, nil
}

// parseTaxRuleForm rebuilds the CLI's "gains:17.2%" syntax from the mode <select>
// and the percentage field, then reuses the canonical domain parser/validation.
func parseTaxRuleForm(r *http.Request) (domain.TaxRule, error) {
	mode := r.FormValue("tax-mode")
	if mode == "" || mode == "none" {
		return domain.TaxRule{}, nil
	}
	// Accept "17.2", "17.2%", "17.2 %": strip spaces and any percent signs, then re-attach one.
	rate := strings.ReplaceAll(strings.ReplaceAll(r.FormValue("tax-rate"), " ", ""), "%", "")
	if rate == "" {
		return domain.TaxRule{}, fmt.Errorf("a tax rate is required when tax is on %s", mode)
	}
	return domain.ParseTaxRule(mode + ":" + rate + "%")
}

func parseAliasList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func taxModeName(m domain.TaxMode) string {
	switch m {
	case domain.TaxOnGains:
		return "gains"
	case domain.TaxOnValue:
		return "value"
	default:
		return "none"
	}
}

func taxRatePct(t domain.TaxRule) string {
	if t.Mode == domain.TaxNone {
		return ""
	}
	return t.Rate.Mul(decimal.NewFromInt(100)).String()
}
