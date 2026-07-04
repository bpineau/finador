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
	// Bounds are enforced BEFORE key derivation (FORMAT.md §2.2): a forged,
	// unauthenticated header must never trigger a panic or a memory bomb. Each
	// case pushes exactly one field out of range; a valid 16-byte salt/id is
	// "AAAA…==" (base64 of 16 zero bytes).
	const okSalt = "AAAAAAAAAAAAAAAAAAAAAA==" // 16 zero bytes
	cases := []struct {
		name   string
		header string
	}{
		{"kdf not argon2id", `{"fmt":"finador-ledger","v":3,"kdf":"scrypt","t":3,"m":65536,"p":4,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"t too low", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":0,"m":65536,"p":4,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"t too high", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":17,"m":65536,"p":4,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"m too low", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":1,"p":4,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"m too high", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":2097152,"p":4,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"p too low", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":0,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"p too high", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":17,"salt":"` + okSalt + `","id":"` + okSalt + `"}`},
		{"salt too short", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"AAAA","id":"` + okSalt + `"}`},
		{"id too short", `{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"` + okSalt + `","id":"AAAA"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseHeader([]byte(c.header)); err == nil {
				t.Fatalf("expected rejection for %s", c.name)
			}
		})
	}
}

func TestHeaderRejectsUnknownVersion(t *testing.T) {
	old := `{"fmt":"finador-ledger","v":2,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","id":"AAAAAAAAAAAAAAAAAAAAAA=="}`
	if _, err := parseHeader([]byte(old)); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected unsupported version rejection, got %v", err)
	}
}
