package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/cli"
	"finador/internal/domain"
	"finador/internal/market"
)

// tryRun exécute finador contre db, mot de passe fourni par l'environnement,
// Keychain désactivé pour ne jamais toucher le vrai trousseau en test.
// --offline est toujours ajouté : aucun test du harnais offline ne touche le réseau.
func tryRun(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain", "--offline"}, args...))
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
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--name", "Amundi MSCI World", "--group", "actions/monde")
	run(t, db, "asset", "add", "Maison à Achères", "--kind", "property", "--group", "immo")
	out := run(t, db, "asset", "list")
	for _, want := range []string{"cw8", "CW8.PA", "actions/monde", "Maison à Achères", "property"} {
		if !strings.Contains(out, want) {
			t.Errorf("asset list: %q manquant dans:\n%s", want, out)
		}
	}
	// estimation datée ; l'enveloppe par défaut est l'unique compte existant
	out = run(t, db, "asset", "set", "Maison à Achères", "450000", "--at", "2026-06-01")
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
	for _, want := range []string{"PEA Zephyr", "gains:17.2%", "CTO IBKR", "USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("list: %q manquant dans:\n%s", want, out)
		}
	}
	if _, err := tryRun(t, db, "account", "add", "PEA Zephyr"); err == nil {
		t.Fatal("doublon accepté")
	}
}

// txIDOf returns the id (first column) of the single tx-list line containing
// kind; fails the test otherwise.
func txIDOf(t *testing.T, db, kind string) string {
	t.Helper()
	out := run(t, db, "tx", "list", "--kind", kind)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == kind {
			return fields[0]
		}
	}
	t.Fatalf("no %s line in tx list:\n%s", kind, out)
	return ""
}

func TestTxListEditRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")
	run(t, db, "cash", "set", "PEA Zephyr", "12500", "--at", "2026-06-02")

	out := run(t, db, "tx", "list")
	if !strings.Contains(out, "buy") || !strings.Contains(out, "statement") {
		t.Fatalf("tx list:\n%s", out)
	}
	if out = run(t, db, "tx", "list", "--kind", "buy"); strings.Contains(out, "statement") {
		t.Fatalf("filtre --kind inopérant:\n%s", out)
	}

	buyID := txIDOf(t, db, "buy")
	run(t, db, "tx", "edit", buyID, "--qty", "12", "--total", "6600")
	if out = run(t, db, "tx", "list", "--kind", "buy"); !strings.Contains(out, "6600 EUR") {
		t.Fatalf("edit inopérant:\n%s", out)
	}

	stmtID := txIDOf(t, db, "statement")
	run(t, db, "tx", "rm", stmtID)
	if out = run(t, db, "tx", "list"); strings.Contains(out, "statement") {
		t.Fatalf("rm inopérant:\n%s", out)
	}
	if _, err := tryRun(t, db, "tx", "rm", "zzzzzzzz"); err == nil {
		t.Fatal("rm d'un ID inconnu aurait dû échouer")
	}
}

