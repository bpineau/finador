package cli

import (
	"fmt"
	"slices"
	"strings"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

// scopeArgs is the scope selection every read command (value, perf, chart,
// export) shares: the positional [scope], --account, --label, --asset and
// --exclude.
type scopeArgs struct {
	ref     string   // positional [scope]: a group, an account or an asset
	account string   // --account: an envelope, and only an envelope
	label   string   // --label: positions carrying that label
	exclude []string // --exclude: assets pruned from the scope
	only    []string // --asset: the only assets kept
}

// resolveScope turns those into one portfolio.Scope. The shape comes from
// [scope]/--account/--label, then --asset and --exclude narrow it: they
// compose with the shape rather than replacing it, so
// `perf "PEA Zephyr" --asset cw8` is one position inside one envelope.
func resolveScope(b *domain.Book, s scopeArgs) (portfolio.Scope, error) {
	scope, err := scopeShape(b, s)
	if err != nil {
		return portfolio.Scope{}, err
	}
	kept, names, err := parseAssetRefs(b, "--asset", s.only)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(kept) > 0 {
		scope.Only = kept
		if list := strings.Join(names, ", "); scope.Kind == portfolio.All {
			scope.Label = list
		} else {
			scope.Label += " › " + list
		}
	}
	excluded, _, err := parseAssetRefs(b, "--exclude", s.exclude)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(excluded) > 0 {
		scope.Excluded = excluded
		scope.Label += " (excluding " + strings.Join(s.exclude, ",") + ")"
	}
	return scope, nil
}

// scopeShape resolves the shape --account/[scope]/--label select, before the
// throwaway asset filters narrow it. --account names an envelope and only an
// envelope (never a group or an asset); what accompanies it may only narrow
// it: a group reference intersects it, a label keeps that label's positions
// held inside it, and a second envelope or an asset is a conflict.
func scopeShape(b *domain.Book, s scopeArgs) (portfolio.Scope, error) {
	if s.ref != "" && s.label != "" {
		return portfolio.Scope{}, fmt.Errorf("use either a [scope] argument or --label, not both")
	}
	if s.account == "" {
		if s.label != "" {
			return portfolio.LabelScope(b, s.label)
		}
		return portfolio.ParseScope(b, s.ref)
	}
	acc, err := b.Account(s.account)
	if err != nil {
		return portfolio.Scope{}, fmt.Errorf("--account: %w", err)
	}
	switch {
	case s.label != "":
		labelled, err := portfolio.LabelScope(b, s.label)
		if err != nil {
			return portfolio.Scope{}, err
		}
		scope := portfolio.EnvelopeScope(labelled, acc)
		scope.Label = acc.Name + " › " + s.label
		return scope, nil
	case s.ref == "":
		return portfolio.AccountScope(acc), nil
	}
	sub, err := portfolio.ParseScope(b, s.ref)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if sub.Kind != portfolio.ByGroup {
		return portfolio.Scope{}, fmt.Errorf(
			"--account %s and [scope] %q conflict: only a group narrows an envelope", s.account, s.ref)
	}
	return portfolio.IntersectScope(acc, sub.Group), nil
}

// currencyOr parses a user-supplied currency, empty meaning fallback.
func currencyOr(s string, fallback domain.Currency) (domain.Currency, error) {
	return domain.CurrencyOr(s, fallback)
}

// dateOrToday parses a --at flag, empty meaning today.
func dateOrToday(s string) (domain.Date, error) {
	if s == "" {
		return domain.Today(), nil
	}
	return domain.ParseDate(s)
}

// parseAssetRefs resolves a comma-or-repeated list of asset references into a
// set of IDs, plus their names in the order given (for the scope label). flag
// names the flag being parsed, so an unresolvable reference blames the right one.
func parseAssetRefs(b *domain.Book, flag string, refs []string) (map[domain.AssetID]bool, []string, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	out := map[domain.AssetID]bool{}
	var names []string
	for _, chunk := range refs {
		for _, ref := range strings.Split(chunk, ",") {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			asset, err := b.Asset(ref)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %s: %w", flag, ref, err)
			}
			if !out[asset.ID] {
				names = append(names, asset.Name)
			}
			out[asset.ID] = true
		}
	}
	return out, names, nil
}

// accountFor picks the envelope of a new transaction: the --account flag, the
// account of the asset's latest transaction, the config default-account, or
// the sole existing account - in that order.
func accountFor(b *domain.Book, flag string, asset *domain.Asset) (*domain.Account, error) {
	if flag != "" {
		return b.Account(flag)
	}
	if asset != nil {
		for i := len(b.Transactions) - 1; i >= 0; i-- {
			if t := b.Transactions[i]; t.Asset == asset.ID {
				return b.Account(string(t.Account))
			}
		}
	}
	if def := b.Config["default-account"]; def != "" {
		return b.Account(def)
	}
	if len(b.Accounts) == 1 {
		return b.Accounts[0], nil
	}
	return nil, fmt.Errorf("specify the account with --account: %w", domain.ErrAmbiguous)
}

// applyAliasEdits adds then removes aliases, case-insensitively and without
// duplicates - shared by asset edit and account edit.
func applyAliasEdits(aliases, add, rm []string) []string {
	for _, al := range add {
		if !slices.ContainsFunc(aliases, func(x string) bool { return strings.EqualFold(x, al) }) {
			aliases = append(aliases, al)
		}
	}
	for _, al := range rm {
		aliases = slices.DeleteFunc(aliases, func(x string) bool { return strings.EqualFold(x, al) })
	}
	return aliases
}
