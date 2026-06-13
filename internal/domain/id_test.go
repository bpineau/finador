package domain

import (
	"strings"
	"testing"
	"time"
)

const idCharset = "0123456789abcdefghjkmnpqrstvwxyz"

func TestNewIDLength(t *testing.T) {
	// 14 bytes Crockford base32, no padding -> ceil(14*8/5) = 23 chars.
	id := NewID()
	if len(id) != 23 {
		t.Fatalf("len(NewID()) = %d, want 23 (%q)", len(id), id)
	}
}

func TestNewIDCharset(t *testing.T) {
	id := NewID()
	for _, r := range id {
		if !strings.ContainsRune(idCharset, r) {
			t.Fatalf("id %q contains rune %q outside Crockford lowercase charset", id, r)
		}
	}
}

func TestNewIDDistinct(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b {
		t.Fatalf("two NewID() calls returned the same value: %q", a)
	}
}

func TestNewIDSortable(t *testing.T) {
	first := NewID()
	time.Sleep(3 * time.Millisecond)
	second := NewID()
	if second <= first {
		t.Fatalf("id created later does not sort strictly after the earlier one: %q !> %q", second, first)
	}
}
