package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

type txPageData struct {
	Aujourdhui domain.Date
	Txs        []txRow
	Accounts   []*domain.Account
	Assets     []*domain.Asset
	Kinds      []string
	Erreur     string
	Flash      string
}

func (s *Server) txPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.renderTxPage(w, http.StatusOK, r.URL.Query().Get("flash"), "")
}

// renderTxPage est appelé verrou (R ou W) déjà pris.
func (s *Server) renderTxPage(w http.ResponseWriter, status int, flash, erreur string) {
	b := s.file.Book
	all, _ := portfolio.ParseScope(b, "")
	data := txPageData{
		Aujourdhui: domain.Today(),
		Txs:        scopeTxs(b, all, 200),
		Accounts:   b.Accounts,
		Assets:     b.Assets,
		Kinds:      []string{"buy", "sell", "deposit", "withdraw", "dividend", "fee", "statement"},
		Erreur:     erreur,
		Flash:      flash,
	}
	s.render(w, status, "tx.html", data)
}

func (s *Server) txCreate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.file.Book
	tx, err := parseTxForm(b, r)
	if err != nil {
		s.renderTxPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	b.Add(tx)
	if err := s.file.Save(); err != nil {
		s.renderTxPage(w, http.StatusInternalServerError, "", "sauvegarde impossible : "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}

func parseTxForm(b *domain.Book, r *http.Request) (domain.Transaction, error) {
	var zero domain.Transaction
	date, err := domain.ParseDate(r.FormValue("date"))
	if err != nil {
		return zero, err
	}
	kind, err := domain.ParseTxKind(r.FormValue("kind"))
	if err != nil {
		return zero, err
	}
	acc, err := b.Account(r.FormValue("account"))
	if err != nil {
		return zero, err
	}
	tx := domain.Transaction{Date: date, Account: acc.ID, Kind: kind, Note: r.FormValue("note")}

	ccy := acc.Currency
	if ref := r.FormValue("asset"); ref != "" {
		asset, err := b.Asset(ref)
		if err != nil {
			return zero, err
		}
		tx.Asset = asset.ID
		ccy = asset.Currency
	}
	if (kind == domain.Buy || kind == domain.Sell || kind == domain.Dividend) && tx.Asset == "" {
		return zero, fmt.Errorf("un %s demande un actif", kind)
	}
	if q := r.FormValue("qty"); q != "" {
		qty, err := decimal.NewFromString(q)
		if err != nil {
			return zero, fmt.Errorf("quantité %q invalide", q)
		}
		tx.Quantity = qty.Abs()
	}
	if (kind == domain.Buy || kind == domain.Sell) && tx.Quantity.IsZero() {
		return zero, fmt.Errorf("quantité requise pour un %s", kind)
	}
	amount, err := decimal.NewFromString(r.FormValue("amount"))
	if err != nil {
		return zero, fmt.Errorf("montant %q invalide", r.FormValue("amount"))
	}
	if c := r.FormValue("ccy"); c != "" {
		if ccy, err = domain.ParseCurrency(c); err != nil {
			return zero, err
		}
	}
	tx.Amount = domain.Money{Amount: amount.Abs(), Currency: ccy}
	return tx, nil
}

type txEditData struct {
	Aujourdhui domain.Date
	Tx         *domain.Transaction
	Accounts   []*domain.Account
	Assets     []*domain.Asset
	Kinds      []string
	Erreur     string
}

func (s *Server) findTx(w http.ResponseWriter, r *http.Request) (*domain.Transaction, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "identifiant invalide")
		return nil, false
	}
	tx, err := s.file.Book.Tx(domain.TxID(id))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "transaction introuvable")
		return nil, false
	}
	return tx, true
}

func (s *Server) txEditPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, ok := s.findTx(w, r)
	if !ok {
		return
	}
	s.renderTxEdit(w, http.StatusOK, tx, "")
}

// renderTxEdit est appelé verrou déjà pris.
func (s *Server) renderTxEdit(w http.ResponseWriter, status int, tx *domain.Transaction, erreur string) {
	b := s.file.Book
	data := txEditData{
		Aujourdhui: domain.Today(),
		Tx:         tx,
		Accounts:   b.Accounts,
		Assets:     b.Assets,
		Kinds:      []string{"buy", "sell", "deposit", "withdraw", "dividend", "fee", "statement"},
		Erreur:     erreur,
	}
	s.render(w, status, "tx-edit.html", data)
}

func (s *Server) txEditSubmit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.findTx(w, r)
	if !ok {
		return
	}
	parsed, err := parseTxForm(s.file.Book, r)
	if err != nil {
		s.renderTxEdit(w, http.StatusBadRequest, tx, err.Error())
		return
	}
	// conserver l'identité et l'empreinte d'import (une ligne éditée ne doit
	// pas réapparaître au prochain ré-import)
	parsed.ID, parsed.ImportHash = tx.ID, tx.ImportHash
	*tx = parsed
	if err := s.file.Save(); err != nil {
		s.renderTxEdit(w, http.StatusInternalServerError, tx, "sauvegarde impossible : "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}

func (s *Server) txDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "identifiant invalide")
		return
	}
	if err := s.file.Book.RemoveTx(domain.TxID(id)); err != nil {
		s.renderError(w, http.StatusNotFound, "transaction introuvable")
		return
	}
	if err := s.file.Save(); err != nil {
		s.renderError(w, http.StatusInternalServerError, "sauvegarde impossible : "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}
