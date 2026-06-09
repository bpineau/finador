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
)

// Scope is what a command evaluates: everything, a group subtree, one
// envelope, or one asset. Resolution order on a free reference: group
// prefix first, then account, then asset.
type Scope struct {
	Kind    ScopeKind
	Group   string // chemin en minuscules
	Account *domain.Account
	Asset   *domain.Asset
	Label   string
}

func ParseScope(b *domain.Book, ref string) (Scope, error) {
	if ref == "" {
		return Scope{Kind: All, Label: "patrimoine"}, nil
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
	return Scope{}, fmt.Errorf("portée %q (ni groupe, ni compte, ni actif): %w", ref, domain.ErrNotFound)
}

// inGroup reports whether an asset group path falls under scope (lowercase),
// matching whole path segments.
func inGroup(assetGroup, scope string) bool {
	g := strings.ToLower(assetGroup)
	return g == scope || strings.HasPrefix(g, scope+"/")
}

func (s Scope) hasAsset(acc *domain.Account, asset *domain.Asset) bool {
	switch s.Kind {
	case All:
		return true
	case ByGroup:
		return inGroup(asset.Group, s.Group)
	case ByAccount:
		return acc.ID == s.Account.ID
	case ByAsset:
		return asset.ID == s.Asset.ID
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
		return "liquidités"
	}
	switch s.Kind {
	case All:
		if asset.Group == "" {
			return "(sans groupe)"
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
	}
	return asset.Name
}
