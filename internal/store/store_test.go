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
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Open(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Accounts) != 1 || back.Book.Accounts[0].Name != "PEA" {
		t.Fatalf("contenu perdu: %+v", back.Book)
	}
}

func TestWrongPassword(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "bon"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "mauvais"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("attendu ErrBadPassword, eu: %v", err)
	}
}

func TestTamperedFile(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "s3cret"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF // altère le dernier octet du sceau
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "s3cret"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("attendu ErrBadPassword, eu: %v", err)
	}
}

func TestNotAFinadorFile(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte("PK\x03\x04 pas finador du tout"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "s3cret"); err == nil || !strings.Contains(err.Error(), "finador") {
		t.Fatalf("attendu erreur de format, eu: %v", err)
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path, "b"); err == nil {
		t.Fatal("Create aurait dû refuser d'écraser")
	}
}

func TestSaveKeepsBackup(t *testing.T) {
	path := tmpPath(t)
	f, err := Create(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Book.AddAccount(&domain.Account{ID: "v2", Name: "V2", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	bak, err := Open(path+".bak", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(bak.Book.Accounts) != 0 {
		t.Fatalf(".bak devrait être l'état précédent (vide), eu %d comptes", len(bak.Book.Accounts))
	}
}
