package web

import (
	"net/url"
	"slices"
	"strings"

	"finador/internal/portfolio"
)

// node is one displayable level of the répartition tree.
type node struct {
	Label    string
	URL      string // vide : non cliquable
	Gross    float64
	Children []node
}

// buildTree shapes position lines into the two-then-three-level tree:
// mode "enveloppe": account → top-group → assets (cash = leaf « liquidités »);
// mode "groupe": top-group → account (intersection link) → assets, with a
// « liquidités » root whose children are envelopes. Every level sorts by
// descending value.
func buildTree(lines []portfolio.PositionLine, mode string) []node {
	type key2 struct{ a, b string }
	if mode == "enveloppe" {
		// account → group → assets
		byAcc := map[string]*node{}
		grp := map[key2]*node{}
		var accOrder []string
		for _, l := range lines {
			accID := string(l.Account.ID)
			root, ok := byAcc[accID]
			if !ok {
				root = &node{Label: l.Account.Name, URL: "/account/" + url.PathEscape(accID)}
				byAcc[accID] = root
				accOrder = append(accOrder, accID)
			}
			root.Gross += l.Gross
			if l.Asset == nil {
				continue // le cash s'ajoute en feuille à la fin
			}
			g := topGroup(l.Asset.Group)
			k := key2{accID, g}
			child, ok := grp[k]
			if !ok {
				child = &node{Label: g, URL: "/account/" + url.PathEscape(accID) + "/group/" + g}
				grp[k] = child
			}
			child.Gross += l.Gross
			child.Children = append(child.Children, node{
				Label: l.Asset.Name, URL: "/asset/" + url.PathEscape(string(l.Asset.ID)), Gross: l.Gross,
			})
		}
		var out []node
		for _, accID := range accOrder {
			root := byAcc[accID]
			cash := 0.0
			for _, l := range lines {
				if string(l.Account.ID) == accID && l.Asset == nil {
					cash += l.Gross
				}
			}
			for k, child := range grp {
				if k.a != accID {
					continue
				}
				sortNodes(child.Children)
				root.Children = append(root.Children, *child)
			}
			sortNodes(root.Children)
			if cash != 0 {
				root.Children = append(root.Children, node{Label: "liquidités", Gross: cash})
			}
			out = append(out, *root)
		}
		sortNodes(out)
		return out
	}

	// mode "groupe" : top-group → account → assets ; cash → racine liquidités
	byGrp := map[string]*node{}
	sub := map[key2]*node{}
	cashRoot := node{Label: "liquidités"}
	cashByAcc := map[string]*node{}
	for _, l := range lines {
		if l.Asset == nil {
			cashRoot.Gross += l.Gross
			accID := string(l.Account.ID)
			c, ok := cashByAcc[accID]
			if !ok {
				c = &node{Label: l.Account.Name}
				cashByAcc[accID] = c
			}
			c.Gross += l.Gross
			continue
		}
		g := topGroup(l.Asset.Group)
		root, ok := byGrp[g]
		if !ok {
			root = &node{Label: g, URL: "/group/" + g}
			byGrp[g] = root
		}
		root.Gross += l.Gross
		accID := string(l.Account.ID)
		k := key2{g, accID}
		child, ok := sub[k]
		if !ok {
			child = &node{Label: l.Account.Name, URL: "/account/" + url.PathEscape(accID) + "/group/" + g}
			sub[k] = child
		}
		child.Gross += l.Gross
		child.Children = append(child.Children, node{
			Label: l.Asset.Name, URL: "/asset/" + url.PathEscape(string(l.Asset.ID)), Gross: l.Gross,
		})
	}
	var out []node
	for g, root := range byGrp {
		for k, child := range sub {
			if k.a != g {
				continue
			}
			sortNodes(child.Children)
			root.Children = append(root.Children, *child)
		}
		sortNodes(root.Children)
		out = append(out, *root)
	}
	if cashRoot.Gross != 0 {
		for _, c := range cashByAcc {
			cashRoot.Children = append(cashRoot.Children, *c)
		}
		sortNodes(cashRoot.Children)
		out = append(out, cashRoot)
	}
	sortNodes(out)
	return out
}

// flatAssets aggregates each asset across envelopes (cash excluded), sorted
// by descending value.
func flatAssets(lines []portfolio.PositionLine) []node {
	byAsset := map[string]*node{}
	for _, l := range lines {
		if l.Asset == nil {
			continue
		}
		id := string(l.Asset.ID)
		n, ok := byAsset[id]
		if !ok {
			n = &node{Label: l.Asset.Name, URL: "/asset/" + url.PathEscape(id)}
			byAsset[id] = n
		}
		n.Gross += l.Gross
	}
	var out []node
	for _, n := range byAsset {
		out = append(out, *n)
	}
	sortNodes(out)
	return out
}

func topGroup(g string) string {
	if g == "" {
		return "(sans groupe)"
	}
	head, _, _ := strings.Cut(strings.ToLower(g), "/")
	return head
}

func sortNodes(ns []node) {
	slices.SortStableFunc(ns, func(a, b node) int {
		switch {
		case a.Gross > b.Gross:
			return -1
		case a.Gross < b.Gross:
			return 1
		}
		return strings.Compare(a.Label, b.Label)
	})
}
