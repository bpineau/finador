package ibkr

import (
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

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

// The already-booked guard. The statement's EUR trade line - 20 CW8 on
// 2026-01-20 for 9007 EUR, commission included - is the one every case is
// built around: manualBuy is what a user would have typed for it.
func manualBuy() domain.Transaction {
	return domain.Transaction{
		Date: day(2026, time.January, 20), Account: "cto-meridia", Asset: "cw8", Kind: domain.Buy,
		Quantity: dec("20"), Amount: domain.Money{Amount: dec("9007"), Currency: domain.EUR},
		Note: "typed by hand",
	}
}

func day(y int, m time.Month, d int) domain.Date { return domain.Date{Year: y, Month: m, Day: d} }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func money(amount string, ccy domain.Currency) domain.Money {
	return domain.Money{Amount: dec(amount), Currency: ccy}
}

// tweak returns t with fn applied - one field of a matching manual entry
// changed, which is what every miss case is.
func tweak(t domain.Transaction, fn func(*domain.Transaction)) domain.Transaction {
	fn(&t)
	return t
}

// guarded books the manual transactions, imports the statement and returns
// the result. The book carries a second envelope so a case can name one.
func guarded(t *testing.T, manual []domain.Transaction, opts Options) (*domain.Book, Result) {
	t.Helper()
	b := book(t)
	if err := b.AddAccount(&domain.Account{ID: "pea-zephyr", Name: "PEA Zephyr", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	for _, m := range manual {
		b.Add(m)
	}
	opts.Account = "cto-meridia"
	res, err := Import(b, statement(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	return b, res
}

func TestGuardOnHandEnteredLines(t *testing.T) {
	// The statement carries 8 mappable lines; a guarded one is not imported.
	tests := []struct {
		name                      string
		manual                    []domain.Transaction
		matched, ambiguous, added int
	}{
		{name: "the same event, typed by hand", manual: []domain.Transaction{manualBuy()}, matched: 1, added: 7},
		{name: "a commission-sized difference", added: 7, matched: 1, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Amount.Amount = dec("9052") }),
		}},
		{name: "exactly 0.5 % away, inclusive", added: 7, matched: 1, manual: []domain.Transaction{
			// 9007 - 9007*0.005 = 8961.965
			tweak(manualBuy(), func(t *domain.Transaction) { t.Amount.Amount = dec("8961.965") }),
		}},
		{name: "one cent past the edge, below", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Amount.Amount = dec("8961.96") }),
		}},
		{name: "one euro past the edge, above", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Amount.Amount = dec("9053") }),
		}},
		{name: "another day", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Date = day(2026, time.January, 21) }),
		}},
		{name: "another envelope", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Account = "pea-zephyr" }),
		}},
		{name: "another security", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Asset = "vt" }),
		}},
		{name: "another direction", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Kind = domain.Sell }),
		}},
		{name: "another quantity", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Quantity = dec("21") }),
		}},
		{name: "another currency", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.Amount.Currency = domain.USD }),
		}},
		{name: "already imported from elsewhere", added: 8, manual: []domain.Transaction{
			tweak(manualBuy(), func(t *domain.Transaction) { t.ImportHash = "meridia:8451327" }),
		}},
		{name: "a dividend, where quantity means nothing", added: 7, matched: 1, manual: []domain.Transaction{{
			Date: day(2026, time.March, 16), Account: "cto-meridia", Asset: "vt", Kind: domain.Dividend,
			Quantity: dec("600"), Amount: money("420", domain.USD),
		}}},
		{name: "a funding movement, no security", added: 7, matched: 1, manual: []domain.Transaction{{
			Date: day(2026, time.January, 5), Account: "cto-meridia", Kind: domain.Deposit,
			Amount: money("25000", domain.EUR),
		}}},
		{name: "two candidates, no way to choose", added: 7, ambiguous: 1, manual: []domain.Transaction{
			manualBuy(), manualBuy(),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, res := guarded(t, tc.manual, Options{})
			if res.Added != tc.added || res.Matched != tc.matched || res.Ambiguous != tc.ambiguous {
				t.Errorf("added=%d matched=%d ambiguous=%d, want %d, %d and %d",
					res.Added, res.Matched, res.Ambiguous, tc.added, tc.matched, tc.ambiguous)
			}
			if len(res.Matches) != tc.matched+tc.ambiguous {
				t.Errorf("%d reported matches, want %d", len(res.Matches), tc.matched+tc.ambiguous)
			}
		})
	}
}

