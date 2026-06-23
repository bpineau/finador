package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/portfolio"
	"finador/internal/store"
)

type txPageData struct {
	Today    domain.Date
	Entries  []ledgerEntry
	Accounts []*domain.Account
	Assets   []*domain.Asset
	Kinds    []string
	Error    string
	Flash    string
}

// ledgerEntry is one row in the full audit log table.
type ledgerEntry struct {
	Seq     int
	Ts      string      // formatted save timestamp
	Kind    string      // display kind (buy/sell/account/asset/…)
	Qty     string      // quantity (buy/sell only)
	Amount  string      // monetary amount (all financial tx)
	Desc    string      // one-line description
	Diff    string      // field-level diff for edited entries; shown as hover tooltip
	TxID    domain.TxID // non-empty for tx/tx-edit entries that still exist
	CanEdit bool
}

func (s *Server) txPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.renderTxPage(w, http.StatusOK, r.URL.Query().Get("flash"), "")
}

// renderTxPage is called with lock (R or W) already held.
func (s *Server) renderTxPage(w http.ResponseWriter, status int, flash, errMsg string) {
	b := s.file.Book
	data := txPageData{
		Today:    domain.Today(),
		Entries:  buildLedgerEntries(b, s.file.LogEntries()),
		Accounts: b.Accounts,
		Assets:   b.Assets,
		Kinds:    []string{"buy", "sell", "deposit", "withdraw", "dividend", "fee", "statement"},
		Error:    errMsg,
		Flash:    flash,
	}
	s.render(w, status, "tx.html", data)
}

// buildLedgerEntries converts raw log entries into display rows, newest first.
func buildLedgerEntries(b *domain.Book, raw []store.LogEntry) []ledgerEntry {
	txExists := map[domain.TxID]bool{}
	for _, t := range b.Transactions {
		txExists[t.ID] = true
	}
	prevTx := map[domain.TxID]*domain.Transaction{}
	prevAsset := map[domain.AssetID]*domain.Asset{}
	prevAcct := map[domain.AccountID]*domain.Account{}

	out := make([]ledgerEntry, 0, len(raw))
	for _, e := range raw {
		row := ledgerEntry{Seq: e.Seq, Ts: e.Ts.Format("2006-01-02 15:04")}
		switch e.Kind {
		case "acct":
			var a domain.Account
			if json.Unmarshal(e.Data, &a) == nil {
				if prev, ok := prevAcct[a.ID]; ok {
					row.Diff = diffAccount(prev, &a)
				}
				prevAcct[a.ID] = &a
				row.Kind = "account"
				row.Desc = a.Name + " (" + a.Tax.String() + ", " + string(a.Currency) + ")"
				if len(a.Aliases) > 0 {
					row.Desc += " | " + strings.Join(a.Aliases, ", ")
				}
			}
		case "acct-del":
			row.Kind = "account rm"
			var ref struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(e.Data, &ref) == nil {
				delete(prevAcct, domain.AccountID(ref.ID))
				row.Desc = "[" + ref.ID + "]"
			}
		case "asset":
			var a domain.Asset
			if json.Unmarshal(e.Data, &a) == nil {
				if prev, ok := prevAsset[a.ID]; ok {
					row.Diff = diffAsset(prev, &a)
				}
				prevAsset[a.ID] = &a
				row.Kind = "asset"
				row.Desc = a.Name
				if a.Ticker != "" && a.Ticker != a.Name {
					row.Desc += " (" + a.Ticker + ")"
				}
				if a.Group != "" {
					row.Desc += " | " + a.Group
				}
			}
		case "asset-del":
			row.Kind = "asset rm"
			var ref struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(e.Data, &ref) == nil {
				delete(prevAsset, domain.AssetID(ref.ID))
				row.Desc = "[" + ref.ID + "]"
			}
		case "config":
			var kv struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if json.Unmarshal(e.Data, &kv) == nil {
				row.Kind = "config"
				row.Desc = kv.Key + " = " + kv.Value
			}
		case "tx", "tx-edit":
			var t domain.Transaction
			if json.Unmarshal(e.Data, &t) == nil {
				row.Kind = t.Kind.String()
				if e.Kind == "tx-edit" {
					row.Kind += " (edit)"
					if prev, ok := prevTx[t.ID]; ok {
						row.Diff = diffTransaction(b, prev, &t)
					}
				}
				prevTx[t.ID] = &t
				if !t.Quantity.IsZero() {
					row.Qty = t.Quantity.String()
				}
				if !t.Amount.Amount.IsZero() {
					row.Amount = t.Amount.String()
				}
				row.Desc = ledgerTxDesc(b, &t)
				if txExists[t.ID] {
					row.TxID = t.ID
					row.CanEdit = true
				}
			}
		case "tx-del":
			row.Kind = "tx rm"
			var ref struct {
				ID domain.TxID `json:"id"`
			}
			if json.Unmarshal(e.Data, &ref) == nil {
				delete(prevTx, ref.ID)
				row.Desc = "[" + string(ref.ID) + "]"
			}
		case "label":
			var l domain.Label
			if json.Unmarshal(e.Data, &l) == nil {
				row.Kind = "label"
				acc := accountName(b, l.Account)
				ast := string(l.Asset)
				if a, err := b.Asset(string(l.Asset)); err == nil {
					ast = a.Name
				}
				row.Desc = l.Name + " | " + acc + " / " + ast
			}
		case "label-del":
			row.Kind = "label rm"
			var ref struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(e.Data, &ref) == nil {
				row.Desc = "[" + ref.ID + "]"
			}
		default:
			row.Kind = e.Kind
		}
		out = append(out, row)
	}
	slices.Reverse(out)
	return out
}

