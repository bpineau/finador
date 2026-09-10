// Package ibkr imports Interactive Brokers activity statements - the
// multi-section CSV that Reports > Statements > Activity exports - into a
// finador book.
//
// One statement covers one IBKR account, which the caller names: the file
// says nothing usable about which envelope of the book it belongs to. Assets
// are matched to the book's own securities by ISIN first, then by ticker;
// symbols the book does not declare are reported, not invented, unless
// Options.CreateMissing says otherwise.
//
// Re-importing the same statement is a no-op: every transaction carries an
// importHash namespaced "ibkr:" (FORMAT.md section 4.5) and the shared
// portfolio.AddImported write path drops the ones already in the book.
//
// Conventions worth knowing before reading the mapping:
//
//   - A trade's commission is folded INTO the trade amount (added on a buy,
//     subtracted from a sell) rather than emitted as a separate fee. Only
//     that keeps the per-position cost basis right: portfolio's position
//     basis replays buys and sells, and ignores fee lines.
//   - Withholding tax IS a separate fee on the same asset: it is not part of
//     any cost basis, and the book's gross dividend plus its tax read
//     correctly on their own lines.
//   - Sections that carry real money but have no faithful equivalent yet
//     (Fees, Interest, Corporate Actions, forex and derivatives trades) are
//     counted and named in the result rather than approximated.
package ibkr

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

// Options configures one import run.
type Options struct {
	// Account is the book reference (id, alias or name) of the envelope the
	// statement belongs to. Required: an IBKR statement covers one account.
	Account string
	// CreateMissing declares the securities the statement names but the book
	// does not, instead of failing with the list of them.
	CreateMissing bool
}

// Result reports what one import did. Added plus Skipped plus the Ignored
// counts is the number of statement lines that carried an event.
type Result struct {
	Added   int            // transactions written to the ledger
	Skipped int            // lines already imported (idempotent replay)
	Ignored map[string]int // reason -> lines deliberately left out
}

// IgnoredReport renders Ignored as "Trades: Forex (3), Fees (2)", sorted by
// reason so the line is stable; "" when nothing was left out.
func (r Result) IgnoredReport() string {
	parts := make([]string, 0, len(r.Ignored))
	for _, reason := range slices.Sorted(maps.Keys(r.Ignored)) {
		parts = append(parts, fmt.Sprintf("%s (%d)", reason, r.Ignored[reason]))
	}
	return strings.Join(parts, ", ")
}

// sectionsWithMoney are the statement sections that carry real cash but that
// this first cut does not map. They are counted and named; every other
// section (Open Positions, Net Asset Value, Codes...) is informational and
// ignored in silence.
var sectionsWithMoney = map[string]bool{
	"Fees":                         true,
	"Interest":                     true,
	"Broker Interest Paid":         true,
	"Broker Interest Received":     true,
	"Bond Interest Paid":           true,
	"Bond Interest Received":       true,
	"Payment In Lieu Of Dividends": true,
	"Corporate Actions":            true,
	"Transaction Fees":             true,
	"Change in Dividend Accruals":  true,
	"Advisor Fees":                 true,
	"Soft Dollar Activity":         true,
	"Interest Accruals":            true,
}

// errUnknownAsset marks a line skipped because its security is not declared;
// the run collects every such symbol and fails once, with all of them.
var errUnknownAsset = errors.New("unknown asset")

