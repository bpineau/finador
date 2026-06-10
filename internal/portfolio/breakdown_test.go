package portfolio

import (
	"testing"

	"finador/internal/domain"
)

func TestBreakdownSumsToValue(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	lines, err := Breakdown(b, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	total := 0.0
	for _, l := range lines {
		total += l.Gross
	}
	want, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "Σ breakdown = Value", total, want.Gross)
	// chaque position porte compte ET actif ; le cash a Asset nil
	var cashLines, positions int
	for _, l := range lines {
		if l.Account == nil {
			t.Fatalf("ligne sans compte: %+v", l)
		}
		if l.Asset == nil {
			cashLines++
		} else {
			positions++
		}
	}
	if cashLines != 2 { // pea et livret sont suivis ; cto non
		t.Errorf("cashLines = %d, attendu 2", cashLines)
	}
	if positions != 3 { // pea/cw8, cto/cw8, immo/maison
		t.Errorf("positions = %d, attendu 3", positions)
	}
}

func TestIntersectScope(t *testing.T) {
	b := valuationBook(t)
	pea, _ := b.Account("pea")
	s := IntersectScope(pea, "actions")
	at := mustDate("2026-06-05")
	v, err := Value(b, s, at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// seulement la position cw8 du PEA : 12 × 560 — ni le cash, ni le cw8 du CTO
	approx(t, "gross pea∩actions", v.Gross, 6720)
	// les séries fonctionnent sur la même portée (perfs/courbes des vues croisées)
	res, err := Series(b, s, mustDate("2026-01-01"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	approx(t, "série pea∩actions", last.Gross, 6720)
}