func TestImportCommand(t *testing.T) {
	db := newDB(t)
	// Accounts must be declared before import.
	run(t, db, "account", "add", "PEA")
	csvPath := filepath.Join(t.TempDir(), "txs.csv")
	content := "date,kind,account,asset,quantity,price,amount,currency,group,note\n" +
		"2026-01-15,buy,PEA,CW8.PA,10,550,,EUR,actions/monde,\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "1 imported, 0 skipped") {
		t.Fatalf("import: %q", out)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "0 imported, 1 skipped") {
		t.Fatalf("re-import: %q", out)
	}
	// Unknown account fails with actionable error.
	badPath := filepath.Join(t.TempDir(), "bad.csv")
	badContent := "date,kind,account,amount,currency\n2026-01-20,deposit,UndeclaredBank,100,EUR\n"
	if err := os.WriteFile(badPath, []byte(badContent), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tryRun(t, db, "import", badPath)
	if err == nil || !strings.Contains(out+err.Error(), "unknown account") {
		t.Fatalf("import with undeclared account should fail: err=%v out=%q", err, out)
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

// fakeSource sert des données de marché déterministes aux tests CLI.
type fakeSource struct{}

func (fakeSource) Resolve(_ context.Context, q string) (market.SymbolInfo, error) {
	if strings.EqualFold(q, "CW8.PA") {
		return market.SymbolInfo{Symbol: "CW8.PA", Name: "Amundi MSCI World UCITS ETF"}, nil
	}
	return market.SymbolInfo{}, domain.ErrNotFound
}

func (fakeSource) Daily(_ context.Context, ref market.Ref, _ domain.Date) (market.DailyData, error) {
	day := func(s string) domain.Date {
		d, err := domain.ParseDate(s)
		if err != nil {
			panic(err)
		}
		return d
	}
	switch ref.Symbol {
	case "CW8.PA":
		return market.DailyData{Currency: domain.EUR, Closes: []domain.PricePoint{
			{Date: day("2026-06-01"), Close: 550},
			{Date: day("2026-06-05"), Close: 560},
		}}, nil
	case "EURUSD=X":
		return market.DailyData{Currency: domain.USD, Closes: []domain.PricePoint{
			{Date: day("2026-01-01"), Close: 1.10},
			{Date: day("2026-06-01"), Close: 1.10},
			{Date: day("2026-06-05"), Close: 1.10},
		}}, nil
	case "GBPUSD=X":
		return market.DailyData{Currency: domain.USD, Closes: []domain.PricePoint{
			{Date: day("2026-01-01"), Close: 1.25},
			{Date: day("2026-06-01"), Close: 1.25},
			{Date: day("2026-06-05"), Close: 1.25},
		}}, nil
	}
	return market.DailyData{}, domain.ErrNotFound
}

// tryRunNet exécute finador SANS --offline, avec la Source factice.
func tryRunNet(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New(cli.WithSource(fakeSource{}))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func runNet(t *testing.T, db string, args ...string) string {
	t.Helper()
	out, err := tryRunNet(t, db, args...)
	if err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestValueEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "value", "--net", "--at", "2026-06-05")
	// 10 × 560 = 5600 ; cash suivi = 5000 − 5500 = −500 → brut 5100
	// base d'enveloppe 5000 → gain 100 → impôt 17.20 → net 5082.80
	for _, want := range []string{"5100.00 EUR", "17.20 EUR", "5082.80 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("value --net: %q manquant dans:\n%s", want, out)
		}
	}

	out = runNet(t, db, "value", "--ccy", "USD", "--at", "2026-06-05")
	if !strings.Contains(out, "5610.00 USD") { // 5100 × 1.10
		t.Errorf("value USD:\n%s", out)
	}

	// le cache permet ensuite le hors-ligne
	out = run(t, db, "value", "--at", "2026-06-05")
	if !strings.Contains(out, "5100.00 EUR") {
		t.Errorf("value --offline après cache:\n%s", out)
	}
}

func TestRefreshCommand(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	out := runNet(t, db, "refresh")
	if !strings.Contains(out, "refreshed") {
		t.Errorf("refresh: %q", out)
	}
	if _, err := tryRun(t, db, "refresh"); err == nil {
		t.Fatal("refresh en --offline aurait dû échouer")
	}
}

func TestAssetAddResolvesFromYahoo(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")
	out := runNet(t, db, "asset", "add", "cw8.pa", "--group", "actions/monde")
	if !strings.Contains(out, "Amundi MSCI World UCITS ETF") {
		t.Errorf("résolution Yahoo absente: %q", out)
	}
	list := run(t, db, "asset", "list")
	if !strings.Contains(list, "CW8.PA") { // ticker canonique résolu
		t.Errorf("asset list:\n%s", list)
	}
}

func TestValueDisplayFXMissing(t *testing.T) {
	// GBP n'est ni une devise de compte ni d'actif : le refresh normal ne la
	// couvre pas. ensureDisplayFX doit la récupérer à la demande (le
	// fakeSource sert GBPUSD=X) pour que --ccy GBP fonctionne.
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// D'abord on remplit le cache EUR (nécessaire pour avoir des prix)
	runNet(t, db, "value", "--at", "2026-06-05")

	// Un fakeSource sans GBPUSD=X : on utilise le fakeSource standard qui le sert maintenant,
	// mais la série GBP n'est pas dans le cache initialement → ensureDisplayFX la fetche.
	out := runNet(t, db, "value", "--ccy", "GBP", "--at", "2026-06-05")
	// 5100 EUR × 1.10/1.25 = 4488.00 GBP
	if !strings.Contains(out, "4488.00 GBP") {
		t.Errorf("value --ccy GBP: %q", out)
	}
}

func TestPerfEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "perf", "--to", "2026-06-05")
	// série : 5000 plat jusqu'au 1er juin (l'achat est neutre), puis
	// 06-05 : 10×560 − 500 = 5100 → TWR origine = +2.00 %
	for _, want := range []string{"PERIOD", "TWR", "XIRR", "inception", "+2.00%", "CAGR", "Sharpe", "Sortino", "max drawdown"} {
		if !strings.Contains(out, want) {
			t.Errorf("perf: %q manquant dans:\n%s", want, out)
		}
	}
	// XIRR des fenêtres courtes : tiret
	if !strings.Contains(out, "—") {
		t.Errorf("tiret XIRR absent:\n%s", out)
	}

	// portée inexistante → erreur propre
	if _, err := tryRun(t, db, "perf", "nimporte"); err == nil {
		t.Fatal("portée inconnue acceptée")
	}
}

func TestChartEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "chart", "--to", "2026-06-05")
	hasBraille := false
	for _, r := range out {
		if r > 0x2800 && r <= 0x28FF {
			hasBraille = true
			break
		}
	}
	if !hasBraille {
		t.Errorf("aucun caractère braille:\n%s", out)
	}
	for _, want := range []string{"2026-01-10", "2026-06-05", "5.1k"} {
		if !strings.Contains(out, want) {
			t.Errorf("chart: %q manquant dans:\n%s", want, out)
		}
	}
	// --net produit aussi une courbe
	if out := runNet(t, db, "chart", "--net", "--to", "2026-06-05"); !strings.Contains(out, "net") {
		t.Errorf("chart --net:\n%s", out)
	}
}

func TestAddTradeCashAndFlows(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")

	out := run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")
	for _, want := range []string{"buy", "5500 EUR", "PEA Zephyr"} {
		if !strings.Contains(out, want) {
			t.Errorf("achat: %q manquant dans %q", want, out)
		}
	}
	out = run(t, db, "asset", "sell", "cw8", "4", "2310", "2026-06-05") // vente, montant total
	if !strings.Contains(out, "sell") || !strings.Contains(out, "2310 EUR") {
		t.Errorf("vente: %q", out)
	}
	// quantité négative possible via asset buy, derrière -- (sinon pflag lit -2 comme un flag)
	out = run(t, db, "asset", "buy", "--", "cw8", "-2", "@577", "2026-06-06")
	if !strings.Contains(out, "sell") || !strings.Contains(out, "1154 EUR") {
		t.Errorf("vente via qté négative: %q", out)
	}
	if _, err := tryRun(t, db, "asset", "buy", "cw8", "5"); err == nil {
		t.Fatal("prix manquant accepté")
	}

	out = run(t, db, "cash", "set", "PEA Zephyr", "12500")
	if !strings.Contains(out, "12500 EUR") {
		t.Errorf("cash set: %q", out)
	}
	out = run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	if !strings.Contains(out, "deposit") || !strings.Contains(out, "5000 EUR") {
		t.Errorf("deposit: %q", out)
	}
	out = run(t, db, "cash", "withdraw", "PEA Zephyr", "1000")
	if !strings.Contains(out, "withdraw") {
		t.Errorf("withdraw: %q", out)
	}
}

