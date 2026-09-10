package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/store"
)

// do issues an arbitrary method against the handler, with no body.
func do(t *testing.T, srv *Server, method, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code
}

// The web UI has no cookies, no sessions and no auth (it binds 127.0.0.1), so
// the only request guard is the method: every mutation is POST-only, so no GET
// - the one request another site can make a browser issue on its own - can
// change the ledger. A GET on a mutating route falls through to the catch-all
// GET route, i.e. the 404 page; any other method on a GET-only route is a 405.
func TestRouteMethodGuards(t *testing.T) {
	srv, f := testServer(t)
	before := len(f.LogEntries())

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/assets/cw8/delete", http.StatusNotFound},
		{http.MethodGet, "/accounts/pea/delete", http.StatusNotFound},
		{http.MethodGet, "/asset/cw8/rename", http.StatusNotFound},
		{http.MethodGet, "/refresh", http.StatusNotFound},
		{http.MethodGet, "/import?x=1", http.StatusOK}, // the form page itself is a GET
		{http.MethodPost, "/assets.csv", http.StatusMethodNotAllowed},
		{http.MethodPost, "/style.css", http.StatusMethodNotAllowed},
		{http.MethodPut, "/assets", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/tx", http.StatusMethodNotAllowed},
		{http.MethodGet, "/nowhere", http.StatusNotFound},
	}
	for _, c := range cases {
		if got := do(t, srv, c.method, c.path); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}
	if got := len(f.LogEntries()); got != before {
		t.Errorf("a refused request wrote to the ledger: %d entries, want %d", got, before)
	}
}

// --- transaction form ------------------------------------------------------

// Every rejection of the transaction form must name what is wrong, answer 400
// and leave the ledger untouched.
func TestTxFormRejections(t *testing.T) {
	valid := func(over url.Values) url.Values {
		f := url.Values{"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"},
			"asset": {"cw8"}, "qty": {"2"}, "amount": {"1115"}}
		for k, v := range over {
			f[k] = v
		}
		return f
	}
	cases := []struct {
		name string
		form url.Values
		want string // substring of the error shown back in the form page
	}{
		{"bad date", valid(url.Values{"date": {"03/06/2026"}}), "date"},
		{"bad kind", valid(url.Values{"kind": {"gift"}}), "kind"},
		{"no account", valid(url.Values{"account": {""}}), "account required"},
		{"unknown account", valid(url.Values{"account": {"cto"}}), "unknown account"},
		{"buy without asset", valid(url.Values{"asset": {""}}), "requires an asset"},
		{"dividend without asset", valid(url.Values{"kind": {"dividend"}, "asset": {""}}), "requires an asset"},
		{"buy without quantity", valid(url.Values{"qty": {""}}), "quantity required"},
		{"quantity not a number", valid(url.Values{"qty": {"ten"}}), "invalid quantity"},
		{"amount not a number", valid(url.Values{"amount": {"1 115"}}), "invalid amount"},
		{"amount missing", valid(url.Values{"amount": {""}}), "invalid amount"},
		{"unknown currency", valid(url.Values{"ccy": {"euros"}}), "currency"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, f := testServer(t)
			before := len(f.Book.Transactions)
			code, body, _ := postForm(t, srv, "/tx", c.form)
			if code != http.StatusBadRequest {
				t.Fatalf("POST /tx = %d, want 400\n%s", code, excerpt(body))
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("error page does not mention %q:\n%s", c.want, excerpt(body))
			}
			if len(f.Book.Transactions) != before {
				t.Errorf("a rejected form added a transaction")
			}
		})
	}
}

