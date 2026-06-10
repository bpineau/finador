package portfolio

import (
	"errors"
	"fmt"
	"strings"

	"finador/internal/domain"
)

type ScopeKind uint8

const (
	All ScopeKind = iota
	ByGroup
	ByAccount
	ByAsset
	ByAccountGroup // intersection enveloppe ∩ groupe (niveaux croisés des arbres web)
)

// Scope is what a command evaluates: everything, a group subtree, one
// envelope, or one asset. Resolution order on a free reference: group
// prefix first, then account, then asset.
type Scope struct {
	Kind     ScopeKind
	Group    string // chemin en minuscules
	Account  *domain.Account
	Asset    *domain.Asset
	Label    string
	Excluded map[domain.AssetID]bool // actifs retirés de la portée (jetable, CLI --exclude)
}

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

// IntersectScope is the crossed level of the web trees: the assets of one
// group held inside one envelope. Cash is excluded (it has no group).
func IntersectScope(acc *domain.Account, group string) Scope {
	g := strings.ToLower(group)
	return Scope{Kind: ByAccountGroup, Account: acc, Group: g, Label: acc.Name + " › " + g}
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
// account name for an asset scope. Cash lines are labelled "liquidités".
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
	}
	return asset.Name
}
