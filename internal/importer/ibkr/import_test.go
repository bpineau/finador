package ibkr

import (
	"os"
	"strings"
	"testing"

	"finador/internal/domain"
)

// The fixture is synthetic: it reproduces the documented section layout of an
// activity statement (Header/Data/SubTotal/Total lines, a section repeated per
// currency, a BOM, CRLF endings, thousands separators) with a fictitious
// account and fictitious amounts.
const fixture = "testdata/activity.csv"

func statement(t *testing.T) *strings.Reader {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(raw))
}

// book returns a book holding the envelope and the two securities the
// statement trades, so nothing has to be created on the fly.
func book(t *testing.T) *domain.Book {
	t.Helper()
	b := domain.NewBook()
	acc := &domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR}
	if err := b.AddAccount(acc); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*domain.Asset{
		{ID: "vt", Kind: domain.Security, Name: "Vanguard Total World", Ticker: "VT", ISIN: "US9220427424", Currency: domain.USD},
		{ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World", Ticker: "CW8.PA", ISIN: "LU1681043599", Currency: domain.EUR},
	} {
		if err := b.AddAsset(a); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func importFixture(t *testing.T, b *domain.Book) Result {
	t.Helper()
	res, err := Import(b, statement(t), Options{Account: "CTO Meridia"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// find returns the single transaction matching a predicate.
func find(t *testing.T, b *domain.Book, match func(*domain.Transaction) bool) *domain.Transaction {
	t.Helper()
	var hits []*domain.Transaction
	for _, tx := range b.Transactions {
		if match(tx) {
			hits = append(hits, tx)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected one matching transaction, got %d", len(hits))
	}
	return hits[0]
}

func TestParseSections(t *testing.T) {
	rows, err := parse(statement(t))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.section]++
	}
	// Data lines only: SubTotal and Total lines never reach the caller.
	for section, want := range map[string]int{
		"Trades": 5, "Dividends": 3, "Withholding Tax": 1,
		"Deposits & Withdrawals": 3, "Fees": 1, "Interest": 1,
		"Financial Instrument Information": 2, "Codes": 2,
	} {
		if counts[section] != want {
			t.Errorf("section %q: %d data lines, want %d", section, counts[section], want)
		}
	}
	// The BOM did not leak into the first section name.
	if rows[0].section != "Statement" {
		t.Errorf("first section = %q", rows[0].section)
	}
	// Columns are read by name, and a quoted thousands separator survives.
	trade := rows[counts["Statement"]+counts["Account Information"]+counts["Net Asset Value"]]
	if got := trade.get("Quantity"); got != "1,000" {
		t.Errorf("Quantity = %q", got)
	}
	if got := trade.get("Code"); got != "O" {
		t.Errorf("Code = %q", got)
	}
}

func TestImportMapsEverySupportedSection(t *testing.T) {
	b := book(t)
	res := importFixture(t, b)
	if res.Added != 8 || res.Skipped != 0 {
		t.Fatalf("added=%d skipped=%d, want 8 and 0", res.Added, res.Skipped)
	}
	counts := map[domain.TxKind]int{}
	for _, tx := range b.Transactions {
		counts[tx.Kind]++
		if tx.Amount.Amount.IsNegative() || tx.Quantity.IsNegative() {
			t.Errorf("%s carries a negative amount or quantity: %+v", tx.Kind, tx)
		}
		if !strings.HasPrefix(tx.ImportHash, "ibkr:") {
			t.Errorf("importHash %q is not namespaced", tx.ImportHash)
		}
	}
	for kind, want := range map[domain.TxKind]int{
		domain.Buy: 2, domain.Sell: 1, domain.Dividend: 2,
		domain.Fee: 1, domain.Deposit: 1, domain.Withdraw: 1,
	} {
		if counts[kind] != want {
			t.Errorf("%s: %d transactions, want %d", kind, counts[kind], want)
		}
	}
}

// A commission is part of what a position cost, so it is folded into the
// trade amount: added on a buy, subtracted from a sell.
func TestTradeAmountsFoldTheCommission(t *testing.T) {
	b := book(t)
	importFixture(t, b)

	buy := find(t, b, func(tx *domain.Transaction) bool {
		return tx.Kind == domain.Buy && tx.Asset == "vt"
	})
	if got := buy.Amount.Amount.String(); got != "100501.5" { // 100500 + 1.5
		t.Errorf("buy amount = %s", got)
	}
	if got := buy.Quantity.String(); got != "1000" {
		t.Errorf("buy quantity = %s", got)
	}
	if buy.Amount.Currency != domain.USD || buy.Date.String() != "2026-01-15" {
		t.Errorf("buy = %s %s", buy.Date, buy.Amount)
	}
	if !strings.Contains(buy.Note, "incl. 1.5 USD commission") {
		t.Errorf("buy note = %q", buy.Note)
	}

	sell := find(t, b, func(tx *domain.Transaction) bool { return tx.Kind == domain.Sell })
	if got := sell.Amount.Amount.String(); got != "43998.8" { // 44000 - 1.2
		t.Errorf("sell amount = %s", got)
	}
	if got := sell.Quantity.String(); got != "400" {
		t.Errorf("sell quantity = %s", got)
	}

	// The second currency section is read with the same header.
	eur := find(t, b, func(tx *domain.Transaction) bool {
		return tx.Kind == domain.Buy && tx.Asset == "cw8"
	})
	if got := eur.Amount.Amount.String(); got != "9007" || eur.Amount.Currency != domain.EUR {
		t.Errorf("EUR buy = %s", eur.Amount)
	}
}

func TestDividendAndWithholdingTax(t *testing.T) {
	b := book(t)
	importFixture(t, b)

	div := find(t, b, func(tx *domain.Transaction) bool {
		return tx.Kind == domain.Dividend && tx.Asset == "vt"
	})
	if got := div.Amount.Amount.String(); got != "420" {
		t.Errorf("dividend amount = %s", got)
	}
	if div.Date.String() != "2026-03-16" || div.Amount.Currency != domain.USD {
		t.Errorf("dividend = %s %s", div.Date, div.Amount)
	}

	// The symbol comes out of the description prefix, the asset out of the
	// ISIN in the parentheses.
	tax := find(t, b, func(tx *domain.Transaction) bool { return tx.Kind == domain.Fee })
	if tax.Asset != "vt" {
		t.Errorf("withholding tax asset = %q", tax.Asset)
	}
	if got := tax.Amount.Amount.String(); got != "63" { // sign carried by the kind
		t.Errorf("withholding tax amount = %s", got)
	}
}

func TestDepositsAndWithdrawals(t *testing.T) {
	b := book(t)
	importFixture(t, b)
	for kind, want := range map[domain.TxKind]string{domain.Deposit: "25000", domain.Withdraw: "1500"} {
		tx := find(t, b, func(tx *domain.Transaction) bool { return tx.Kind == kind })
		if got := tx.Amount.Amount.String(); got != want {
			t.Errorf("%s amount = %s, want %s", kind, got, want)
		}
		if tx.Asset != "" {
			t.Errorf("%s carries an asset: %q", kind, tx.Asset)
		}
	}
}

func TestUnsupportedSectionsAreCountedNotGuessed(t *testing.T) {
	b := book(t)
	res := importFixture(t, b)
	want := map[string]int{
		"Trades: Forex": 1, "Dividends: reversals": 1, "Fees": 1, "Interest": 1,
	}
	for reason, n := range want {
		if res.Ignored[reason] != n {
			t.Errorf("ignored[%q] = %d, want %d", reason, res.Ignored[reason], n)
		}
	}
	if len(res.Ignored) != len(want) {
		t.Errorf("ignored = %v", res.Ignored)
	}
	if got := res.IgnoredReport(); got != "Dividends: reversals (1), Fees (1), Interest (1), Trades: Forex (1)" {
		t.Errorf("report = %q", got)
	}
}

// Replaying the same statement writes nothing: the importHash of every line
// is already in the book.
func TestImportIsIdempotent(t *testing.T) {
	b := book(t)
	importFixture(t, b)
	res, err := Import(b, statement(t), Options{Account: "cto-meridia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Skipped != 8 {
		t.Fatalf("re-import: added=%d skipped=%d, want 0 and 8", res.Added, res.Skipped)
	}
	if len(b.Transactions) != 8 {
		t.Fatalf("%d transactions after two imports", len(b.Transactions))
	}
}

// An edited import keeps its fingerprint, so a replay still skips it.
func TestEditedTransactionStaysSkipped(t *testing.T) {
	b := book(t)
	importFixture(t, b)
	b.Transactions[0].Note = "corrected by hand"
	res, err := Import(b, statement(t), Options{Account: "cto-meridia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 {
		t.Fatalf("re-import after an edit added %d", res.Added)
	}
}

func TestUnknownAssetsAreListed(t *testing.T) {
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	_, err := Import(b, statement(t), Options{Account: "CTO Meridia"})
	if err == nil {
		t.Fatal("an undeclared security should fail the import")
	}
	for _, want := range []string{"CW8", "VT", "--create-missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if len(b.Transactions) != 0 {
		t.Errorf("%d transactions written by a failed import", len(b.Transactions))
	}
}

func TestCreateMissingDeclaresTheSecurities(t *testing.T) {
	b := domain.NewBook()
	if err := b.AddAccount(&domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	res, err := Import(b, statement(t), Options{Account: "CTO Meridia", CreateMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 8 {
		t.Fatalf("added = %d", res.Added)
	}
	vt, err := b.Asset("US9220427424") // resolvable by the ISIN of the instrument section
	if err != nil {
		t.Fatal(err)
	}
	if vt.Ticker != "VT" || vt.Currency != domain.USD || vt.Name != "VANGUARD TOTAL WORLD STOCK ETF" {
		t.Errorf("created asset = %+v", vt)
	}
}

func TestAccountIsRequiredAndResolved(t *testing.T) {
	b := book(t)
	if _, err := Import(b, statement(t), Options{}); err == nil {
		t.Error("an import with no account should fail")
	}
	if _, err := Import(b, statement(t), Options{Account: "Nowhere"}); err == nil {
		t.Error("an import naming an undeclared account should fail")
	}
}

func TestDescribedInstrument(t *testing.T) {
	for _, tc := range []struct{ desc, symbol, isin string }{
		{"VT(US9220427424) Cash Dividend USD 0.70 per Share", "VT", "US9220427424"},
		{"VT(US9220427424) Cash Dividend USD 0.70 per Share - US Tax", "VT", "US9220427424"},
		{"CW8 Cash Dividend EUR 1.25 per Share", "CW8", ""},
		{"IEF(12345678) Cash Dividend", "IEF", ""}, // a conid is not an ISIN
		{"", "", ""},
	} {
		symbol, isin := describedInstrument(tc.desc)
		if symbol != tc.symbol || isin != tc.isin {
			t.Errorf("%q -> (%q, %q), want (%q, %q)", tc.desc, symbol, isin, tc.symbol, tc.isin)
		}
	}
}

func TestNumberAndDate(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "0"}, {"1,000.5", "1000.5"}, {"-100,500", "-100500"}, {" 42 ", "42"},
	} {
		got, err := number(tc.in)
		if err != nil || got.String() != tc.want {
			t.Errorf("number(%q) = %s, %v", tc.in, got, err)
		}
	}
	if _, err := number("n/a"); err == nil {
		t.Error("a non-numeric cell should fail")
	}
	d, err := tradeDate("2026-01-15, 10:31:02")
	if err != nil || d.String() != "2026-01-15" {
		t.Errorf("tradeDate = %s, %v", d, err)
	}
	if _, err := tradeDate("01/15/2026"); err == nil {
		t.Error("a non-ISO date should fail rather than be guessed")
	}
}
