package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseTaxRule(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		mode TaxMode
		rate string // expected decimal rate
	}{
		{"none", true, TaxNone, "0"},
		{"", true, TaxNone, "0"},
		{"gains:17.2%", true, TaxOnGains, "0.172"},
		{"value:20%", true, TaxOnValue, "0.2"},
		{"gains:30", true, TaxOnGains, "0.3"}, // the % is optional
		{"plusvalue:30%", false, 0, ""},
		{"gains:abc%", false, 0, ""},
		{"gains:-5%", false, 0, ""},
		{"value:250%", false, 0, ""},
	} {
		r, err := ParseTaxRule(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseTaxRule(%q): err=%v, ok attendu=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && (r.Mode != tc.mode || !r.Rate.Equal(decimal.RequireFromString(tc.rate))) {
			t.Errorf("ParseTaxRule(%q) = %+v", tc.in, r)
		}
	}
}

func TestTaxRuleRoundTrip(t *testing.T) {
	for _, s := range []string{"none", "gains:17.2%", "value:20%"} {
		r, err := ParseTaxRule(s)
		if err != nil {
			t.Fatalf("ParseTaxRule(%q): %v", s, err)
		}
		if r.String() != s {
			t.Errorf("String() = %q, attendu %q", r.String(), s)
		}
	}
}
