package cli

import (
	"fmt"

	"finador/internal/domain"
)

// dateOrToday parses a --at flag, empty meaning today.
func dateOrToday(s string) (domain.Date, error) {
	if s == "" {
		return domain.Today(), nil
	}
	return domain.ParseDate(s)
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
	return nil, fmt.Errorf("précisez l'enveloppe avec --account: %w", domain.ErrAmbiguous)
}
