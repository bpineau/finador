package portfolio

import (
	"errors"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
)

// SeriesPoint is one day of a scope's value.
type SeriesPoint struct {
	Date       domain.Date
	Gross, Net float64
}

// ExternalFlow is money entering (>0) or leaving (<0) the scope, in display
// currency — what TWR neutralizes and XIRR consumes.
type ExternalFlow struct {
	Date   domain.Date
	Amount float64
}

// SeriesResult bundles the daily curve with the scope's external flows.
type SeriesResult struct {
	Points []SeriesPoint
	Flows  []ExternalFlow
}

// Series walks the ledger once and produces the daily value of the scope
// between from and to. Same cash/tax rules as Value(); days lacking price or
// FX data contribute zero (a curve must stay drawable). A zero `from`
// defaults to the first transaction.
func Series(b *domain.Book, scope Scope, from, to domain.Date, ccy domain.Currency, fx FX) (SeriesResult, error) {
	txs := Sorted(b)
	if from.IsZero() {
		if len(txs) == 0 {
			return SeriesResult{}, errors.New("aucune transaction : rien à tracer")
		}
		from = txs[0].Date
	}
	if to.Before(from) {
		return SeriesResult{}, errors.New("borne de fin antérieure au début")
	}

	w := newWalker(b, scope, ccy, fx)
	var out SeriesResult
	ti := 0
	for d := from; !to.Before(d); d = d.AddDays(1) {
		// Apply all transactions up to and including day d.
		// Transactions strictly after from are collected as flows.
		for ti < len(txs) && !d.Before(txs[ti].Date) {
			collect := from.Before(txs[ti].Date) // strictly after from → collect as flow
			w.applyTx(txs[ti], collect)
			ti++
		}
		w.applyDividends(d, from.Before(d))
		gross, net := w.valueAt(d)
		out.Points = append(out.Points, SeriesPoint{Date: d, Gross: gross, Net: net})
	}
	out.Flows = w.flows
	return out, nil
}

// walker carries the incremental replay state.
type walker struct {
	b     *domain.Book
	scope Scope
	ccy   domain.Currency
	fx    FX

	pairs    map[pairKey]*pairState
	order    []pairKey
	accounts map[domain.AccountID]*accountState
	manual   map[domain.AssetID]bool
	flows    []ExternalFlow
}

type pairKey struct {
	acc   domain.AccountID
	asset domain.AssetID
}

type pairState struct {
	acc   *domain.Account
	asset *domain.Asset
	qty   float64
	basis float64 // average cost in display currency, flows converted at their date

	// for property: statement value (last seen) and first estimate
	stmt   *domain.Money
	first  float64 // first statement converted to display ccy at its date
	hasFst bool
}

type accountState struct {
	acc       *domain.Account
	tracked   bool
	cash      float64 // balance in account currency, anchored on last Statement
	anchor    domain.Date
	flowBasis float64 // envelope basis in display currency (deposits - withdrawals)
}

func newWalker(b *domain.Book, scope Scope, ccy domain.Currency, fx FX) *walker {
	w := &walker{
		b: b, scope: scope, ccy: ccy, fx: fx,
		pairs:    map[pairKey]*pairState{},
		accounts: map[domain.AccountID]*accountState{},
		manual:   manualDividendAssets(b),
	}
	for _, acc := range b.Accounts {
		w.accounts[acc.ID] = &accountState{acc: acc, tracked: CashTracked(b, acc.ID)}
	}
	return w
}

func (w *walker) pair(t *domain.Transaction) *pairState {
	k := pairKey{t.Account, t.Asset}
	if p, ok := w.pairs[k]; ok {
		return p
	}
	acc, errA := w.b.Account(string(t.Account))
	asset, errB := w.b.Asset(string(t.Asset))
	if errA != nil || errB != nil {
		return nil // orphaned reference: skip
	}
	p := &pairState{acc: acc, asset: asset}
	w.pairs[k] = p
	w.order = append(w.order, k)
	return p
}

// conv converts a Money to display currency at a date; returns 0 on failure
// (series semantics: missing FX → contribute 0, don't fail).
func (w *walker) conv(m domain.Money, to domain.Currency, at domain.Date) float64 {
	v, err := w.fx.Convert(toF(m.Amount), m.Currency, to, at)
	if err != nil {
		return 0
	}
	return v
}

