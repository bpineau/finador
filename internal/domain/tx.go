package domain

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

// TxKind is the nature of a ledger line. It carries the direction of the
// money: quantities and amounts are always positive.
type TxKind uint8

const (
	Buy TxKind = iota + 1
	Sell
	Dividend
	Fee
	Deposit   // external contribution: feeds the tax basis and the performance flows
	Withdraw  // external withdrawal
	Statement // cash balance or property estimate, recorded on a date
)

var txKindNames = map[TxKind]string{
	Buy: "buy", Sell: "sell", Dividend: "dividend", Fee: "fee",
	Deposit: "deposit", Withdraw: "withdraw", Statement: "statement",
}

var txKindByName = lo.Invert(txKindNames)

// ParseTxKind reads a kind name ("buy", "statement", …), case-insensitive.
func ParseTxKind(s string) (TxKind, error) {
	k, ok := txKindByName[strings.ToLower(s)]
	if !ok {
		return 0, fmt.Errorf("unknown transaction kind %q", s)
	}
	return k, nil
}

func (k TxKind) String() string { return txKindNames[k] }

func (k TxKind) MarshalText() ([]byte, error) {
	name, ok := txKindNames[k]
	if !ok {
		return nil, fmt.Errorf("undefined TxKind %d", uint8(k))
	}
	return []byte(name), nil
}

func (k *TxKind) UnmarshalText(b []byte) error {
	parsed, err := ParseTxKind(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// TxID is a transaction's stable identifier (a NewID): random, time-sortable,
// never reused. Users may refer to it by unique prefix, like short git SHAs.
type TxID string

// Transaction is one immutable-by-default ledger line; everything derived
// (positions, tax bases, series) is recomputed from the ledger.
// Quantity and Amount are always positive: Kind carries the direction.
type Transaction struct {
	ID         TxID            `json:"id"`
	Date       Date            `json:"date"`
	Account    AccountID       `json:"account"`
	Asset      AssetID         `json:"asset,omitempty"` // empty: pure account cash
	Kind       TxKind          `json:"kind"`
	Quantity   decimal.Decimal `json:"qty"`
	Amount     Money           `json:"amount"`
	Note       string          `json:"note,omitempty"`
	ImportHash string          `json:"importHash,omitempty"`
}
