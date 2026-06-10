package portfolio

import (
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// FX converts an amount between currencies at a date.
type FX interface {
	Convert(amount float64, from, to domain.Currency, at domain.Date) (float64, error)
}

// Valuation is the value of a scope at a date, in one display currency.
// Line taxes are position-by-position (documented approximation); the total
// of All/Account scopes uses the exact envelope rule — TaxNote is set when
// the two visibly diverge.
type Valuation struct {
	Currency        domain.Currency
	Gross, Tax, Net float64
	Lines           []Line
	Stale           []string
	TaxNote         string
}

type Line struct {
	Label           string
	Gross, Tax, Net float64
}

const staleAfterDays = 5

// ValueOption adjusts a single valuation — nothing is ever persisted.
type ValueOption func(*valuer)

// WithLinesByAccount breaks lines down by envelope instead of group; an
// envelope line carries its positions AND its cash.
func WithLinesByAccount() ValueOption { return func(v *valuer) { v.byAccount = true } }

// WithPriceOverrides forces the price of given assets, in their quote
// currency — the throwaway hypotheses of « value --what-if ddog=280 ».
// For a property, the override replaces the whole estimate.
func WithPriceOverrides(p map[domain.AssetID]float64) ValueOption {
	return func(v *valuer) { v.overrides = p }
}

func Value(b *domain.Book, scope Scope, at domain.Date, ccy domain.Currency, fx FX, opts ...ValueOption) (Valuation, error) {
	v := &valuer{b: b, fx: fx, at: at, ccy: ccy}
	for _, o := range opts {
		o(v)
	}
	out := Valuation{Currency: ccy}

	lines := map[string]*Line{}
	var order []string
	add := func(label string, gross, tax float64) {
		l, ok := lines[label]
		if !ok {
			l = &Line{Label: label}
			lines[label] = l
			order = append(order, label)
		}
		l.Gross += gross
		l.Tax += tax
	}
	perAccount := map[domain.AccountID]float64{}

	label := func(acc *domain.Account, asset *domain.Asset) string {
		if v.byAccount {
			return acc.Name
		}
		return scope.lineLabel(acc, asset)
	}

	// 1. positions titres
	for _, h := range Holdings(b, at) {
		if h.Asset.Kind == domain.Property || !scope.hasAsset(h.Account, h.Asset) {
			continue // les biens sont valorisés par relevés (section 2)
		}
		gross, err := v.positionValue(h)
		if err != nil {
			return out, err
		}
		tax, err := v.positionTax(h.Account, h.Asset, gross)
		if err != nil {
			return out, err
		}
		add(label(h.Account, h.Asset), gross, tax)
		perAccount[h.Account.ID] += gross
	}

	// 2. biens, valorisés par relevés
	for _, p := range statementPairs(b, at) {
		if p.asset.Kind != domain.Property || !scope.hasAsset(p.account, p.asset) {
			continue
		}
		gross, err := v.statementValue(p.account.ID, p.asset)
		if err != nil {
			return out, err
		}
		tax, err := v.propertyTax(p.account, p.asset, gross)
		if err != nil {
			return out, err
		}
		add(label(p.account, p.asset), gross, tax)
		perAccount[p.account.ID] += gross
	}

	// 3. liquidités des comptes suivis
	for _, acc := range b.Accounts {
		if !scope.hasCash(acc) || !CashTracked(b, acc.ID) {
			continue
		}
		gross, err := v.cashValue(acc)
		if err != nil {
			return out, err
		}
		if gross == 0 {
			continue
		}
		tax := 0.0
		if acc.Tax.Mode == domain.TaxOnValue {
			tax = gross * rate(acc.Tax)
		}
		add(label(acc, nil), gross, tax)
		perAccount[acc.ID] += gross
	}

	for _, label := range order {
		l := lines[label]
		l.Net = l.Gross - l.Tax
		out.Lines = append(out.Lines, *l)
		out.Gross += l.Gross
		out.Tax += l.Tax
	}
	// All/Account : l'impôt total exact est celui de la règle d'enveloppe
	if scope.Kind == All || scope.Kind == ByAccount {
		exact := 0.0
		for accID, gross := range perAccount {
			acc, err := b.Account(string(accID))
			if err != nil {
				continue
			}
			t, err := v.accountTax(acc, gross)
			if err != nil {
				return out, err
			}
			exact += t
		}
		if d := exact - out.Tax; d > 0.01 || d < -0.01 {
			out.TaxNote = "total tax follows the per-account rule; the per-line breakdown is approximate"
		}
		out.Tax = exact
	}
	out.Net = out.Gross - out.Tax
	out.Stale = v.stale
	return out, nil
}

type valuer struct {
	b         *domain.Book
	fx        FX
	at        domain.Date
	ccy       domain.Currency
	stale     []string
	byAccount bool
	overrides map[domain.AssetID]float64
}

// trimFloat formats a float64 without trailing zeros.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func rate(t domain.TaxRule) float64 { f, _ := t.Rate.Float64(); return f }
func toF(d decimal.Decimal) float64 { f, _ := d.Float64(); return f }

func (v *valuer) convertAt(m domain.Money, to domain.Currency, at domain.Date) (float64, error) {
	return v.fx.Convert(toF(m.Amount), m.Currency, to, at)
}

// positionValue: market close if a series exists, else last statement of the
// (account, asset) pair, else zero — each fallback flagged.
func (v *valuer) positionValue(h Holding) (float64, error) {
	if ov, ok := v.overrides[h.Asset.ID]; ok {
		v.stale = append(v.stale, fmt.Sprintf("what-if: %s at %s %s",
			h.Asset.Name, trimFloat(ov), h.Asset.Currency))
		return v.fx.Convert(toF(h.Qty)*ov, h.Asset.Currency, v.ccy, v.at)
	}
	if close, cdate, ok := v.b.Market.Prices[h.Asset.ID].At(v.at); ok {
		if cdate.AddDays(staleAfterDays).Before(v.at) {
			v.stale = append(v.stale, fmt.Sprintf("%s: last quote on %s", h.Asset.Name, cdate))
		}
		return v.fx.Convert(toF(h.Qty)*close, h.Asset.Currency, v.ccy, v.at)
	}
	if tx, ok := v.lastStatement(h.Account.ID, h.Asset.ID); ok {
		v.stale = append(v.stale, fmt.Sprintf("%s: valued from its %s statement", h.Asset.Name, tx.Date))
		return v.convertAt(tx.Amount, v.ccy, v.at)
	}
	v.stale = append(v.stale, fmt.Sprintf("%s: no quote nor statement — counted as 0", h.Asset.Name))
	return 0, nil
}

func (v *valuer) statementValue(acc domain.AccountID, asset *domain.Asset) (float64, error) {
	if ov, ok := v.overrides[asset.ID]; ok {
		v.stale = append(v.stale, fmt.Sprintf("what-if: %s at %s %s",
			asset.Name, trimFloat(ov), asset.Currency))
		return v.fx.Convert(ov, asset.Currency, v.ccy, v.at)
	}
	tx, ok := v.lastStatement(acc, asset.ID)
	if !ok {
		return 0, nil
	}
	return v.convertAt(tx.Amount, v.ccy, v.at)
}

func (v *valuer) lastStatement(acc domain.AccountID, asset domain.AssetID) (*domain.Transaction, bool) {
	var last *domain.Transaction
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Kind != domain.Statement || t.Account != acc || t.Asset != asset {
			continue
		}
		last = t
	}
	return last, last != nil
}