func TestPerfAndValueExclude(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// valeur sans cw8 : il ne reste que le cash (5000 − 5500 = −500)
	out := runNet(t, db, "value", "--exclude", "cw8", "--at", "2026-06-05")
	if !strings.Contains(out, "-500.00 EUR") {
		t.Errorf("value --exclude:\n%s", out)
	}
	// perf accepte la même exclusion (liste à virgules)
	out = runNet(t, db, "perf", "--exclude", "cw8", "--to", "2026-06-05")
	if !strings.Contains(out, "inception") {
		t.Errorf("perf --exclude:\n%s", out)
	}
	// référence inconnue dans --exclude → erreur propre
	if _, err := tryRun(t, db, "value", "--exclude", "zzz"); err == nil {
		t.Fatal("exclusion inconnue acceptée")
	}
}

func TestValueWhatIfAndByAccount(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// hypothèse : cw8 à 600 → 10×600 − 500 = 5500 brut, et un delta vs réel (5100)
	out := runNet(t, db, "value", "--what-if", "cw8=600", "--at", "2026-06-05")
	for _, want := range []string{"5500.00 EUR", "what-if", "+400.00 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("what-if: %q manquant dans:\n%s", want, out)
		}
	}
	// breakdown by account
	out = runNet(t, db, "value", "--by", "account", "--at", "2026-06-05")
	if !strings.Contains(out, "PEA Zephyr") {
		t.Errorf("--by account:\n%s", out)
	}
	// erreurs propres
	if _, err := tryRun(t, db, "value", "--what-if", "zzz=10"); err == nil {
		t.Fatal("what-if sur actif inconnu accepté")
	}
	if _, err := tryRun(t, db, "value", "--what-if", "cw8"); err == nil {
		t.Fatal("what-if sans prix accepté")
	}
	if _, err := tryRun(t, db, "value", "--by", "n'importe"); err == nil {
		t.Fatal("--by invalide accepté")
	}
}

func TestAssetEditAndRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions")
	run(t, db, "asset", "add", "VIZR", "--alias", "vizr", "--group", "actions")

	run(t, db, "asset", "edit", "vizr", "--add-alias", "Vizor", "--withholding", "15%")
	out := run(t, db, "asset", "list")
	if !strings.Contains(out, "Vizor") || !strings.Contains(out, "15") {
		t.Errorf("asset list après edit:\n%s", out)
	}
	// l'alias résout
	run(t, db, "asset", "edit", "vizr", "--rm-alias", "Vizor")
	if out = run(t, db, "asset", "list"); strings.Contains(out, "Vizor,") {
		t.Errorf("alias non retiré:\n%s", out)
	}
	// collision refusée
	if _, err := tryRun(t, db, "asset", "edit", "vizr", "--add-alias", "CW8.PA"); err == nil {
		t.Fatal("collision d'alias acceptée")
	}
	// rm : refus si référencé, ok sinon
	run(t, db, "asset", "buy", "cw8", "1", "@550", "2026-06-01")
	if _, err := tryRun(t, db, "asset", "rm", "cw8"); err == nil {
		t.Fatal("rm d'un actif référencé accepté")
	}
	run(t, db, "asset", "rm", "vizr")
	if out = run(t, db, "asset", "list"); strings.Contains(out, "vizr") {
		t.Errorf("vizr devrait avoir disparu:\n%s", out)
	}
}

