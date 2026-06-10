package web

import (
	"testing"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

func sampleLines(t *testing.T) []portfolio.PositionLine {
	t.Helper()
	pea := &domain.Account{ID: "pea", Name: "PEA"}
	cto := &domain.Account{ID: "cto", Name: "CTO"}
	cw8 := &domain.Asset{ID: "cw8", Name: "CW8", Group: "actions/monde"}
	aapl := &domain.Asset{ID: "aapl", Name: "Apple", Group: "actions/us"}
	maison := &domain.Asset{ID: "maison", Name: "Maison", Group: "immo"}
	return []portfolio.PositionLine{
		{Account: pea, Asset: cw8, Gross: 6720},
		{Account: cto, Asset: cw8, Gross: 1120},
		{Account: cto, Asset: aapl, Gross: 2000},
		{Account: pea, Asset: maison, Gross: 450000},
		{Account: pea, Gross: 4050}, // cash
	}
}

func TestBuildTreeByAccount(t *testing.T) {
	tree := buildTree(sampleLines(t), "enveloppe")
	if len(tree) != 2 || tree[0].Label != "PEA" { // trié décroissant : PEA d'abord
		t.Fatalf("racines = %+v", tree)
	}
	pea := tree[0]
	if pea.URL != "/account/pea" {
		t.Errorf("URL pea = %q", pea.URL)
	}
	// enfants du PEA : immo (450000), actions (6720), liquidités (4050)
	if len(pea.Children) != 3 || pea.Children[0].Label != "immo" {
		t.Fatalf("enfants pea = %+v", pea.Children)
	}
	if pea.Children[1].URL != "/account/pea/group/actions" {
		t.Errorf("URL intersection = %q", pea.Children[1].URL)
	}
	if last := pea.Children[2]; last.Label != "liquidités" || last.URL != "" {
		t.Errorf("feuille cash = %+v", last)
	}
	// petits-enfants : les actifs
	if pea.Children[1].Children[0].URL != "/asset/cw8" {
		t.Errorf("feuille actif = %+v", pea.Children[1].Children[0])
	}
}

func TestBuildTreeByGroup(t *testing.T) {
	tree := buildTree(sampleLines(t), "groupe")
	// racines : immo (450000), actions (9840), liquidités (4050)
	if len(tree) != 3 || tree[1].Label != "actions" {
		t.Fatalf("racines = %+v", tree)
	}
	actions := tree[1]
	if actions.URL != "/group/actions" || actions.Gross != 9840 {
		t.Errorf("actions = %+v", actions)
	}
	// enfants : enveloppes, lien intersection
	if len(actions.Children) != 2 || actions.Children[0].URL != "/account/pea/group/actions" {
		t.Fatalf("enfants actions = %+v", actions.Children)
	}
	// liquidités : enfants = enveloppes non cliquables au 3e niveau
	liq := tree[2]
	if liq.URL != "" || len(liq.Children) != 1 || liq.Children[0].Label != "PEA" {
		t.Errorf("liquidités = %+v", liq)
	}
}

func TestFlatAssets(t *testing.T) {
	flat := flatAssets(sampleLines(t))
	// maison, cw8 (6720+1120 agrégé), apple — le cash n'y figure pas
	if len(flat) != 3 || flat[0].Label != "Maison" {
		t.Fatalf("flat = %+v", flat)
	}
	if flat[1].Label != "CW8" || flat[1].Gross != 7840 || flat[1].URL != "/asset/cw8" {
		t.Errorf("agrégat cw8 = %+v", flat[1])
	}
}
