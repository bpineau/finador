package domain

// AccountID is an account's stable identifier: the slug of its original name
// (Slugify), unchanged by later renames.
type AccountID string

// Account is an envelope: where assets are held, and how it is taxed.
// Name is free-form ("PEA Zephyr"); ID is its stable slug.
type Account struct {
	ID       AccountID `json:"id"`
	Name     string    `json:"name"`
	Currency Currency  `json:"ccy"`
	Tax      TaxRule   `json:"tax"`
	Aliases  []string  `json:"aliases,omitempty"`
}
