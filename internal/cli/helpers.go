package cli

import (
	"fmt"
	"slices"
	"strings"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

// resolveScope resolves the [scope]/--label/--asset/--exclude quadruple every
// read command shares: an empty ref is the whole portfolio, --label restricts
// to labelled positions, --asset keeps only the named assets (and drops cash,
// which is not an asset), --exclude prunes assets. The two asset flags narrow
// whatever the scope argument selected - they compose with it rather than
// replacing it, so `perf "PEA Zephyr" --asset cw8` is one position inside one
// envelope.
func resolveScope(b *domain.Book, ref, label string, exclude, only []string) (portfolio.Scope, error) {
	if ref != "" && label != "" {
		return portfolio.Scope{}, fmt.Errorf("use either a [scope] argument or --label, not both")
	}
	var scope portfolio.Scope
	var err error
	if label != "" {
		scope, err = portfolio.LabelScope(b, label)
	} else {
		scope, err = portfolio.ParseScope(b, ref)
	}
	if err != nil {
		return portfolio.Scope{}, err
	}
	kept, names, err := parseAssetRefs(b, "--asset", only)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(kept) > 0 {
		scope.Only = kept
		if list := strings.Join(names, ", "); ref == "" && label == "" {
			scope.Label = list
		} else {
			scope.Label += " › " + list
		}
	}
	excluded, _, err := parseAssetRefs(b, "--exclude", exclude)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(excluded) > 0 {
		scope.Excluded = excluded
		scope.Label += " (excluding " + strings.Join(exclude, ",") + ")"
	}
	return scope, nil
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
