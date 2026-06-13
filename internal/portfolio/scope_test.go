package portfolio

import (
	"testing"

	"finador/internal/domain"
)

func labelBook(t *testing.T) *domain.Book {
	t.Helper()
	b := valuationBook(t)
	_ = b.AddLabel(&domain.Label{
		ID: "lbl1", Account: "pea", Asset: "cw8", Name: "retraite",
	})
	_ = b.AddLabel(&domain.Label{
		ID: "lbl2", Account: "cto", Asset: "cw8", Name: "retraite",
	})
	return b
}

func TestLabelScopeBuildsSet(t *testing.T) {
	b := labelBook(t)
	s, err := LabelScope(b, "retraite")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != ByLabel {
		t.Fatalf("Kind = %v, want ByLabel", s.Kind)
	}
	if len(s.Pairs) != 2 {
		t.Fatalf("Pairs = %d, want 2", len(s.Pairs))
	}
	if s.Label != "retraite" {
		t.Fatalf("Label = %q", s.Label)
	}
}

func TestLabelScopeUnknownLabel(t *testing.T) {
	b := labelBook(t)
	if _, err := LabelScope(b, "unknown"); err == nil {
		t.Fatal("expected error for unknown label")
	}
}

func TestLabelScopeCaseInsensitive(t *testing.T) {
	b := labelBook(t)
	if _, err := LabelScope(b, "RETRAITE"); err != nil {
		t.Fatal(err)
	}
}

func TestLabelScopeHasAsset(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	pea, _ := b.Account("pea")
	cto, _ := b.Account("cto")
	cw8, _ := b.Asset("cw8")

	if !s.HasAsset(pea, cw8) {
		t.Error("pea/cw8 should be in retraite scope")
	}
	if !s.HasAsset(cto, cw8) {
		t.Error("cto/cw8 should be in retraite scope")
	}
	// A pair not in the label set returns false
	maison, _ := b.Asset("maison")
	immo, _ := b.Account("immo")
	if s.HasAsset(immo, maison) {
		t.Error("immo/maison should not be in retraite scope")
	}
}

func TestLabelScopeExcludedWins(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	s.Excluded = map[domain.AssetID]bool{"cw8": true}
	pea, _ := b.Account("pea")
	cw8, _ := b.Asset("cw8")
	if s.HasAsset(pea, cw8) {
		t.Error("Excluded should override label membership")
	}
}

func TestLabelScopeHasCashFalse(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	pea, _ := b.Account("pea")
	if s.HasCash(pea) {
		t.Error("ByLabel scope must not include cash")
	}
}

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