func (v *valuer) firstStatement(acc domain.AccountID, asset domain.AssetID) (*domain.Transaction, bool) {
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) {
			break
		}
		if t.Kind == domain.Statement && t.Account == acc && t.Asset == asset {
			return t, true
		}
	}
	return nil, false
}

// positionTax: per-position rule — TaxOnValue: value × rate; TaxOnGains:
// max(0, value − average-cost basis, flows converted at their date) × rate.
func (v *valuer) positionTax(acc *domain.Account, asset *domain.Asset, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		basis, err := v.positionBasis(acc.ID, asset.ID)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

// positionBasis replays the pair's trades as average cost, in display currency.
func (v *valuer) positionBasis(acc domain.AccountID, asset domain.AssetID) (float64, error) {
	var qty, basis float64
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc || t.Asset != asset {
			continue
		}
		switch t.Kind {
		case domain.Buy:
			amt, err := v.convertAt(t.Amount, v.ccy, t.Date)
			if err != nil {
				return 0, err
			}
			basis += amt
			qty += toF(t.Quantity)
		case domain.Sell:
			if qty <= 0 {
				continue
			}
			sold := min(toF(t.Quantity), qty)
			basis -= basis * sold / qty
			qty -= sold
		}
	}
	return basis, nil
}

// propertyTax: gains are measured from the FIRST known estimate.
func (v *valuer) propertyTax(acc *domain.Account, asset *domain.Asset, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		first, ok := v.firstStatement(acc.ID, asset.ID)
		if !ok {
			return 0, nil
		}
		basis, err := v.convertAt(first.Amount, v.ccy, first.Date)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

// accountTax: the exact envelope rule. TaxOnGains basis: net external
// contributions when cash is tracked, buys − sells otherwise (spec §3).
func (v *valuer) accountTax(acc *domain.Account, gross float64) (float64, error) {
	switch acc.Tax.Mode {
	case domain.TaxOnValue:
		return gross * rate(acc.Tax), nil
	case domain.TaxOnGains:
		basis, err := v.accountBasis(acc)
		if err != nil {
			return 0, err
		}
		return max(0, gross-basis) * rate(acc.Tax), nil
	}
	return 0, nil
}

func (v *valuer) accountBasis(acc *domain.Account) (float64, error) {
	tracked := CashTracked(v.b, acc.ID)
	basis := 0.0
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID {
			continue
		}
		sign := 0.0
		switch {
		case tracked && t.Kind == domain.Deposit:
			sign = 1
		case tracked && t.Kind == domain.Withdraw:
			sign = -1
		case !tracked && t.Kind == domain.Buy:
			sign = 1
		case !tracked && t.Kind == domain.Sell:
			sign = -1
		default:
			continue
		}
		amt, err := v.convertAt(t.Amount, v.ccy, t.Date)
		if err != nil {
			return 0, err
		}
		basis += sign * amt
	}
	// Les biens valorisés par relevés entrent dans la base par leur première
	// estimation connue — sinon une enveloppe immo serait taxée sur la valeur
	// totale et non la plus-value. Approximation documentée : si un apport
	// suivi a financé le bien ET que le bien a un relevé initial, la base
	// compte les deux (cas inhabituel, à corriger à la main si rencontré).
	for _, p := range statementPairs(v.b, v.at) {
		if p.account.ID != acc.ID || p.asset.Kind != domain.Property {
			continue
		}
		first, ok := v.firstStatement(acc.ID, p.asset.ID)
		if !ok {
			continue
		}
		amt, err := v.convertAt(first.Amount, v.ccy, first.Date)
		if err != nil {
			return 0, err
		}
		basis += amt
	}
	// Base négative (retraits > apports) : plafonnée à 0 — approximation v1,
	// la fiscalité réelle traite les sur-retraits proportionnellement.
	return max(0, basis), nil
}

