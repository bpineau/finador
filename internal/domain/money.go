package domain

import "github.com/shopspring/decimal"

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