// convF converts a float amount from one currency to another; returns 0 on failure.
func (w *walker) convF(amount float64, from, to domain.Currency, at domain.Date) float64 {
	v, err := w.fx.Convert(amount, from, to, at)
	if err != nil {
		return 0
	}
	return v
}

func (w *walker) addFlow(d domain.Date, amount float64, collect bool) {
	if collect && amount != 0 {
		w.flows = append(w.flows, ExternalFlow{Date: d, Amount: amount})
	}
}

func (w *walker) applyTx(t *domain.Transaction, collect bool) {
	acc := w.accounts[t.Account]
	if acc == nil {
		return
	}
	inCash := w.scope.hasCash(acc.acc)

	switch t.Kind {
	case domain.Buy, domain.Sell:
		p := w.pair(t)
		if p == nil {
			return
		}
		disp := w.conv(t.Amount, w.ccy, t.Date)
		sign := 1.0
		if t.Kind == domain.Sell {
			sign = -1
		}

		// Update position state (not for property — property stays statement-valued)
		if p.asset.Kind != domain.Property {
			if t.Kind == domain.Buy {
				p.basis += disp
				p.qty += toF(t.Quantity)
			} else if p.qty > 0 {
				sold := min(toF(t.Quantity), p.qty)
				p.basis -= p.basis * sold / p.qty
				p.qty -= sold
			}
		}

		// Update cash balance for tracked accounts
		if acc.tracked {
			// Buy reduces cash, Sell adds cash (in account currency)
			cashAmt := w.conv(t.Amount, acc.acc.Currency, t.Date)
			if t.Kind == domain.Buy {
				acc.cash -= cashAmt
			} else {
				acc.cash += cashAmt
			}
		} else {
			// untracked: accumulate envelope basis for tax purposes
			acc.flowBasis += sign * disp
		}

		// Determine if this is an external flow
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
			if w.scope.hasAsset(acc.acc, p.asset) {
				w.addFlow(t.Date, sign*disp, collect)
			}
		default: // All, ByAccount
			if inCash && !acc.tracked {
				w.addFlow(t.Date, sign*disp, collect)
			}
		}

	case domain.Deposit, domain.Withdraw:
		sign := 1.0
		if t.Kind == domain.Withdraw {
			sign = -1
		}
		disp := w.conv(t.Amount, w.ccy, t.Date)
		cashAmt := w.conv(t.Amount, acc.acc.Currency, t.Date)
		acc.cash += sign * cashAmt
		acc.flowBasis += sign * disp
		if inCash {
			w.addFlow(t.Date, sign*disp, collect)
		}

	case domain.Dividend:
		p := w.pair(t)
		disp := w.conv(t.Amount, w.ccy, t.Date)
		if acc.tracked {
			cashAmt := w.conv(t.Amount, acc.acc.Currency, t.Date)
			acc.cash += cashAmt
		}
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
			if p != nil && w.scope.hasAsset(acc.acc, p.asset) {
				w.addFlow(t.Date, -disp, collect) // dividend leaves the pocket
			}
		default:
			if inCash && !acc.tracked {
				w.addFlow(t.Date, -disp, collect) // revenue collected outside scope
			}
		}

	case domain.Fee:
		if acc.tracked {
			cashAmt := w.conv(t.Amount, acc.acc.Currency, t.Date)
			acc.cash -= cashAmt
		}
		// never a flow: a cost must weigh on performance

	case domain.Statement:
		if t.Asset == "" {
			// Pure cash statement: anchor the balance
			if acc.tracked {
				cashAmt := w.conv(t.Amount, acc.acc.Currency, t.Date)
				acc.cash = cashAmt
				acc.anchor = t.Date
			}
			return
		}
		p := w.pair(t)
		if p == nil {
			return
		}
		m := t.Amount
		p.stmt = &m
		if !p.hasFst && p.asset.Kind == domain.Property {
			p.first = w.conv(t.Amount, w.ccy, t.Date)
			p.hasFst = true
		}
	}
}

