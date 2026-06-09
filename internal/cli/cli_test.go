package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/cli"
)

// tryRun exécute finador contre db, mot de passe fourni par l'environnement,
// Keychain désactivé pour ne jamais toucher le vrai trousseau en test.
func tryRun(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func run(t *testing.T, db string, args ...string) string {
	t.Helper()
	out, err := tryRun(t, db, args...)
	if err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func newDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.fin")
	run(t, db, "init")
	return db
}

func TestInitCreatesFile(t *testing.T) {
	db := newDB(t)
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
	if _, err := tryRun(t, db, "init"); err == nil {
		t.Fatal("init sur un fichier existant devrait échouer")
	}
}

func TestAccountAddAndList(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "CTO IBKR", "--tax", "gains:30%", "--ccy", "USD")
	out := run(t, db, "account", "list")
	for _, want := range []string{"pea-zephyr", "PEA Zephyr", "gains:17.2%", "cto-ibkr", "USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("list: %q manquant dans:\n%s", want, out)
		}
	}
	if _, err := tryRun(t, db, "account", "add", "PEA Zephyr"); err == nil {
		t.Fatal("doublon accepté")
	}
}
