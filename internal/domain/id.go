package domain

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// idEncoding is Crockford base32 (lowercase, no padding): a stable, dependency-
// free alphabet shared by every implementation reading this format.
var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID returns a random, time-sortable identifier: 6 bytes big-endian Unix
// milliseconds followed by 8 random bytes, Crockford base32 (lowercase, no
// padding). Lexicographically sortable by creation time; collision-free across
// machines. No external dependency.
func NewID() string {
	var buf [14]byte
	ms := uint64(time.Now().UnixMilli())
	// uint48 big-endian: the low 6 bytes of the millisecond timestamp.
	binary.BigEndian.PutUint64(buf[:8], ms)
	copy(buf[:6], buf[2:8])
	if _, err := rand.Read(buf[6:]); err != nil {
		panic(err) // no system CSPRNG: nothing sensible to do
	}
	return idEncoding.EncodeToString(buf[:])
}
