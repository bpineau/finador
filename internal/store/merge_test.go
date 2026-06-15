package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"finador/internal/domain"
)

// rejectResolve fails the test if a conflict ever surfaces.
func rejectResolve(t *testing.T) func(Conflict) (int, error) {
	t.Helper()
	return func(c Conflict) (int, error) {
		t.Fatalf("unexpected conflict on %s/%s @ %s: %v", c.Class, c.ID, c.Ts, c.Candidates)
		return 0, nil
	}
}

// sealRecords builds a fresh chained log of recs (with their own Ts) under f's
// header and persists it, then re-opens - giving a File whose entries carry the
// exact Ts we chose. This is the test seam around Save stamping time.Now().
func sealRecords(t *testing.T, f *File, recs []record) {
	t.Helper()
	g := gcmOf(f.keyLog)
	var entries []entry
	prev := lastTagOrZero(entries)
	for i, r := range recs {
		line, tag := sealLine(g, f.hdrHash, uint64(i+1), prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)
	if err := atomicWrite(f.Path, out, true); err != nil {
		t.Fatal(err)
	}
	book, err := replay(entries)
	if err != nil {
		t.Fatal(err)
	}
	f.Book = book
	f.entries = entries
	f.snap = snapshotOf(book)
	f.disk, _ = stampOf(f.Path)
}

func txRec(k recKind, id, ts, note string) record {
	tx := domain.Transaction{ID: domain.TxID(id), Account: "acct1", Kind: domain.Deposit, Note: note}
	return record{K: k, Ts: ts, D: mustJSON(tx)}
}

func txDelRec(id, ts string) record {
	return record{K: kTxDel, Ts: ts, D: mustJSON(txRef{ID: domain.TxID(id)})}
}

func hasTxNote(b *domain.Book, id, note string) bool {
	for _, t := range b.Transactions {
		if string(t.ID) == id {
			return t.Note == note
		}
	}
	return false
}

// twoCopies makes two open Files that are byte-identical copies of the same
// freshly created ledger (same header id), each at its own path.
func twoCopies(t *testing.T, pw string) (*File, *File) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	p1 := dir + "/f1.fin"
	p2 := dir + "/f2.fin"
	f1, err := Create(p1, pw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	f2, err := Open(p2, pw)
	if err != nil {
		t.Fatal(err)
	}
	return f1, f2
}

// TestMergeUnionNoLoss: distinct ids on each side both survive.
func TestMergeUnionNoLoss(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{txRec(kTx, "A", "2026-06-13T10:00:00Z", "from f1")})
	sealRecords(t, f2, []record{txRec(kTx, "B", "2026-06-13T10:00:01Z", "from f2")})

	if _, err := f1.Merge(f2, rejectResolve(t)); err != nil {
		t.Fatal(err)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Transactions) != 2 {
		t.Fatalf("expected both A and B, got %d txs: %+v", len(back.Book.Transactions), back.Book.Transactions)
	}
	if !hasTxNote(back.Book, "A", "from f1") || !hasTxNote(back.Book, "B", "from f2") {
		t.Fatalf("a distinct entity was lost: %+v", back.Book.Transactions)
	}
}

// TestMergeLWWByTs: same tx edited on both sides, later ts wins.
func TestMergeLWWByTs(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{
		txRec(kTx, "X", "2026-06-13T10:00:00Z", "create"),
		txRec(kTxEdit, "X", "2026-06-13T11:00:00Z", "f1 edit (earlier)"),
	})
	sealRecords(t, f2, []record{
		txRec(kTx, "X", "2026-06-13T10:00:00Z", "create"),
		txRec(kTxEdit, "X", "2026-06-13T12:00:00Z", "f2 edit (later)"),
	})

	if _, err := f1.Merge(f2, rejectResolve(t)); err != nil {
		t.Fatal(err)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Transactions) != 1 || !hasTxNote(back.Book, "X", "f2 edit (later)") {
		t.Fatalf("LWW wrong: %+v", back.Book.Transactions)
	}
}