func TestAccountEdit(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "Savings")
	run(t, db, "account", "edit", "PEA Zephyr", "--add-alias", "p")
	out := run(t, db, "account", "list")
	if !strings.Contains(out, "ALIASES") || !strings.Contains(out, "p") {
		t.Errorf("aliases column:\n%s", out)
	}
	// l'alias résout pour la saisie
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	run(t, db, "cash", "deposit", "p", "1000", "2026-01-10")
	if out := run(t, db, "tx", "list", "--account", "p"); !strings.Contains(out, "deposit") {
		t.Errorf("alias should resolve in tx list:\n%s", out)
	}
	// collision refusée, --rm-alias marche, edit du taux marche
	if _, err := tryRun(t, db, "account", "edit", "Savings", "--add-alias", "PEA Zephyr"); err == nil {
		t.Fatal("alias collision accepted")
	}
	run(t, db, "account", "edit", "p", "--rm-alias", "p", "--tax", "gains:30%")
	if out := run(t, db, "account", "list"); !strings.Contains(out, "gains:30%") {
		t.Errorf("tax edit:\n%s", out)
	}
}

func TestPerfColors(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// pas un terminal → pas de couleur par défaut
	out := runNet(t, db, "perf", "--to", "2026-06-05")
	if strings.Contains(out, "\x1b[") {
		t.Errorf("séquences ANSI sans terminal:\n%q", out)
	}
	// forçage pour les tests : les TWR positifs sont verts
	t.Setenv("FINADOR_FORCE_COLOR", "1")
	out = runNet(t, db, "perf", "--to", "2026-06-05")
	if !strings.Contains(out, "\x1b[32m") {
		t.Errorf("vert absent avec FINADOR_FORCE_COLOR:\n%q", out)
	}
	// --no-color gagne sur le forçage
	out = runNet(t, db, "perf", "--no-color", "--to", "2026-06-05")
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--no-color inopérant:\n%q", out)
	}
}

func TestServeRefusesOfflineBindWarning(t *testing.T) {
	db := newDB(t)
	// pas de listen réel : on vérifie seulement la validation des flags
	if _, err := tryRun(t, db, "serve", "--addr", "pas-une-adresse"); err == nil {
		t.Fatal("adresse invalide acceptée")
	}
}

func TestAccountRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "Savings")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")

	// rm on an account with a transaction is rejected
	run(t, db, "asset", "buy", "cw8", "1", "@550", "2026-06-01", "--account", "PEA Zephyr")
	if _, err := tryRun(t, db, "account", "rm", "PEA Zephyr"); err == nil {
		t.Fatal("rm of referenced account should be rejected")
	}
	// the account must still be there
	out := run(t, db, "account", "list")
	if !strings.Contains(out, "PEA Zephyr") {
		t.Errorf("referenced account disappeared:\n%s", out)
	}

	// rm on an unreferenced account succeeds
	run(t, db, "account", "rm", "Savings")
	out = run(t, db, "account", "list")
	if strings.Contains(out, "Savings") {
		t.Errorf("Savings should have been removed:\n%s", out)
	}

	// rm on unknown account returns an error
	if _, err := tryRun(t, db, "account", "rm", "DoesNotExist"); err == nil {
		t.Fatal("rm of unknown account should fail")
	}
}

func TestAssetDividendAndFee(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")

	out := run(t, db, "asset", "dividend", "cw8", "42.50", "--account", "PEA Zephyr")
	for _, want := range []string{"dividend", "42.5 EUR", "PEA Zephyr"} {
		if !strings.Contains(out, want) {
			t.Errorf("dividend: %q manquant dans %q", want, out)
		}
	}

	out = run(t, db, "asset", "fee", "cw8", "9.90", "--note", "courtage")
	for _, want := range []string{"fee", "9.9 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("fee: %q manquant dans %q", want, out)
		}
	}

	// both should appear in tx list
	out = run(t, db, "tx", "list")
	if !strings.Contains(out, "dividend") || !strings.Contains(out, "fee") {
		t.Errorf("dividend/fee missing from tx list:\n%s", out)
	}

	// amounts are always positive, date arg works
	out = run(t, db, "asset", "fee", "cw8", "5.00", "2026-01-15")
	if !strings.Contains(out, "5 EUR") || !strings.Contains(out, "2026-01-15") {
		t.Errorf("fee with date: %q", out)
	}
}

