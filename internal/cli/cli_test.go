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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "CTO IBKR", "--tax", "gains:30%", "--ccy", "USD")
	out := run(t, db, "account", "list")
	for _, want := range []string{"pea-bforbank", "PEA BforBank", "gains:17.2%", "cto-ibkr", "USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("list: %q manquant dans:\n%s", want, out)
		}
	}
	if _, err := tryRun(t, db, "account", "add", "PEA BforBank"); err == nil {
		t.Fatal("doublon accepté")
	}
}

func TestTxListEditRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA BforBank")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	run(t, db, "cash", "set", "pea-bforbank", "12500", "--at", "2026-06-02")

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

// fakeSource sert des données de marché déterministes aux tests CLI.
type fakeSource struct{}

func (fakeSource) Resolve(_ context.Context, q string) (market.SymbolInfo, error) {
	if strings.EqualFold(q, "CW8.PA") {
		return market.SymbolInfo{Symbol: "CW8.PA", Name: "Amundi MSCI World UCITS ETF"}, nil
	}
	return market.SymbolInfo{}, domain.ErrNotFound
}

func (fakeSource) Daily(_ context.Context, sym string, _ domain.Date) (market.DailyData, error) {
	day := func(s string) domain.Date {
		d, err := domain.ParseDate(s)
		if err != nil {
			panic(err)
		}
		return d
	}
	switch sym {
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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

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
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	out := runNet(t, db, "refresh")
	if !strings.Contains(out, "rafraîchie") {
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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "perf", "--to", "2026-06-05")
	// série : 5000 plat jusqu'au 1er juin (l'achat est neutre), puis
	// 06-05 : 10×560 − 500 = 5100 → TWR origine = +2.00 %
	for _, want := range []string{"PÉRIODE", "TWR", "XIRR", "inception", "+2.00%", "CAGR", "Sharpe", "Sortino", "max drawdown"} {
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
	run(t, db, "account", "add", "PEA BforBank")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")

	out := run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	for _, want := range []string{"buy", "5500 EUR", "PEA BforBank"} {
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

	out = run(t, db, "cash", "set", "pea-bforbank", "12500")
	if !strings.Contains(out, "12500 EUR") {
		t.Errorf("cash set: %q", out)
	}
	out = run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	if !strings.Contains(out, "deposit") || !strings.Contains(out, "5000 EUR") {
		t.Errorf("deposit: %q", out)
	}
	out = run(t, db, "withdraw", "PEA BforBank", "1000")
	if !strings.Contains(out, "withdraw") {
		t.Errorf("withdraw: %q", out)
	}
}

func TestPerfAndValueExclude(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

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
	run(t, db, "account", "add", "PEA BforBank", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

	// hypothèse : cw8 à 600 → 10×600 − 500 = 5500 brut, et un delta vs réel (5100)
	out := runNet(t, db, "value", "--what-if", "cw8=600", "--at", "2026-06-05")
	for _, want := range []string{"5500.00 EUR", "what-if", "+400.00 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("what-if: %q manquant dans:\n%s", want, out)
		}
	}
	// ventilation par enveloppe
	out = runNet(t, db, "value", "--by", "enveloppe", "--at", "2026-06-05")
	if !strings.Contains(out, "PEA BforBank") {
		t.Errorf("--by enveloppe:\n%s", out)
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
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions")
	run(t, db, "asset", "add", "DDOG", "--id", "ddog", "--group", "actions")

	run(t, db, "asset", "edit", "ddog", "--add-alias", "Datadog", "--withholding", "15%")
	out := run(t, db, "asset", "list")
	if !strings.Contains(out, "Datadog") || !strings.Contains(out, "15") {
		t.Errorf("asset list après edit:\n%s", out)
	}
	// l'alias résout
	run(t, db, "asset", "edit", "datadog", "--rm-alias", "Datadog")
	if out = run(t, db, "asset", "list"); strings.Contains(out, "Datadog,") {
		t.Errorf("alias non retiré:\n%s", out)
	}
	// collision refusée
	if _, err := tryRun(t, db, "asset", "edit", "ddog", "--add-alias", "CW8.PA"); err == nil {
		t.Fatal("collision d'alias acceptée")
	}
	// rm : refus si référencé, ok sinon
	run(t, db, "add", "cw8", "1", "@550", "2026-06-01")
	if _, err := tryRun(t, db, "asset", "rm", "cw8"); err == nil {
		t.Fatal("rm d'un actif référencé accepté")
	}
	run(t, db, "asset", "rm", "ddog")
	if out = run(t, db, "asset", "list"); strings.Contains(out, "ddog") {
		t.Errorf("ddog devrait avoir disparu:\n%s", out)
	}
}

func TestPerfColors(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA BforBank")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "deposit", "PEA BforBank", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

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
