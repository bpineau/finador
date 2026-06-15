package domain

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type TaxMode uint8

const (
	TaxNone TaxMode = iota
	TaxOnGains
	TaxOnValue
)

// TaxRule estimates the latent tax of an envelope.
// TaxOnGains taxes the value beyond the contribution basis (PEA, CTO, AV);
// TaxOnValue taxes the whole value (PER deducted at entry).
type TaxRule struct {
	Mode TaxMode
	Rate decimal.Decimal // 0.172 for 17.2%
}

var hundred = decimal.NewFromInt(100)

// ParseTaxRule reads "none", "gains:17.2%" or "value:20%" (the % is optional).
func ParseTaxRule(s string) (TaxRule, error) {
	if s == "" || s == "none" {
		return TaxRule{}, nil
	}
	mode, pct, ok := strings.Cut(s, ":")
	if !ok {
		return TaxRule{}, fmt.Errorf("invalid tax rule %q: expected none, gains:N%% or value:N%%", s)
	}
	rate, err := decimal.NewFromString(strings.TrimSuffix(pct, "%"))
	if err != nil {
		return TaxRule{}, fmt.Errorf("invalid tax rule %q: invalid rate: %w", s, err)
	}
	if rate.IsNegative() || rate.GreaterThan(hundred) {
		return TaxRule{}, fmt.Errorf("invalid tax rule %q: rate outside [0%%, 100%%]", s)
	}
	rule := TaxRule{Rate: rate.Div(hundred)}
	switch mode {
	case "gains":
		rule.Mode = TaxOnGains
	case "value":
		rule.Mode = TaxOnValue
	default:
		return TaxRule{}, fmt.Errorf("invalid tax rule %q: unknown mode %q", s, mode)
	}
	return rule, nil
}

func (r TaxRule) String() string {
	pct := r.Rate.Mul(hundred).String() + "%"
	switch r.Mode {
	case TaxOnGains:
		return "gains:" + pct
	case TaxOnValue:
		return "value:" + pct
	default:
		return "none"
	}
}

func (r TaxRule) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

func (r *TaxRule) UnmarshalText(b []byte) error {
	parsed, err := ParseTaxRule(string(b))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
