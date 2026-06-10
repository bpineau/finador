package domain

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type Currency string

const (
	EUR Currency = "EUR"
	USD Currency = "USD"
)

// Money is an exact amount in a given currency. Ledger amounts are always
// decimal; performance math (phase C) works on float64 instead.
type Money struct {
	Amount   decimal.Decimal `json:"amount"`
	Currency Currency        `json:"ccy"`
}

func (m Money) String() string { return m.Amount.String() + " " + string(m.Currency) }

// ParseCurrency normalizes a currency code: 3 ASCII letters, upper-cased.
func ParseCurrency(s string) (Currency, error) {
	c := strings.ToUpper(strings.TrimSpace(s))
	if len(c) != 3 || strings.ContainsFunc(c, func(r rune) bool { return r < 'A' || r > 'Z' }) {
		return "", fmt.Errorf("invalid currency %q (expected a 3-letter ISO code, e.g. EUR)", s)
	}
	return Currency(c), nil
}
