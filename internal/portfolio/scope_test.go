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
	// valuation respects the exclusion
	v, err := Value(b, scope, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross sans cw8", v.Gross, 0) // in the fixture, actions only contains cw8
}

func TestPairScope(t *testing.T) {
	b := valuationBook(t)
	pea, _ := b.Account("pea")
	cto, _ := b.Account("cto")
	cw8, _ := b.Asset("cw8")
	maison, _ := b.Asset("maison")

	s := PairScope(pea, cw8)
	if !s.HasAsset(pea, cw8) {
		t.Error("PairScope must keep its own pair")
	}
	if s.HasAsset(cto, cw8) || s.HasAsset(pea, maison) {
		t.Error("PairScope must reject other accounts and assets")
	}
	if s.HasCash(pea) {
		t.Error("PairScope never keeps cash (the envelope row owns it)")
	}
}

func TestEnvelopeScope(t *testing.T) {
	b := valuationBook(t)
	pea, _ := b.Account("pea")
	cto, _ := b.Account("cto")
	immo, _ := b.Account("immo")
	cw8, _ := b.Asset("cw8")
	maison, _ := b.Asset("maison")

	// All scope restricted to one account: its assets AND its cash.
	all := Scope{Kind: All, Label: "portfolio"}
	s := EnvelopeScope(all, pea)
	if !s.HasAsset(pea, cw8) || !s.HasCash(pea) {
		t.Error("EnvelopeScope(All) must keep the account's assets and cash")
	}
	if s.HasAsset(cto, cw8) || s.HasCash(cto) {
		t.Error("EnvelopeScope(All) must reject other accounts")
	}

	// Group scope restricted to one account: only that group, no cash.
	grp := scopeOf(t, b, "actions")
	s = EnvelopeScope(grp, pea)
	if !s.HasAsset(pea, cw8) {
		t.Error("EnvelopeScope(ByGroup) must keep the group's assets in the account")
	}
	if s.HasAsset(immo, maison) || s.HasCash(pea) {
		t.Error("EnvelopeScope(ByGroup) must reject other groups and cash")
	}

	// Excluded assets carry through.
	all.Excluded = map[domain.AssetID]bool{"cw8": true}
	if EnvelopeScope(all, pea).HasAsset(pea, cw8) {
		t.Error("EnvelopeScope must carry Excluded through")
	}

	// Label scope restricted to one account: only that account's pairs.
	lb := labelBook(t)
	lpea, _ := lb.Account("pea")
	lcto, _ := lb.Account("cto")
	lcw8, _ := lb.Asset("cw8")
	ls, _ := LabelScope(lb, "retraite")
	s = EnvelopeScope(ls, lpea)
	if !s.HasAsset(lpea, lcw8) || s.HasAsset(lcto, lcw8) {
		t.Error("EnvelopeScope(ByLabel) must keep only the account's labelled pairs")
	}
}

func TestFilterScope(t *testing.T) {
	b := valuationBook(t)
	pea, _ := b.Account("pea")
	livret, _ := b.Account("livret")
	cw8, _ := b.Asset("cw8")
	maison, _ := b.Asset("maison")

	lines := []PositionLine{
		{Account: pea, Asset: cw8, Gross: 100, Net: 90},
		{Account: pea, Gross: 50, Net: 50}, // pea cash
		{Account: livret, Gross: 20, Net: 20},
		{Account: pea, Asset: maison, Gross: 400, Net: 300},
	}

	got := FilterScope(lines, Scope{Kind: ByAccount, Account: pea})
	if len(got) != 3 { // cw8 + cash + maison, not the livret cash
		t.Fatalf("ByAccount kept %d lines, want 3", len(got))
	}

	grp := scopeOf(t, b, "actions")
	got = FilterScope(lines, grp)
	if len(got) != 1 || got[0].Asset != cw8 {
		t.Fatalf("ByGroup kept %v, want the single cw8 line", got)
	}

	excl := Scope{Kind: All, Excluded: map[domain.AssetID]bool{"cw8": true}}
	got = FilterScope(lines, excl)
	if len(got) != 3 {
		t.Fatalf("Excluded kept %d lines, want 3 (cw8 dropped)", len(got))
	}
}

func TestScopeOnlyFiltersAssetsAndDropsCash(t *testing.T) {
	acc := &domain.Account{ID: "acc1", Name: "PEA Zephyr", Currency: domain.EUR}
	kept := &domain.Asset{ID: "a1", Kind: domain.Security, Name: "CW8", Currency: domain.EUR}
	dropped := &domain.Asset{ID: "a2", Kind: domain.Security, Name: "AAPL", Currency: domain.EUR}

	s := Scope{Kind: All, Label: "portfolio", Only: map[domain.AssetID]bool{kept.ID: true}}
	if !s.HasAsset(acc, kept) {
		t.Error("an asset named by --asset must stay in scope")
	}
	if s.HasAsset(acc, dropped) {
		t.Error("an asset not named by --asset must leave the scope")
	}
	if s.HasCash(acc) {
		t.Error("an asset filter excludes cash: cash is not an asset")
	}

	// --exclude still wins: the two flags may name the same asset.
	s.Excluded = map[domain.AssetID]bool{kept.ID: true}
	if s.HasAsset(acc, kept) {
		t.Error("--exclude must win over --asset")
	}

	// A tree's envelope rows measure the same positions as the scope itself.
	env := EnvelopeScope(Scope{Kind: All, Only: map[domain.AssetID]bool{kept.ID: true}}, acc)
	if env.HasAsset(acc, dropped) || !env.HasAsset(acc, kept) {
		t.Error("EnvelopeScope must carry Only through")
	}
}
