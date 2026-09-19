package domain

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// idEncoding is Crockford base32 (lowercase, no padding): a stable, dependency-
// free alphabet shared by every implementation reading this format.
var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// idClock serializes NewID and remembers the last id it handed out, which is
// what makes a burst of ids sort in minting order. Process-local by design:
// nothing here is persisted, and a reader must never rely on it (FORMAT.md
// 4.2).
var idClock struct {
	sync.Mutex
	last [14]byte
	used bool
}

// NewID returns a time-sortable identifier: 6 bytes big-endian Unix
// milliseconds followed by 8 random bytes, Crockford base32 (lowercase, no
// padding). Lexicographically sortable by creation time; the 64 random bits
// make it collision-free across machines. No external dependency.
//
// Within one process the sequence is also strictly increasing, ULID-style.
// The millisecond clock is far too coarse for a burst: an import mints
// hundreds of records inside one tick, and with a fresh random tail each time
// their relative order would be drawn at random and then frozen in the ledger
// - where replay order is (date, id), so an intraday buy and sell could land
// either way round and move the average-cost basis and the latent tax with it.
// So when the clock has not advanced past the last id's millisecond (or went
// backwards, which a time adjustment does), NewID reuses that millisecond and
// increments the random part by one instead of drawing a new one. File order
// is then ledger order.
//
// Persisted ids keep exactly the same length, alphabet and timestamp prefix,
// and stay opaque: the guarantee is local to one process and no reader may
// assume it.
func NewID() string {
	var buf [14]byte
	putIDMillis(&buf, uint64(time.Now().UnixMilli()))
	if _, err := rand.Read(buf[6:]); err != nil {
		panic(err) // no system CSPRNG: nothing sensible to do
	}

	idClock.Lock()
	defer idClock.Unlock()
	if idClock.used && string(buf[:6]) <= string(idClock.last[:6]) {
		buf = nextID(idClock.last)
	}
	idClock.last, idClock.used = buf, true
	return idEncoding.EncodeToString(buf[:])
}

// nextID returns the smallest id strictly greater than last: its 8 random
// bytes incremented by one, same millisecond prefix. An all-ones tail carries
// into the prefix - the next millisecond, with a fresh random tail - which
// takes 2^64 ids inside one tick to reach.
func nextID(last [14]byte) [14]byte {
	next := last
	if tail := binary.BigEndian.Uint64(next[6:]); tail != math.MaxUint64 {
		binary.BigEndian.PutUint64(next[6:], tail+1)
		return next
	}
	putIDMillis(&next, idMillis(next)+1)
	if _, err := rand.Read(next[6:]); err != nil {
		panic(err)
	}
	return next
}

// putIDMillis writes ms as the id's 48-bit big-endian timestamp prefix: the
// low 6 bytes of its uint64 big-endian encoding.
func putIDMillis(buf *[14]byte, ms uint64) {
	var full [8]byte
	binary.BigEndian.PutUint64(full[:], ms)
	copy(buf[:6], full[2:])
}

// idMillis reads back the 48-bit timestamp prefix written by putIDMillis.
func idMillis(buf [14]byte) uint64 {
	var full [8]byte
	copy(full[2:], buf[:6])
	return binary.BigEndian.Uint64(full[:])
}
