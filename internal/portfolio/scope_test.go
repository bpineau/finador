package portfolio

import (
	"testing"

	"finador/internal/domain"
)

func TestScopeExclusions(t *testing.T) {
	b := valuationBook(t)
	scope := scopeOf(t, b, "actions")
	cw8, _ := b.Asset("cw8")
	pea, _ := b.Account("pea")
	if !scope.HasAsset(pea, cw8) {
		t.Fatal("cw8 devrait être dans actions")
	}
	scope.Excluded = map[domain.AssetID]bool{"cw8": true}
	if scope.HasAsset(pea, cw8) {
		t.Fatal("cw8 exclu devrait être filtré")
	}
	// la valorisation respecte l'exclusion
	v, err := Value(b, scope, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross sans cw8", v.Gross, 0) // actions ne contient que cw8 dans la fixture
}
