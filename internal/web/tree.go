package web

import (
	"cmp"
	"html/template"
	"math"
	"net/url"
	"slices"
	"strings"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/portfolio"
)

// node is one displayable level of the breakdown tree.
type node struct {
	Label    string
	URL      string // empty: not clickable
	Gross    float64
	Labels   []string // name-labels on a leaf (account, asset) position
	Children []node
}

// labelLookup returns the name-labels on a (account, asset) position; nil is a
// no-op used by tests that don't exercise labels.
type labelLookup func(domain.AccountID, domain.AssetID) []string

func (f labelLookup) at(acc domain.AccountID, asset domain.AssetID) []string {
	if f == nil {
		return nil
	}
	return f(acc, asset)
}

// buildTree shapes position lines into the two-then-three-level tree:
// mode "account": account → top-group → assets (cash = leaf "cash");
// mode "group": top-group → account (intersection link) → assets, with a
// "cash" root whose children are envelopes. Every level sorts by
// descending value. Leaf asset positions carry their name-labels (labelsFor).
func buildTree(lines []portfolio.PositionLine, mode string, labelsFor labelLookup) []node {
	type key2 struct{ a, b string }
	if mode == "account" {
		// account → group → assets
		byAcc := map[string]*node{}
		grp := map[key2]*node{}
		cashOf := map[string]float64{}
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
				cashOf[accID] += l.Gross // "cash" leaf added at the end
				continue
			}
			g := topGroup(l.Asset.Group)
			k := key2{accID, g}
			child, ok := grp[k]
			if !ok {
				child = &node{Label: g, URL: "/account/" + url.PathEscape(accID) + "/group/" + escapeGroup(g)}
				grp[k] = child
			}
			child.Gross += l.Gross
			child.Children = append(child.Children, node{
				Label: l.Asset.Name, URL: "/asset/" + url.PathEscape(string(l.Asset.ID)), Gross: l.Gross,
				Labels: labelsFor.at(l.Account.ID, l.Asset.ID),
			})
		}
		var out []node
		for _, accID := range accOrder {
			root := byAcc[accID]
			cash := cashOf[accID]
			for k, child := range grp {
				if k.a != accID {
					continue
				}
				sortNodes(child.Children)
				root.Children = append(root.Children, *child)
			}
			sortNodes(root.Children)
			if cash != 0 {
				root.Children = append(root.Children, node{Label: "cash", Gross: cash})
			}
			out = append(out, *root)
		}
		sortNodes(out)
		return out
	}

	// mode "group": top-group → account → assets ; cash → root "cash"
	byGrp := map[string]*node{}
	sub := map[key2]*node{}
	cashRoot := node{Label: "cash"}
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
			root = &node{Label: g, URL: "/group/" + escapeGroup(g)}
			byGrp[g] = root
		}
		root.Gross += l.Gross
		accID := string(l.Account.ID)
		k := key2{g, accID}
		child, ok := sub[k]
		if !ok {
			child = &node{Label: l.Account.Name, URL: "/account/" + url.PathEscape(accID) + "/group/" + escapeGroup(g)}
			sub[k] = child
		}
		child.Gross += l.Gross
		child.Children = append(child.Children, node{
			Label: l.Asset.Name, URL: "/asset/" + url.PathEscape(string(l.Asset.ID)), Gross: l.Gross,
			Labels: labelsFor.at(l.Account.ID, l.Asset.ID),
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

// escapeGroup escapes each path segment, keeping the slashes routable.
func escapeGroup(g string) string {
	segs := strings.Split(g, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func topGroup(g string) string {
	if g == "" {
		return "(ungrouped)"
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

// pieSlice is one legend entry of the allocation donut.
type pieSlice struct {
	Label   string
	URL     string
	Color   template.CSS // always a constant from chart.PiePalette
	Amount  float64
	Percent int
}

// buildPie aggregates Breakdown lines into top-level group + cash slices,
// sorted by amount descending, and renders the SVG donut + its legend data.
func buildPie(lines []portfolio.PositionLine) (template.HTML, []pieSlice) {
	amounts := map[string]float64{}
	var order []string
	for _, l := range lines {
		key := "cash"
		if l.Asset != nil {
			key = topGroup(l.Asset.Group)
		}
		if _, seen := amounts[key]; !seen {
			order = append(order, key)
		}
		amounts[key] += l.Gross
	}

	var out []pieSlice
	total := 0.0
	for _, k := range order {
		if amounts[k] <= 0 {
			continue
		}
		ps := pieSlice{Label: k, Amount: amounts[k]}
		if k != "cash" {
			ps.URL = "/group/" + escapeGroup(k)
		}
		out = append(out, ps)
		total += amounts[k]
	}
	if len(out) == 0 {
		return "", nil
	}
	slices.SortStableFunc(out, func(a, b pieSlice) int {
		return cmp.Or(cmp.Compare(b.Amount, a.Amount), strings.Compare(a.Label, b.Label))
	})

	values := make([]float64, len(out))
	colors := make([]string, len(out))
	for i := range out {
		color := chart.PiePalette[i%len(chart.PiePalette)]
		out[i].Color = template.CSS(color) // constant palette - no user data
		out[i].Percent = int(math.Round(out[i].Amount / total * 100))
		values[i], colors[i] = out[i].Amount, color
	}
	return template.HTML(chart.Pie(values, colors, 190)), out // #nosec G203 - our own SVG
}
