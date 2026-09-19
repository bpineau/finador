package domain

import (
	"strings"
	"sync"
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

// TestNewIDMonotonic is the property an import depends on: a tight loop mints
// far more ids than the millisecond clock can separate, and they must still
// come out strictly increasing - and unique.
func TestNewIDMonotonic(t *testing.T) {
	const n = 10000
	ids := make([]string, n)
	seen := make(map[string]int, n)
	for i := range ids {
		ids[i] = NewID()
		if prev, dup := seen[ids[i]]; dup {
			t.Fatalf("id %q minted twice (calls %d and %d)", ids[i], prev, i)
		}
		seen[ids[i]] = i
		if i > 0 && ids[i] <= ids[i-1] {
			t.Fatalf("id %d (%q) does not sort strictly after id %d (%q)", i, ids[i], i-1, ids[i-1])
		}
		if len(ids[i]) != 23 {
			t.Fatalf("id %d has length %d, want 23 (%q)", i, len(ids[i]), ids[i])
		}
	}
}

// TestNewIDConcurrent pins thread safety: ids stay unique and well-formed
// under parallel minting. Order across goroutines is whatever the scheduler
// decides - only uniqueness is a promise there.
func TestNewIDConcurrent(t *testing.T) {
	const goroutines, each = 16, 500
	out := make(chan string, goroutines*each)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				out <- NewID()
			}
		}()
	}
	wg.Wait()
	close(out)

	seen := make(map[string]bool, goroutines*each)
	for id := range out {
		if seen[id] {
			t.Fatalf("concurrent minting produced the duplicate id %q", id)
		}
		seen[id] = true
	}
	if len(seen) != goroutines*each {
		t.Fatalf("got %d distinct ids, want %d", len(seen), goroutines*each)
	}
}

// TestNewIDBackwardsClock: a clock stepping backwards (an NTP correction, a
// suspended laptop) must not break ordering. The generator then keeps the
// last millisecond it used and walks the random tail instead.
func TestNewIDBackwardsClock(t *testing.T) {
	before := NewID()

	idClock.Lock()
	future := idClock.last
	putIDMillis(&future, idMillis(future)+60_000) // pretend the clock ran an hour fast
	idClock.last, idClock.used = future, true
	idClock.Unlock()

	ahead := NewID()
	back := NewID() // minted with the wall clock now far BEHIND the last id
	if before >= ahead || ahead >= back {
		t.Fatalf("ids not strictly increasing across a backwards clock step: %q, %q, %q", before, ahead, back)
	}
}

// TestNextIDCarriesIntoTheNextMillisecond exercises the overflow branch a
// real run needs 2^64 ids per millisecond to reach.
func TestNextIDCarriesIntoTheNextMillisecond(t *testing.T) {
	var last [14]byte
	putIDMillis(&last, 1_700_000_000_000)
	for i := 6; i < 14; i++ {
		last[i] = 0xff
	}
	next := nextID(last)
	if got, want := idMillis(next), uint64(1_700_000_000_001); got != want {
		t.Fatalf("overflow millisecond = %d, want %d", got, want)
	}
	if string(next[:]) <= string(last[:]) {
		t.Fatalf("overflowed id does not sort after its predecessor")
	}
}