// ledgerTxDesc builds the description for a financial transaction (qty/amount are separate columns).
func ledgerTxDesc(b *domain.Book, t *domain.Transaction) string {
	var parts []string
	if t.Asset != "" {
		name := string(t.Asset)
		if a, err := b.Asset(string(t.Asset)); err == nil {
			name = a.Name
		}
		parts = append(parts, name)
	}
	parts = append(parts, t.Date.String())
	parts = append(parts, accountName(b, t.Account))
	if t.Note != "" {
		parts = append(parts, "("+t.Note+")")
	}
	return strings.Join(parts, " | ")
}

func diffTransaction(b *domain.Book, prev, next *domain.Transaction) string {
	var lines []string
	if prev.Date != next.Date {
		lines = append(lines, "date: "+prev.Date.String()+" -> "+next.Date.String())
	}
	if prev.Kind != next.Kind {
		lines = append(lines, "kind: "+prev.Kind.String()+" -> "+next.Kind.String())
	}
	if prev.Account != next.Account {
		lines = append(lines, "account: "+accountName(b, prev.Account)+" -> "+accountName(b, next.Account))
	}
	if prev.Asset != next.Asset {
		pa, na := string(prev.Asset), string(next.Asset)
		if a, err := b.Asset(string(prev.Asset)); err == nil {
			pa = a.Name
		}
		if a, err := b.Asset(string(next.Asset)); err == nil {
			na = a.Name
		}
		lines = append(lines, "asset: "+pa+" -> "+na)
	}
	if !prev.Quantity.Equal(next.Quantity) {
		lines = append(lines, "qty: "+prev.Quantity.String()+" -> "+next.Quantity.String())
	}
	if prev.Amount.String() != next.Amount.String() {
		lines = append(lines, "amount: "+prev.Amount.String()+" -> "+next.Amount.String())
	}
	if prev.Note != next.Note {
		lines = append(lines, "note: \""+prev.Note+"\" -> \""+next.Note+"\"")
	}
	return strings.Join(lines, "\n")
}

func diffAsset(prev, next *domain.Asset) string {
	var lines []string
	if prev.Name != next.Name {
		lines = append(lines, "name: "+prev.Name+" -> "+next.Name)
	}
	if prev.Ticker != next.Ticker {
		lines = append(lines, "ticker: "+prev.Ticker+" -> "+next.Ticker)
	}
	if prev.ISIN != next.ISIN {
		lines = append(lines, "isin: "+prev.ISIN+" -> "+next.ISIN)
	}
	if prev.Group != next.Group {
		lines = append(lines, "group: \""+prev.Group+"\" -> \""+next.Group+"\"")
	}
	if prev.Currency != next.Currency {
		lines = append(lines, "ccy: "+string(prev.Currency)+" -> "+string(next.Currency))
	}
	if strings.Join(prev.Aliases, ",") != strings.Join(next.Aliases, ",") {
		lines = append(lines, "aliases: ["+strings.Join(prev.Aliases, ", ")+"] -> ["+strings.Join(next.Aliases, ", ")+"]")
	}
	if prev.Withholding != next.Withholding {
		lines = append(lines, fmt.Sprintf("withholding: %.4g%% -> %.4g%%", prev.Withholding*100, next.Withholding*100))
	}
	return strings.Join(lines, "\n")
}