func TestAccountAddAlias(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--alias", "pea", "--alias", "zephyr")

	// aliases listed
	out := run(t, db, "account", "list")
	if !strings.Contains(out, "pea") || !strings.Contains(out, "zephyr") {
		t.Errorf("aliases missing from list:\n%s", out)
	}

	// aliases resolve in commands
	run(t, db, "cash", "deposit", "pea", "5000", "2026-01-10")
	out = run(t, db, "tx", "list", "--account", "zephyr")
	if !strings.Contains(out, "deposit") {
		t.Errorf("alias 'zephyr' did not resolve:\n%s", out)
	}

	// duplicate alias rejected
	run(t, db, "account", "add", "Savings")
	if _, err := tryRun(t, db, "account", "add", "NewBank", "--alias", "pea"); err == nil {
		t.Fatal("duplicate alias should be rejected")
	}
}

// TestMergeCommandUnion: two copies of the same ledger, each with a distinct new
// transaction, union with no loss and no conflict via the CLI.
func TestMergeCommandUnion(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--alias", "pea")
	run(t, db, "cash", "deposit", "pea", "1000", "2026-01-10")

	// A byte copy shares the same header id: it is the same ledger.
	other := filepath.Join(t.TempDir(), "other.fin")
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Diverge: each copy gets a distinct deposit.
	run(t, db, "cash", "deposit", "pea", "2000", "2026-02-10")
	run(t, other, "cash", "deposit", "pea", "3000", "2026-03-10")

	out := run(t, db, "merge", other)
	if !strings.Contains(out, "conflicts resolved") {
		t.Fatalf("merge summary missing: %q", out)
	}
	// All three deposits survive after merge.
	list := run(t, db, "tx", "list")
	for _, want := range []string{"1000 EUR", "2000 EUR", "3000 EUR"} {
		if !strings.Contains(list, want) {
			t.Errorf("tx %q lost after merge:\n%s", want, list)
		}
	}
}

func TestLabelAddListRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--alias", "pea")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")

	out := run(t, db, "label", "add", "retraite", "--asset", "cw8", "--account", "pea")
	if !strings.Contains(out, "retraite") || !strings.Contains(out, "PEA Zephyr") {
		t.Fatalf("label add: %q", out)
	}
	run(t, db, "label", "add", "core", "--asset", "cw8", "--account", "pea")

	// list shows both, with account and asset names
	out = run(t, db, "label", "list")
	for _, want := range []string{"retraite", "core", "PEA Zephyr", "CW8.PA", "ACCOUNT", "ASSET", "LABEL"} {
		if !strings.Contains(out, want) {
			t.Errorf("label list: %q missing in:\n%s", want, out)
		}
	}

	// --name filter (case-insensitive substring)
	if out = run(t, db, "label", "list", "--name", "RETRAI"); !strings.Contains(out, "retraite") || strings.Contains(out, "core") {
		t.Errorf("label list --name filter:\n%s", out)
	}

	// duplicate (same pair + name, case-insensitive) is rejected
	if _, err := tryRun(t, db, "label", "add", "RETRAITE", "--asset", "cw8", "--account", "pea"); err == nil {
		t.Fatal("duplicate label should be rejected")
	}

	// unknown account errors
	if _, err := tryRun(t, db, "label", "add", "x", "--asset", "cw8", "--account", "nope"); err == nil {
		t.Fatal("label add with unknown account should fail")
	}

	// rm by id prefix removes it
	id := strings.Fields(firstLineContaining(t, run(t, db, "label", "list", "--name", "retraite"), "retraite"))[0]
	run(t, db, "label", "rm", id)
	if out = run(t, db, "label", "list"); strings.Contains(out, "retraite") {
		t.Errorf("label rm did not remove retraite:\n%s", out)
	}
	if _, err := tryRun(t, db, "label", "rm", "zzzzzzzz"); err == nil {
		t.Fatal("rm of unknown label should fail")
	}
}