// TestMergeConflictTie: same tx, same ts, different payloads -> resolve invoked.
func TestMergeConflictTie(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	const ts = "2026-06-13T12:00:00Z"
	sealRecords(t, f1, []record{txRec(kTx, "X", ts, "value-A")})
	sealRecords(t, f2, []record{txRec(kTx, "X", ts, "value-B")})

	var seen Conflict
	stats, err := f1.Merge(f2, func(c Conflict) (int, error) {
		seen = c
		// keep the candidate from the other file (value-B)
		for i, cand := range c.Candidates {
			if strings.Contains(cand, "value-B") {
				return i, nil
			}
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Conflicts != 1 {
		t.Fatalf("expected 1 conflict, got %d", stats.Conflicts)
	}
	if len(seen.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %v", len(seen.Candidates), seen.Candidates)
	}
	if seen.Class != "tx" || seen.ID != "X" || seen.Ts != ts {
		t.Fatalf("conflict descriptor wrong: %+v", seen)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTxNote(back.Book, "X", "value-B") {
		t.Fatalf("chosen candidate not kept: %+v", back.Book.Transactions)
	}
}

// TestMergeDeleteWins: delete with the later ts removes the entity.
func TestMergeDeleteWins(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{
		txRec(kTx, "X", "2026-06-13T10:00:00Z", "create"),
		txRec(kTxEdit, "X", "2026-06-13T11:00:00Z", "f1 edit (earlier)"),
	})
	sealRecords(t, f2, []record{
		txRec(kTx, "X", "2026-06-13T10:00:00Z", "create"),
		txDelRec("X", "2026-06-13T12:00:00Z"),
	})

	stats, err := f1.Merge(f2, rejectResolve(t))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entities != 0 {
		t.Fatalf("expected 0 live entities (X deleted), got %d", stats.Entities)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Transactions) != 0 {
		t.Fatalf("X should be absent after merge: %+v", back.Book.Transactions)
	}
}

// TestMergeEditVsDeleteSameTsConflict: an edit and a delete at the SAME ts are
// distinct payloads -> a conflict the user must resolve. Picking the delete
// drops the entity; picking the edit keeps it.
func TestMergeEditVsDeleteSameTsConflict(t *testing.T) {
	const ts = "2026-06-13T12:00:00Z"

	// User keeps the delete candidate -> entity gone.
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{txRec(kTxEdit, "X", ts, "kept-edit")})
	sealRecords(t, f2, []record{txDelRec("X", ts)})
	stats, err := f1.Merge(f2, func(c Conflict) (int, error) {
		for i, cand := range c.Candidates {
			if containsKind(cand, "tx-del") {
				return i, nil
			}
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Conflicts != 1 {
		t.Fatalf("expected 1 conflict, got %d", stats.Conflicts)
	}
	back, _ := Open(f1.Path, "pw")
	if len(back.Book.Transactions) != 0 {
		t.Fatalf("delete chosen, X should be gone: %+v", back.Book.Transactions)
	}

	// User keeps the edit candidate -> entity stays.
	g1, g2 := twoCopies(t, "pw")
	sealRecords(t, g1, []record{txRec(kTxEdit, "Y", ts, "kept-edit")})
	sealRecords(t, g2, []record{txDelRec("Y", ts)})
	if _, err := g1.Merge(g2, func(c Conflict) (int, error) {
		for i, cand := range c.Candidates {
			if containsKind(cand, "tx-edit") {
				return i, nil
			}
		}
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
	back2, _ := Open(g1.Path, "pw")
	if !hasTxNote(back2.Book, "Y", "kept-edit") {
		t.Fatalf("edit chosen, Y should stay: %+v", back2.Book.Transactions)
	}
}

func containsKind(candidate, kind string) bool {
	return strings.Contains(candidate, "["+kind+"]")
}

// TestMergeDifferentLedgersRefused: different header ids -> error, no write.
func TestMergeDifferentLedgersRefused(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	f1, err := Create(dir+"/a.fin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Create(dir+"/b.fin", "pw") // independent header id
	if err != nil {
		t.Fatal(err)
	}
	sealRecords(t, f1, []record{txRec(kTx, "A", "2026-06-13T10:00:00Z", "a")})
	before, _ := os.ReadFile(f1.Path)

	if _, err := f1.Merge(f2, rejectResolve(t)); err == nil {
		t.Fatal("merging different ledgers should fail")
	} else if !strings.Contains(err.Error(), "different ledgers") {
		t.Fatalf("unexpected error: %v", err)
	}
	after, _ := os.ReadFile(f1.Path)
	if string(before) != string(after) {
		t.Fatal("refused merge must not write anything")
	}
}

// TestMergeIntegrityPreserved: the merged file reopens cleanly (chain + head
// verify) and replays to the expected Book.
func TestMergeIntegrityPreserved(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	// f1: account + tx A + a config; f2: tx B + an edit of A (later).
	acct := record{K: kAcct, Ts: "2026-06-13T09:00:00Z", D: mustJSON(domain.Account{ID: "acct1", Name: "PEA", Currency: domain.EUR})}
	cfg := record{K: kConfig, Ts: "2026-06-13T09:00:01Z", D: mustJSON(cfgKV{Key: "risk-free", Value: "2%"})}
	sealRecords(t, f1, []record{
		acct,
		cfg,
		txRec(kTx, "A", "2026-06-13T10:00:00Z", "a-create"),
	})
	sealRecords(t, f2, []record{
		acct,
		txRec(kTx, "A", "2026-06-13T10:00:00Z", "a-create"),
		txRec(kTxEdit, "A", "2026-06-13T13:00:00Z", "a-edited-later"),
		txRec(kTx, "B", "2026-06-13T11:00:00Z", "b-create"),
	})

	stats, err := f1.Merge(f2, rejectResolve(t))
	if err != nil {
		t.Fatal(err)
	}
	// acct + config + tx A + tx B = 4 live entities.
	if stats.Entities != 4 {
		t.Fatalf("expected 4 entities, got %d", stats.Entities)
	}

	back, err := Open(f1.Path, "pw") // a clean Open verifies the whole chain + head
	if err != nil {
		t.Fatalf("merged file failed to reopen/verify: %v", err)
	}
	if len(back.Book.Accounts) != 1 || back.Book.Accounts[0].Name != "PEA" {
		t.Fatalf("account lost: %+v", back.Book.Accounts)
	}
	if back.Book.Config["risk-free"] != "2%" {
		t.Fatalf("config lost: %+v", back.Book.Config)
	}
	if len(back.Book.Transactions) != 2 {
		t.Fatalf("expected A and B, got: %+v", back.Book.Transactions)
	}
	if !hasTxNote(back.Book, "A", "a-edited-later") {
		t.Fatalf("LWW edit of A lost: %+v", back.Book.Transactions)
	}
	if !hasTxNote(back.Book, "B", "b-create") {
		t.Fatalf("B lost: %+v", back.Book.Transactions)
	}

	// Records must be chronologically ordered and preserve their original ts.
	var prev string
	for i, e := range back.entries {
		if e.rec.Ts < prev {
			t.Fatalf("record %d out of ts order: %q after %q", i, e.rec.Ts, prev)
		}
		prev = e.rec.Ts
	}
}

// TestMergeIdenticalConcurrentEditsNotAConflict: same edit on both sides at the
// same ts is not a conflict (identical payload).
func TestMergeIdenticalConcurrentEditsNotAConflict(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	const ts = "2026-06-13T12:00:00Z"
	sealRecords(t, f1, []record{txRec(kTx, "X", ts, "same")})
	sealRecords(t, f2, []record{txRec(kTx, "X", ts, "same")})

	stats, err := f1.Merge(f2, rejectResolve(t))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Conflicts != 0 {
		t.Fatalf("identical concurrent writes must not conflict, got %d", stats.Conflicts)
	}
	if stats.Entities != 1 {
		t.Fatalf("expected 1 entity, got %d", stats.Entities)
	}
}

// TestMergeConfigByKey: config entities are grouped by key, not id.
func TestMergeConfigByKey(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{
		{K: kConfig, Ts: "2026-06-13T10:00:00Z", D: mustJSON(cfgKV{Key: "k", Value: "early"})},
	})
	sealRecords(t, f2, []record{
		{K: kConfig, Ts: "2026-06-13T11:00:00Z", D: mustJSON(cfgKV{Key: "k", Value: "late"})},
		{K: kConfig, Ts: "2026-06-13T10:30:00Z", D: mustJSON(cfgKV{Key: "other", Value: "v"})},
	})

	if _, err := f1.Merge(f2, rejectResolve(t)); err != nil {
		t.Fatal(err)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if back.Book.Config["k"] != "late" {
		t.Fatalf("config LWW by key wrong: %+v", back.Book.Config)
	}
	if back.Book.Config["other"] != "v" {
		t.Fatalf("distinct config key lost: %+v", back.Book.Config)
	}
}

func labelRec(id, ts, name string) record {
	l := domain.Label{ID: domain.LabelID(id), Account: "acct1", Asset: "cw8", Name: name}
	return record{K: kLabel, Ts: ts, D: mustJSON(l)}
}

// TestMergeUnionsLabels: two copies each tag the SAME (account, asset) pair with
// a different label name. The labels are distinct random-id entities, so the
// merge unions them through the generic id machinery - both must survive, with
// no conflict.
func TestMergeUnionsLabels(t *testing.T) {
	f1, f2 := twoCopies(t, "pw")
	sealRecords(t, f1, []record{labelRec("L1", "2026-06-13T10:00:00Z", "retraite")})
	sealRecords(t, f2, []record{labelRec("L2", "2026-06-13T10:00:01Z", "core")})

	if _, err := f1.Merge(f2, rejectResolve(t)); err != nil {
		t.Fatal(err)
	}
	back, err := Open(f1.Path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	got := back.Book.LabelsFor("acct1", "cw8")
	if len(got) != 2 || got[0] != "core" || got[1] != "retraite" {
		t.Fatalf("merge should union both labels, got %v", got)
	}
}

// TestMergeEntityIDDecodes sanity-checks entityID across classes.
func TestMergeEntityID(t *testing.T) {
	cases := []struct {
		rec  record
		want string
	}{
		{record{K: kAcct, D: mustJSON(idRef{ID: "a1"})}, "a1"},
		{record{K: kAcctDel, D: mustJSON(idRef{ID: "a1"})}, "a1"},
		{record{K: kConfig, D: mustJSON(cfgKV{Key: "ttl", Value: "x"})}, "ttl"},
		{record{K: kTxDel, D: mustJSON(txRef{ID: "t1"})}, "t1"},
	}
	for _, c := range cases {
		got, err := entityID(c.rec)
		if err != nil {
			t.Fatalf("entityID(%s): %v", c.rec.K, err)
		}
		if got != c.want {
			t.Fatalf("entityID(%s) = %q, want %q", c.rec.K, got, c.want)
		}
	}
	// And the json tags round-trip the way we expect.
	var ref idRef
	if err := json.Unmarshal(mustJSON(idRef{ID: "z"}), &ref); err != nil || ref.ID != "z" {
		t.Fatalf("idRef round-trip: %v %q", err, ref.ID)
	}
}
