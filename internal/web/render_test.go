package web

import (
	"testing"

	"finador/internal/domain"
)

func TestFrMoneyCarryBoundaries(t *testing.T) {
	for v, want := range map[float64]string{
		0.995:      "1,00 €",
		0.999:      "1,00 €",
		999.995:    "1 000,00 €",
		1.996:      "2,00 €",
		5100:       "5 100,00 €",
		1234567.89: "1 234 567,89 €",
		-4230.5:    "−4 230,50 €",
		0:          "0,00 €",
	} {
		if got := frMoney(v, domain.EUR); got != want {
			t.Errorf("frMoney(%v) = %q, attendu %q", v, got, want)
		}
	}
}