// cashValue: anchor on the last cash statement ≤ at, then post-anchor flows
// converted to the account currency at their date, plus auto-dividends; the
// final balance converts to the display currency at `at`.
func (v *valuer) cashValue(acc *domain.Account) (float64, error) {
	var balance float64
	var anchor domain.Date
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID || t.Asset != "" || t.Kind != domain.Statement {
			continue
		}
		amt, err := v.convertAt(t.Amount, acc.Currency, t.Date)
		if err != nil {
			return 0, err
		}
		balance, anchor = amt, t.Date
	}
	for _, t := range Sorted(v.b) {
		if v.at.Before(t.Date) || t.Account != acc.ID {
			continue
		}
		if !anchor.IsZero() && !anchor.Before(t.Date) {
			continue // déjà inclus dans le relevé d'ancrage
		}
		sign := 0.0
		switch t.Kind {
		case domain.Deposit, domain.Sell, domain.Dividend:
			sign = 1
		case domain.Withdraw, domain.Buy, domain.Fee:
			sign = -1
		default:
			continue
		}
		amt, err := v.convertAt(t.Amount, acc.Currency, t.Date)
		if err != nil {
			return 0, err
		}
		balance += sign * amt
	}
	div, err := v.autoDividends(acc, anchor)
	if err != nil {
		return 0, err
	}
	balance += div
	return v.fx.Convert(balance, acc.Currency, v.ccy, v.at)
}

// autoDividends credits Yahoo-known distributions for assets without any
// manual Dividend transaction: quantity held at ex-date × gross amount.
func (v *valuer) autoDividends(acc *domain.Account, after domain.Date) (float64, error) {
	manual := manualDividendAssets(v.b)
	total := 0.0
	for id, events := range v.b.Market.Dividends {
		if manual[id] {
			continue
		}
		asset, err := v.b.Asset(string(id))
		if err != nil {
			continue
		}
		for _, ev := range events {
			if v.at.Before(ev.ExDate) {
				continue
			}
			if !after.IsZero() && !after.Before(ev.ExDate) {
				continue
			}
			qty := Quantity(v.b, acc.ID, id, ev.ExDate)
			if qty.IsZero() {
				continue
			}
			amt, err := v.fx.Convert(toF(qty)*ev.Amount*(1-asset.Withholding), asset.Currency, acc.Currency, ev.ExDate)
			if err != nil {
				return 0, err
			}
			total += amt
		}
	}
	return total, nil
}

func manualDividendAssets(b *domain.Book) map[domain.AssetID]bool {
	out := map[domain.AssetID]bool{}
	for _, t := range b.Transactions {
		if t.Kind == domain.Dividend && t.Asset != "" {
			out[t.Asset] = true
		}
	}
	return out
}

type pair struct {
	account *domain.Account
	asset   *domain.Asset
}

// statementPairs lists the distinct (account, asset) couples having at least
// one statement dated ≤ at, in first-seen order.
func statementPairs(b *domain.Book, at domain.Date) []pair {
	type key struct {
		acc   domain.AccountID
		asset domain.AssetID
	}
	seen := map[key]bool{}
	var out []pair
	for _, t := range Sorted(b) {
		if at.Before(t.Date) || t.Kind != domain.Statement || t.Asset == "" {
			continue
		}
		k := key{t.Account, t.Asset}
		if seen[k] {
			continue
		}
		seen[k] = true
		acc, errA := b.Account(string(t.Account))
		asset, errB := b.Asset(string(t.Asset))
		if errA != nil || errB != nil {
			continue
		}
		out = append(out, pair{acc, asset})
	}
	return out
}
