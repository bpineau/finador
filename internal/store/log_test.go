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
	recs := []record{{K: kTx, D: mustJSON(domain.Transaction{ID: 1, Kind: domain.Deposit})}, {K: kTx, D: mustJSON(domain.Transaction{ID: 2, Kind: domain.Deposit})}}
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
