package cli

import (
	"fmt"
	"strings"

	"finador/internal/domain"
)

// currencyOr parses a user-supplied currency, empty meaning fallback.
func currencyOr(s string, fallback domain.Currency) (domain.Currency, error) {
	if s == "" {
		return fallback, nil
	}
	return domain.ParseCurrency(s)
}

// dateOrToday parses a --at flag, empty meaning today.
func dateOrToday(s string) (domain.Date, error) {
	if s == "" {
		return domain.Today(), nil
	}
	return domain.ParseDate(s)
}

// parseExclusions resolves a comma-or-repeated --exclude list into asset IDs.
func parseExclusions(b *domain.Book, refs []string) (map[domain.AssetID]bool, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := map[domain.AssetID]bool{}
	for _, chunk := range refs {
		for _, ref := range strings.Split(chunk, ",") {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			asset, err := b.Asset(ref)
			if err != nil {
				return nil, fmt.Errorf("--exclude %s: %w", ref, err)
			}
			out[asset.ID] = true
		}
	}
	return out, nil
}

// accountFor picks the envelope of a new transaction: the --account flag, the
// account of the asset's latest transaction, the config default-account, or
// the sole existing account — in that order.
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
