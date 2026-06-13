package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	fmtLedger     = "finador-ledger"
	formatVersion = 3
	nonceSize     = 12
	tagSize       = 16
	saltSize      = 16
	fileIDSize    = 16
)

// header is the clear, authenticated first line of the ledger file. Its exact
// bytes are hashed into every record's AAD, so any edit to it fails decryption.
type header struct {
	Fmt      string `json:"fmt"`
	Version  int    `json:"v"`
	KDF      string `json:"kdf"`
	Time     uint32 `json:"t"`
	MemoryKB uint32 `json:"m"`
	Threads  uint8  `json:"p"`
	Salt     []byte `json:"salt"` // encoding/json renders []byte as base64
	ID       []byte `json:"id"`
}

func defaultHeader() header {
	h := header{
		Fmt: fmtLedger, Version: formatVersion, KDF: "argon2id",
		Time: 3, MemoryKB: 64 * 1024, Threads: uint8(min(4, runtime.NumCPU())),
		Salt: make([]byte, saltSize), ID: make([]byte, fileIDSize),
	}
	mustRand(h.Salt)
	mustRand(h.ID)
	return h
}

func mustRand(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(err) // no system CSPRNG: nothing sensible to do
	}
}

func (h header) encode() []byte {
	b, err := json.Marshal(h)
	if err != nil {
		panic(err)
	}
	return b
}

func parseHeader(line []byte) (header, error) {
	var h header
	if err := json.Unmarshal(line, &h); err != nil || h.Fmt != fmtLedger {
		return header{}, fmt.Errorf("not a finador file")
	}
	if h.Version != formatVersion {
		return header{}, fmt.Errorf("unsupported version %d (finador too old?)", h.Version)
	}
	// Parameters are read before any authentication (the key derives from them):
	// strict bounds avoid a panic or a memory bomb on a forged header.
	if h.KDF != "argon2id" ||
		h.Time < 1 || h.Time > 16 ||
		h.MemoryKB < 8 || h.MemoryKB > 1<<20 || // <= 1 GiB
		h.Threads < 1 || h.Threads > 16 ||
		len(h.Salt) != saltSize || len(h.ID) != fileIDSize {
		return header{}, fmt.Errorf("finador header: parameters out of bounds")
	}
	return h, nil
}

// deriveKeys turns the password into two independent 32-byte subkeys: one for
// the ledger log, one for the market cache sidecar.
func deriveKeys(password string, h header) (keyLog, keyCache [32]byte) {
	master := argon2.IDKey([]byte(password), h.Salt, h.Time, h.MemoryKB, h.Threads, 32)
	return hkdfKey(master, "finador-ledger-v2"), hkdfKey(master, "finador-cache-v2")
}

func hkdfKey(master []byte, info string) [32]byte {
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	var k [32]byte
	if _, err := io.ReadFull(r, k[:]); err != nil {
		panic(err)
	}
	return k
}
