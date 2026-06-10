package web

import (
	"testing"

	"finador/internal/domain"
)

func TestFmtMoneyCarryBoundaries(t *testing.T) {
	// the separator before the currency symbol is U+00A0 NO-BREAK SPACE
	const nb = " "
	for v, want := range map[float64]string{
		0.995:      "1.00" + nb + "€",
		0.999:      "1.00" + nb + "€",
		999.995:    "1,000.00" + nb + "€",
		1.996:      "2.00" + nb + "€",
		5100:       "5,100.00" + nb + "€",
		1234567.89: "1,234,567.89" + nb + "€",
		-4230.5:    "−4,230.50" + nb + "€",
		0:          "0.00" + nb + "€",
	} {
		if got := fmtMoney(v, domain.EUR); got != want {
			t.Errorf("fmtMoney(%v) = %q, want %q", v, got, want)
		}
	}
}
