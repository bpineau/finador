package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestMoneyString(t *testing.T) {
	m := Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}
	if got := m.String(); got != "5500.5 EUR" {
		t.Errorf("String() = %q", got)
	}
}
