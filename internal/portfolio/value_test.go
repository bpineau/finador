package portfolio

import (
	"errors"
	"strings"
	"testing"

	"finador/internal/domain"
)

// identityFX converts 1:1 between identical currencies and fails otherwise,
// unless a fixed rate is provided for a pair.
type fxStub struct{ rates map[string]float64 } // "EUR→USD" → 1.10

func (f fxStub) Convert(amount float64, from, to domain.Currency, _ domain.Date) (float64, error) {
	if from == to {
		return amount, nil
	}
	if r, ok := f.rates[string(from)+"→"+string(to)]; ok {
		return amount * r, nil
	}
	return 0, errors.New("taux absent: " + string(from) + "→" + string(to))
}

func approx(t *testing.T, what string, got, want float64) {
	t.Helper()
	if d := got - want; d > 0.005 || d < -0.005 {
		t.Errorf("%s = %.4f, attendu %.4f", what, got, want)
	}
}

// rich fixture: PEA (gains:17.2%, cash tracked), CTO (gains:30%, cash not
// tracked), Immo (gains:30%) with a property having two statements.
func valuationBook(t *testing.T) *domain.Book {
	t.Helper()
	b := sampleBook(t) // from replay_test.go: pea/cto/livret + cw8 + trades
	pea, _ := b.Account("pea")
	pea.Tax, _ = domain.ParseTaxRule("gains:17.2%")
	cto, _ := b.Account("cto")
	cto.Tax, _ = domain.ParseTaxRule("gains:30%")
	if err := b.AddAccount(&domain.Account{ID: "immo", Name: "Immo", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	immo, _ := b.Account("immo")
	immo.Tax, _ = domain.ParseTaxRule("gains:30%")
	if err := b.AddAsset(&domain.Asset{ID: "maison", Kind: domain.Property,
		Name: "Maison à Rénover", Currency: domain.EUR, Group: "immo"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "immo", Asset: "maison",
		Kind: domain.Statement, Amount: eur("400000")})
	b.Add(domain.Transaction{Date: mustDate("2026-06-01"), Account: "immo", Asset: "maison",
		Kind: domain.Statement, Amount: eur("450000")})
	// cw8 price series: close 560 on June 5
	b.Market.Price("cw8").Merge([]domain.PricePoint{
		{Date: mustDate("2026-03-20"), Close: 540},
		{Date: mustDate("2026-06-05"), Close: 560},
	})
	return b
}

func scopeOf(t *testing.T, b *domain.Book, ref string) Scope {
	t.Helper()
	s, err := ParseScope(b, ref)
	if err != nil {
		t.Fatalf("ParseScope(%q): %v", ref, err)
	}
	return s
}

func TestValueAll(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA: 12 × 560 = 6720; tracked cash = 10000 − 5000 − 2750 + 1800 = 4050
	// CTO: 2 × 560 = 1120; cash not tracked
	// Livret: statement 12000; Maison: statement 450000
	approx(t, "gross", v.Gross, 6720+4050+1120+12000+450000)
	// exact tax per envelope:
	// PEA gains:17.2% basis 10000 (contributions), value 10770 → 770 × 0.172 = 132.44
	// CTO gains:30% basis 1100 (buys−sells), value 1120 → 20 × 0.30 = 6
	// Livret none → 0; Immo gains:30% basis 400000, value 450000 → 15000
	approx(t, "tax", v.Tax, 132.44+6+15000)
	approx(t, "net", v.Net, v.Gross-v.Tax)
	if v.TaxNote == "" {
		t.Error("TaxNote attendue (ventilation approximative ≠ enveloppe)")
	}
	// lines by top-level group + cash
	labels := map[string]bool{}
	for _, l := range v.Lines {
		labels[l.Label] = true
	}
	for _, want := range []string{"actions", "immo", "cash"} {
		if !labels[want] {
			t.Errorf("ligne %q absente (%v)", want, v.Lines)
		}
	}
}

func TestValueGroupScope(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, "ACTIONS"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross", v.Gross, 6720+1120)
	// tax position by position:
	// PEA: average cost = 7750 − 7750×3/15 = 6200; gain 520 × 0.172 = 89.44
	// CTO: average cost 1100; gain 20 × 0.30 = 6
	approx(t, "tax", v.Tax, 89.44+6)
}

func TestValueAccountAndAssetScopes(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	v, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross pea", v.Gross, 6720+4050)
	approx(t, "tax pea", v.Tax, 132.44) // exact envelope

	v, err = Value(b, scopeOf(t, b, "cw8"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross cw8", v.Gross, 6720+1120)
	if len(v.Lines) != 2 { // one line per envelope
		t.Errorf("lines = %+v", v.Lines)
	}
}

func TestValueAtEarlierDate(t *testing.T) {
	b := valuationBook(t)
	// on March 21: forward-filled close 540 from March 20, house at first statement
	v, err := Value(b, scopeOf(t, b, ""), mustDate("2026-03-21"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA 12×540=6480, cash 4050; CTO 2×540=1080; livret 12000; house 400000
	approx(t, "gross", v.Gross, 6480+4050+1080+12000+400000)
}

func TestValueOtherCurrency(t *testing.T) {
	b := valuationBook(t)
	fx := fxStub{rates: map[string]float64{"EUR→USD": 1.10}}
	v, err := Value(b, scopeOf(t, b, "PEA"), mustDate("2026-06-05"), domain.USD, fx)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross usd", v.Gross, (6720+4050)*1.10)
}

func TestValueStaleMarkers(t *testing.T) {
	b := valuationBook(t)
	// on March 30, last close from March 20 → > 5 days → stale
	v, err := Value(b, scopeOf(t, b, "cw8"), mustDate("2026-03-30"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Stale) == 0 || !strings.Contains(v.Stale[0], "2026-03-20") {
		t.Errorf("stale = %v", v.Stale)
	}
}

func TestValueAutoDividends(t *testing.T) {
	b := valuationBook(t)
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	at := mustDate("2026-06-05")
	// PEA holds 15 shares on March 1 (buys 10+5, sell after) → +30 EUR of cash
	v, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross avec dividendes", v.Gross, 6720+4050+30)

	// a manual Dividend on cw8 disables the automatic one
	b.Add(domain.Transaction{Date: mustDate("2026-03-02"), Account: "pea", Asset: "cw8",
		Kind: domain.Dividend, Amount: eur("25")})
	v, err = Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross avec dividende manuel", v.Gross, 6720+4050+25)
}

func TestPropertyWithBuyNotDoubleCounted(t *testing.T) {
	b := valuationBook(t)
	// a buy recorded on the property (notary fees, etc.) must not make it
	// count twice: it stays valued by its statements
	b.Add(domain.Transaction{Date: mustDate("2026-01-01"), Account: "immo", Asset: "maison",
		Kind: domain.Buy, Quantity: dec("1"), Amount: eur("400000")})
	v, err := Value(b, scopeOf(t, b, "Immo"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "gross immo", v.Gross, 450000)
}

// Fix 3: negative envelope basis (withdrawals > contributions) clamped to 0.
// Derivation:
//
//	PEA contributions: 10000; PEA withdrawals: 15000 → basis = max(0, 10000−15000) = 0
//	Cash = 10000 − 5000 − 2750 + 1800 − 15000 = −10950
//	cw8 PEA = 12 × 560 = 6720
//	Gross PEA = 6720 + (−10950) = −4230
//	Gain = −4230 − 0 = −4230 (negative) → tax = max(0, −4230) × 0.172 = 0
func TestNegativeEnvelopeBasisClamped(t *testing.T) {
	b := valuationBook(t)
	b.Add(domain.Transaction{Date: mustDate("2026-04-01"), Account: "pea",
		Kind: domain.Withdraw, Amount: eur("15000")})
	v, err := Value(b, scopeOf(t, b, "PEA"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// basis = max(0, 10000−15000) = 0 → negative gain → tax = 0
	approx(t, "gross", v.Gross, -4230)
	approx(t, "tax", v.Tax, 0)
}

func TestValueLinesByAccount(t *testing.T) {
	b := valuationBook(t)
	v, err := Value(b, scopeOf(t, b, ""), mustDate("2026-06-05"), domain.EUR, fxStub{},
		WithLinesByAccount())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, l := range v.Lines {
		got[l.Label] = l.Gross
	}
	// PEA = positions (6720) + cash (4050) on a single envelope line
	approx(t, "ligne PEA", got["PEA"], 6720+4050)
	approx(t, "ligne CTO", got["CTO"], 1120)
	approx(t, "ligne Livret", got["Livret"], 12000)
	approx(t, "ligne Immo", got["Immo"], 450000)
	// the total does not change with the breakdown
	approx(t, "gross", v.Gross, 473890)
}

func TestValueWhatIf(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	// what-if: cw8 at 600 (instead of the 560 quote) and house at 500000
	v, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{},
		WithPriceOverrides(map[domain.AssetID]float64{"cw8": 600, "maison": 500000}))
	if err != nil {
		t.Fatal(err)
	}
	// 14 cw8 shares (12 pea + 2 cto) × 600; house 500000; the rest unchanged
	approx(t, "gross hypothétique", v.Gross, 14*600+4050+12000+500000)
	// the what-if marker is present
	found := false
	for _, s := range v.Stale {
		if strings.Contains(s, "what-if") {
			found = true
		}
	}
	if !found {
		t.Errorf("what-if marker absent: %v", v.Stale)
	}
}

func TestAutoDividendWithholding(t *testing.T) {
	b := valuationBook(t)
	cw8, _ := b.Asset("cw8")
	cw8.Withholding = 0.15
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	v, err := Value(b, scopeOf(t, b, "PEA"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// 15 shares × 2 × (1−0.15) = 25.50 instead of 30
	approx(t, "gross avec retenue", v.Gross, 6720+4050+25.5)
}

func TestParseScopeOrderAndErrors(t *testing.T) {
	b := valuationBook(t)
	for ref, kind := range map[string]ScopeKind{
		"": All, "actions": ByGroup, "actions/monde": ByGroup,
		"PEA": ByAccount, "cw8": ByAsset, "CW8.PA": ByAsset,
	} {
		s, err := ParseScope(b, ref)
		if err != nil || s.Kind != kind {
			t.Errorf("ParseScope(%q) = %v kind=%v err=%v", ref, s, s.Kind, err)
		}
	}
	if _, err := ParseScope(b, "inconnu"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ParseScope(inconnu): %v", err)
	}
}

// An asset held in several accounts is valued once per position, so an
// asset-level note (here a what-if override) must not be repeated.
func TestValueWhatIfNoteDeduped(t *testing.T) {
	b := valuationBook(t) // cw8 held in both pea (12) and cto (2)
	v, err := Value(b, scopeOf(t, b, ""), mustDate("2026-06-05"), domain.EUR, fxStub{},
		WithPriceOverrides(map[domain.AssetID]float64{"cw8": 600}))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range v.Stale {
		if strings.Contains(s, "what-if") && strings.Contains(s, "CW8") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("what-if note appeared %d times, want 1:\n%v", n, v.Stale)
	}
}
