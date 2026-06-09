package domain

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestMoneyString(t *testing.T) {
	m := Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}
	if got := m.String(); got != "5500.5 EUR" {
		t.Errorf("String() = %q", got)
	}
}

func TestMoneyJSON(t *testing.T) {
	m := Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}
	raw, err := json.Marshal(m)
	if err != nil || string(raw) != `{"amount":"5500.5","ccy":"EUR"}` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var back Money
	if err := json.Unmarshal(raw, &back); err != nil || !back.Amount.Equal(m.Amount) || back.Currency != EUR {
		t.Fatalf("unmarshal = %+v, err=%v", back, err)
	}
}