// Import reads an activity statement and appends its transactions to b.
// Nothing is written when it returns an error; the caller's write path
// (cli.mutate) only persists a successful run.
func Import(b *domain.Book, r io.Reader, opts Options) (Result, error) {
	var zero Result
	if strings.TrimSpace(opts.Account) == "" {
		return zero, errors.New("an IBKR statement covers one account: name it with --account")
	}
	acc, err := portfolio.ResolveAccount(b, opts.Account)
	if err != nil {
		return zero, err
	}
	rows, err := parse(r)
	if err != nil {
		return zero, err
	}

	im := &importer{
		b: b, acc: acc, opts: opts,
		instruments: instruments(rows),
		assets:      map[string]*domain.Asset{},
		missing:     map[string]bool{},
		ignored:     map[string]int{},
	}
	for _, row := range rows {
		if err := im.row(row); err != nil && !errors.Is(err, errUnknownAsset) {
			return zero, fmt.Errorf("%s line %d: %w", row.section, row.line, err)
		}
	}
	if len(im.missing) > 0 {
		return zero, fmt.Errorf("securities not declared in the book: %s - add them with `finador asset add`, or re-run with --create-missing",
			strings.Join(slices.Sorted(maps.Keys(im.missing)), ", "))
	}

	res := Result{Ignored: im.ignored}
	for _, tx := range im.txs {
		if portfolio.AddImported(b, tx) {
			res.Added++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

type importer struct {
	b           *domain.Book
	acc         *domain.Account
	opts        Options
	instruments map[string]instrument
	assets      map[string]*domain.Asset // symbol -> resolved security
	missing     map[string]bool
	ignored     map[string]int
	txs         []domain.Transaction
}

func (im *importer) ignore(reason string) { im.ignored[reason]++ }

// row maps one statement line onto the ledger. Sections this cut does not
// read fall through to the counter.
func (im *importer) row(r row) error {
	switch r.section {
	case "Trades":
		return im.trade(r)
	case "Dividends":
		return im.dividend(r)
	case "Withholding Tax":
		return im.withholding(r)
	case "Deposits & Withdrawals", "Deposits/Withdrawals":
		return im.cashFlow(r)
	default:
		if sectionsWithMoney[r.section] {
			im.ignore(r.section)
		}
		return nil
	}
}

// trade maps an executed order. Quantity carries the direction, and the
// commission is folded into the amount: a buy costs proceeds plus commission,
// a sell brings in proceeds minus commission. See the package doc.
func (im *importer) trade(r row) error {
	if d := r.get("DataDiscriminator"); d != "" && d != "Order" {
		return nil // ClosedLot lines restate an order lot by lot
	}
	if category := r.get("Asset Category"); category != "Stocks" {
		// Forex, futures and options price and settle unlike a share line.
		im.ignore("Trades: " + cmp.Or(category, "unknown category"))
		return nil
	}
	ccy, err := currency(r.get("Currency"))
	if err != nil {
		return err
	}
	date, err := tradeDate(r.getAny("Date/Time", "Date", "Trade Date"))
	if err != nil {
		return err
	}
	qty, err := number(r.get("Quantity"))
	if err != nil {
		return fmt.Errorf("quantity: %w", err)
	}
	if qty.IsZero() {
		return nil
	}
	proceeds, err := number(r.get("Proceeds"))
	if err != nil {
		return fmt.Errorf("proceeds: %w", err)
	}
	commission, err := number(r.getAny("Comm/Fee", "Comm/Tax", "Commission"))
	if err != nil {
		return fmt.Errorf("commission: %w", err)
	}
	if proceeds.IsZero() {
		price, err := number(r.getAny("T. Price", "TradePrice", "Price"))
		if err != nil {
			return fmt.Errorf("price: %w", err)
		}
		proceeds = price.Mul(qty)
	}

	kind, sign := domain.Buy, decimal.NewFromInt(1)
	if qty.IsNegative() {
		kind, sign = domain.Sell, decimal.NewFromInt(-1)
	}
	amount := proceeds.Abs().Add(commission.Abs().Mul(sign))
	if amount.IsNegative() {
		return fmt.Errorf("commission %s exceeds proceeds %s", commission, proceeds)
	}

	symbol := r.get("Symbol")
	asset, err := im.asset(symbol, "", ccy)
	if err != nil {
		return err
	}
	note := "IBKR trade"
	if !commission.IsZero() {
		verb := "incl."
		if kind == domain.Sell {
			verb = "net of"
		}
		note = fmt.Sprintf("IBKR trade, %s %s %s commission", verb, commission.Abs(), ccy)
	}
	im.add(r, domain.Transaction{
		Date: date, Account: im.acc.ID, Asset: asset.ID, Kind: kind,
		Quantity: qty.Abs(), Amount: domain.Money{Amount: amount, Currency: ccy}, Note: note,
	}, symbol, qty, proceeds, ccy)
	return nil
}

// dividend maps a cash dividend. The description names the instrument:
// "VT(US9220427424) Cash Dividend USD 0.7 per Share".
func (im *importer) dividend(r row) error {
	ccy, date, amount, description, ok, err := im.income(r)
	if !ok || err != nil {
		return err
	}
	if !amount.IsPositive() {
		im.ignore("Dividends: reversals") // cancelled or restated dividends
		return nil
	}
	symbol, isin := describedInstrument(description)
	asset, err := im.asset(symbol, isin, ccy)
	if err != nil {
		return err
	}
	im.add(r, domain.Transaction{
		Date: date, Account: im.acc.ID, Asset: asset.ID, Kind: domain.Dividend,
		Quantity: decimal.Zero, Amount: domain.Money{Amount: amount, Currency: ccy}, Note: description,
	}, symbol, decimal.Zero, amount, ccy)
	return nil
}

// withholding maps source tax to a fee on the taxed security: a cost that
// buys nothing, which is exactly what the book's Fee kind means.
func (im *importer) withholding(r row) error {
	ccy, date, amount, description, ok, err := im.income(r)
	if !ok || err != nil {
		return err
	}
	if !amount.IsNegative() {
		im.ignore("Withholding Tax: refunds")
		return nil
	}
	symbol, isin := describedInstrument(description)
	asset, err := im.asset(symbol, isin, ccy)
	if err != nil {
		return err
	}
	im.add(r, domain.Transaction{
		Date: date, Account: im.acc.ID, Asset: asset.ID, Kind: domain.Fee,
		Quantity: decimal.Zero, Amount: domain.Money{Amount: amount.Abs(), Currency: ccy}, Note: description,
	}, symbol, decimal.Zero, amount, ccy)
	return nil
}

// cashFlow maps a funding movement: positive in, negative out.
func (im *importer) cashFlow(r row) error {
	ccy, date, amount, description, ok, err := im.income(r)
	if !ok || err != nil {
		return err
	}
	if amount.IsZero() {
		return nil
	}
	kind := domain.Deposit
	if amount.IsNegative() {
		kind = domain.Withdraw
	}
	im.add(r, domain.Transaction{
		Date: date, Account: im.acc.ID, Kind: kind,
		Quantity: decimal.Zero, Amount: domain.Money{Amount: amount.Abs(), Currency: ccy},
		Note: description,
	}, "", decimal.Zero, amount, ccy)
	return nil
}

// income reads the shape the cash sections share: Currency, a date, a
// description and a signed Amount. ok is false for the per-currency total
// lines some statements emit as Data, recognisable by a Currency cell that
// is not a currency ("Total", "Total in EUR").
func (im *importer) income(r row) (ccy domain.Currency, date domain.Date, amount decimal.Decimal, description string, ok bool, err error) {
	raw := r.get("Currency")
	if len(raw) != 3 {
		return "", date, amount, "", false, nil
	}
	if ccy, err = currency(raw); err != nil {
		return "", date, amount, "", false, err
	}
	if date, err = tradeDate(r.getAny("Date", "Settle Date", "Value Date", "Date/Time")); err != nil {
		return "", date, amount, "", false, err
	}
	if amount, err = number(r.get("Amount")); err != nil {
		return "", date, amount, "", false, err
	}
	return ccy, date, amount, r.get("Description"), true, nil
}

// add stages a transaction with its external reference. The signature keeps
// the raw statement values the fallback fingerprint is built from, so a
// direction (a signed quantity, a signed amount) still discriminates two
// otherwise identical lines.
func (im *importer) add(r row, tx domain.Transaction, symbol string, qty, amount decimal.Decimal, ccy domain.Currency) {
	tx.ImportHash = importHash(r, tx.Date, symbol, qty, amount, ccy)
	im.txs = append(im.txs, tx)
}

// importHash is the transaction's external reference (FORMAT.md 4.5),
// namespaced so it can never collide with another broker's. Flex-style
// statements carry IBKR's own trade id, which is stable by construction;
// activity statements do not, and fall back to the content fingerprint.
//
// Known limit, shared with the reference CSV importer: two genuinely distinct
// lines identical in date, section, symbol, quantity, amount and currency
// fingerprint alike, and the second is read as a duplicate.
func importHash(r row, date domain.Date, symbol string, qty, amount decimal.Decimal, ccy domain.Currency) string {
	if id := r.getAny("Trade ID", "TransactionID", "Transaction ID"); id != "" {
		return "ibkr:" + id
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		date.String(), r.section, symbol, qty.String(), amount.String(), string(ccy),
	}, "|")))
	return "ibkr:" + hex.EncodeToString(sum[:8])
}

