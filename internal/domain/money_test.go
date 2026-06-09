package domain

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseCurrency(t *testing.T) {
	for _, tc := range []struct {
		in  string
		ok  bool
		out Currency
	}{
		{"usd", true, USD},
		{" eur ", true, EUR},
		{"banana", false, ""},
		{"E1R", false, ""},
		{"", false, ""},
	} {
		got, err := ParseCurrency(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseCurrency(%q): err=%v, ok attendu=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.out {
			t.Errorf("ParseCurrency(%q) = %q, attendu %q", tc.in, got, tc.out)
		}
	}
}

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
