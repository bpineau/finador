package domain

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseTxKind(t *testing.T) {
	for in, want := range map[string]TxKind{
		"buy": Buy, "SELL": Sell, "dividend": Dividend, "fee": Fee,
		"deposit": Deposit, "withdraw": Withdraw, "statement": Statement,
	} {
		k, err := ParseTxKind(in)
		if err != nil || k != want {
			t.Errorf("ParseTxKind(%q) = %v, %v", in, k, err)
		}
	}
	if _, err := ParseTxKind("achat"); err == nil {
		t.Error("ParseTxKind(\"achat\") aurait dû échouer")
	}
}

func TestTxKindJSON(t *testing.T) {
	raw, err := json.Marshal(Buy)
	if err != nil || string(raw) != `"buy"` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var k TxKind
	if err := json.Unmarshal([]byte(`"statement"`), &k); err != nil || k != Statement {
		t.Fatalf("unmarshal = %v, err=%v", k, err)
	}
}

func TestParseAssetKind(t *testing.T) {
	if k, err := ParseAssetKind("security"); err != nil || k != Security {
		t.Errorf("security: %v, %v", k, err)
	}
	if k, err := ParseAssetKind("property"); err != nil || k != Property {
		t.Errorf("property: %v, %v", k, err)
	}
	if _, err := ParseAssetKind("crypto"); err == nil {
		t.Error("crypto aurait dû échouer")
	}
}

func TestTransactionJSONRoundTrip(t *testing.T) {
	d, _ := ParseDate("2026-06-01")
	tx := Transaction{
		ID: 7, Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
		Quantity: decimal.RequireFromString("10"),
		Amount:   Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR},
		Note:     "premier achat",
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":7,"date":"2026-06-01","account":"pea-zephyr","asset":"cw8","kind":"buy","qty":"10","amount":{"amount":"5500.5","ccy":"EUR"},"note":"premier achat"}`
	if string(raw) != want {
		t.Fatalf("format de fil dérivé:\n  got  %s\n  want %s", raw, want)
	}
	var back Transaction
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != tx.ID || back.Kind != Buy || !back.Quantity.Equal(tx.Quantity) || !back.Amount.Amount.Equal(tx.Amount.Amount) {
		t.Fatalf("roundtrip altéré: %+v", back)
	}
}

func TestUnsetKindsFailLoudlyAtMarshal(t *testing.T) {
	if _, err := json.Marshal(Transaction{}); err == nil {
		t.Fatal("marshaler une Transaction sans Kind aurait dû échouer")
	}
	if _, err := json.Marshal(Asset{}); err == nil {
		t.Fatal("marshaler un Asset sans Kind aurait dû échouer")
	}
}
