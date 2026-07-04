package store

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"

	"finador/internal/domain"
)

// TestKDFVectorsMatchSpec pins the published test vectors of docs/FORMAT.md §9.1.
// keyLog/keyCache derive from the Argon2id master, so matching them also pins the
// master, the Argon2 parameters, the HKDF info strings and every output length.
// If this drifts, the on-disk format changed and every independent reader (the
// Android client) breaks with it: update FORMAT.md deliberately, never silently.
func TestKDFVectorsMatchSpec(t *testing.T) {
	salt, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	h := header{Time: 3, MemoryKB: 65536, Threads: 4, Salt: salt}
	keyLog, keyCache := deriveKeys("correct horse battery staple", h)

	wantLog, _ := hex.DecodeString("156457f5a4060765068beda9f37d0fa8257deb767190905231e1fc1e4327167b")
	wantCache, _ := hex.DecodeString("7c39ddca718165d3a72ccd023957c2af4814c198ce871c0dda490d54e1b00b3a")
	if !bytes.Equal(keyLog[:], wantLog) {
		t.Errorf("keyLog vector drift (FORMAT.md §9.1):\n got  %x\n want %x", keyLog, wantLog)
	}
	if !bytes.Equal(keyCache[:], wantCache) {
		t.Errorf("keyCache vector drift (FORMAT.md §9.1):\n got  %x\n want %x", keyCache, wantCache)
	}
}

// TestSampleLedgerDecodes decrypts the committed reference fixture end-to-end and
// checks it reproduces the contents documented in docs/format-testdata/README.md.
// This is the spec's §9.2 promise: an independent reader must decode this exact
// file. It guards the whole pipeline (header parse, KDF, AAD chain, envelope,
// payload schemas, head trailer, fold) against any format regression.
func TestSampleLedgerDecodes(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir()) // keep the sidecar out of the real cache
	path := filepath.Join("..", "..", "docs", "format-testdata", "sample.ledger")

	f, err := Open(path, "finador-format-spec-v3")
	if err != nil {
		t.Fatalf("sample.ledger failed to decode (format drift?): %v", err)
	}
	b := f.Book

	if len(f.entries) != 7 {
		t.Fatalf("expected 7 records, got %d", len(f.entries))
	}
	if len(b.Accounts) != 2 || len(b.Assets) != 2 || len(b.Transactions) != 3 {
		t.Fatalf("folded shape wrong: accts=%d assets=%d txs=%d", len(b.Accounts), len(b.Assets), len(b.Transactions))
	}

	pea := b.Accounts[0]
	if pea.ID != "06fjrvs8x4n9bf1545k5e3r" || pea.Name != "PEA Zephyr" || pea.Currency != domain.EUR {
		t.Fatalf("account 1 wrong: %+v", pea)
	}
	if pea.Tax.String() != "gains:17.2%" {
		t.Fatalf("account tax rule wrong: %q", pea.Tax.String())
	}
	if b.Accounts[1].Tax.String() != "none" {
		t.Fatalf("Livret A should be tax none, got %q", b.Accounts[1].Tax.String())
	}

	if b.Assets[0].Kind != domain.Property || b.Assets[0].Name != "Appart Lyon" {
		t.Fatalf("asset 1 (property) wrong: %+v", b.Assets[0])
	}
	if b.Assets[1].Kind != domain.Security || b.Assets[1].Ticker != "CW8.PA" {
		t.Fatalf("asset 2 (security) wrong: %+v", b.Assets[1])
	}
}

// TestReplayRejectsUnknownKind pins the financial-safety rule of FORMAT.md §5/§8:
// a record kind the reader does not understand is a HARD error, never silently
// skipped (a skipped record could hide money).
func TestReplayRejectsUnknownKind(t *testing.T) {
	entries := []entry{{rec: record{K: "totally-unknown", D: mustJSON(idRef{ID: "x"})}}}
	if _, err := replay(entries); err == nil {
		t.Fatal("replay must hard-error on an unknown record kind, not skip it")
	}
}

// TestReplayTombstones exercises the delete/tombstone fold for every entity class
// (account, asset, label). A create followed by its *-del must leave nothing.
func TestReplayTombstones(t *testing.T) {
	h := defaultHeader()
	recs := []record{
		{K: kAcct, D: mustJSON(domain.Account{ID: "a1", Name: "A", Currency: domain.EUR})},
		{K: kAsset, D: mustJSON(domain.Asset{ID: "s1", Kind: domain.Security, Name: "S", Currency: domain.EUR})},
		{K: kLabel, D: mustJSON(domain.Label{ID: "l1", Account: "a1", Asset: "s1", Name: "core"})},
		{K: kAcctDel, D: mustJSON(idRef{ID: "a1"})},
		{K: kAssetDel, D: mustJSON(idRef{ID: "s1"})},
		{K: kLabelDel, D: mustJSON(idRef{ID: "l1"})},
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
	if len(b.Accounts) != 0 || len(b.Assets) != 0 || len(b.Labels) != 0 {
		t.Fatalf("tombstones left residue: accts=%d assets=%d labels=%d",
			len(b.Accounts), len(b.Assets), len(b.Labels))
	}
}
