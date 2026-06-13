package store

import (
	"crypto/sha256"
	"errors"
	"testing"

	"finador/internal/domain"
)

func TestSealOpenLineRoundTrip(t *testing.T) {
	h := defaultHeader()
	keyLog, _ := deriveKeys("pw", h)
	g := gcmOf(keyLog)
	hh := sha256.Sum256(h.encode())
	prev := make([]byte, tagSize)

	rec := record{K: kTx, D: mustJSON(domain.Transaction{ID: 7, Kind: domain.Buy})}
	line, tag := sealLine(g, hh[:], 1, prev, rec)

	got, gotTag, err := openLine(g, hh[:], 1, prev, line)
	if err != nil {
		t.Fatal(err)
	}
	if got.K != kTx || string(got.D) != string(rec.D) {
		t.Fatalf("record not preserved: %+v", got)
	}
	if string(gotTag) != string(tag) {
		t.Fatal("tag mismatch")
	}
}

func TestOpenLineWrongChainFails(t *testing.T) {
	h := defaultHeader()
	keyLog, _ := deriveKeys("pw", h)
	g := gcmOf(keyLog)
	hh := sha256.Sum256(h.encode())
	prev := make([]byte, tagSize)
	line, _ := sealLine(g, hh[:], 1, prev, record{K: kAcct, D: mustJSON("x")})

	wrong := make([]byte, tagSize)
	wrong[0] = 0xFF
	if _, _, err := openLine(g, hh[:], 1, wrong, line); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword on broken chain, got %v", err)
	}
	if _, _, err := openLine(g, hh[:], 2, prev, line); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword on wrong seq, got %v", err)
	}
}

func TestHeadSealVerify(t *testing.T) {
	h := defaultHeader()
	keyLog, _ := deriveKeys("pw", h)
	g := gcmOf(keyLog)
	hh := sha256.Sum256(h.encode())
	lastTag := make([]byte, tagSize)
	lastTag[0] = 0xAB

	head := sealHead(g, hh[:], 3, lastTag)

	hp, err := openHead(g, hh[:], 3, head)
	if err != nil {
		t.Fatal(err)
	}
	if hp.Count != 3 || string(hp.Head) != string(lastTag) {
		t.Fatalf("head payload wrong: %+v", hp)
	}
	// A different observed count (truncation) must fail via AAD.
	if _, err := openHead(g, hh[:], 2, head); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword on count mismatch, got %v", err)
	}
}