// asset resolves the security a statement line is about: by ISIN first (an
// ISIN survives a reticker and does not depend on the listing exchange),
// then by the IBKR symbol, both through the book's own tiered resolution -
// so an ambiguity propagates instead of quietly creating a second asset.
func (im *importer) asset(symbol, isin string, ccy domain.Currency) (*domain.Asset, error) {
	if symbol == "" {
		return nil, errors.New("no instrument symbol")
	}
	if asset, ok := im.assets[symbol]; ok {
		return asset, nil
	}
	known := im.instruments[symbol]
	isin = cmp.Or(isin, known.isin)
	for _, ref := range []string{isin, symbol} {
		if ref == "" {
			continue
		}
		asset, err := im.b.Asset(ref)
		if err == nil {
			im.assets[symbol] = asset
			return asset, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err // ambiguity: never mask it by creating
		}
	}
	if !im.opts.CreateMissing {
		im.missing[symbol] = true
		return nil, errUnknownAsset
	}
	asset := &domain.Asset{
		ID: domain.AssetID(domain.Slugify(symbol)), Kind: domain.Security,
		Name: cmp.Or(known.description, symbol), Ticker: symbol, ISIN: isin, Currency: ccy,
	}
	if err := im.b.AddAsset(asset); err != nil {
		return nil, err
	}
	im.assets[symbol] = asset
	return asset, nil
}

