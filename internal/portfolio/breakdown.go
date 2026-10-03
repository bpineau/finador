package portfolio

import (
	"slices"

	"finador/internal/domain"
)

// PositionLine is one valued position - or one envelope's cash when Asset is
// nil. The raw material of the web's hierarchical allocation trees. Net is the
// after-tax value under the per-position rule (a documented approximation: the
// exact net follows the per-envelope rule, see Value).
type PositionLine struct {
	Account    *domain.Account
	Asset      *domain.Asset
	Gross, Net float64
	// Note labels a price that is an estimate (see Prices), to be shown
	// wherever the line is: empty for a published price.
	Note string
}

// Breakdown values every security position, property and declared cash at
// `at`, in the display currency. Σ Gross equals Value(All).Gross.
func Breakdown(b *domain.Book, at domain.Date, ccy domain.Currency, fx FX) ([]PositionLine, error) {
	v := newValuer(b, fx, at, ccy)
	var out []PositionLine
	for _, h := range Holdings(b, at) {
		if h.Asset.Kind == domain.Property {
			continue // statement-valued below
		}
		gross, err := v.positionValue(h)
		if err != nil {
			return nil, err
		}
		tax, err := v.positionTax(h.Account, h.Asset, gross)
		if err != nil {
			return nil, err
		}
		out = append(out, PositionLine{Account: h.Account, Asset: h.Asset, Gross: gross, Net: gross - tax,
			Note: v.estimateNote(h.Asset.ID)})
	}
	for _, p := range statementPairs(b, at) {
		if p.asset.Kind != domain.Property {
			continue
		}
		gross, err := v.statementValue(p.account.ID, p.asset)
		if err != nil {
			return nil, err
		}
		tax, err := v.propertyTax(p.account, p.asset, gross)
		if err != nil {
			return nil, err
		}
		out = append(out, PositionLine{Account: p.account, Asset: p.asset, Gross: gross, Net: gross - tax})
	}
	for _, acc := range b.Accounts {
		gross, err := v.cashValue(acc)
		if err != nil {
			return nil, err
		}
		if gross != 0 {
			net := gross
			if acc.Tax.Mode == domain.TaxOnValue {
				net = gross - gross*rate(acc.Tax)
			}
			out = append(out, PositionLine{Account: acc, Gross: gross, Net: net})
		}
	}
	return out, nil
}

// Notes returns the distinct notes of lines, in first-seen order: an asset
// held in several envelopes is one estimate, said once.
func Notes(lines []PositionLine) []string {
	var out []string
	for _, l := range lines {
		if l.Note != "" && !slices.Contains(out, l.Note) {
			out = append(out, l.Note)
		}
	}
	return out
}
