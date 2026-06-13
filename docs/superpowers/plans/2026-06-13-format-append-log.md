# Format v2 — journal append-only + cache sidecar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remplacer le format de stockage `FINADOR1` (blob AES-GCM unique réécrit en entier) par un journal append-only chiffré, texte base64, un enregistrement par ligne, plus un cache marché en sidecar local — propice à la synchro git multi-machines.

**Architecture:** Le grand-livre devient une suite de lignes : en-tête JSON clair, puis un enregistrement scellé (AES-256-GCM, nonce propre, AAD = `hash(en-tête) ‖ seq ‖ tag-précédent`) par ligne base64, puis une ligne de tête authentifiée (compte + tag de fin). Le `Book` est rejoué depuis le log à l'ouverture. L'écriture est **diff-on-save** : l'API de mutation existante (`b.Add`, `b.RemoveTx`, `tx edit`) reste intacte ; le store calcule le diff vs l'état persisté et n'ajoute que les records utiles, ré-émettant les lignes inchangées byte-identiques. Le cache marché (`MarketData`) sort vers `os.UserCacheDir()/finador/<id>.cache`, chiffré avec une sous-clé HKDF distincte. **Abandon total de FINADOR1, aucune migration.**

**Tech Stack:** Go 1.26, stdlib (`crypto/aes`, `crypto/cipher`, `crypto/sha256`, `crypto/rand`, `encoding/base64`, `encoding/binary`, `encoding/json`, `compress/gzip`), `golang.org/x/crypto/argon2` + `golang.org/x/crypto/hkdf` (déjà dans l'arbre de deps), `shopspring/decimal`, `spf13/cobra`.

**Spec:** `docs/superpowers/specs/2026-06-13-format-append-log-design.md` — décision `DECISIONS.md` D15.

---

## File Structure

Paquet `internal/store` réorganisé (l'actuel `store.go` est entièrement remplacé) :

- `internal/store/header.go` — type `header`, encode/parse de la ligne 1 (JSON clair), dérivation de clés (Argon2id master + sous-clés HKDF).
- `internal/store/record.go` — types de records (union taguée), scellement/ouverture d'**une** ligne (base64, nonce, AAD, chaînage), ligne de tête (scellement/vérif).
- `internal/store/log.go` — lecture du log entier (parse + vérif chaîne + tête), rejeu → `Book`, sérialisation du log, snapshot + diff-on-save.
- `internal/store/store.go` — type `File`, `Create`/`Open`/`Save`/`Compact`, helper `atomicWrite`, glue (réutilise flock + diskStamp).
- `internal/store/cache.go` — sidecar marché : chemin (`os.UserCacheDir`/id, override `FINADOR_CACHE_DIR`), `SaveCache`/`loadCache`.
- `internal/store/flock_unix.go`, `flock_other.go` — **inchangés**.
- `internal/store/store_test.go`, `record_test.go`, `cache_test.go` — tests (l'ancien `store_test.go` format-spécifique est remplacé).

Wiring (hors `store`) :
- `internal/cli/cli.go` (`ensureFresh`), `internal/cli/refresh.go`, `internal/web/import.go` (`refresh`) — `f.Save()` du cache → `f.SaveCache()`.
- `internal/cli/compact.go` (nouveau) — commande `finador compact`.
- `README.md` — section édition du passé.

---

## Task 1: En-tête v2 et constantes

**Files:**
- Create: `internal/store/header.go`
- Test: `internal/store/header_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
	bad := `{"fmt":"finador-ledger","v":2,"kdf":"argon2id","t":3,"m":1,"p":4,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","id":"AAAAAAAAAAAAAAAAAAAAAA=="}`
	if _, err := parseHeader([]byte(bad)); err == nil {
		t.Fatal("expected out-of-bounds memory rejection")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestHeader -v`
Expected: FAIL — `undefined: defaultHeader` etc.

- [ ] **Step 3: Write the implementation**

```go
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
	formatVersion = 2
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestHeader -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/header.go internal/store/header_test.go
git commit -m "feat(store): v2 ledger header + HKDF subkeys"
```

---

## Task 2: Scellement/ouverture d'un enregistrement (AAD + chaînage)

**Files:**
- Create: `internal/store/record.go`
- Test: `internal/store/record_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"crypto/sha256"
	"errors"
	"testing"

	"finador/internal/domain"
)

func testGCMs(t *testing.T) (cipher, hdrHash []byte) { return nil, nil } // placeholder removed below

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
```

Delete the `testGCMs` placeholder line before running (it exists only to show it must not be used).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSealOpen -v`
Expected: FAIL — `undefined: gcmOf, record, sealLine, openLine, kTx, mustJSON`

- [ ] **Step 3: Write the implementation**

```go
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"finador/internal/domain"
)

type recKind string

const (
	kAcct    recKind = "acct"
	kAcctDel recKind = "acct-del"
	kAsset   recKind = "asset"
	kAssetDel recKind = "asset-del"
	kConfig  recKind = "config"
	kTx      recKind = "tx"
	kTxEdit  recKind = "tx-edit"
	kTxDel   recKind = "tx-del"
)

// record is one log entry: a kind tag and the JSON of its payload.
type record struct {
	K recKind         `json:"k"`
	D json.RawMessage `json:"d"`
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func gcmOf(key [32]byte) cipher.AEAD {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err) // fixed key size: cannot fail
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

// aad binds a record to the file (header hash), its position (seq) and the
// previous record's tag (hash chain): reorder/drop/splice break decryption.
func aad(hdrHash []byte, seq uint64, prevTag []byte) []byte {
	b := make([]byte, 0, len(hdrHash)+8+tagSize)
	b = append(b, hdrHash...)
	b = binary.BigEndian.AppendUint64(b, seq)
	return append(b, prevTag...)
}

// sealLine seals one record and returns its base64 line and its GCM tag.
func sealLine(g cipher.AEAD, hdrHash []byte, seq uint64, prevTag []byte, rec record) (line string, tag []byte) {
	pt := mustJSON(rec)
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	ct := g.Seal(nil, nonce, pt, aad(hdrHash, seq, prevTag))
	raw := append(append([]byte(nil), nonce...), ct...)
	return base64.StdEncoding.EncodeToString(raw), ct[len(ct)-tagSize:]
}

// openLine opens one record line. Any failure (malformed, wrong key, broken
// chain, tampering) is reported as domain.ErrBadPassword — indistinguishable.
func openLine(g cipher.AEAD, hdrHash []byte, seq uint64, prevTag []byte, line string) (record, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) < nonceSize+tagSize {
		return record{}, nil, domain.ErrBadPassword
	}
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	pt, err := g.Open(nil, nonce, ct, aad(hdrHash, seq, prevTag))
	if err != nil {
		return record{}, nil, domain.ErrBadPassword
	}
	var rec record
	if err := json.Unmarshal(pt, &rec); err != nil {
		return record{}, nil, fmt.Errorf("unreadable record: %w", err)
	}
	return rec, ct[len(ct)-tagSize:], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSealOpen -v && go test ./internal/store/ -run TestOpenLineWrongChain -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/record.go internal/store/record_test.go
git commit -m "feat(store): per-record seal/open with chained AAD"
```

---

## Task 3: Ligne de tête authentifiée (anti-troncature)

**Files:**
- Modify: `internal/store/record.go` (append)
- Test: `internal/store/record_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestHeadSealVerify -v`
Expected: FAIL — `undefined: sealHead, openHead`

- [ ] **Step 3: Write the implementation (append to record.go)**

```go
// headPayload is the authenticated trailer committing the record count and the
// final tag, so truncation (dropping trailing records) is detected.
type headPayload struct {
	Count int    `json:"count"`
	Head  []byte `json:"head"`
}

func headAAD(hdrHash []byte, count int) []byte {
	b := append(append([]byte(nil), hdrHash...), []byte("finador-head")...)
	return binary.BigEndian.AppendUint64(b, uint64(count))
}

func sealHead(g cipher.AEAD, hdrHash []byte, count int, lastTag []byte) string {
	pt := mustJSON(headPayload{Count: count, Head: lastTag})
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	ct := g.Seal(nil, nonce, pt, headAAD(hdrHash, count))
	raw := append(append([]byte(nil), nonce...), ct...)
	return base64.StdEncoding.EncodeToString(raw)
}

func openHead(g cipher.AEAD, hdrHash []byte, count int, line string) (headPayload, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) < nonceSize+tagSize {
		return headPayload{}, domain.ErrBadPassword
	}
	pt, err := g.Open(nil, raw[:nonceSize], raw[nonceSize:], headAAD(hdrHash, count))
	if err != nil {
		return headPayload{}, domain.ErrBadPassword
	}
	var hp headPayload
	if err := json.Unmarshal(pt, &hp); err != nil {
		return headPayload{}, domain.ErrBadPassword
	}
	return hp, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestHeadSealVerify -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/record.go internal/store/record_test.go
git commit -m "feat(store): authenticated head trailer (anti-truncation)"
```

---

## Task 4: Lecture/écriture du log + rejeu en Book

**Files:**
- Create: `internal/store/log.go`
- Test: `internal/store/log_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"finador/internal/domain"
)

// buildLog seals a sequence of records into a full file body (for tests).
func buildLog(t *testing.T, h header, password string, recs []record) []byte {
	t.Helper()
	keyLog, _ := deriveKeys(password, h)
	g := gcmOf(keyLog)
	hh := sha256.Sum256(h.encode())
	var entries []entry
	prev := make([]byte, tagSize)
	for i, r := range recs {
		line, tag := sealLine(g, hh[:], uint64(i+1), prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, hh[:], len(entries), lastTagOrZero(entries))
	return writeLog(h.encode(), entries, head)
}

func TestReadLogAndReplay(t *testing.T) {
	h := defaultHeader()
	recs := []record{
		{K: kAcct, D: mustJSON(domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})},
		{K: kTx, D: mustJSON(domain.Transaction{ID: 1, Account: "pea", Kind: domain.Deposit})},
		{K: kTx, D: mustJSON(domain.Transaction{ID: 2, Account: "pea", Kind: domain.Deposit})},
		{K: kTxDel, D: mustJSON(txRef{ID: 1})},
	}
	raw := buildLog(t, h, "pw", recs)

	_, _, entries, _, _, err := readLog("t.fin", raw, "pw")
	if err != nil {
		t.Fatal(err)
	}
	b, err := replay(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Accounts) != 1 || len(b.Transactions) != 1 || b.Transactions[0].ID != 2 {
		t.Fatalf("replay wrong: accts=%d txs=%+v", len(b.Accounts), b.Transactions)
	}
	if b.LastTxID != 2 {
		t.Fatalf("LastTxID=%d, want 2", b.LastTxID)
	}
}

func TestReadLogDetectsTamper(t *testing.T) {
	h := defaultHeader()
	recs := []record{{K: kTx, D: mustJSON(domain.Transaction{ID: 1})}, {K: kTx, D: mustJSON(domain.Transaction{ID: 2})}}
	raw := buildLog(t, h, "pw", recs)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	// Reorder the two record lines (lines[1] and lines[2]).
	lines[1], lines[2] = lines[2], lines[1]
	reordered := []byte(strings.Join(lines, "\n") + "\n")
	if _, _, _, _, _, err := readLog("t.fin", reordered, "pw"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword on reorder, got %v", err)
	}

	// Drop the last record line (truncation): head count no longer matches.
	truncated := buildLog(t, h, "pw", recs)
	tl := strings.Split(strings.TrimRight(string(truncated), "\n"), "\n")
	tl = append(tl[:2], tl[3:]...) // remove second record, keep head
	if _, _, _, _, _, err := readLog("t.fin", []byte(strings.Join(tl, "\n")+"\n"), "pw"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword on truncation, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestReadLog -v`
Expected: FAIL — `undefined: entry, lastTagOrZero, writeLog, readLog, replay, txRef`

- [ ] **Step 3: Write the implementation**

```go
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"finador/internal/domain"
)

// entry is one decoded log line kept in memory: its verbatim base64 line (so
// Save can re-emit unchanged lines byte-identically), its GCM tag (for the
// chain) and the decoded record.
type entry struct {
	line string
	tag  []byte
	rec  record
}

func lastTagOrZero(entries []entry) []byte {
	if len(entries) == 0 {
		return make([]byte, tagSize)
	}
	return entries[len(entries)-1].tag
}

func writeLog(hdrLine []byte, entries []entry, headLine string) []byte {
	var b bytes.Buffer
	b.Write(hdrLine)
	b.WriteByte('\n')
	for _, e := range entries {
		b.WriteString(e.line)
		b.WriteByte('\n')
	}
	b.WriteString(headLine)
	b.WriteByte('\n')
	return b.Bytes()
}

// readLog parses and fully authenticates a ledger file body. Returns the
// header (with its exact on-disk line), the entries, and both subkeys.
func readLog(path string, raw []byte, password string) (h header, hdrLine []byte, entries []entry, keyLog, keyCache [32]byte, err error) {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s is not a finador file", path)
	}
	h, err = parseHeader([]byte(lines[0]))
	if err != nil {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, err)
	}
	keyLog, keyCache = deriveKeys(password, h)
	hh := sha256.Sum256([]byte(lines[0]))
	g := gcmOf(keyLog)

	recordLines := lines[1 : len(lines)-1]
	prev := make([]byte, tagSize)
	for i, line := range recordLines {
		rec, tag, e := openLine(g, hh[:], uint64(i+1), prev, line)
		if e != nil {
			return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, e)
		}
		entries = append(entries, entry{line: line, tag: tag, rec: rec})
		prev = tag
	}
	hp, e := openHead(g, hh[:], len(entries), lines[len(lines)-1])
	if e != nil || hp.Count != len(entries) || !bytes.Equal(hp.Head, lastTagOrZero(entries)) {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, domain.ErrBadPassword)
	}
	return h, []byte(lines[0]), entries, keyLog, keyCache, nil
}

type idRef struct {
	ID string `json:"id"`
}

type txRef struct {
	ID domain.TxID `json:"id"`
}

type cfgKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// replay folds the log into a materialized Book. LastTxID is the max tx id ever
// seen (including superseded/tombstoned), so new ids never collide after delete.
func replay(entries []entry) (*domain.Book, error) {
	b := domain.NewBook()
	var lastTx domain.TxID
	for _, e := range entries {
		switch e.rec.K {
		case kAcct:
			var a domain.Account
			if err := json.Unmarshal(e.rec.D, &a); err != nil {
				return nil, err
			}
			b.Accounts = upsertAccount(b.Accounts, &a)
		case kAcctDel:
			var ref idRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Accounts = rejectAccount(b.Accounts, domain.AccountID(ref.ID))
		case kAsset:
			var a domain.Asset
			if err := json.Unmarshal(e.rec.D, &a); err != nil {
				return nil, err
			}
			b.Assets = upsertAsset(b.Assets, &a)
		case kAssetDel:
			var ref idRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Assets = rejectAsset(b.Assets, domain.AssetID(ref.ID))
		case kConfig:
			var kv cfgKV
			if err := json.Unmarshal(e.rec.D, &kv); err != nil {
				return nil, err
			}
			b.Config[kv.Key] = kv.Value
		case kTx, kTxEdit:
			var t domain.Transaction
			if err := json.Unmarshal(e.rec.D, &t); err != nil {
				return nil, err
			}
			b.Transactions = upsertTx(b.Transactions, &t)
			if t.ID > lastTx {
				lastTx = t.ID
			}
		case kTxDel:
			var ref txRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Transactions = rejectTx(b.Transactions, ref.ID)
			if ref.ID > lastTx {
				lastTx = ref.ID
			}
		default:
			return nil, fmt.Errorf("unknown record kind %q", e.rec.K)
		}
	}
	b.LastTxID = lastTx
	return b, nil
}

func upsertAccount(xs []*domain.Account, a *domain.Account) []*domain.Account {
	for i, x := range xs {
		if x.ID == a.ID {
			xs[i] = a
			return xs
		}
	}
	return append(xs, a)
}

func rejectAccount(xs []*domain.Account, id domain.AccountID) []*domain.Account {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertAsset(xs []*domain.Asset, a *domain.Asset) []*domain.Asset {
	for i, x := range xs {
		if x.ID == a.ID {
			xs[i] = a
			return xs
		}
	}
	return append(xs, a)
}

func rejectAsset(xs []*domain.Asset, id domain.AssetID) []*domain.Asset {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertTx(xs []*domain.Transaction, t *domain.Transaction) []*domain.Transaction {
	for i, x := range xs {
		if x.ID == t.ID {
			xs[i] = t
			return xs
		}
	}
	return append(xs, t)
}

func rejectTx(xs []*domain.Transaction, id domain.TxID) []*domain.Transaction {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestReadLog -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/log.go internal/store/log_test.go
git commit -m "feat(store): log read/write + replay into Book"
```

---

## Task 5: Snapshot et diff-on-save

**Files:**
- Modify: `internal/store/log.go` (append)
- Test: `internal/store/log_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func recKinds(recs []record) []recKind {
	out := make([]recKind, len(recs))
	for i, r := range recs {
		out[i] = r.K
	}
	return out
}

func TestDiffDetectsChanges(t *testing.T) {
	b := domain.NewBook()
	_ = b.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	b.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	snap := snapshotOf(b)

	if d := diff(snap, b); len(d) != 0 {
		t.Fatalf("no-op diff should be empty, got %v", recKinds(d))
	}

	// New transaction -> one kTx record.
	b.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	if d := diff(snap, b); len(d) != 1 || d[0].K != kTx {
		t.Fatalf("expected one kTx, got %v", recKinds(d))
	}

	// Edit the first transaction in place -> kTxEdit.
	snap = snapshotOf(b)
	b.Transactions[0].Note = "corrigé"
	if d := diff(snap, b); len(d) != 1 || d[0].K != kTxEdit {
		t.Fatalf("expected one kTxEdit, got %v", recKinds(d))
	}

	// Delete a transaction -> kTxDel.
	snap = snapshotOf(b)
	_ = b.RemoveTx(b.Transactions[1].ID)
	if d := diff(snap, b); len(d) != 1 || d[0].K != kTxDel {
		t.Fatalf("expected one kTxDel, got %v", recKinds(d))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestDiffDetectsChanges -v`
Expected: FAIL — `undefined: snapshotOf, diff`

- [ ] **Step 3: Write the implementation (append to log.go)**

```go
// snapshot is the last-persisted state, by stable identity, used to compute the
// minimal set of records to append on Save. Entities are compared by their JSON.
type snapshot struct {
	accts  map[domain.AccountID][]byte
	assets map[domain.AssetID][]byte
	txs    map[domain.TxID][]byte
	config map[string]string
}

func snapshotOf(b *domain.Book) snapshot {
	s := snapshot{
		accts:  map[domain.AccountID][]byte{},
		assets: map[domain.AssetID][]byte{},
		txs:    map[domain.TxID][]byte{},
		config: map[string]string{},
	}
	for _, a := range b.Accounts {
		s.accts[a.ID] = mustJSON(a)
	}
	for _, a := range b.Assets {
		s.assets[a.ID] = mustJSON(a)
	}
	for _, t := range b.Transactions {
		s.txs[t.ID] = mustJSON(t)
	}
	for k, v := range b.Config {
		s.config[k] = v
	}
	return s
}

// diff returns the records to append so the log materializes b, given prev as
// the last-persisted state. Order: config, accounts (+deletes), assets
// (+deletes), then transactions (+deletes) — definitions before references.
func diff(prev snapshot, b *domain.Book) []record {
	var recs []record

	for k, v := range b.Config {
		if old, ok := prev.config[k]; !ok || old != v {
			recs = append(recs, record{K: kConfig, D: mustJSON(cfgKV{Key: k, Value: v})})
		}
	}

	for _, a := range b.Accounts {
		cur := mustJSON(a)
		if old, ok := prev.accts[a.ID]; !ok || !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kAcct, D: cur})
		}
	}
	for id := range prev.accts {
		if !hasAccount(b, id) {
			recs = append(recs, record{K: kAcctDel, D: mustJSON(idRef{ID: string(id)})})
		}
	}

	for _, a := range b.Assets {
		cur := mustJSON(a)
		if old, ok := prev.assets[a.ID]; !ok || !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kAsset, D: cur})
		}
	}
	for id := range prev.assets {
		if !hasAsset(b, id) {
			recs = append(recs, record{K: kAssetDel, D: mustJSON(idRef{ID: string(id)})})
		}
	}

	for _, t := range b.Transactions {
		cur := mustJSON(t)
		old, ok := prev.txs[t.ID]
		if !ok {
			recs = append(recs, record{K: kTx, D: cur})
		} else if !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kTxEdit, D: cur})
		}
	}
	for id := range prev.txs {
		if !hasTx(b, id) {
			recs = append(recs, record{K: kTxDel, D: mustJSON(txRef{ID: id})})
		}
	}
	return recs
}

func hasAccount(b *domain.Book, id domain.AccountID) bool {
	for _, a := range b.Accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

func hasAsset(b *domain.Book, id domain.AssetID) bool {
	for _, a := range b.Assets {
		if a.ID == id {
			return true
		}
	}
	return false
}

func hasTx(b *domain.Book, id domain.TxID) bool {
	for _, t := range b.Transactions {
		if t.ID == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestDiffDetectsChanges -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/log.go internal/store/log_test.go
git commit -m "feat(store): snapshot + diff-on-save record computation"
```

---

## Task 6: Type File — Create/Open/Save (glue + écriture atomique)

**Files:**
- Create: `internal/store/store.go` (replaces the old file entirely — delete old contents first)
- Test: `internal/store/store_test.go` (replaces the old format-specific tests)

- [ ] **Step 1: Replace the test file**

Replace the entire contents of `internal/store/store_test.go` with:

```go
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/domain"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir()) // keep the market sidecar out of the real cache dir
	return filepath.Join(t.TempDir(), "test.fin")
}

func TestCreateOpenRoundTrip(t *testing.T) {
	path := tmpPath(t)
	f, err := Create(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	f.Book.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Open(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Accounts) != 1 || back.Book.Accounts[0].Name != "PEA" || len(back.Book.Transactions) != 1 {
		t.Fatalf("content lost: %+v", back.Book)
	}
}

func TestAppendKeepsPrefixByteStable(t *testing.T) {
	path := tmpPath(t)
	f, _ := Create(path, "pw")
	_ = f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	f.Book.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	prefixLines := strings.Split(strings.TrimRight(string(before), "\n"), "\n")
	keep := strings.Join(prefixLines[:len(prefixLines)-1], "\n") // header + records, drop head

	f.Book.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), keep+"\n") {
		t.Fatal("appending a transaction rewrote the existing record lines (prefix not byte-stable)")
	}
}

func TestWrongPassword(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "bon"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "mauvais"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword, got %v", err)
	}
}

func TestTamperedFile(t *testing.T) {
	path := tmpPath(t)
	f, _ := Create(path, "s3cret")
	_ = f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	_ = f.Save()
	raw, _ := os.ReadFile(path)
	raw[len(raw)-2] ^= 0xFF // flip a byte in the head line
	_ = os.WriteFile(path, raw, 0o600)
	if _, err := Open(path, "s3cret"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("expected ErrBadPassword, got %v", err)
	}
}

func TestNotAFinadorFile(t *testing.T) {
	path := tmpPath(t)
	_ = os.WriteFile(path, []byte("PK\x03\x04 not finador\nx\n"), 0o600)
	if _, err := Open(path, "s3cret"); err == nil || !strings.Contains(err.Error(), "finador") {
		t.Fatalf("expected format error, got %v", err)
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path, "b"); err == nil {
		t.Fatal("Create should refuse to overwrite")
	}
}

func TestConcurrentWriteRejected(t *testing.T) {
	path := tmpPath(t)
	f1, _ := Create(path, "pw")
	f2, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	_ = f1.Book.AddAccount(&domain.Account{ID: "a", Name: "A", Currency: domain.EUR})
	if err := f1.Save(); err != nil { // bumps mtime/size on disk
		t.Fatal(err)
	}
	_ = f2.Book.AddAccount(&domain.Account{ID: "b", Name: "B", Currency: domain.EUR})
	if err := f2.Save(); !errors.Is(err, ErrConcurrent) {
		t.Fatalf("expected ErrConcurrent, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCreateOpenRoundTrip -v`
Expected: FAIL — the old `store.go` still defines a conflicting `File`/`Create`/`Open`; compilation error or behavior mismatch. (We replace `store.go` next.)

- [ ] **Step 3: Replace `internal/store/store.go` entirely**

```go
// Package store reads and writes the encrypted portfolio file.
//
// Layout (UTF-8 text, one record per line):
//
//	line 1            clear JSON header: format, Argon2 params, salt, file id
//	lines 2..N+1      base64( nonce ‖ AES-256-GCM(record, AAD) ), AAD chains records
//	last line         base64( nonce ‖ AES-256-GCM(head, AAD) ): record count + final tag
//
// Writes are diff-on-save: unchanged record lines are re-emitted byte-for-byte,
// so a small logical change is a small change on disk (git-friendly). The market
// cache lives in a separate local sidecar (see cache.go), never in this file.
package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"finador/internal/domain"
)

// ErrConcurrent signals another process wrote the file since it was opened.
var ErrConcurrent = errors.New("file modified by another process since it was opened — retry the command")

type diskStamp struct {
	size  int64
	mtime int64 // ns
}

func stampOf(path string) (diskStamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return diskStamp{}, false
	}
	return diskStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}, true
}

// File is an open, decrypted portfolio file.
type File struct {
	Path string
	Book *domain.Book

	hdr      header
	hdrLine  []byte // exact on-disk header bytes (so re-emission is byte-stable)
	hdrHash  []byte // sha256(hdrLine), the AAD prefix
	keyLog   [32]byte
	keyCache [32]byte
	entries  []entry
	snap     snapshot
	disk     diskStamp // (size, mtime) at Open; zero at Create
}

// Create makes a new encrypted file holding an empty Book. It refuses to overwrite.
func Create(path, password string) (*File, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	}
	h := defaultHeader()
	hdrLine := h.encode()
	hh := sha256.Sum256(hdrLine)
	keyLog, keyCache := deriveKeys(password, h)
	f := &File{
		Path: path, Book: domain.NewBook(),
		hdr: h, hdrLine: hdrLine, hdrHash: hh[:],
		keyLog: keyLog, keyCache: keyCache,
		snap: snapshotOf(domain.NewBook()),
	}
	return f, f.Save()
}

// Open reads and decrypts a file. A wrong password and a tampered file are
// indistinguishable by construction: both yield domain.ErrBadPassword.
func Open(path, password string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist — run 'finador init' to create it", path)
		}
		return nil, err
	}
	h, hdrLine, entries, keyLog, keyCache, err := readLog(path, raw, password)
	if err != nil {
		return nil, err
	}
	book, err := replay(entries)
	if err != nil {
		return nil, fmt.Errorf("%s: unreadable content: %w", path, err)
	}
	hh := sha256.Sum256(hdrLine)
	f := &File{
		Path: path, Book: book,
		hdr: h, hdrLine: hdrLine, hdrHash: hh[:],
		keyLog: keyLog, keyCache: keyCache,
		entries: entries, snap: snapshotOf(book),
	}
	f.loadCache()
	f.disk, _ = stampOf(path)
	return f, nil
}

// Save appends the records needed to materialize the current Book, re-emitting
// unchanged lines byte-for-byte, then writes atomically (tmp + fsync + rename;
// the previous version becomes .bak). Optimistic concurrency: ErrConcurrent if
// another process wrote since Open.
func (f *File) Save() error {
	unlock, err := lockSidecar(f.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	if f.disk != (diskStamp{}) {
		if cur, ok := stampOf(f.Path); ok && cur != f.disk {
			return fmt.Errorf("%s: %w", f.Path, ErrConcurrent)
		}
	}

	g := gcmOf(f.keyLog)
	entries := append([]entry(nil), f.entries...) // local copy: commit only on success
	prev := lastTagOrZero(entries)
	seq := uint64(len(entries))
	for _, r := range diff(f.snap, f.Book) {
		seq++
		line, tag := sealLine(g, f.hdrHash, seq, prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)

	if err := atomicWrite(f.Path, out, true); err != nil {
		return err
	}
	f.entries = entries
	f.snap = snapshotOf(f.Book)
	f.disk, _ = stampOf(f.Path)
	return nil
}

// atomicWrite writes data to path via tmp + fsync + rename. When backup is true,
// an existing file is rotated to .bak first.
func atomicWrite(path string, data []byte, backup bool) error {
	tmp := path + ".tmp"
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if backup {
		if _, err := os.Stat(path); err == nil {
			if err := os.Rename(path, path+".bak"); err != nil {
				return err
			}
		}
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: Run the store tests**

Run: `go test ./internal/store/ -v`
Expected: PASS (all tasks 1–6 tests). `cache.go` does not exist yet, so `loadCache` must be added — if compilation fails on `loadCache`, proceed to Task 7 which defines it, then re-run. (To keep this task self-contained, add a temporary `func (f *File) loadCache() {}` stub in `store.go`, replaced in Task 7.)

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): File Create/Open/Save on append-only log"
```

---

## Task 7: Cache marché en sidecar

**Files:**
- Create: `internal/store/cache.go` (remove the temporary `loadCache` stub from `store.go`)
- Test: `internal/store/cache_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"path/filepath"
	"testing"

	"finador/internal/domain"
)

func TestMarketCacheRoundTripsViaSidecar(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "x.fin")
	f, err := Create(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	f.Book.Market.Price("cw8").Merge([]domain.PricePoint{{Date: domain.Date{Year: 2025, Month: 1, Day: 2}, Close: 12.5}})
	if err := f.SaveCache(); err != nil {
		t.Fatal(err)
	}

	back, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if back.Book.Market.Prices["cw8"] == nil || len(back.Book.Market.Prices["cw8"].Points) != 1 {
		t.Fatalf("market not restored from sidecar: %+v", back.Book.Market)
	}
}

func TestMissingSidecarIsEmptyMarketNoError(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "y.fin")
	if _, err := Create(path, "pw"); err != nil {
		t.Fatal(err)
	}
	back, err := Open(path, "pw") // no SaveCache ever called
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Market.Prices) != 0 {
		t.Fatal("expected empty market")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMarketCache -v`
Expected: FAIL — `undefined: (*File).SaveCache` (and remove the Task 6 stub `loadCache`).

- [ ] **Step 3: Write the implementation**

Remove the temporary `func (f *File) loadCache() {}` stub from `store.go`, then create `cache.go`:

```go
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"finador/internal/domain"
)

const cacheMagic = "FINCACHE2"

// cacheDir is os.UserCacheDir() unless FINADOR_CACHE_DIR overrides it (tests).
func cacheDir() (string, error) {
	if d := os.Getenv("FINADOR_CACHE_DIR"); d != "" {
		return d, nil
	}
	return os.UserCacheDir()
}

// cachePath derives the sidecar path from the file id: deterministic and stable
// across machines, physically outside any git repo.
func (f *File) cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(f.hdr.ID)
	return filepath.Join(dir, "finador", id+".cache"), nil
}

// SaveCache writes the market cache to its encrypted sidecar (cache subkey). It
// is independent of Save(): only market-touching paths (refresh) call it.
func (f *File) SaveCache() error {
	p, err := f.cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var plain bytes.Buffer
	zw := gzip.NewWriter(&plain)
	if err := json.NewEncoder(zw).Encode(f.Book.Market); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	g := gcmOf(f.keyCache)
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	out := append([]byte(cacheMagic), nonce...)
	out = g.Seal(out, nonce, plain.Bytes(), []byte(cacheMagic))
	return atomicWrite(p, out, false)
}

// loadCache populates Book.Market from the sidecar. A missing or unreadable
// sidecar leaves the market empty (a refresh rebuilds it): never an error.
func (f *File) loadCache() {
	p, err := f.cachePath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if len(raw) < len(cacheMagic)+nonceSize+tagSize || string(raw[:len(cacheMagic)]) != cacheMagic {
		return
	}
	g := gcmOf(f.keyCache)
	nonce := raw[len(cacheMagic) : len(cacheMagic)+nonceSize]
	plain, err := g.Open(nil, nonce, raw[len(cacheMagic)+nonceSize:], []byte(cacheMagic))
	if err != nil {
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return
	}
	defer zr.Close()
	var md domain.MarketData
	if json.NewDecoder(zr).Decode(&md) == nil {
		f.Book.Market = md
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (whole store package)

- [ ] **Step 5: Commit**

```bash
git add internal/store/cache.go internal/store/store.go internal/store/cache_test.go
git commit -m "feat(store): market cache sidecar (HKDF cache subkey)"
```

---

## Task 8: Câbler le refresh vers SaveCache

**Files:**
- Modify: `internal/cli/cli.go:74-78` (`ensureFresh`)
- Modify: `internal/cli/refresh.go:28-30`
- Modify: `internal/web/import.go` (`refresh` handler — the `s.file.Save()` after `market.Refresh`)

- [ ] **Step 1: Update `ensureFresh` in `internal/cli/cli.go`**

Replace the save block:

```go
	if len(sum.Fetched) > 0 {
		if err := f.SaveCache(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: cache not saved:", err)
		}
	}
```

- [ ] **Step 2: Update `internal/cli/refresh.go`**

Replace `if err := f.Save(); err != nil {` with:

```go
			if err := f.SaveCache(); err != nil {
				return err
			}
```

- [ ] **Step 3: Update the web `refresh` handler in `internal/web/import.go`**

Replace its `if err := s.file.Save(); err != nil {` (the one right after `market.Refresh`) with:

```go
	if err := s.file.SaveCache(); err != nil {
		http.Redirect(w, r, "/assets?error="+url.QueryEscape("could not save: "+err.Error()), http.StatusSeeOther)
		return
	}
```

> The CSV-import handler keeps `s.file.Save()` (it mutates the ledger, not the cache).

- [ ] **Step 4: Run the full suite**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS. If a market/cli/web test asserts that refreshed market data survives a reopen, add `t.Setenv("FINADOR_CACHE_DIR", t.TempDir())` at its top and ensure its refresh path now persists via `SaveCache`. Fix each such failure; no other package logic changes.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/refresh.go internal/web/import.go internal/cli/*_test.go internal/web/*_test.go internal/market/*_test.go
git commit -m "feat: persist refreshed market data to the sidecar cache"
```

---

## Task 9: Commande `finador compact`

**Files:**
- Modify: `internal/store/store.go` (append `Compact`)
- Create: `internal/cli/compact.go`
- Modify: `internal/cli/cli.go:50-53` (register `compactCmd(a)`)
- Test: `internal/store/store_test.go` (append)

- [ ] **Step 1: Write the failing test (append to `store_test.go`)**

```go
func TestCompactDropsDeadRecords(t *testing.T) {
	path := tmpPath(t)
	f, _ := Create(path, "pw")
	_ = f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	t1 := f.Book.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	f.Book.Add(domain.Transaction{Account: "pea", Kind: domain.Deposit})
	_ = f.Save()
	_ = f.Book.RemoveTx(t1.ID) // tombstone
	_ = f.Save()

	bigger, _ := os.ReadFile(path)
	linesBefore := strings.Count(string(bigger), "\n")

	if err := f.Compact(); err != nil {
		t.Fatal(err)
	}
	smaller, _ := os.ReadFile(path)
	if strings.Count(string(smaller), "\n") >= linesBefore {
		t.Fatal("compaction did not shrink the log")
	}
	back, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Transactions) != 1 || len(back.Book.Accounts) != 1 {
		t.Fatalf("compacted book differs: %+v", back.Book)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCompactDropsDeadRecords -v`
Expected: FAIL — `undefined: (*File).Compact`

- [ ] **Step 3: Implement `Compact` (append to `store.go`)**

```go
// Compact rewrites a minimal log from the current Book — dropping superseded and
// tombstoned records — with a fresh chain. One full-file rewrite; rare.
func (f *File) Compact() error {
	unlock, err := lockSidecar(f.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if f.disk != (diskStamp{}) {
		if cur, ok := stampOf(f.Path); ok && cur != f.disk {
			return fmt.Errorf("%s: %w", f.Path, ErrConcurrent)
		}
	}

	g := gcmOf(f.keyLog)
	var entries []entry
	prev := lastTagOrZero(entries)
	for i, r := range diff(snapshot{}, f.Book) { // empty prev -> all creates
		line, tag := sealLine(g, f.hdrHash, uint64(i+1), prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)
	if err := atomicWrite(f.Path, out, true); err != nil {
		return err
	}
	f.entries = entries
	f.snap = snapshotOf(f.Book)
	f.disk, _ = stampOf(f.Path)
	return nil
}
```

> `diff` reads `prev.config[k]`, `prev.accts`, etc. on the zero `snapshot{}` (nil maps): reads and `range` over nil maps are valid no-ops in Go, so every current entity is emitted as a create.

- [ ] **Step 4: Create `internal/cli/compact.go`**

```go
package cli

import (
	"github.com/spf13/cobra"
)

func compactCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Rewrite the ledger, dropping superseded and deleted records",
		Long: "Rewrite the ledger file into a minimal form. Past edits and deletions " +
			"are stored as extra journal records (so syncing stays append-friendly); " +
			"compact discards those dead records. Rarely needed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			if err := f.Compact(); err != nil {
				return err
			}
			cmd.Println("Ledger compacted.")
			return nil
		},
	}
}
```

- [ ] **Step 5: Register the command in `internal/cli/cli.go`**

In `New()`, add `compactCmd(a)` to the `root.AddCommand(...)` list (e.g. after `lockCmd(a)`).

- [ ] **Step 6: Run tests + commit**

Run: `go test ./internal/store/ ./internal/cli/ -count=1`
Expected: PASS

```bash
git add internal/store/store.go internal/store/store_test.go internal/cli/compact.go internal/cli/cli.go
git commit -m "feat(store,cli): finador compact — rewrite a minimal log"
```

---

## Task 10: Documentation édition du passé + régénération demo.fin

**Files:**
- Modify: `README.md` (CLI section)
- Regenerate: `demo.fin`, `demo.fin.bak` (remove stale FINADOR1 artifacts)

- [ ] **Step 1: Add a README subsection on editing the past**

Under the CLI commands section of `README.md`, add:

```markdown
### Correcting the past

The ledger is an append-only journal, but every entry stays editable — a
correction is just a new record appended to the file (so git syncing stays
small and conflict-light). Use the same commands whatever the date:

- Fix a wrong quantity or amount:
  `finador tx edit 142 --qty 100 --total 4567.80`
- Move a transaction to the right envelope:
  `finador tx edit 142 --account "PEA Zephyr"`
- Change its date or note:
  `finador tx edit 142 --date 2026-03-01 --note "corrected"`
- Remove a spurious or duplicate entry:
  `finador tx rm 142`
- List transactions to find the id:
  `finador tx list --account "PEA Zephyr"`

The web UI offers the same edit/delete on every transaction row. Edits and
deletions accumulate as journal records; run `finador compact` occasionally to
rewrite a minimal log (rarely necessary).
```

- [ ] **Step 2: Regenerate the demo file**

Run (uses the new format; adjust the demo seed script if the repo has one, otherwise create a minimal demo):

```bash
rm -f demo.fin demo.fin.bak demo.fin.lock
FINADOR_DB=demo.fin ./bin/finador init --no-keychain <<<'demo' 2>/dev/null || true
# If a demo-seeding script exists (e.g. scripts/demo.sh), run it here instead.
```

Verify it opens:

```bash
FINADOR_DB=demo.fin ./bin/finador --no-keychain value <<<'demo'
```

Expected: opens without error (the file is the new text format).

- [ ] **Step 3: Commit**

```bash
git add README.md demo.fin
git commit -m "docs: editing the past in the CLI; regenerate demo for v2 format"
```

---

## Task 11: Vérification finale (vet, lint, suite complète, byte-stabilité git)

**Files:** none (verification only)

- [ ] **Step 1: Static checks and full test suite**

Run: `make` (or `go vet ./... && go test ./... -count=1`)
Expected: 0 vet issues, all tests PASS. If `golangci-lint` is configured (`.golangci.yml`), run `golangci-lint run` and fix reported issues.

- [ ] **Step 2: Manual git-friendliness check**

```bash
tmp=$(mktemp -d); cd "$tmp"; git init -q
FINADOR_DB=p.fin "$OLDPWD/bin/finador" init --no-keychain <<<'pw'
git add p.fin && git commit -qm init
FINADOR_DB=p.fin "$OLDPWD/bin/finador" --no-keychain cash deposit --account default --amount 100 <<<'pw' 2>/dev/null || true
git add p.fin && git commit -qm "one deposit"
git show --stat HEAD   # expect a tiny diff: ~1 line added, head line changed
cd "$OLDPWD"; rm -rf "$tmp"
```

Expected: the second commit's diff touches only the trailing lines (one record line added + the head line), the earlier record lines unchanged.

- [ ] **Step 3: Final commit (if any lint fixes were made)**

```bash
git add -A
git commit -m "chore: vet/lint fixes for v2 store" || echo "nothing to commit"
```

---

## Self-Review notes (done while writing)

- **Spec coverage:** header v2 (T1), KDF+HKDF subkeys (T1), per-record seal/AAD chain (T2), head/anti-truncation (T3), read+replay (T4), diff-on-save (T5), File API + byte-stable prefix + concurrency (T6), sidecar cache (T7), refresh→SaveCache wiring (T8), compaction (T9), edit-the-past docs (T10), verification + git check (T11). Abandon of FINADOR1 = old `store.go`/`store_test.go` fully replaced (T6). Future Stooq fallback is explicitly out of scope (noted in spec/memory).
- **Placeholder scan:** every code step contains complete code; the only deliberate stub (`loadCache` in T6) is introduced and removed within the plan (T6→T7) with explicit instructions.
- **Type consistency:** `header`, `record`/`recKind` constants, `entry`, `snapshot`, `aad`, `sealLine`/`openLine`, `sealHead`/`openHead`, `readLog`/`writeLog`/`replay`, `diff`/`snapshotOf`, `File` fields, `atomicWrite(path,data,backup)`, `SaveCache`/`loadCache`/`cachePath` — names and signatures are used consistently across tasks. `readLog` returns `(header, []byte, []entry, [32]byte, [32]byte, error)` everywhere it is called (T4 test, T6 `Open`).
```