// instrument is what the Financial Instrument Information section says about
// a symbol.
type instrument struct {
	isin        string
	description string
}

// instruments indexes that section, when the statement includes it.
func instruments(rows []row) map[string]instrument {
	out := map[string]instrument{}
	for _, r := range rows {
		if r.section != "Financial Instrument Information" {
			continue
		}
		if symbol := r.get("Symbol"); symbol != "" {
			out[symbol] = instrument{isin: rowISIN(r), description: r.get("Description")}
		}
	}
	return out
}

// rowISIN reads the row's ISIN. IBKR writes it in "Security ID", a column
// that holds whatever identifier the account prefers (CUSIP, SEDOL...), so
// the value is trusted only when it has an ISIN's shape; failing that, the
// row is scanned, which covers the layouts that give the ISIN its own column.
func rowISIN(r row) string {
	if isin := r.getAny("Security ID", "ISIN"); isISIN(isin) {
		return isin
	}
	for _, f := range r.fields {
		if f = strings.TrimSpace(f); isISIN(f) {
			return f
		}
	}
	return ""
}

// describedInstrument reads the instrument out of a cash description, which
// IBKR writes as "SYM(ISIN) Cash Dividend USD 0.7 per Share".
func describedInstrument(description string) (symbol, isin string) {
	head, rest, parenthesized := strings.Cut(description, "(")
	symbol, _, _ = strings.Cut(strings.TrimSpace(head), " ")
	if parenthesized {
		if id, _, closed := strings.Cut(rest, ")"); closed && isISIN(strings.TrimSpace(id)) {
			isin = strings.TrimSpace(id)
		}
	}
	return symbol, isin
}

// isISIN reports the ISO 6166 shape: two country letters, nine alphanumerics,
// one check digit. The check digit itself is not verified - this only has to
// tell an ISIN from a ticker or a CUSIP.
func isISIN(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i, r := range s {
		digit := r >= '0' && r <= '9'
		letter := r >= 'A' && r <= 'Z'
		switch {
		case !digit && !letter:
			return false
		case i < 2 && !letter: // country code
			return false
		case i == 11 && !digit: // check digit
			return false
		}
	}
	return true
}

// number reads an IBKR figure: an empty cell is zero, thousands separators
// are noise.
func number(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// tradeDate reads a statement date, dropping the time a "Date/Time" cell
// appends after a comma. IBKR renders dates per the statement's own date
// format preference; only the default, ISO, is accepted - guessing between
// 03/04/2026 read American and read European would be a silent one-month lie.
func tradeDate(s string) (domain.Date, error) {
	day, _, _ := strings.Cut(s, ",")
	return domain.ParseDate(strings.TrimSpace(day))
}

// currency parses a cell that must hold an ISO code.
func currency(s string) (domain.Currency, error) { return domain.ParseCurrency(s) }
