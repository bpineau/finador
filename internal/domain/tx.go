package domain

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type TxKind uint8

const (
	Buy TxKind = iota + 1
	Sell
	Dividend
	Fee
	Deposit   // apport externe : alimente la base fiscale et le XIRR
	Withdraw  // retrait externe
	Statement // solde de cash ou estimation de bien, constaté à une date
)

var txKindNames = map[TxKind]string{
	Buy: "buy", Sell: "sell", Dividend: "dividend", Fee: "fee",
	Deposit: "deposit", Withdraw: "withdraw", Statement: "statement",
}

var txKindByName = lo.Invert(txKindNames)

func ParseTxKind(s string) (TxKind, error) {
	k, ok := txKindByName[strings.ToLower(s)]
	if !ok {
		return 0, fmt.Errorf("type de transaction %q inconnu", s)
	}
	return k, nil
}

func (k TxKind) String() string { return txKindNames[k] }

func (k TxKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *TxKind) UnmarshalText(b []byte) error {
	parsed, err := ParseTxKind(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

type TxID uint64

// Transaction is one immutable-by-default ledger line; everything derived
// (positions, bases fiscales, séries) is recomputed from the ledger.
// Quantity and Amount are always positive: Kind carries the direction.
type Transaction struct {
	ID         TxID            `json:"id"`
	Date       Date            `json:"date"`
	Account    AccountID       `json:"account"`
	Asset      AssetID         `json:"asset,omitempty"` // vide : cash pur du compte
	Kind       TxKind          `json:"kind"`
	Quantity   decimal.Decimal `json:"qty"`
	Amount     Money           `json:"amount"`
	Note       string          `json:"note,omitempty"`
	ImportHash string          `json:"importHash,omitempty"`
}
