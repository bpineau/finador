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