func diffAccount(prev, next *domain.Account) string {
	var lines []string
	if prev.Name != next.Name {
		lines = append(lines, "name: "+prev.Name+" -> "+next.Name)
	}
	if prev.Tax.String() != next.Tax.String() {
		lines = append(lines, "tax: "+prev.Tax.String()+" -> "+next.Tax.String())
	}
	if prev.Currency != next.Currency {
		lines = append(lines, "ccy: "+string(prev.Currency)+" -> "+string(next.Currency))
	}
	if strings.Join(prev.Aliases, ",") != strings.Join(next.Aliases, ",") {
		lines = append(lines, "aliases: ["+strings.Join(prev.Aliases, ", ")+"] -> ["+strings.Join(next.Aliases, ", ")+"]")
	}
	return strings.Join(lines, "\n")
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
	if err := s.persist(r.Context(), "web: new tx"); err != nil {
		s.renderTxPage(w, http.StatusInternalServerError, "", "could not save: "+err.Error())
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

	accRef := r.FormValue("account")
	if accRef == "" {
		return zero, fmt.Errorf("account required")
	}
	acc, err := portfolio.ResolveAccount(b, accRef)
	if err != nil {
		return zero, err
	}
	accCcy := acc.Currency

	assetRef := r.FormValue("asset")
	ccy := accCcy
	var asset *domain.Asset
	if assetRef != "" {
		asset, err = b.Asset(assetRef)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return zero, err
		}
		if asset != nil {
			ccy = asset.Currency
		} // an asset to be created will be priced in the account's currency
	}
	if (kind == domain.Buy || kind == domain.Sell || kind == domain.Dividend) && assetRef == "" {
		return zero, fmt.Errorf("a %s requires an asset", kind)
	}

	tx := domain.Transaction{Date: date, Kind: kind, Note: r.FormValue("note")}
	if q := r.FormValue("qty"); q != "" {
		qty, err := decimal.NewFromString(q)
		if err != nil {
			return zero, fmt.Errorf("invalid quantity %q", q)
		}
		tx.Quantity = qty.Abs()
	}
	if (kind == domain.Buy || kind == domain.Sell) && tx.Quantity.IsZero() {
		return zero, fmt.Errorf("quantity required for a %s", kind)
	}
	amount, err := decimal.NewFromString(r.FormValue("amount"))
	if err != nil {
		return zero, fmt.Errorf("invalid amount %q", r.FormValue("amount"))
	}
	if c := r.FormValue("ccy"); c != "" {
		if ccy, err = domain.ParseCurrency(c); err != nil {
			return zero, err
		}
	}

	// Assets are created on the fly; accounts must already be declared.
	if assetRef != "" && asset == nil {
		if asset, err = portfolio.EnsureAsset(b, assetRef, accCcy, ""); err != nil {
			return zero, err
		}
	}
	tx.Account = acc.ID
	if asset != nil {
		tx.Asset = asset.ID
	}
	tx.Amount = domain.Money{Amount: amount.Abs(), Currency: ccy}
	return tx, nil
}

type txEditData struct {
	Today       domain.Date
	Tx          *domain.Transaction
	AccountName string
	AssetName   string
	Accounts    []*domain.Account
	Assets      []*domain.Asset
	Kinds       []string
	Error       string
}

func (s *Server) findTx(w http.ResponseWriter, r *http.Request) (*domain.Transaction, bool) {
	tx, err := s.file.Book.ResolveTx(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "transaction not found")
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

// renderTxEdit is called with lock already held.
func (s *Server) renderTxEdit(w http.ResponseWriter, status int, tx *domain.Transaction, errMsg string) {
	b := s.file.Book
	data := txEditData{
		Today:    domain.Today(),
		Tx:       tx,
		Accounts: b.Accounts,
		Assets:   b.Assets,
		Kinds:    []string{"buy", "sell", "deposit", "withdraw", "dividend", "fee", "statement"},
		Error:    errMsg,
	}
	// Pre-fill resolved names for datalist inputs.
	if acc, err := b.Account(string(tx.Account)); err == nil {
		data.AccountName = acc.Name
	}
	if tx.Asset != "" {
		if asset, err := b.Asset(string(tx.Asset)); err == nil {
			data.AssetName = asset.Name
		}
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
	// keep identity and import fingerprint (an edited line must not reappear on re-import)
	parsed.ID, parsed.ImportHash = tx.ID, tx.ImportHash
	*tx = parsed
	if err := s.persist(r.Context(), "web: edit tx"); err != nil {
		s.renderTxEdit(w, http.StatusInternalServerError, tx, "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}

// assetRename changes an asset's display name globally. It renames by the
// asset's stable ID, so every transaction, ranking and chart that references it
// just shows the new label - nothing is reclassified and no second asset is
// created. (Retyping the name in a transaction's "asset" field does the
// opposite: it reassigns that one entry, creating a new asset on the fly.)
func (s *Server) assetRename(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.file.Book
	asset, err := b.Asset(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "asset not found")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.renderError(w, http.StatusBadRequest, "a name is required")
		return
	}
	old := asset.Name
	asset.Name = name
	if err := b.CheckAssetRefs(asset); err != nil {
		asset.Name = old
		s.renderError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.file.Save(); err != nil {
		asset.Name = old
		s.renderError(w, http.StatusInternalServerError, "could not save: "+err.Error())
		return
	}
	if err := s.syncSaved(r.Context(), "web: rename asset"); err != nil {
		s.renderError(w, http.StatusInternalServerError, "saved locally, but could not sync to the remote: "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx?flash="+url.QueryEscape("renamed to "+name), http.StatusSeeOther)
}

func (s *Server) txDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.file.Book.ResolveTx(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "transaction not found")
		return
	}
	if err := s.file.Book.RemoveTx(tx.ID); err != nil {
		s.renderError(w, http.StatusNotFound, "transaction not found")
		return
	}
	if err := s.persist(r.Context(), "web: delete tx"); err != nil {
		s.renderError(w, http.StatusInternalServerError, "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}
