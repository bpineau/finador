package portfolio

import "finador/internal/domain"

// PositionLine is one valued position - or one envelope's cash when Asset is
// nil. The raw material of the web's hierarchical allocation trees.
type PositionLine struct {
	Account *domain.Account
	Asset   *domain.Asset
	Gross   float64
}

// Breakdown values every security position, property and tracked cash at
// `at`, in the display currency. Σ Gross equals Value(All).Gross.
func Breakdown(b *domain.Book, at domain.Date, ccy domain.Currency, fx FX) ([]PositionLine, error) {
	v := &valuer{b: b, fx: fx, at: at, ccy: ccy}
	var out []PositionLine
	for _, h := range Holdings(b, at) {
		if h.Asset.Kind == domain.Property {
			continue // statement-valued below
		}
		gross, err := v.positionValue(h)
		if err != nil {
			return nil, err
		}
		out = append(out, PositionLine{Account: h.Account, Asset: h.Asset, Gross: gross})
	}
	for _, p := range statementPairs(b, at) {
		if p.asset.Kind != domain.Property {
			continue
		}
		gross, err := v.statementValue(p.account.ID, p.asset)
		if err != nil {
			return nil, err
		}
		out = append(out, PositionLine{Account: p.account, Asset: p.asset, Gross: gross})
	}
	for _, acc := range b.Accounts {
		if !CashTracked(b, acc.ID) {
			continue
		}
		gross, err := v.cashValue(acc)
		if err != nil {
			return nil, err
		}
		if gross != 0 {
			out = append(out, PositionLine{Account: acc, Gross: gross})
		}
	}
	return out, nil
}