// The forms the CLI accepts must go through here too: a cash statement carries
// no asset, a fee carries no quantity, and an explicit currency overrides the
// account's own.
func TestTxFormAccepts(t *testing.T) {
	cases := []struct {
		name  string
		form  url.Values
		check func(*testing.T, *domain.Transaction)
	}{
		{"cash statement", url.Values{"date": {"2026-06-03"}, "kind": {"statement"},
			"account": {"pea"}, "amount": {"5100"}},
			func(t *testing.T, tx *domain.Transaction) {
				if tx.Asset != "" || tx.Kind != domain.Statement {
					t.Errorf("tx = %+v, want an asset-less statement", tx)
				}
			}},
		{"fee", url.Values{"date": {"2026-06-03"}, "kind": {"fee"},
			"account": {"pea"}, "amount": {"12"}},
			func(t *testing.T, tx *domain.Transaction) {
				if !tx.Quantity.IsZero() || tx.Amount.Amount.String() != "12" {
					t.Errorf("tx = %+v, want a quantity-less 12 fee", tx)
				}
			}},
		{"currency override", url.Values{"date": {"2026-06-03"}, "kind": {"deposit"},
			"account": {"pea"}, "amount": {"1000"}, "ccy": {"USD"}},
			func(t *testing.T, tx *domain.Transaction) {
				if tx.Amount.Currency != domain.USD {
					t.Errorf("currency = %s, want USD", tx.Amount.Currency)
				}
			}},
		{"signs are dropped, the kind carries the direction",
			url.Values{"date": {"2026-06-03"}, "kind": {"sell"}, "account": {"pea"},
				"asset": {"cw8"}, "qty": {"-3"}, "amount": {"-1700"}},
			func(t *testing.T, tx *domain.Transaction) {
				if tx.Quantity.IsNegative() || tx.Amount.Amount.IsNegative() {
					t.Errorf("tx = %+v, want positive quantity and amount", tx)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, f := testServer(t)
			code, body, loc := postForm(t, srv, "/tx", c.form)
			if code != http.StatusSeeOther || loc != "/tx" {
				t.Fatalf("POST /tx = %d → %q\n%s", code, loc, excerpt(body))
			}
			c.check(t, f.Book.Transactions[len(f.Book.Transactions)-1])
		})
	}
}

// A transaction edit keeps the entry's identity and its import fingerprint: an
// edit is not a re-import, or replaying the broker statement would duplicate
// the corrected line.
func TestTxEditKeepsIdentityAndImportHash(t *testing.T) {
	srv, f := testServer(t)
	tx := f.Book.Transactions[1] // the cw8 buy
	tx.ImportHash = "ibkr:deadbeef"
	id := tx.ID

	code, body, _ := postForm(t, srv, "/tx/"+string(id)+"/edit", url.Values{
		"date": {"2026-06-02"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"11"}, "amount": {"6000"}, "note": {"corrected"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("POST edit = %d\n%s", code, excerpt(body))
	}
	got, err := f.Book.Tx(id)
	if err != nil {
		t.Fatalf("edited tx gone: %v", err)
	}
	if got.ImportHash != "ibkr:deadbeef" {
		t.Errorf("importHash = %q, want it carried through the edit", got.ImportHash)
	}
	if got.ID != id {
		t.Errorf("id = %q, want %q", got.ID, id)
	}
}

// A rejected edit must not touch the live transaction, and an unknown id is a
// clean 404 on both the page and the delete route.
func TestTxEditAndDeleteEdgeCases(t *testing.T) {
	srv, f := testServer(t)
	id := string(f.Book.Transactions[1].ID)
	was := f.Book.Transactions[1].Quantity.String()

	code, body, _ := postForm(t, srv, "/tx/"+id+"/edit", url.Values{
		"date": {"2026-06-01"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"12"}, "amount": {"nope"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid amount") {
		t.Fatalf("invalid edit = %d\n%s", code, excerpt(body))
	}
	if f.Book.Transactions[1].Quantity.String() != was {
		t.Errorf("a rejected edit changed the transaction")
	}

	if code, _, _ := postForm(t, srv, "/tx/nope/edit", url.Values{}); code != http.StatusNotFound {
		t.Errorf("edit unknown tx = %d, want 404", code)
	}
	if code, _, _ := postForm(t, srv, "/tx/nope/delete", url.Values{}); code != http.StatusNotFound {
		t.Errorf("delete unknown tx = %d, want 404", code)
	}
}

// Renaming by ID is refused when the new label collides with another entity's
// reference, and 404s on an unknown asset - in both cases the old name stands.
func TestAssetRenameGuards(t *testing.T) {
	srv, f := testServer(t)
	if err := f.Book.AddAsset(&domain.Asset{ID: "gold", Kind: domain.Security,
		Name: "Gold ETC", Ticker: "IGLN.L", Currency: domain.USD}); err != nil {
		t.Fatal(err)
	}
	code, body, _ := postForm(t, srv, "/asset/cw8/rename", url.Values{"name": {"Gold ETC"}})
	if code != http.StatusBadRequest {
		t.Fatalf("colliding rename = %d, want 400\n%s", code, excerpt(body))
	}
	if a, _ := f.Book.Asset("cw8"); a.Name != "Amundi MSCI World" {
		t.Errorf("name = %q, want the original after a refused rename", a.Name)
	}
	if code, _, _ := postForm(t, srv, "/asset/nope/rename", url.Values{"name": {"X"}}); code != http.StatusNotFound {
		t.Errorf("rename unknown asset = %d, want 404", code)
	}
}

// --- account and asset forms ----------------------------------------------

func TestAccountFormRejections(t *testing.T) {
	cases := []struct {
		name, path string
		form       url.Values
		want       string
	}{
		{"no name", "/accounts", url.Values{"name": {"  "}}, "name is required"},
		{"unknown currency", "/accounts", url.Values{"name": {"CTO Meridia"}, "ccy": {"pesos"}}, "currency"},
		{"tax mode without a rate", "/accounts", url.Values{"name": {"CTO Meridia"},
			"tax-mode": {"gains"}, "tax-rate": {" "}}, "tax rate is required"},
		{"tax rate not a number", "/accounts", url.Values{"name": {"CTO Meridia"},
			"tax-mode": {"gains"}, "tax-rate": {"trente"}}, "tax"},
		{"edit: no name", "/accounts/pea/edit", url.Values{"name": {""}}, "name is required"},
		{"edit: unknown currency", "/accounts/pea/edit", url.Values{"name": {"PEA Zephyr"},
			"ccy": {"pesos"}}, "currency"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, f := testServer(t)
			before := len(f.Book.Accounts)
			code, body, _ := postForm(t, srv, c.path, c.form)
			if code != http.StatusBadRequest {
				t.Fatalf("POST %s = %d, want 400\n%s", c.path, code, excerpt(body))
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("error page does not mention %q:\n%s", c.want, excerpt(body))
			}
			if len(f.Book.Accounts) != before {
				t.Error("a rejected form added an account")
			}
		})
	}
}

// A refused edit must restore the live account exactly: the pointer is the one
// the book holds, so a half-applied edit would corrupt the in-memory state.
func TestAccountEditCollisionRollsBack(t *testing.T) {
	srv, f := testServer(t)
	if err := f.Book.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia",
		Currency: domain.EUR, Aliases: []string{"meridia"}}); err != nil {
		t.Fatal(err)
	}
	code, body, _ := postForm(t, srv, "/accounts/pea/edit", url.Values{
		"name": {"CTO Meridia"}, "ccy": {"USD"}, "tax-mode": {"value"}, "tax-rate": {"1"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("colliding edit = %d, want 400\n%s", code, excerpt(body))
	}
	acc, _ := f.Book.Account("pea")
	if acc.Name != "PEA Zephyr" || acc.Currency != domain.EUR || acc.Tax.String() != "gains:17.2%" {
		t.Errorf("account after a refused edit = %+v, want it untouched", acc)
	}
}

func TestAssetFormRejections(t *testing.T) {
	cases := []struct {
		name, path string
		form       url.Values
		want       string
	}{
		{"unknown kind", "/assets", url.Values{"name": {"Maison"}, "kind": {"house"}}, "asset kind"},
		{"unknown currency", "/assets", url.Values{"name": {"Maison"}, "ccy": {"pesos"}}, "currency"},
		{"withholding not a percentage", "/assets", url.Values{"name": {"Maison"},
			"withholding": {"quinze"}}, "withholding"},
		{"edit: no name", "/assets/cw8/edit", url.Values{"name": {""}}, "name is required"},
		{"edit: unknown kind", "/assets/cw8/edit", url.Values{"name": {"W"}, "kind": {"house"}}, "asset kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, f := testServer(t)
			before := len(f.Book.Assets)
			code, body, _ := postForm(t, srv, c.path, c.form)
			if code != http.StatusBadRequest {
				t.Fatalf("POST %s = %d, want 400\n%s", c.path, code, excerpt(body))
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("error page does not mention %q:\n%s", c.want, excerpt(body))
			}
			if len(f.Book.Assets) != before {
				t.Error("a rejected form added an asset")
			}
		})
	}
}

func TestAssetEditCollisionRollsBack(t *testing.T) {
	srv, f := testServer(t)
	if err := f.Book.AddAsset(&domain.Asset{ID: "gold", Kind: domain.Security,
		Name: "Gold ETC", Ticker: "IGLN.L", Currency: domain.USD}); err != nil {
		t.Fatal(err)
	}
	code, body, _ := postForm(t, srv, "/assets/cw8/edit", url.Values{
		"name": {"World Tracker"}, "kind": {"property"}, "ticker": {"IGLN.L"}, "ccy": {"USD"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("colliding edit = %d, want 400\n%s", code, excerpt(body))
	}
	a, _ := f.Book.Asset("cw8")
	if a.Name != "Amundi MSCI World" || a.Ticker != "CW8.PA" ||
		a.Kind != domain.Security || a.Currency != domain.EUR {
		t.Errorf("asset after a refused edit = %+v, want it untouched", a)
	}
}

// The edit form pre-fills the derived display fields: the tax mode and rate of
// an account, the withholding percentage of an asset.
func TestEditFormPrefills(t *testing.T) {
	if got := taxModeName(domain.TaxOnValue); got != "value" {
		t.Errorf("taxModeName(value) = %q", got)
	}
	if got := taxModeName(domain.TaxNone); got != "none" {
		t.Errorf("taxModeName(none) = %q", got)
	}
	if got := taxRatePct(domain.TaxRule{}); got != "" {
		t.Errorf("taxRatePct(none) = %q, want empty", got)
	}
	rule, err := domain.ParseTaxRule("value:1.5%")
	if err != nil {
		t.Fatal(err)
	}
	if got := taxRatePct(rule); got != "1.5" {
		t.Errorf("taxRatePct(value:1.5%%) = %q, want 1.5", got)
	}
	for w, want := range map[float64]string{0: "", -1: "", 0.15: "15", 0.125: "12.5"} {
		if got := withholdPct(w); got != want {
			t.Errorf("withholdPct(%v) = %q, want %q", w, got, want)
		}
	}
}

// --- the ledger audit log --------------------------------------------------

// buildLedgerEntries renders one row per ledger record, newest first, for
// every record kind the store can hold - including the deletions and the
// records only the CLI writes (config, labels), and an unknown future kind.
func TestBuildLedgerEntriesEveryKind(t *testing.T) {
	_, f := testServer(t)
	b := f.Book
	raw := func(kind string, v any) store.LogEntry {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return store.LogEntry{Kind: kind, Data: data, Ts: time.Now()}
	}
	acc, _ := b.Account("pea")
	asset, _ := b.Asset("cw8")
	tx := b.Transactions[1]

	entries := []store.LogEntry{
		raw("acct", acc),
		raw("acct-del", map[string]string{"id": "old-acct"}),
		raw("asset", asset),
		raw("asset-del", map[string]string{"id": "old-asset"}),
		raw("config", map[string]string{"key": "display-currency", "value": "EUR"}),
		raw("tx", tx),
		raw("tx-del", map[string]string{"id": "old-tx"}),
		raw("label", domain.Label{ID: "l1", Account: "pea", Asset: "cw8", Name: "retraite"}),
		raw("label-del", map[string]string{"id": "l1"}),
		raw("mystery", map[string]string{"x": "1"}),
	}
	rows := buildLedgerEntries(b, entries)
	if len(rows) != len(entries) {
		t.Fatalf("rows = %d, want %d", len(rows), len(entries))
	}
	// newest first
	if rows[0].Kind != "mystery" {
		t.Errorf("first row = %q, want the last entry (newest first)", rows[0].Kind)
	}
	want := []struct{ kind, desc string }{
		{"mystery", ""},
		{"label rm", "[l1]"},
		{"label", "retraite | PEA Zephyr / Amundi MSCI World"},
		{"tx rm", "[old-tx]"},
		{"buy", "Amundi MSCI World | 2026-06-01 | PEA Zephyr"},
		{"config", "display-currency = EUR"},
		{"asset rm", "[old-asset]"},
		{"asset", "Amundi MSCI World (CW8.PA) | actions/monde"},
		{"account rm", "[old-acct]"},
		{"account", "PEA Zephyr (gains:17.2%, EUR)"},
	}
	for i, w := range want {
		if rows[i].Kind != w.kind || rows[i].Desc != w.desc {
			t.Errorf("row %d = %q / %q, want %q / %q", i, rows[i].Kind, rows[i].Desc, w.kind, w.desc)
		}
	}
	// the buy carries its quantity and amount in their own columns, and is
	// editable because the transaction still exists in the book
	buy := rows[4]
	if buy.Qty != "10" || buy.Amount == "" || !buy.CanEdit || buy.TxID != tx.ID {
		t.Errorf("buy row = %+v", buy)
	}
}

// An edit row shows a field-level diff against the previous state of the same
// entity - the audit log's whole point. Every mutable field must appear.
func TestLedgerDiffs(t *testing.T) {
	_, f := testServer(t)
	b := f.Book
	if err := b.AddAccount(&domain.Account{ID: "cto", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	marshal := func(kind string, v any) store.LogEntry {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return store.LogEntry{Kind: kind, Data: data, Ts: time.Now()}
	}

	tx := *b.Transactions[1]
	edited := tx
	edited.Date = day(t, "2026-06-02")
	edited.Kind = domain.Sell
	edited.Account = "cto"
	edited.Asset = ""
	edited.Quantity = dec("11")
	edited.Amount = domain.Money{Amount: dec("6000"), Currency: domain.EUR}
	edited.Note = "corrected"

	oldAcc := domain.Account{ID: "pea", Name: "PEA Zephyr", Currency: domain.EUR}
	newAcc := oldAcc
	newAcc.Name = "PEA Boursa"
	newAcc.Currency = domain.USD
	newAcc.Aliases = []string{"boursa"}
	newAcc.Tax, _ = domain.ParseTaxRule("gains:18.6%")

	oldAsset := domain.Asset{ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions/monde"}
	newAsset := oldAsset
	newAsset.Kind = domain.Property
	newAsset.Name = "World Tracker"
	newAsset.Ticker = "MWRD.PA"
	newAsset.ISIN = "LU1681043599"
	newAsset.Group = "actions/global"
	newAsset.Currency = domain.USD
	newAsset.Aliases = []string{"monde"}
	newAsset.Withholding = 0.15

	rows := buildLedgerEntries(b, []store.LogEntry{
		marshal("tx", b.Transactions[1]), marshal("tx-edit", &edited),
		marshal("acct", &oldAcc), marshal("acct", &newAcc),
		marshal("asset", &oldAsset), marshal("asset", &newAsset),
	})
	// newest first: asset edit, account edit, tx edit
	diffs := map[string]string{"asset": rows[0].Diff, "account": rows[2].Diff, "tx": rows[4].Diff}
	if !strings.HasSuffix(rows[4].Kind, "(edit)") {
		t.Errorf("tx-edit row kind = %q, want the edit marker", rows[4].Kind)
	}
	wants := map[string][]string{
		"tx": {"date: 2026-06-01 -> 2026-06-02", "kind: buy -> sell",
			"account: PEA Zephyr -> CTO Meridia", "asset: Amundi MSCI World ->",
			"qty: 10 -> 11", "amount:", `note: "" -> "corrected"`},
		"account": {"name: PEA Zephyr -> PEA Boursa", "tax: none -> gains:18.6%",
			"ccy: EUR -> USD", "aliases: [] -> [boursa]"},
		"asset": {"kind: security -> property", "name: Amundi MSCI World -> World Tracker",
			"ticker: CW8.PA -> MWRD.PA", "isin:  -> LU1681043599",
			`group: "actions/monde" -> "actions/global"`, "ccy: EUR -> USD",
			"aliases: [] -> [monde]", "withholding: 0% -> 15%"},
	}
	for what, lines := range wants {
		for _, line := range lines {
			if !strings.Contains(diffs[what], line) {
				t.Errorf("%s diff missing %q:\n%s", what, line, diffs[what])
			}
		}
	}
}

// An edit that changes ONLY the asset kind must still be readable in the audit
// log: kind drives valuation (a property statement re-declares the whole
// estimate, a security's is per share), so a silent empty diff hides the one
// field that changed the numbers.
func TestLedgerDiffAssetKindOnly(t *testing.T) {
	_, f := testServer(t)
	before := domain.Asset{ID: "maison", Kind: domain.Security, Name: "Maison", Currency: domain.EUR}
	after := before
	after.Kind = domain.Property
	marshal := func(v any) store.LogEntry {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return store.LogEntry{Kind: "asset", Data: data, Ts: time.Now()}
	}
	rows := buildLedgerEntries(f.Book, []store.LogEntry{marshal(&before), marshal(&after)})
	if got := rows[0].Diff; got != "kind: security -> property" {
		t.Errorf("diff = %q, want the kind change", got)
	}
}

// The audit log page shows the diff of an edit made through the UI.
func TestTxPageShowsEditDiff(t *testing.T) {
	srv, f := testServer(t)
	id := string(f.Book.Transactions[1].ID)
	if code, _, _ := postForm(t, srv, "/tx/"+id+"/edit", url.Values{
		"date": {"2026-06-01"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"14"}, "amount": {"7700"}, "note": {"corrected"},
	}); code != http.StatusSeeOther {
		t.Fatal("edit refused")
	}
	code, body := get(t, srv, "/tx")
	if code != http.StatusOK {
		t.Fatalf("GET /tx = %d", code)
	}
	for _, want := range []string{"buy (edit)", "qty: 10 -&gt; 14"} {
		if !strings.Contains(body, want) {
			t.Errorf("/tx: %q missing:\n%s", want, excerpt(body))
		}
	}
}

// --- scoping and exports ---------------------------------------------------

// Every scope route answers on any of the tiered references (id, ticker, alias,
// name, unique prefix) and 404s cleanly on an unknown one.
func TestScopeRoutesResolveReferences(t *testing.T) {
	srv, f := testServer(t)
	acc, _ := f.Book.Account("pea")
	acc.Aliases = []string{"zephyr"}
	cases := []struct {
		path string
		want int
	}{
		{"/account/pea", http.StatusOK},
		{"/account/zephyr", http.StatusOK},                          // by alias
		{"/account/" + url.PathEscape("PEA Zephyr"), http.StatusOK}, // by name
		{"/account/unknown", http.StatusNotFound},
		{"/asset/CW8.PA", http.StatusOK}, // by ticker
		{"/asset/unknown", http.StatusNotFound},
		{"/group/actions", http.StatusOK},
		{"/group/actions/monde", http.StatusOK},
		{"/group/nothing-here", http.StatusNotFound}, // no asset carries it: not a scope
		{"/account/pea/group/actions", http.StatusOK},
		{"/account/unknown/group/actions", http.StatusNotFound},
	}
	for _, c := range cases {
		if code, body := get(t, srv, c.path); code != c.want {
			t.Errorf("GET %s = %d, want %d\n%s", c.path, code, c.want, excerpt(body))
		}
	}
}

// The CSV export is a download of every line, cash included, in the display
// currency - the one place the gross is published next to the net.
func TestAssetsCSVExport(t *testing.T) {
	srv, _ := testServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets.csv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets.csv = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "finador-assets.csv") {
		t.Errorf("content-disposition = %q", cd)
	}
	body := rec.Body.String()
	for _, want := range []string{"CW8.PA", "Amundi MSCI World", "cash"} {
		if !strings.Contains(body, want) {
			t.Errorf("assets.csv: %q missing:\n%s", want, excerpt(body))
		}
	}
}

// --- price captions and refresh -------------------------------------------

// The freshness caption never guesses: a stored close is dated as a close, a
// nowcast is flagged as an estimate, and an instrument nothing has priced says
// nothing at all.
func TestQuoteNoteCaptions(t *testing.T) {
	srv, f := testServer(t)
	cw8, _ := f.Book.Asset("cw8")

	// no spot pass yet: the last stored close, dated
	note := srv.quoteNote(cw8)
	last, _ := f.Book.Market.Price("cw8").Last()
	if !strings.Contains(note, "560.00 EUR") || !strings.Contains(note, "close of "+last.Date.String()) {
		t.Errorf("stored-close caption = %q", note)
	}

	// an asset nothing has ever priced carries no caption
	if err := f.Book.AddAsset(&domain.Asset{ID: "maison", Kind: domain.Property,
		Name: "Maison", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	maison, _ := f.Book.Asset("maison")
	if note := srv.quoteNote(maison); note != "" {
		t.Errorf("caption of an unpriced asset = %q, want empty", note)
	}

	// a quote observed by a spot pass but struck at a close is captioned as one
	srv.mergeSpot(map[domain.AssetID]market.Quote{
		"cw8": {Price: 561, Currency: domain.EUR, Time: time.Now(), Live: false},
	})
	if note := srv.quoteNote(cw8); !strings.Contains(note, "close of") || strings.Contains(note, "live at") {
		t.Errorf("non-live spot caption = %q", note)
	}
}

// An empty pass knows nothing: it must not erase what the last good one saw,
// or every asset page would go silent after one outage.
func TestMergeSpotKeepsPreviousQuotes(t *testing.T) {
	srv, _ := testServer(t)
	srv.mergeSpot(map[domain.AssetID]market.Quote{"cw8": {Price: 560, Currency: domain.EUR}})
	srv.mergeSpot(nil)
	if q, ok := srv.spot["cw8"]; !ok || q.Price != 560 {
		t.Errorf("spot after an empty pass = %+v, want the previous quote kept", q)
	}
}

// The refresh button online reports what it fetched and what went wrong, and
// leaves the ledger alone (quotes live in the cache sidecar).
func TestRefreshButtonOnline(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	srv, f := testServer(t)
	srv.offline = false
	srv.source = &spotSrc{quote: market.Quote{Price: 561.5, Time: time.Now(),
		Currency: domain.EUR, Live: true}}
	before := len(f.LogEntries())

	code, _, loc := postForm(t, srv, "/refresh", url.Values{})
	if code != http.StatusSeeOther {
		t.Fatalf("POST /refresh = %d", code)
	}
	flash, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	msg := flash.Query().Get("flash")
	if !strings.Contains(msg, "1 series refreshed") || !strings.Contains(msg, "1 quotes") {
		t.Errorf("flash = %q, want the counts of what was refreshed", msg)
	}
	if got := len(f.LogEntries()); got != before {
		t.Errorf("a refresh wrote %d ledger entries, want none", got-before)
	}
}

// A source that answers in the wrong currency is a unit bug: the quote is
// dropped and the warning reaches the user through the flash.
func TestRefreshButtonSurfacesWarnings(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	srv, _ := testServer(t)
	srv.offline = false
	srv.source = &spotSrc{quote: market.Quote{Price: 610, Time: time.Now(),
		Currency: domain.USD, Live: true}} // cw8 is declared in EUR
	_, _, loc := postForm(t, srv, "/refresh", url.Values{})
	flash, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if msg := flash.Query().Get("flash"); !strings.Contains(msg, "warning") ||
		!strings.Contains(msg, "quote ignored") {
		t.Errorf("flash = %q, want the off-currency warning", msg)
	}
}

// AutoRefresh runs a first pass immediately (so the first page load is live),
// then keeps ticking until the context is cancelled.
func TestAutoRefreshTicksUntilCancelled(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	srv, f := testServer(t)
	srv.offline = false
	src := &spotSrc{quote: market.Quote{Price: 561.5, Time: time.Now(), Currency: domain.EUR, Live: true}}
	srv.source = src

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.AutoRefresh(ctx, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for src.dailyCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoRefresh did not return after its context was cancelled")
	}
	if src.dailyCalls.Load() == 0 {
		t.Error("AutoRefresh never ran its first pass")
	}
	srv.mu.RLock()
	fetched := f.Book.Market.Price("cw8").FetchedAt.IsZero()
	srv.mu.RUnlock()
	if fetched {
		t.Error("AutoRefresh did not refresh the cache")
	}
}

// Offline, or with no interval, AutoRefresh is a no-op that returns at once -
// never a ticker hammering a network that is not there.
func TestAutoRefreshNoop(t *testing.T) {
	srv, _ := testServer(t) // offline
	done := make(chan struct{})
	go func() {
		srv.AutoRefresh(context.Background(), time.Millisecond)
		srv.offline = false
		srv.AutoRefresh(context.Background(), 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AutoRefresh did not return immediately when disabled")
	}
}

// --- rendering helpers -----------------------------------------------------

func TestRenderHelpers(t *testing.T) {
	for ccy, want := range map[domain.Currency]string{
		domain.EUR: "€", domain.USD: "$", "GBP": "£", "CHF": "CHF",
	} {
		if got := symbol(ccy); got != want {
			t.Errorf("symbol(%s) = %q, want %q", ccy, got, want)
		}
	}
	for x, want := range map[float64]string{1: "pos", -1: "neg", 0: ""} {
		if got := signe(x); got != want {
			t.Errorf("signe(%v) = %q, want %q", x, got, want)
		}
	}
	for x, want := range map[float64]string{1: "➚", -1: "➘", 0: ""} {
		if got := arrow(x); got != want {
			t.Errorf("arrow(%v) = %q, want %q", x, got, want)
		}
	}
	// negatives use U+2212, never the ASCII hyphen
	if got := fmtPct(-0.0234); got != "−2.34%" {
		t.Errorf("fmtPct(-0.0234) = %q", got)
	}
	if got := fmtNum(-1.256); got != "−1.26" {
		t.Errorf("fmtNum(-1.256) = %q", got)
	}
	if got := fmtDate(day(t, "2026-06-10")); got != "Wednesday 10 June 2026" {
		t.Errorf("fmtDate = %q", got)
	}
	if got := fmtMoney(-1234.5, domain.USD); got != "\u22121,234.50\u00a0$" {
		t.Errorf("fmtMoney = %q", got)
	}
}

// An unknown template is reported, never served as a blank page.
func TestRenderUnknownTemplate(t *testing.T) {
	srv, _ := testServer(t)
	rec := httptest.NewRecorder()
	srv.render(rec, http.StatusOK, "nope.html", nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "template not found") {
		t.Errorf("render(unknown) = %d %q", rec.Code, rec.Body.String())
	}
}

// The two chart selectors of a page resolve their own parameter, and default
// on anything they do not know.
func TestChartAndPriceRanges(t *testing.T) {
	today := day(t, "2026-06-10")
	for q, want := range map[string]string{
		"": "1y", "prange=1d": "1d", "prange=1m": "1m", "prange=3m": "3m",
		"prange=all": "all", "prange=bogus": "1y",
	} {
		r := httptest.NewRequest(http.MethodGet, "/asset/cw8?"+q, nil)
		from, name := priceRange(r, today)
		if name != want {
			t.Errorf("priceRange(%q) = %q, want %q", q, name, want)
		}
		if (name == "1d" || name == "all") != from.IsZero() {
			t.Errorf("priceRange(%q) from = %v for range %q", q, from, name)
		}
	}
	for q, want := range map[string]string{
		"": "all", "range=1m": "1m", "range=3m": "3m", "range=1y": "1y", "range=bogus": "all",
	} {
		r := httptest.NewRequest(http.MethodGet, "/?"+q, nil)
		if _, name := chartRange(r, today); name != want {
			t.Errorf("chartRange(%q) = %q, want %q", q, name, want)
		}
	}
}

// The asset page's fallback: when intraday is unavailable, the 1d view says so
// and falls back to the daily closes rather than showing an empty frame.
func TestPriceRangeViewsRender(t *testing.T) {
	srv, _ := testServer(t)
	for _, q := range []string{"", "?prange=1m", "?prange=3m", "?prange=all", "?prange=1d"} {
		code, body := get(t, srv, "/asset/cw8"+q)
		if code != http.StatusOK {
			t.Fatalf("GET /asset/cw8%s = %d", q, code)
		}
		if q == "?prange=1d" && !strings.Contains(body, "intraday unavailable") {
			t.Errorf("1d view without intraday must say so:\n%s", excerpt(body))
		}
	}
}

func TestDedupeWarnings(t *testing.T) {
	got := dedupeWarnings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("dedupeWarnings = %v, want %v", got, want)
	}
	if dedupeWarnings(nil) != nil {
		t.Error("dedupeWarnings(nil) should stay nil")
	}
}

// --- import ----------------------------------------------------------------

// The upload guards: no file, an oversized body and a malformed CSV all come
// back on the import page with an explanation, never a 500.
func TestImportUploadRejections(t *testing.T) {
	srv, f := testServer(t)
	before := len(f.Book.Transactions)

	code, _, loc := postForm(t, srv, "/import", url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(loc, "no+file+received") {
		t.Errorf("upload without a file = %d → %q", code, loc)
	}

	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader("x"))
	req.ContentLength = 11 << 20
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "too+large") {
		t.Errorf("oversized upload = %d → %q", rec.Code, rec.Header().Get("Location"))
	}

	body, ct := multipartCSV(t, "date,kind,account\nnot-a-date,buy,PEA Zephyr\n")
	req = httptest.NewRequest(http.MethodPost, "/import", body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Errorf("malformed CSV = %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.Book.Transactions) != before {
		t.Error("a refused import wrote transactions")
	}
	// the error is shown back on the page, naming the offending line
	if code, page := get(t, srv, rec.Header().Get("Location")); code != http.StatusOK ||
		!strings.Contains(page, "line 2") {
		t.Errorf("import page does not show the error:\n%s", excerpt(page))
	}
}

// multipartCSV wraps a CSV body as a file upload, the way the browser form does.
func multipartCSV(t *testing.T, csv string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "txs.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}
