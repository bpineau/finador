package market

import (
	"fmt"

	"finador/internal/domain"
)

// Converter converts amounts between currencies by crossing through the USD,
// using the cached FX series (value of one unit in USD).
type Converter struct {
	FX map[domain.Currency]*domain.PriceSeries
}

// usdValue returns how many USD one unit of c is worth at d.
func (cv Converter) usdValue(c domain.Currency, d domain.Date) (float64, error) {
	if c == domain.USD {
		return 1, nil
	}
	rate, _, ok := cv.FX[c].At(d) // At est nil-safe
	if !ok {
		return 0, fmt.Errorf("missing %s exchange rate on %s — run 'finador refresh'", c, d)
	}
	return rate, nil
}

// Rate returns the multiplier turning an amount in from into to, at date d.
func (cv Converter) Rate(from, to domain.Currency, d domain.Date) (float64, error) {
	if from == to {
		return 1, nil
	}
	f, err := cv.usdValue(from, d)
	if err != nil {
		return 0, err
	}
	t, err := cv.usdValue(to, d)
	if err != nil {
		return 0, err
	}
	return f / t, nil
}

func (cv Converter) Convert(amount float64, from, to domain.Currency, d domain.Date) (float64, error) {
	rate, err := cv.Rate(from, to, d)
	if err != nil {
		return 0, err
	}
	return amount * rate, nil
}