// firstLineContaining returns the first line of out that contains sub.
func firstLineContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", sub, out)
	return ""
}

func labelDB(t *testing.T) string {
	t.Helper()
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "label", "add", "retraite", "--asset", "cw8", "--account", "PEA Zephyr")
	return db
}

func TestPerfByLabel(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "perf", "--label", "retraite", "--to", "2026-06-05")
	if !strings.Contains(out, "retraite") {
		t.Errorf("label name missing in output:\n%s", out)
	}
	if !strings.Contains(out, "inception") {
		t.Errorf("perf --label missing inception row:\n%s", out)
	}
}

func TestValueByLabel(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "value", "--label", "retraite", "--at", "2026-06-05")
	// 10 × 560 = 5600, no cash (ByLabel has no cash)
	if !strings.Contains(out, "5600.00 EUR") {
		t.Errorf("value --label: expected 5600.00 EUR:\n%s", out)
	}
	if !strings.Contains(out, "retraite") {
		t.Errorf("label name missing in output:\n%s", out)
	}
}

func TestLabelAndScopeArgMutuallyExclusive(t *testing.T) {
	db := labelDB(t)
	if _, err := tryRun(t, db, "perf", "actions/monde", "--label", "retraite"); err == nil {
		t.Fatal("perf with both scope arg and --label should fail")
	}
	if _, err := tryRun(t, db, "value", "actions/monde", "--label", "retraite"); err == nil {
		t.Fatal("value with both scope arg and --label should fail")
	}
}

func TestLabelUnknownErrors(t *testing.T) {
	db := newDB(t)
	if _, err := tryRun(t, db, "perf", "--label", "nonexistent"); err == nil {
		t.Fatal("perf --label with unknown label should fail")
	}
	if _, err := tryRun(t, db, "value", "--label", "nonexistent"); err == nil {
		t.Fatal("value --label with unknown label should fail")
	}
}

func TestLabelWithExclude(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// --label retraite --exclude cw8 → cw8 is excluded, so the labeled position vanishes
	out := runNet(t, db, "value", "--label", "retraite", "--exclude", "cw8", "--at", "2026-06-05")
	if strings.Contains(out, "5600") {
		t.Errorf("cw8 should be excluded:\n%s", out)
	}
}

// TestMergeCommandDifferentLedgers: merging two unrelated ledgers is refused.
func TestMergeCommandDifferentLedgers(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA")

	other := filepath.Join(t.TempDir(), "unrelated.fin")
	run(t, other, "init") // independent header id

	if _, err := tryRun(t, db, "merge", other); err == nil {
		t.Fatal("merging unrelated ledgers should fail")
	}
}

// TestBuyAutoCreatesAsset: asset buy on an unknown ticker creates it on the fly
// (offline → no Yahoo enrichment; ticker = ref).
func TestBuyAutoCreatesAsset(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")

	// NEWT does not exist yet — buy should create it and record the transaction.
	out := run(t, db, "asset", "buy", "NEWT", "50", "5000",
		"--account", "PEA Zephyr", "--group", "equities/test")
	if !strings.Contains(out, "created") {
		t.Errorf("asset buy did not report creation: %q", out)
	}
	if !strings.Contains(out, "buy") {
		t.Errorf("buy transaction line missing: %q", out)
	}

	list := run(t, db, "asset", "list")
	if !strings.Contains(list, "NEWT") {
		t.Errorf("NEWT missing from asset list after auto-creation:\n%s", list)
	}
	if !strings.Contains(list, "equities/test") {
		t.Errorf("group missing from asset list:\n%s", list)
	}
}

