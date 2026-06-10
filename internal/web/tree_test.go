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
	tree := buildTree(sampleLines(t), "account")
	if len(tree) != 2 || tree[0].Label != "PEA" { // sorted descending: PEA first
		t.Fatalf("roots = %+v", tree)
	}
	pea := tree[0]
	if pea.URL != "/account/pea" {
		t.Errorf("URL pea = %q", pea.URL)
	}
	// PEA children: immo (450000), actions (6720), cash (4050)
	if len(pea.Children) != 3 || pea.Children[0].Label != "immo" {
		t.Fatalf("pea children = %+v", pea.Children)
	}
	if pea.Children[1].URL != "/account/pea/group/actions" {
		t.Errorf("intersection URL = %q", pea.Children[1].URL)
	}
	if last := pea.Children[2]; last.Label != "cash" || last.URL != "" {
		t.Errorf("cash leaf = %+v", last)
	}
	// grandchildren: assets
	if pea.Children[1].Children[0].URL != "/asset/cw8" {
		t.Errorf("asset leaf = %+v", pea.Children[1].Children[0])
	}
}

func TestBuildTreeByGroup(t *testing.T) {
	tree := buildTree(sampleLines(t), "group")
	// roots: immo (450000), actions (9840), cash (4050)
	if len(tree) != 3 || tree[1].Label != "actions" {
		t.Fatalf("roots = %+v", tree)
	}
	actions := tree[1]
	if actions.URL != "/group/actions" || actions.Gross != 9840 {
		t.Errorf("actions = %+v", actions)
	}
	// children: envelopes, intersection link
	if len(actions.Children) != 2 || actions.Children[0].URL != "/account/pea/group/actions" {
		t.Fatalf("actions children = %+v", actions.Children)
	}
	// cash: children = non-clickable envelopes at third level
	liq := tree[2]
	if liq.URL != "" || len(liq.Children) != 1 || liq.Children[0].Label != "PEA" {
		t.Errorf("cash = %+v", liq)
	}
}
