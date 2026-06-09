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

func TestAssetAddSetList(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "Patrimoine")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--name", "Amundi MSCI World", "--group", "actions/monde")
	run(t, db, "asset", "add", "Maison à Achères", "--kind", "property", "--group", "immo")
	out := run(t, db, "asset", "list")
	for _, want := range []string{"cw8", "CW8.PA", "actions/monde", "maison-a-acheres", "property"} {
		if !strings.Contains(out, want) {
			t.Errorf("asset list: %q manquant dans:\n%s", want, out)
		}
	}
	// estimation datée ; l'enveloppe par défaut est l'unique compte existant
	out = run(t, db, "asset", "set", "maison-a-acheres", "450000", "--at", "2026-06-01")
	for _, want := range []string{"450000 EUR", "2026-06-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("asset set: %q manquant dans %q", want, out)
		}
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

func TestTxListEditRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	run(t, db, "cash", "set", "pea-zephyr", "12500", "--at", "2026-06-02")

	out := run(t, db, "tx", "list")
	if !strings.Contains(out, "buy") || !strings.Contains(out, "statement") {
		t.Fatalf("tx list:\n%s", out)
	}
	if out = run(t, db, "tx", "list", "--kind", "buy"); strings.Contains(out, "statement") {
		t.Fatalf("filtre --kind inopérant:\n%s", out)
	}

	run(t, db, "tx", "edit", "1", "--qty", "12", "--total", "6600")
	if out = run(t, db, "tx", "list", "--kind", "buy"); !strings.Contains(out, "6600 EUR") {
		t.Fatalf("edit inopérant:\n%s", out)
	}

	run(t, db, "tx", "rm", "2")
	if out = run(t, db, "tx", "list"); strings.Contains(out, "statement") {
		t.Fatalf("rm inopérant:\n%s", out)
	}
	if _, err := tryRun(t, db, "tx", "rm", "99"); err == nil {
		t.Fatal("rm d'un ID inconnu aurait dû échouer")
	}
}

func TestImportCommand(t *testing.T) {
	db := newDB(t)
	csvPath := filepath.Join(t.TempDir(), "txs.csv")
	content := "date,kind,account,asset,quantity,price,amount,currency,group,note\n" +
		"2026-01-15,buy,PEA,CW8.PA,10,550,,EUR,actions/monde,\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "1 importée(s), 0 ignorée(s)") {
		t.Fatalf("import: %q", out)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "0 importée(s), 1 ignorée(s)") {
		t.Fatalf("ré-import: %q", out)
	}
}

func TestConfigSetGet(t *testing.T) {
	db := newDB(t)
	run(t, db, "config", "set", "risk-free", "2.4%")
	if out := run(t, db, "config", "get", "risk-free"); !strings.Contains(out, "2.4%") {
		t.Fatalf("config get: %q", out)
	}
	if out := run(t, db, "config", "get"); !strings.Contains(out, "risk-free = 2.4%") {
		t.Fatalf("config get (tout): %q", out)
	}
}

func TestCurrencyNormalized(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "Compte US", "--ccy", "usd")
	if out := run(t, db, "account", "list"); !strings.Contains(out, "USD") {
		t.Fatalf("devise non normalisée:\n%s", out)
	}
	if _, err := tryRun(t, db, "account", "add", "Compte X", "--ccy", "banana"); err == nil {
		t.Fatal("devise invalide acceptée")
	}
}

func TestAddTradeCashAndFlows(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")

	out := run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	for _, want := range []string{"buy", "5500 EUR", "PEA Zephyr"} {
		if !strings.Contains(out, want) {
			t.Errorf("achat: %q manquant dans %q", want, out)
		}
	}
	out = run(t, db, "sell", "cw8", "4", "2310", "2026-06-05") // vente, montant total
	if !strings.Contains(out, "sell") || !strings.Contains(out, "2310 EUR") {
		t.Errorf("vente: %q", out)
	}
	// quantité négative possible via add, derrière -- (sinon pflag lit -4 comme un flag)
	out = run(t, db, "add", "--", "cw8", "-2", "@577", "2026-06-06")
	if !strings.Contains(out, "sell") || !strings.Contains(out, "1154 EUR") {
		t.Errorf("vente via qté négative: %q", out)
	}
	if _, err := tryRun(t, db, "add", "cw8", "5"); err == nil {
		t.Fatal("prix manquant accepté")
	}

	out = run(t, db, "cash", "set", "pea-zephyr", "12500")
	if !strings.Contains(out, "12500 EUR") {
		t.Errorf("cash set: %q", out)
	}
	out = run(t, db, "deposit", "PEA Zephyr", "5000", "2026-01-10")
	if !strings.Contains(out, "deposit") || !strings.Contains(out, "5000 EUR") {
		t.Errorf("deposit: %q", out)
	}
	out = run(t, db, "withdraw", "PEA Zephyr", "1000")
	if !strings.Contains(out, "withdraw") {
		t.Errorf("withdraw: %q", out)
	}
}