// TestBuyWithAlias: --alias on an on-the-fly buy makes the asset resolvable by it.
func TestBuyWithAlias(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")

	run(t, db, "asset", "buy", "CW8.PA", "10", "5500",
		"--account", "PEA Zephyr", "--alias", "cw8", "--group", "equities/world")

	// The alias resolves: a second buy by "cw8" must hit the same asset, not create another.
	out := run(t, db, "asset", "buy", "cw8", "5", "2800", "--account", "PEA Zephyr")
	if strings.Contains(out, "created") {
		t.Errorf("alias cw8 did not resolve — a duplicate asset was created:\n%s", out)
	}
	list := run(t, db, "asset", "list")
	if strings.Count(list, "security") != 1 {
		t.Errorf("expected exactly one asset after the alias-resolved buy:\n%s", list)
	}
	if !strings.Contains(list, "cw8") {
		t.Errorf("alias cw8 missing from asset list:\n%s", list)
	}
}

// TestSellDoesNotAutoCreate: asset sell on an unknown ticker must fail, not create.
func TestSellDoesNotAutoCreate(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")

	if _, err := tryRun(t, db, "asset", "sell", "GHOST", "10", "1000"); err == nil {
		t.Fatal("sell on unknown asset should fail, not auto-create")
	}
}

// TestBuyWithInlineLabels: --label flags tag the (account, asset) pair.
func TestBuyWithInlineLabels(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")

	run(t, db, "asset", "buy", "LBL", "10", "1000",
		"--account", "PEA Zephyr",
		"--label", "core", "--label", "retraite")

	out := run(t, db, "label", "list")
	if !strings.Contains(out, "core") {
		t.Errorf("label 'core' missing: %q", out)
	}
	if !strings.Contains(out, "retraite") {
		t.Errorf("label 'retraite' missing: %q", out)
	}

	// Repeated --label core must not error (ErrDuplicate is silently ignored).
	run(t, db, "asset", "buy", "LBL", "5", "600",
		"--account", "PEA Zephyr",
		"--label", "core")
}

// TestDividendAutoCreatesAsset: asset dividend on an unknown ticker creates the security.
func TestDividendAutoCreatesAsset(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")

	out := run(t, db, "asset", "dividend", "DIVT", "42.50", "--account", "PEA Zephyr")
	if !strings.Contains(out, "created") {
		t.Errorf("dividend did not auto-create asset: %q", out)
	}
	if !strings.Contains(out, "dividend") {
		t.Errorf("dividend transaction missing: %q", out)
	}
	list := run(t, db, "asset", "list")
	if !strings.Contains(list, "DIVT") {
		t.Errorf("DIVT missing from asset list:\n%s", list)
	}
}

// TestAssetSetWithLabel: --label on asset set tags the pair (no auto-create).
func TestAssetSetWithLabel(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "Patrimoine")
	run(t, db, "asset", "add", "Maison Lyon", "--kind", "property")

	run(t, db, "asset", "set", "Maison Lyon", "300000",
		"--account", "Patrimoine", "--label", "immo")

	out := run(t, db, "label", "list")
	if !strings.Contains(out, "immo") {
		t.Errorf("label 'immo' missing after asset set --label: %q", out)
	}
}

// TestExportCSV: `finador export` emits one CSV row per held asset.
func TestExportCSV(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8",
		"--name", "Amundi MSCI World", "--isin", "LU1681043599", "--group", "actions/monde")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")
	// prime the price cache (online via the fake source), then export offline.
	runNet(t, db, "value", "--at", "2026-06-05")

	out := run(t, db, "export", "--at", "2026-06-05")
	// header, then the held position: 10 × 560 = 5600 gross ; base 5500 →
	// gain 100 → tax 17.20 → net 5582.80.
	if !strings.HasPrefix(out, "ticker,name,isin,gross,net,currency\n") {
		t.Errorf("missing CSV header:\n%s", out)
	}
	if !strings.Contains(out, "CW8.PA,Amundi MSCI World,LU1681043599,5600.00,5582.80,EUR") {
		t.Errorf("asset row missing/incorrect:\n%s", out)
	}
}
