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
		{K: kTx, D: mustJSON(domain.Transaction{ID: "tx-a", Account: "pea", Kind: domain.Deposit})},
		{K: kTx, D: mustJSON(domain.Transaction{ID: "tx-b", Account: "pea", Kind: domain.Deposit})},
		{K: kTxDel, D: mustJSON(txRef{ID: "tx-a"})},
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
	if len(b.Accounts) != 1 || len(b.Transactions) != 1 || b.Transactions[0].ID != "tx-b" {
		t.Fatalf("replay wrong: accts=%d txs=%+v", len(b.Accounts), b.Transactions)
	}
}

func TestReadLogDetectsTamper(t *testing.T) {
	h := defaultHeader()
	recs := []record{{K: kTx, D: mustJSON(domain.Transaction{ID: "tx-a", Kind: domain.Deposit})}, {K: kTx, D: mustJSON(domain.Transaction{ID: "tx-b", Kind: domain.Deposit})}}
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

func TestDiffEmitsLabelRecords(t *testing.T) {
	b := domain.NewBook()
	_ = b.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	_ = b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8", Currency: domain.EUR})
	snap := snapshotOf(b)

	// Add a label -> one kLabel record.
	lbl := &domain.Label{ID: domain.LabelID(domain.NewID()), Account: "pea", Asset: "cw8", Name: "retraite"}
	if err := b.AddLabel(lbl); err != nil {
		t.Fatal(err)
	}
	if d := diff(snap, b); len(d) != 1 || d[0].K != kLabel {
		t.Fatalf("expected one kLabel, got %v", recKinds(d))
	}

	// Remove it -> one kLabelDel record.
	snap = snapshotOf(b)
	if err := b.RemoveLabel(lbl.ID); err != nil {
		t.Fatal(err)
	}
	if d := diff(snap, b); len(d) != 1 || d[0].K != kLabelDel {
		t.Fatalf("expected one kLabelDel, got %v", recKinds(d))
	}
}

// TestLabelRoundTrip: a label survives a Save -> Open round-trip on a real file.
func TestLabelRoundTrip(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := t.TempDir() + "/rt.fin"
	f, err := Create(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR})
	_ = f.Book.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "CW8", Currency: domain.EUR})
	if err := f.Book.AddLabel(&domain.Label{ID: domain.LabelID(domain.NewID()), Account: "pea", Asset: "cw8", Name: "retraite"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	back, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Book.LabelsFor("pea", "cw8"); len(got) != 1 || got[0] != "retraite" {
		t.Fatalf("label lost on round-trip: %v", got)
	}
}