// A match is reported with the line it came from, what the line said and the
// manual entry it belongs to - the user needs all three to act.
func TestGuardReportsWhatItRecognised(t *testing.T) {
	b, res := guarded(t, []domain.Transaction{manualBuy()}, Options{})
	manual := find(t, b, func(tx *domain.Transaction) bool { return tx.Note == "typed by hand" })
	got := res.Matches[0].String()
	for _, want := range []string{"2026-01-20 buy CW8 20 9007 EUR", "matches manual entry " + string(manual.ID)} {
		if !strings.Contains(got, want) {
			t.Errorf("report = %q, want %q in it", got, want)
		}
	}
	// The line was not imported, and the manual entry was not touched.
	if manual.ImportHash != "" {
		t.Errorf("the guard stamped a manual entry: %q", manual.ImportHash)
	}

	// Ambiguity names every candidate and adopts none.
	_, res = guarded(t, []domain.Transaction{manualBuy(), manualBuy()}, Options{Guard: GuardReconcile})
	if got, want := res.Matches[0].String(), "ambiguous: "; !strings.Contains(got, want) {
		t.Errorf("report = %q, want %q in it", got, want)
	}
	if res.Adopted != 0 || len(res.Matches[0].Manual) != 2 {
		t.Errorf("adopted=%d candidates=%v", res.Adopted, res.Matches[0].Manual)
	}
}

// --reconcile adopts the hand-entered transaction: it takes the statement
// line's fingerprint and nothing else changes, so a replay skips the line.
func TestReconcileAdoptsTheManualEntry(t *testing.T) {
	b, res := guarded(t, []domain.Transaction{manualBuy()}, Options{Guard: GuardReconcile})
	if res.Adopted != 1 || res.Added != 7 || res.Matched != 0 {
		t.Fatalf("adopted=%d added=%d matched=%d, want 1, 7 and 0", res.Adopted, res.Added, res.Matched)
	}
	manual := find(t, b, func(tx *domain.Transaction) bool { return tx.Note == "typed by hand" })
	if !strings.HasPrefix(manual.ImportHash, "ibkr:") {
		t.Fatalf("importHash = %q", manual.ImportHash)
	}
	// Every other field is byte-stable: the store diffs the record's JSON, so
	// the tx-edit an adoption produces must differ by importHash alone.
	before, after := recordFields(t, manualBuy()), recordFields(t, *manual)
	delete(after, "importHash")
	delete(after, "id") // assigned by the ledger, not by the adoption
	delete(before, "id")
	if !maps.Equal(before, after) {
		t.Errorf("adoption changed more than the fingerprint:\nbefore %v\nafter  %v", before, after)
	}

	// Replaying the statement now recognises the line as its own.
	res, err := Import(b, statement(t), Options{Account: "cto-meridia"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Skipped != 8 || res.Matched != 0 {
		t.Fatalf("replay: added=%d skipped=%d matched=%d, want 0, 8 and 0", res.Added, res.Skipped, res.Matched)
	}
}

// recordFields renders a transaction the way the ledger persists it, field by
// field, so a test can name exactly what an edit changed.
func recordFields(t *testing.T, tx domain.Transaction) map[string]string {
	t.Helper()
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range fields {
		out[k] = string(v)
	}
	return out
}

// --no-guard is the way back: the line is imported, duplicate or not.
func TestGuardOffImportsAnyway(t *testing.T) {
	b, res := guarded(t, []domain.Transaction{manualBuy()}, Options{Guard: GuardOff})
	if res.Added != 8 || res.Matched != 0 {
		t.Fatalf("added=%d matched=%d, want 8 and 0", res.Added, res.Matched)
	}
	if n := len(b.Transactions); n != 9 { // the manual entry plus the whole statement
		t.Fatalf("%d transactions, want 9", n)
	}
}

// --since ignores the years already booked, counting them apart.
func TestSinceIgnoresOlderLines(t *testing.T) {
	b, res := guarded(t, nil, Options{Since: day(2026, time.March, 1)})
	// Before March: three stock trades, the forex one, the funding deposit
	// and the dividend reversal - a line out of range is counted as out of
	// range and nothing else, even when another rule would have dropped it.
	// After: two dividends, the withholding tax, the disbursement.
	if res.BeforeSince != 6 || res.Added != 4 {
		t.Fatalf("before=%d added=%d, want 6 and 4", res.BeforeSince, res.Added)
	}
	for _, tx := range b.Transactions {
		if tx.Date.Before(day(2026, time.March, 1)) {
			t.Errorf("imported a line dated %s", tx.Date)
		}
	}

	// A line out of range is dropped before its security is resolved, so an
	// old statement cannot fail over a symbol the book no longer declares.
	bare := domain.NewBook()
	if err := bare.AddAccount(&domain.Account{ID: "cto-meridia", Name: "CTO Meridia", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	res, err := Import(bare, statement(t), Options{Account: "cto-meridia", Since: day(2026, time.April, 1)})
	if err != nil {
		t.Fatalf("everything out of range should import cleanly: %v", err)
	}
	if res.Added != 0 || res.BeforeSince != 10 {
		t.Fatalf("added=%d before=%d, want 0 and 10", res.Added, res.BeforeSince)
	}
}

func TestResultSummary(t *testing.T) {
	if got, want := (Result{Added: 12, Skipped: 3}).Summary(), "12 imported, 3 skipped (duplicates)"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	full := Result{Added: 1, Skipped: 2, Matched: 3, Adopted: 4, Ambiguous: 5, BeforeSince: 6}.Summary()
	want := "1 imported, 2 skipped (duplicates), 3 matched (manual entries), " +
		"4 adopted (manual entries), 5 ambiguous, 6 before --since"
	if full != want {
		t.Errorf("summary = %q, want %q", full, want)
	}
}
