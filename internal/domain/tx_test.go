package domain

import (
	"encoding/json"
	"testing"
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
