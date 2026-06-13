package store

import (
	"bytes"
	"strings"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	h := defaultHeader()
	line := h.encode()
	got, err := parseHeader(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != formatVersion || got.KDF != "argon2id" {
		t.Fatalf("bad header: %+v", got)
	}
	if len(got.Salt) != saltSize || len(got.ID) != fileIDSize {
		t.Fatalf("salt/id sizes: %d/%d", len(got.Salt), len(got.ID))
	}
	if !bytes.Equal(got.Salt, h.Salt) || !bytes.Equal(got.ID, h.ID) {
		t.Fatal("salt/id not preserved")
	}
}

func TestHeaderRejectsForeign(t *testing.T) {
	if _, err := parseHeader([]byte("PK\x03\x04 not finador")); err == nil || !strings.Contains(err.Error(), "finador") {
		t.Fatalf("expected finador format error, got %v", err)
	}
}

func TestHeaderRejectsBadParams(t *testing.T) {
	bad := `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":1,"p":4,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","id":"AAAAAAAAAAAAAAAAAAAAAA=="}`
	if _, err := parseHeader([]byte(bad)); err == nil {
		t.Fatal("expected out-of-bounds memory rejection")
	}
}

func TestHeaderRejectsUnknownVersion(t *testing.T) {
	old := `{"fmt":"finador-ledger","v":2,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","id":"AAAAAAAAAAAAAAAAAAAAAA=="}`
	if _, err := parseHeader([]byte(old)); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected unsupported version rejection, got %v", err)
	}
}
