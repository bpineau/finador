package domain

import "fmt"

// AssetKind tells how an asset is valued: at the market price (Security) or
// from dated Statement estimates (Property).
type AssetKind uint8

const (
	Security AssetKind = iota + 1 // quoted: valued at the market price
	Property                      // property: valued from dated estimates
)

// ParseAssetKind reads "security" or "property".
func ParseAssetKind(s string) (AssetKind, error) {
	switch s {
	case "security":
		return Security, nil
	case "property":
		return Property, nil
	}
	return 0, fmt.Errorf("invalid asset kind %q: expected security or property", s)
}

func (k AssetKind) String() string {
	switch k {
	case Security:
		return "security"
	case Property:
		return "property"
	}
	return fmt.Sprintf("AssetKind(%d)", uint8(k))
}

func (k AssetKind) MarshalText() ([]byte, error) {
	switch k {
	case Security, Property:
		return []byte(k.String()), nil
	}
	return nil, fmt.Errorf("undefined AssetKind %d", uint8(k))
}

func (k *AssetKind) UnmarshalText(b []byte) error {
	parsed, err := ParseAssetKind(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// AssetID is an asset's stable identifier: the slug of its original ticker or
// name (Slugify), unchanged by later renames or retickers.
type AssetID string

// Asset is anything owned: a quoted security or a free-form property.
// Cash is not an asset - it belongs to each Account.
type Asset struct {
	ID       AssetID   `json:"id"`
	Kind     AssetKind `json:"kind"`
	Name     string    `json:"name"`
	Ticker   string    `json:"ticker,omitempty"` // Yahoo symbol ("CW8.PA")
	ISIN     string    `json:"isin,omitempty"`
	Aliases  []string  `json:"aliases,omitempty"`
	Currency Currency  `json:"ccy"`
	Group    string    `json:"group,omitempty"` // hierarchical path: "actions/us/tech"
	// Withholding is the source-tax rate applied to AUTOMATIC dividends
	// (net = gross × (1−w)); manual Dividend lines are assumed already net.
	Withholding float64 `json:"withholding,omitempty"`
}