// applyDividends credits the day's automatic dividends (assets without any
// manual Dividend tx) and emits the matching scope flows.
func (w *walker) applyDividends(d domain.Date, collect bool) {
	for _, k := range w.order {
		p := w.pairs[k]
		if p.qty <= 0 || w.manual[p.asset.ID] {
			continue
		}
		for _, ev := range w.b.Market.Dividends[p.asset.ID] {
			if ev.ExDate != d {
				continue
			}
			gross := domain.Money{
				Amount:   decimal.NewFromFloat(p.qty * ev.Amount),
				Currency: p.asset.Currency,
			}
			disp := w.conv(gross, w.ccy, d)
			acc := w.accounts[p.acc.ID]
			if acc.tracked {
				cashAmt := w.conv(gross, acc.acc.Currency, d)
				acc.cash += cashAmt
			}
			switch {
			case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
				if w.scope.hasAsset(p.acc, p.asset) {
					w.addFlow(d, -disp, collect)
				}
			default:
				if w.scope.hasCash(acc.acc) && !acc.tracked {
					w.addFlow(d, -disp, collect)
				}
			}
		}
	}
}

// valueAt prices the current state at day d, with the same tax rules as
// Value(): envelope-exact for All/Account, per-position for Group/Asset.
func (w *walker) valueAt(d domain.Date) (gross, net float64) {
	perAccount := map[domain.AccountID]float64{}

	// 1. Security and property positions
	for _, k := range w.order {
		p := w.pairs[k]
		if !w.scope.hasAsset(p.acc, p.asset) {
			continue
		}
		var val float64
		switch {
		case p.asset.Kind == domain.Property:
			// Property: valued by last statement
			if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
		default:
			// Security: market price with forward-fill, fall back to last statement
			if p.qty <= 0 {
				break
			}
			if close, _, ok := w.b.Market.Prices[p.asset.ID].At(d); ok {
				val = w.convF(p.qty*close, p.asset.Currency, w.ccy, d)
			} else if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
			// else: no price yet → val stays 0 (series semantics)
		}
		gross += val
		perAccount[k.acc] += val
	}

	// 2. Cash balances for tracked accounts in scope
	for accID, accSt := range w.accounts {
		if !w.scope.hasCash(accSt.acc) || !accSt.tracked {
			continue
		}
		// acc.cash is in account currency; convert to display currency at d
		v := w.convF(accSt.cash, accSt.acc.Currency, w.ccy, d)
		if v == 0 {
			continue
		}
		gross += v
		perAccount[accID] += v
	}

	// 3. Compute tax
	positionTax := 0.0
	for _, k := range w.order {
		p := w.pairs[k]
		if !w.scope.hasAsset(p.acc, p.asset) {
			continue
		}
		// Recompute val for tax (same logic as above)
		var val float64
		switch {
		case p.asset.Kind == domain.Property:
			if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
		default:
			if p.qty <= 0 {
				break
			}
			if close, _, ok := w.b.Market.Prices[p.asset.ID].At(d); ok {
				val = w.convF(p.qty*close, p.asset.Currency, w.ccy, d)
			} else if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
		}
		accSt := w.accounts[p.acc.ID]
		switch accSt.acc.Tax.Mode {
		case domain.TaxOnValue:
			positionTax += val * rate(accSt.acc.Tax)
		case domain.TaxOnGains:
			basis := p.basis
			if p.asset.Kind == domain.Property {
				basis = p.first
			}
			positionTax += max(0, val-basis) * rate(accSt.acc.Tax)
		}
	}
	// Also add TaxOnValue for cash
	for _, accSt := range w.accounts {
		if !w.scope.hasCash(accSt.acc) || !accSt.tracked {
			continue
		}
		if accSt.acc.Tax.Mode == domain.TaxOnValue {
			v := w.convF(accSt.cash, accSt.acc.Currency, w.ccy, d)
			positionTax += v * rate(accSt.acc.Tax)
		}
	}

	tax := positionTax
	if w.scope.Kind == All || w.scope.Kind == ByAccount {
		// Envelope-exact tax rule: compute per account
		tax = 0
		for accID, g := range perAccount {
			accSt := w.accounts[accID]
			switch accSt.acc.Tax.Mode {
			case domain.TaxOnValue:
				tax += g * rate(accSt.acc.Tax)
			case domain.TaxOnGains:
				basis := accSt.flowBasis
				// Add first-statement basis for property assets in this account
				for _, k := range w.order {
					p := w.pairs[k]
					if p.acc.ID == accID && p.asset.Kind == domain.Property && p.hasFst {
						basis += p.first
					}
				}
				tax += max(0, g-max(0, basis)) * rate(accSt.acc.Tax)
			}
		}
	}

	return gross, gross - tax
}
