package portfolio

import (
	"errors"
	"fmt"
	"strings"

	"finador/internal/domain"
)

// ScopeKind is the shape of a Scope: which positions and whose cash it keeps.
type ScopeKind uint8

const (
	All ScopeKind = iota
	ByGroup
	ByAccount
	ByAsset
	ByAccountGroup // intersection envelope ∩ group (crossed levels of the web trees)
	ByLabel        // positions carrying a specific label name
)

// Scope is what a command evaluates: everything, a group subtree, one
// envelope, or one asset. Resolution order on a free reference: group
// prefix first, then account, then asset.
type Scope struct {
	Kind     ScopeKind
	Group    string // lowercase path
	Account  *domain.Account
	Asset    *domain.Asset
	Label    string
	Pairs    map[pairKey]bool        // populated for ByLabel
	Excluded map[domain.AssetID]bool // assets removed from the scope (throwaway, CLI --exclude)
}

// ParseScope resolves the free scope argument of value/perf/chart: empty is
// the whole portfolio, otherwise group prefix first, then account, then asset
// (an ambiguity stops the search instead of falling through).
func ParseScope(b *domain.Book, ref string) (Scope, error) {
	if ref == "" {
		return Scope{Kind: All, Label: "portfolio"}, nil
	}
	low := strings.ToLower(ref)
	for _, a := range b.Assets {
		if inGroup(a.Group, low) {
			return Scope{Kind: ByGroup, Group: low, Label: low}, nil
		}
	}
	if acc, err := b.Account(ref); err == nil {
		return Scope{Kind: ByAccount, Account: acc, Label: acc.Name}, nil
	} else if errors.Is(err, domain.ErrAmbiguous) {
		return Scope{}, err
	}
	if asset, err := b.Asset(ref); err == nil {
		return Scope{Kind: ByAsset, Asset: asset, Label: asset.Name}, nil
	} else if errors.Is(err, domain.ErrAmbiguous) {
		return Scope{}, err
	}
	return Scope{}, fmt.Errorf("unknown scope %q (not a group, account or asset): %w", ref, domain.ErrNotFound)
}

// LabelScope builds a scope limited to the (account, asset) pairs that carry
// the given label name (case-insensitive). Returns an error if no such pair exists.
func LabelScope(b *domain.Book, name string) (Scope, error) {
	pairs := map[pairKey]bool{}
	low := strings.ToLower(name)
	for _, l := range b.Labels {
		if strings.ToLower(l.Name) == low {
			pairs[pairKey{acc: l.Account, asset: l.Asset}] = true
		}
	}
	if len(pairs) == 0 {
		return Scope{}, fmt.Errorf("no positions carry label %q", name)
	}
	return Scope{
		Kind:     ByLabel,
		Label:    name,
		Pairs:    pairs,
		Excluded: map[domain.AssetID]bool{},
	}, nil
}

// IntersectScope is the crossed level of the web trees: the assets of one
// group held inside one envelope. Cash is excluded (it has no group).
func IntersectScope(acc *domain.Account, group string) Scope {
	g := strings.ToLower(group)
	return Scope{Kind: ByAccountGroup, Account: acc, Group: g, Label: acc.Name + " › " + g}
}

// PairScope is the scope of a single (account, asset) position: what one
// line of a tree view measures. Cash is excluded (the envelope row owns it).
func PairScope(acc *domain.Account, asset *domain.Asset) Scope {
	return Scope{
		Kind:  ByLabel,
		Label: acc.Name + " › " + asset.Name,
		Pairs: map[pairKey]bool{{acc: acc.ID, asset: asset.ID}: true},
	}
}

// EnvelopeScope restricts s to one account: what the account's row of a tree
// view measures. Cash is kept exactly when s itself keeps it.
func EnvelopeScope(s Scope, acc *domain.Account) Scope {
	out := Scope{Kind: ByAccount, Account: acc, Label: acc.Name, Excluded: s.Excluded}
	switch s.Kind {
	case All, ByAccount:
		return out
	case ByGroup:
		out.Kind, out.Group = ByAccountGroup, s.Group
		return out
	case ByAsset:
		return Scope{Kind: ByLabel, Label: acc.Name, Excluded: s.Excluded,
			Pairs: map[pairKey]bool{{acc: acc.ID, asset: s.Asset.ID}: true}}
	case ByLabel:
		pairs := map[pairKey]bool{}
		for k := range s.Pairs {
			if k.acc == acc.ID {
				pairs[k] = true
			}
		}
		return Scope{Kind: ByLabel, Label: acc.Name, Excluded: s.Excluded, Pairs: pairs}
	}
	return out
}

// FilterScope keeps the breakdown lines that belong to s: the positions s
// accepts, and the cash of accounts whose cash s accepts.
func FilterScope(lines []PositionLine, s Scope) []PositionLine {
	out := make([]PositionLine, 0, len(lines))
	for _, l := range lines {
		if l.Asset == nil {
			if s.hasCash(l.Account) {
				out = append(out, l)
			}
			continue
		}
		if s.hasAsset(l.Account, l.Asset) {
			out = append(out, l)
		}
	}
	return out
}

// inGroup reports whether an asset group path falls under scope (lowercase),
// matching whole path segments.
func inGroup(assetGroup, scope string) bool {
	g := strings.ToLower(assetGroup)
	return g == scope || strings.HasPrefix(g, scope+"/")
}

// HasAsset reports whether the (account, asset) position belongs to the scope.
func (s Scope) HasAsset(acc *domain.Account, asset *domain.Asset) bool { return s.hasAsset(acc, asset) }

// HasCash reports whether the account's cash belongs to the scope.
func (s Scope) HasCash(acc *domain.Account) bool { return s.hasCash(acc) }

func (s Scope) hasAsset(acc *domain.Account, asset *domain.Asset) bool {
	if s.Excluded[asset.ID] {
		return false
	}
	switch s.Kind {
	case All:
		return true
	case ByGroup:
		return inGroup(asset.Group, s.Group)
	case ByAccount:
		return acc.ID == s.Account.ID
	case ByAsset:
		return asset.ID == s.Asset.ID
	case ByAccountGroup:
		return acc.ID == s.Account.ID && inGroup(asset.Group, s.Group)
	case ByLabel:
		return s.Pairs[pairKey{acc: acc.ID, asset: asset.ID}]
	}
	return false
}

func (s Scope) hasCash(acc *domain.Account) bool {
	switch s.Kind {
	case All:
		return true
	case ByAccount:
		return acc.ID == s.Account.ID
	}
	return false
}

// lineLabel groups the breakdown lines: top-level group for All, next path
// segment (or asset name) inside a group, asset name inside an account,
// account name for an asset scope. Cash lines are labelled "cash".
func (s Scope) lineLabel(acc *domain.Account, asset *domain.Asset) string {
	if asset == nil {
		return "cash"
	}
	switch s.Kind {
	case All:
		if asset.Group == "" {
			return "(ungrouped)"
		}
		head, _, _ := strings.Cut(strings.ToLower(asset.Group), "/")
		return head
	case ByGroup:
		g := strings.ToLower(asset.Group)
		if g == s.Group {
			return asset.Name
		}
		seg, _, _ := strings.Cut(strings.TrimPrefix(g, s.Group+"/"), "/")
		return s.Group + "/" + seg
	case ByAccount:
		return asset.Name
	case ByAsset:
		return acc.Name
	case ByAccountGroup:
		return asset.Name
	case ByLabel:
		return asset.Name
	}
	return asset.Name
}
