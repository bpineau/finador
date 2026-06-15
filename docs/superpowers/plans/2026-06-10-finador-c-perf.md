# Finador phase C - performance & courbes : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Les rendements (TWR par périodes, XIRR), les métriques (CAGR, volatilité, Sharpe, Sortino, max drawdown) et les courbes d'évolution (braille en terminal, SVG pour le web de la phase D), sur toute portée.

**Architecture:** `perf` = maths pures sur séries de points et flux (zéro dépendance hors domain pour les dates). `portfolio` gagne un constructeur de séries journalières (marche jour par jour avec état incrémental - PAS un appel à Value() par date, trop coûteux). `chart` = modèle Series + deux renderers golden-testés. CLI : `perf` et `chart`.

**Tech Stack:** Go 1.26 pur, stdlib uniquement (math, strings).

**Référence:** spec §6/§7 ; conventions des plans A/B (TDD strict, gofmt/vet silencieux, messages français, commits exacts, pas de binaire committé).

**Décisions actées phase C :**
- La série de valeur est **journalière calendaire** (nécessaire aux courbes et aux flux datés), avec report des dernières clôtures. Les **rendements pour les métriques ne gardent que les jours de semaine** (le forward-fill des week-ends fabriquerait ~28 % de rendements nuls et écraserait la volatilité) ; annualisation √252 - approximation standard, documentée.
- Convention de flux TWR : flux en **début de jour**, `r_t = (V_t − F_t)/V_{t−1}`, ignoré si `V_{t−1} ≤ 0`.
- Convention XIRR : flux du point de vue de l'investisseur (apport = négatif, valeur finale = positive), résolu par **bissection** sur [−0.9999, 100], erreur claire si pas de changement de signe.
- Flux externes relatifs à la portée (spec §6) : All → Deposit/Withdraw, plus Buy/Sell **des comptes au cash non suivi** ; Account → idem restreint au compte ; Group/Asset → Buy/Sell des actifs de la portée (l'argent entre/sort de la poche), dividendes sortants. Les dividendes automatiques d'un compte non suivi sont un flux sortant (revenu encaissé hors scope).
- Jours sans données (avant la première clôture / FX manquant) : la position contribue **0** à la série, sans erreur - la courbe démarre avec l'historique disponible. (Différent de Value() qui, lui, échoue sur FX manquant : une courbe doit rester traçable.)
- Courbe `--net` : impôt latent recalculé chaque jour avec l'état incrémental (règle d'enveloppe pour All/Account, position par position pour Group/Asset - mêmes règles que Value()).
- CAGR = TWR annualisé en jours calendaires : `(1+TWR)^(365.25/jours) − 1`. Sharpe = `(moyenne(r)×252 − rf) / vol` (annualisation arithmétique, documentée) ; Sortino pareil avec écart-type des seuls rendements sous `rf/252`.

---

### Task C1: perf - maths pures (TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD)

**Files:**
- Create: `internal/perf/perf.go`, `internal/perf/periods.go`
- Test: `internal/perf/perf_test.go`, `internal/perf/periods_test.go`

- [ ] **Step 1: tests qui échouent**

`internal/perf/perf_test.go`:

```go
package perf

import (
	"math"
	"strings"
	"testing"

	"finador/internal/domain"
)

func d(s string) domain.Date {
	dd, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func approx(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, attendu %.6f (±%.6f)", what, got, want, tol)
	}
}

func TestTWRNoFlows(t *testing.T) {
	pts := []Point{
		{d("2026-06-01"), 100}, {d("2026-06-02"), 110}, {d("2026-06-03"), 99},
	}
	// 100→110 : +10 % ; 110→99 : −10 % ; composé : 0.99 − 1 = −1 %
	approx(t, "TWR", TWR(pts, nil), -0.01, 1e-9)
}

func TestTWRNeutralizesFlows(t *testing.T) {
	// Jour 2 : apport de 100 juste avant ouverture, la valeur passe à 210 puis
	// le marché fait +10 % → 231. Le TWR ne doit voir que les +5 % du jour 1
	// (100→105) et les +10 % du jour 2 ((231−100... non : (V2−F2)/V1.
	pts := []Point{{d("2026-06-01"), 100}, {d("2026-06-02"), 105}, {d("2026-06-03"), 215.5}}
	flows := []Flow{{d("2026-06-03"), 100}}
	// r3 = (215.5 − 100)/105 = 1.10 → +10 %. TWR = 1.05×1.10 − 1 = 15.5 %
	approx(t, "TWR", TWR(pts, flows), 0.155, 1e-9)
}

func TestTWRSkipsZeroBase(t *testing.T) {
	pts := []Point{{d("2026-06-01"), 0}, {d("2026-06-02"), 100}, {d("2026-06-03"), 110}}
	flows := []Flow{{d("2026-06-02"), 100}}
	// jour 2 : base nulle → ignoré ; jour 3 : +10 %
	approx(t, "TWR", TWR(pts, flows), 0.10, 1e-9)
}

func TestDailyReturnsWeekdaysOnly(t *testing.T) {
	// vendredi 5 juin 2026, samedi 6, dimanche 7, lundi 8
	pts := []Point{
		{d("2026-06-04"), 100}, {d("2026-06-05"), 102},
		{d("2026-06-06"), 102}, {d("2026-06-07"), 102}, {d("2026-06-08"), 104},
	}
	rs := DailyReturns(pts, nil)
	// vendredi (+2 %) et lundi (104/102 − 1) gardés ; samedi/dimanche éliminés
	if len(rs) != 2 {
		t.Fatalf("returns = %v, attendu 2 valeurs", rs)
	}
	approx(t, "r[0]", rs[0], 0.02, 1e-9)
	approx(t, "r[1]", rs[1], 104.0/102.0-1, 1e-9)
}

func TestXIRRKnownValue(t *testing.T) {
	// Référence vérifiable : −1000 le 1er janv, +1100 le 31 déc 2026
	// (364 jours). 1100/1000 = 1.10 sur 364/365.25 ans → r ≈ 10.03 %
	r, err := XIRR([]Flow{{d("2026-01-01"), -1000}, {d("2026-12-31"), 1100}})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "XIRR", r, math.Pow(1.10, 365.25/364)-1, 1e-6)
}

func TestXIRRWithIntermediateFlow(t *testing.T) {
	// −1000 au départ, −500 à mi-année, +1600 au bout d'un an.
	// Vérité indépendante : NPV(r)=0 ; vérifier que NPV(XIRR)≈0.
	flows := []Flow{{d("2026-01-01"), -1000}, {d("2026-07-01"), -500}, {d("2027-01-01"), 1600}}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	npv := 0.0
	for _, f := range flows {
		days := f.Date.Time().Sub(d("2026-01-01").Time()).Hours() / 24
		npv += f.Amount * math.Pow(1+r, -days/365.25)
	}
	approx(t, "NPV(XIRR)", npv, 0, 1e-6)
	if r < 0.05 || r > 0.10 {
		t.Errorf("XIRR = %v, hors de la plage plausible [5%%, 10%%]", r)
	}
}

func TestXIRRNoSolution(t *testing.T) {
	if _, err := XIRR([]Flow{{d("2026-01-01"), -100}, {d("2026-06-01"), -50}}); err == nil ||
		!strings.Contains(err.Error(), "XIRR") {
		t.Fatalf("err = %v", err)
	}
}

func TestCAGR(t *testing.T) {
	// +21 % en 2 ans → 10 % annuel
	approx(t, "CAGR", CAGR(0.21, 731), math.Pow(1.21, 365.25/731)-1, 1e-9)
	approx(t, "CAGR 1an", CAGR(0.10, 365), math.Pow(1.10, 365.25/365)-1, 1e-9)
}

func TestVolSharpeSortino(t *testing.T) {
	rs := []float64{0.01, -0.005, 0.002, 0.007, -0.003}
	mean := (0.01 - 0.005 + 0.002 + 0.007 - 0.003) / 5
	var ss float64
	for _, r := range rs {
		ss += (r - mean) * (r - mean)
	}
	wantVol := math.Sqrt(ss/4) * math.Sqrt(252) // écart-type échantillon, annualisé
	approx(t, "Vol", Vol(rs), wantVol, 1e-9)

	wantSharpe := (mean*252 - 0.02) / wantVol
	approx(t, "Sharpe", Sharpe(rs, 0.02), wantSharpe, 1e-9)

	// Sortino : seuls les rendements sous rf/252 comptent dans le dénominateur
	rfDaily := 0.02 / 252
	var dss float64
	n := 0
	for _, r := range rs {
		if r < rfDaily {
			dss += (r - rfDaily) * (r - rfDaily)
			n++
		}
	}
	_ = n
	wantDown := math.Sqrt(dss/float64(len(rs))) * math.Sqrt(252)
	approx(t, "Sortino", Sortino(rs, 0.02), (mean*252-0.02)/wantDown, 1e-9)
}

func TestVolEmptyAndSingle(t *testing.T) {
	if v := Vol(nil); v != 0 {
		t.Errorf("Vol(nil) = %v", v)
	}
	if v := Vol([]float64{0.01}); v != 0 {
		t.Errorf("Vol(1 point) = %v", v)
	}
	if s := Sharpe(nil, 0.02); s != 0 {
		t.Errorf("Sharpe(nil) = %v", s)
	}
}

func TestMaxDrawdown(t *testing.T) {
	pts := []Point{
		{d("2026-01-01"), 100}, {d("2026-02-01"), 120}, {d("2026-03-01"), 90},
		{d("2026-04-01"), 100}, {d("2026-05-01"), 125},
	}
	dd := MaxDrawdown(pts)
	approx(t, "depth", dd.Depth, -0.25, 1e-9) // 120 → 90
	if dd.Peak != d("2026-02-01") || dd.Trough != d("2026-03-01") {
		t.Errorf("pic/creux = %s/%s", dd.Peak, dd.Trough)
	}
	if dd.Recovered == nil || *dd.Recovered != d("2026-05-01") {
		t.Errorf("récupération = %v", dd.Recovered)
	}
}

func TestMaxDrawdownNotRecovered(t *testing.T) {
	pts := []Point{{d("2026-01-01"), 100}, {d("2026-02-01"), 80}}
	dd := MaxDrawdown(pts)
	approx(t, "depth", dd.Depth, -0.20, 1e-9)
	if dd.Recovered != nil {
		t.Errorf("récupération = %v, attendu nil", dd.Recovered)
	}
}
```

`internal/perf/periods_test.go`:

```go
package perf

import "testing"

func TestPeriodRange(t *testing.T) {
	today := d("2026-06-10") // un mercredi
	for _, tc := range []struct {
		name string
		from string
	}{
		{"1j", "2026-06-09"},
		{"2j", "2026-06-08"},
		{"5j", "2026-06-05"},
		{"7j", "2026-06-03"},
		{"1m", "2026-05-10"},
		{"3m", "2026-03-10"},
		{"ytd", "2025-12-31"},
		{"1a", "2025-06-10"},
	} {
		from, to, err := PeriodRange(tc.name, today)
		if err != nil || from != d(tc.from) || to != today {
			t.Errorf("PeriodRange(%s) = %s..%s, %v ; attendu %s..%s", tc.name, from, to, err, tc.from, today)
		}
	}
	// année civile précédente : bornes fixes
	from, to, err := PeriodRange("an-1", today)
	if err != nil || from != d("2024-12-31") || to != d("2025-12-31") {
		t.Errorf("an-1 = %s..%s, %v", from, to, err)
	}
	if _, _, err := PeriodRange("42x", today); err == nil {
		t.Error("période inconnue acceptée")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/perf/` → FAIL - undefined: Point, TWR, etc.

- [ ] **Step 3: implémenter**

`internal/perf/perf.go`:

```go
// Package perf computes performance figures on value series: pure math,
// no I/O, no market access. Inputs come from portfolio.Series.
package perf

import (
	"errors"
	"math"
	"time"

	"finador/internal/domain"
)

// Point is one day of a value series.
type Point struct {
	Date  domain.Date
	Value float64
}

// Flow is an external in/out of the measured scope (positive = money in),
// assumed to happen at the start of its day.
type Flow struct {
	Date   domain.Date
	Amount float64
}

// TWR chain-links daily returns r_t = (V_t − F_t)/V_{t−1}, neutralizing
// external flows: it measures the strategy, not the saver. Days with a
// non-positive base are skipped.
func TWR(points []Point, flows []Flow) float64 {
	byDay := flowsByDay(flows)
	total := 1.0
	for i := 1; i < len(points); i++ {
		prev := points[i-1].Value
		if prev <= 0 {
			continue
		}
		total *= (points[i].Value - byDay[points[i].Date]) / prev
	}
	return total - 1
}

// DailyReturns yields the flow-adjusted weekday returns of a calendar-daily
// series. Week-ends are forward-filled flats: keeping them would dilute the
// volatility, so they are dropped (≈252 returns a year, annualized with √252).
func DailyReturns(points []Point, flows []Flow) []float64 {
	byDay := flowsByDay(flows)
	var out []float64
	for i := 1; i < len(points); i++ {
		prev := points[i-1].Value
		if prev <= 0 {
			continue
		}
		if wd := points[i].Date.Time().Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		out = append(out, (points[i].Value-byDay[points[i].Date])/prev-1)
	}
	return out
}

func flowsByDay(flows []Flow) map[domain.Date]float64 {
	byDay := map[domain.Date]float64{}
	for _, f := range flows {
		byDay[f.Date] += f.Amount
	}
	return byDay
}

// XIRR solves the money-weighted annual rate by bisection. Cashflows follow
// the investor's convention: invested money negative, final value positive.
func XIRR(cashflows []Flow) (float64, error) {
	if len(cashflows) < 2 {
		return 0, errors.New("XIRR: au moins deux flux requis")
	}
	t0 := cashflows[0].Date.Time()
	npv := func(r float64) float64 {
		sum := 0.0
		for _, f := range cashflows {
			years := f.Date.Time().Sub(t0).Hours() / 24 / 365.25
			sum += f.Amount * math.Pow(1+r, -years)
		}
		return sum
	}
	lo, hi := -0.9999, 100.0
	flo, fhi := npv(lo), npv(hi)
	if math.IsNaN(flo) || math.IsNaN(fhi) || flo*fhi > 0 {
		return 0, errors.New("XIRR non défini pour ces flux (pas de changement de signe)")
	}
	for range 200 {
		mid := (lo + hi) / 2
		if fm := npv(mid); fm == 0 || hi-lo < 1e-10 {
			return mid, nil
		} else if fm*flo < 0 {
			hi = mid
		} else {
			lo, flo = mid, fm
		}
	}
	return (lo + hi) / 2, nil
}

// CAGR annualizes a total return over a calendar-day span.
func CAGR(totalReturn float64, days int) float64 {
	if days <= 0 || totalReturn <= -1 {
		return 0
	}
	return math.Pow(1+totalReturn, 365.25/float64(days)) - 1
}

// Vol is the annualized sample standard deviation of daily returns.
func Vol(returns []float64) float64 {
	if len(returns) < 2 {
		return 0
	}
	m := mean(returns)
	ss := 0.0
	for _, r := range returns {
		ss += (r - m) * (r - m)
	}
	return math.Sqrt(ss/float64(len(returns)-1)) * math.Sqrt(252)
}

// Sharpe uses arithmetic annualization of the mean daily excess return -
// the usual simple convention, documented in the plan.
func Sharpe(returns []float64, rfAnnual float64) float64 {
	v := Vol(returns)
	if v == 0 {
		return 0
	}
	return (mean(returns)*252 - rfAnnual) / v
}

// Sortino replaces the denominator with the downside deviation against the
// daily risk-free target.
func Sortino(returns []float64, rfAnnual float64) float64 {
	if len(returns) == 0 {
		return 0
	}
	target := rfAnnual / 252
	ss := 0.0
	for _, r := range returns {
		if r < target {
			ss += (r - target) * (r - target)
		}
	}
	down := math.Sqrt(ss/float64(len(returns))) * math.Sqrt(252)
	if down == 0 {
		return 0
	}
	return (mean(returns)*252 - rfAnnual) / down
}

func mean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Drawdown describes the worst peak-to-trough loss of a series.
type Drawdown struct {
	Depth        float64 // négatif : −0.25 = −25 %
	Peak, Trough domain.Date
	Recovered    *domain.Date // nil si le pic n'est pas regagné
}

func MaxDrawdown(points []Point) Drawdown {
	var dd Drawdown
	if len(points) == 0 {
		return dd
	}
	peak := points[0]
	for _, p := range points[1:] {
		if p.Value > peak.Value {
			peak = p
			continue
		}
		if peak.Value <= 0 {
			continue
		}
		if depth := (p.Value - peak.Value) / peak.Value; depth < dd.Depth {
			dd.Depth, dd.Peak, dd.Trough = depth, peak.Date, p.Date
			dd.Recovered = nil
		}
	}
	// première date où le pic du drawdown maximal est regagné
	inDD := false
	for _, p := range points {
		if p.Date == dd.Peak {
			inDD = true
			continue
		}
		if inDD && p.Date.Time().After(dd.Trough.Time()) && p.Value >= valueAt(points, dd.Peak) {
			rec := p.Date
			dd.Recovered = &rec
			break
		}
	}
	return dd
}

func valueAt(points []Point, d domain.Date) float64 {
	for _, p := range points {
		if p.Date == d {
			return p.Value
		}
	}
	return math.Inf(1)
}
```

`internal/perf/periods.go`:

```go
package perf

import (
	"fmt"

	"finador/internal/domain"
)

// PeriodRange resolves a period name into [from, to]: the value at `from` is
// the comparison base, so "ytd" starts at Dec 31 of last year.
func PeriodRange(name string, today domain.Date) (from, to domain.Date, err error) {
	to = today
	switch name {
	case "1j":
		return today.AddDays(-1), to, nil
	case "2j":
		return today.AddDays(-2), to, nil
	case "5j":
		return today.AddDays(-5), to, nil
	case "7j":
		return today.AddDays(-7), to, nil
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), to, nil
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), to, nil
	case "ytd":
		return domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, to, nil
	case "1a":
		return domain.DateOf(today.Time().AddDate(-1, 0, 0)), to, nil
	case "an-1":
		return domain.Date{Year: today.Year - 2, Month: 12, Day: 31},
			domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, nil
	}
	return from, to, fmt.Errorf("période %q inconnue (1j, 2j, 5j, 7j, 1m, 3m, ytd, 1a, an-1)", name)
}

// Names lists the period table shown by `finador perf`, in display order.
func Names() []string {
	return []string{"1j", "5j", "1m", "3m", "ytd", "1a", "an-1"}
}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/perf/ -v && go test ./...` → PASS. gofmt/vet silencieux. Les valeurs attendues des tests sont dérivées dans les tests eux-mêmes (vérité indépendante) - ne pas les modifier pour faire passer.

- [ ] **Step 5: commit**

```bash
git add internal/perf
git commit -m "feat(perf): TWR, XIRR, CAGR, volatilité, Sharpe, Sortino, max drawdown - maths pures"
```

---

### Task C2: portfolio - séries journalières (marche incrémentale)

**Files:**
- Create: `internal/portfolio/series.go`
- Test: `internal/portfolio/series_test.go`

La série NE rappelle PAS Value() par date (O(jours × ledger) intenable) : une seule passe
sur le ledger trié, état incrémental (quantités, coût moyen, cash, bases), valeur calculée
chaque jour par lookups binaires dans les séries de prix/FX. Les règles fiscales et de cash
sont CELLES de Value() - le test d'or vérifie l'égalité aux extrémités.

Conventions de flux externes (cf. tête de plan) :
- All/Account : Deposit/Withdraw (+/−) ; Buy/Sell des comptes **non suivis** (+/−) ;
  Dividend (manuel ou auto) d'un compte non suivi → flux **sortant** (revenu encaissé hors
  portée). Comptes suivis : trades et dividendes restent internes (le cash est dans la
  portée).
- Group/Asset : Buy = +montant, Sell = −montant, Dividend (manuel/auto) = −montant -
  l'argent entre/sort de la poche, quel que soit le suivi du cash (le cash n'est jamais
  dans une portée groupe/actif).
- Fee : jamais un flux (c'est un coût) ; ignoré si le compte n'a pas de cash suivi
  (documenté).
- FX ou cours manquant un jour donné : contribution **0** ce jour-là (la courbe démarre
  avec l'historique disponible) - contrairement à Value() qui échoue, une courbe doit
  rester traçable.

- [ ] **Step 1: tests qui échouent**

`internal/portfolio/series_test.go`:

```go
package portfolio

import (
	"testing"

	"finador/internal/domain"
)

func TestSeriesMatchesValueAtEndpoint(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	want, err := Value(b, scopeOf(t, b, ""), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	if last.Date != at {
		t.Fatalf("dernier point au %s, attendu %s", last.Date, at)
	}
	approx(t, "gross fin de série vs Value", last.Gross, want.Gross)
	approx(t, "net fin de série vs Value", last.Net, want.Net)
}

func TestSeriesAccountScopeMatchesValue(t *testing.T) {
	b := valuationBook(t)
	at := mustDate("2026-06-05")
	want, err := Value(b, scopeOf(t, b, "PEA"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scopeOf(t, b, "PEA"), mustDate("2026-01-01"), at, domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	last := res.Points[len(res.Points)-1]
	approx(t, "gross", last.Gross, want.Gross)
	approx(t, "net", last.Net, want.Net)
}

func TestSeriesExternalFlowsAllScope(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// PEA est suivi : ses trades sont internes. Seuls flux : deposit pea
	// +10000 (01-10) et buy cto +1100 (01-20, compte non suivi).
	if len(res.Flows) != 2 {
		t.Fatalf("flows = %+v, attendu 2", res.Flows)
	}
	if res.Flows[0].Date != mustDate("2026-01-10") {
		t.Errorf("flow[0] = %+v", res.Flows[0])
	}
	approx(t, "flow deposit", res.Flows[0].Amount, 10000)
	if res.Flows[1].Date != mustDate("2026-01-20") {
		t.Errorf("flow[1] = %+v", res.Flows[1])
	}
	approx(t, "flow buy cto", res.Flows[1].Amount, 1100)
}

func TestSeriesExternalFlowsGroupScope(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, "actions"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// tous les trades sur cw8 sont des flux de la poche : +5000, +1100, +2750, −1800
	wantFlows := []struct {
		date string
		amt  float64
	}{
		{"2026-01-15", 5000}, {"2026-01-20", 1100}, {"2026-02-15", 2750}, {"2026-03-15", -1800},
	}
	if len(res.Flows) != len(wantFlows) {
		t.Fatalf("flows = %+v", res.Flows)
	}
	for i, w := range wantFlows {
		if res.Flows[i].Date != mustDate(w.date) {
			t.Errorf("flow[%d].Date = %s", i, res.Flows[i].Date)
		}
		approx(t, "flow", res.Flows[i].Amount, w.amt)
	}
}

func TestSeriesBeforeMarketData(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), mustDate("2026-01-01"), mustDate("2026-01-12"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// au 12 janv : aucune clôture cw8 (la série commence le 20 mars) → la
	// position contribue 0 ; cash pea 10000 (déposé le 10), livret 12000
	// (relevé du 5), maison 400000 (relevé du 1er)
	last := res.Points[len(res.Points)-1]
	approx(t, "gross avant données marché", last.Gross, 10000+12000+400000)
}

func TestSeriesDefaultFrom(t *testing.T) {
	b := valuationBook(t)
	res, err := Series(b, scopeOf(t, b, ""), domain.Date{}, mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// from zéro → première transaction du ledger (relevé maison du 1er janv)
	if res.Points[0].Date != mustDate("2026-01-01") {
		t.Errorf("premier point = %s", res.Points[0].Date)
	}
}

func TestSeriesAutoDividendFlows(t *testing.T) {
	b := valuationBook(t)
	b.Market.Dividends = map[domain.AssetID][]domain.DividendEvent{
		"cw8": {{ExDate: mustDate("2026-03-01"), Amount: 2}},
	}
	// portée groupe : le dividende sort de la poche → flux −(15+2)×2 ?
	// pea détient 15 parts au 1er mars, cto 2 → −34 au total
	res, err := Series(b, scopeOf(t, b, "actions"), mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	var divFlow float64
	for _, f := range res.Flows {
		if f.Date == mustDate("2026-03-01") {
			divFlow += f.Amount
		}
	}
	approx(t, "flux dividende sortant", divFlow, -34)
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/portfolio/` → FAIL - undefined: Series.

- [ ] **Step 3: implémenter**

`internal/portfolio/series.go`:

```go
package portfolio

import (
	"errors"

	"finador/internal/domain"
)

// SeriesPoint is one day of a scope's value.
type SeriesPoint struct {
	Date       domain.Date
	Gross, Net float64
}

// ExternalFlow is money entering (>0) or leaving (<0) the scope, in display
// currency - what TWR neutralizes and XIRR consumes.
type ExternalFlow struct {
	Date   domain.Date
	Amount float64
}

// SeriesResult bundles the daily curve with the scope's external flows.
type SeriesResult struct {
	Points []SeriesPoint
	Flows  []ExternalFlow
}

// Series walks the ledger once and produces the daily value of the scope
// between from and to. Same cash/tax rules as Value(); days lacking price or
// FX data contribute zero (a curve must stay drawable). A zero `from`
// defaults to the first transaction.
func Series(b *domain.Book, scope Scope, from, to domain.Date, ccy domain.Currency, fx FX) (SeriesResult, error) {
	txs := Sorted(b)
	if from.IsZero() {
		if len(txs) == 0 {
			return SeriesResult{}, errors.New("aucune transaction : rien à tracer")
		}
		from = txs[0].Date
	}
	if to.Before(from) {
		return SeriesResult{}, errors.New("borne de fin antérieure au début")
	}

	w := newWalker(b, scope, ccy, fx)
	var out SeriesResult
	ti := 0
	for d := from.AddDays(0); !to.Before(d); d = d.AddDays(1) {
		for ti < len(txs) && !d.Before(txs[ti].Date) {
			// les transactions antérieures à from construisent l'état initial
			collect := txs[ti].Date.After(from.Time()) // voir note ci-dessous
			w.applyTx(txs[ti], collect)
			ti++
		}
		w.applyDividends(d, d.After(from.Time()))
		gross, net := w.valueAt(d)
		out.Points = append(out.Points, SeriesPoint{Date: d, Gross: gross, Net: net})
	}
	out.Flows = w.flows
	return out, nil
}
```

NOTE pour l'implémenteur : `domain.Date` n'a pas de méthode After - utiliser
`from.Before(txs[ti].Date)` pour « strictement après from » (les flux du jour `from`
sont déjà dans la valeur de base V₀ et ne doivent PAS être collectés). Adapter le code
ci-dessus en conséquence : `collect := from.Before(txs[ti].Date)` et
`w.applyDividends(d, from.Before(d))`. La boucle des jours s'écrit
`for d := from; !to.Before(d); d = d.AddDays(1)`.

```go
// walker carries the incremental replay state.
type walker struct {
	b     *domain.Book
	scope Scope
	ccy   domain.Currency
	fx    FX

	pairs    map[pairKey]*pairState
	order    []pairKey
	accounts map[domain.AccountID]*accountState
	manual   map[domain.AssetID]bool
	flows    []ExternalFlow
}

type pairKey struct {
	acc   domain.AccountID
	asset domain.AssetID
}

type pairState struct {
	acc    *domain.Account
	asset  *domain.Asset
	qty    float64
	basis  float64 // coût moyen, devise d'affichage, flux convertis à leur date
	stmt   *domain.Money
	first  float64 // base des biens : première estimation convertie à sa date
	hasFst bool
}

type accountState struct {
	acc       *domain.Account
	tracked   bool
	cash      float64 // devise du compte
	flowBasis float64 // base d'enveloppe (devise d'affichage)
}

func newWalker(b *domain.Book, scope Scope, ccy domain.Currency, fx FX) *walker {
	w := &walker{
		b: b, scope: scope, ccy: ccy, fx: fx,
		pairs:    map[pairKey]*pairState{},
		accounts: map[domain.AccountID]*accountState{},
		manual:   manualDividendAssets(b),
	}
	for _, acc := range b.Accounts {
		w.accounts[acc.ID] = &accountState{acc: acc, tracked: CashTracked(b, acc.ID)}
	}
	return w
}

func (w *walker) pair(t *domain.Transaction) *pairState {
	k := pairKey{t.Account, t.Asset}
	if p, ok := w.pairs[k]; ok {
		return p
	}
	acc, errA := w.b.Account(string(t.Account))
	asset, errB := w.b.Asset(string(t.Asset))
	if errA != nil || errB != nil {
		return nil // référence orpheline : ignorée
	}
	p := &pairState{acc: acc, asset: asset}
	w.pairs[k] = p
	w.order = append(w.order, k)
	return p
}

// conv ignores conversion failures by contributing zero: series semantics.
func (w *walker) conv(m domain.Money, to domain.Currency, at domain.Date) float64 {
	v, err := w.fx.Convert(toF(m.Amount), m.Currency, to, at)
	if err != nil {
		return 0
	}
	return v
}

func (w *walker) addFlow(d domain.Date, amount float64, collect bool) {
	if collect && amount != 0 {
		w.flows = append(w.flows, ExternalFlow{Date: d, Amount: amount})
	}
}

func (w *walker) applyTx(t *domain.Transaction, collect bool) {
	acc := w.accounts[t.Account]
	if acc == nil {
		return
	}
	inCash := w.scope.hasCash(acc.acc)
	switch t.Kind {
	case domain.Buy, domain.Sell:
		p := w.pair(t)
		if p == nil {
			return
		}
		disp := w.conv(t.Amount, w.ccy, t.Date)
		sign := 1.0
		if t.Kind == domain.Sell {
			sign = -1
		}
		if p.asset.Kind != domain.Property {
			if t.Kind == domain.Buy {
				p.basis += disp
				p.qty += toF(t.Quantity)
			} else if p.qty > 0 {
				sold := min(toF(t.Quantity), p.qty)
				p.basis -= p.basis * sold / p.qty
				p.qty -= sold
			}
		}
		if acc.tracked {
			acc.cash -= sign * w.conv(t.Amount, acc.acc.Currency, t.Date)
		} else {
			acc.flowBasis += sign * disp
		}
		inAsset := w.scope.hasAsset(acc.acc, p.asset)
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
			if inAsset {
				w.addFlow(t.Date, sign*disp, collect) // l'argent entre/sort de la poche
			}
		default: // All, ByAccount
			if inCash && !acc.tracked {
				w.addFlow(t.Date, sign*disp, collect)
			}
		}
	case domain.Deposit, domain.Withdraw:
		sign := 1.0
		if t.Kind == domain.Withdraw {
			sign = -1
		}
		disp := w.conv(t.Amount, w.ccy, t.Date)
		acc.cash += sign * w.conv(t.Amount, acc.acc.Currency, t.Date)
		acc.flowBasis += sign * disp
		if inCash {
			w.addFlow(t.Date, sign*disp, collect)
		}
	case domain.Dividend:
		p := w.pair(t) // peut être nil (dividende de cash pur improbable)
		disp := w.conv(t.Amount, w.ccy, t.Date)
		if acc.tracked {
			acc.cash += w.conv(t.Amount, acc.acc.Currency, t.Date)
		}
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
			if p != nil && w.scope.hasAsset(acc.acc, p.asset) {
				w.addFlow(t.Date, -disp, collect) // le dividende sort de la poche
			}
		default:
			if inCash && !acc.tracked {
				w.addFlow(t.Date, -disp, collect) // revenu encaissé hors portée
			}
		}
	case domain.Fee:
		if acc.tracked {
			acc.cash -= w.conv(t.Amount, acc.acc.Currency, t.Date)
		}
		// jamais un flux : un coût doit peser sur la performance
	case domain.Statement:
		if t.Asset == "" {
			if acc.tracked {
				acc.cash = w.conv(t.Amount, acc.acc.Currency, t.Date) // ancre
			}
			return
		}
		p := w.pair(t)
		if p == nil {
			return
		}
		m := t.Amount
		p.stmt = &m
		if !p.hasFst && p.asset.Kind == domain.Property {
			p.first = w.conv(t.Amount, w.ccy, t.Date)
			p.hasFst = true
		}
	}
}

// applyDividends credits the day's automatic dividends (assets without any
// manual Dividend tx) and emits the matching scope flows.
func (w *walker) applyDividends(d domain.Date, collect bool) {
	for _, k := range w.order {
		p := w.pairs[k]
		if p.qty <= 0 || w.manual[p.asset.ID] {
			continue
		}
		for _, ev := range w.b.Market.Dividends[p.asset.ID] {
			if ev.ExDate != d {
				continue
			}
			gross := domain.Money{Amount: decimalFromFloat(p.qty * ev.Amount), Currency: p.asset.Currency}
			disp := w.conv(gross, w.ccy, d)
			acc := w.accounts[p.acc.ID]
			if acc.tracked {
				acc.cash += w.conv(gross, acc.acc.Currency, d)
			}
			switch {
			case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
				if w.scope.hasAsset(p.acc, p.asset) {
					w.addFlow(d, -disp, collect)
				}
			default:
				if w.scope.hasCash(acc.acc) && !acc.tracked {
					w.addFlow(d, -disp, collect)
				}
			}
		}
	}
}

// valueAt prices the current state at day d, with the same tax rules as
// Value(): envelope-exact for All/Account, per-position for Group/Asset.
func (w *walker) valueAt(d domain.Date) (gross, net float64) {
	perAccount := map[domain.AccountID]float64{}
	positionTax := 0.0

	for _, k := range w.order {
		p := w.pairs[k]
		if !w.scope.hasAsset(p.acc, p.asset) {
			continue
		}
		var val float64
		switch {
		case p.asset.Kind == domain.Property:
			if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
		default:
			if p.qty <= 0 {
				break // pas de position → rien, même si un relevé traîne (== Value)
			}
			if close, _, ok := w.b.Market.Prices[p.asset.ID].At(d); ok {
				if rate, err := w.fx.Convert(close, p.asset.Currency, w.ccy, d); err == nil {
					val = p.qty * rate
				}
			} else if p.stmt != nil {
				val = w.conv(*p.stmt, w.ccy, d)
			}
		}
		gross += val
		perAccount[p.acc.ID] += val
		// impôt position par position (portées groupe/actif)
		acc := w.accounts[p.acc.ID]
		switch acc.acc.Tax.Mode {
		case domain.TaxOnValue:
			positionTax += val * rate(acc.acc.Tax)
		case domain.TaxOnGains:
			basis := p.basis
			if p.asset.Kind == domain.Property {
				basis = p.first
			}
			positionTax += max(0, val-basis) * rate(acc.acc.Tax)
		}
	}
	for _, accSt := range w.accounts {
		if !w.scope.hasCash(accSt.acc) || !accSt.tracked || accSt.cash == 0 {
			continue
		}
		v, err := w.fx.Convert(accSt.cash, accSt.acc.Currency, w.ccy, d)
		if err != nil {
			continue
		}
		gross += v
		perAccount[accSt.acc.ID] += v
		if accSt.acc.Tax.Mode == domain.TaxOnValue {
			positionTax += v * rate(accSt.acc.Tax)
		}
	}

	tax := positionTax
	if w.scope.Kind == All || w.scope.Kind == ByAccount {
		tax = 0
		for accID, g := range perAccount {
			accSt := w.accounts[accID]
			switch accSt.acc.Tax.Mode {
			case domain.TaxOnValue:
				tax += g * rate(accSt.acc.Tax)
			case domain.TaxOnGains:
				basis := accSt.flowBasis
				for _, k := range w.order {
					p := w.pairs[k]
					if p.acc.ID == accID && p.asset.Kind == domain.Property && p.hasFst {
						basis += p.first
					}
				}
				tax += max(0, g-max(0, basis)) * rate(accSt.acc.Tax)
			}
		}
	}
	return gross, gross - tax
}
```

NOTE : `decimalFromFloat` n'existe pas - utiliser `decimal.NewFromFloat` (import
`github.com/shopspring/decimal`). La base d'enveloppe est plafonnée à 0
(`max(0, basis)`) comme dans Value(). `perAccount` est une map : son itération
n'affecte que des SOMMES (commutatif), le résultat reste déterministe.

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/portfolio/ -v -run TestSeries && go test ./...` → PASS.
Le test d'or `TestSeriesMatchesValueAtEndpoint` est non négociable : si la série et
Value() divergent à la date finale, c'est la série qui est fausse - investiguer
(ordre d'application des relevés/ancres, dividendes, bases) sans toucher à Value().

- [ ] **Step 5: commit**

```bash
git add internal/portfolio
git commit -m "feat(portfolio): séries journalières de valeur et flux externes par portée"
```

---

### Task C3: chart - renderer braille pour le terminal

**Files:**
- Create: `internal/chart/braille.go`
- Test: `internal/chart/braille_test.go`

Le rendu utilise les caractères braille U+2800-U+28FF : chaque cellule porte 2×4 points,
donc une courbe de width×height cellules a une résolution de (2·width)×(4·height). Les
colonnes adjacentes sont reliées verticalement (pas de trous dans les pentes raides).
Étiquettes : max en haut à gauche, min en bas à gauche, dates de début/fin dessous.

- [ ] **Step 1: tests qui échouent**

`internal/chart/braille_test.go`:

```go
package chart

import (
	"strings"
	"testing"

	"finador/internal/domain"
	"finador/internal/perf"
)

func d(s string) domain.Date {
	dd, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return dd
}

func ramp(n int) []perf.Point {
	pts := make([]perf.Point, n)
	for i := range pts {
		pts[i] = perf.Point{Date: d("2026-01-01").AddDays(i), Value: float64(100 + i)}
	}
	return pts
}

func TestBrailleShape(t *testing.T) {
	out := Braille(ramp(60), 30, 8)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 8 lignes de courbe + 1 ligne de dates
	if len(lines) != 9 {
		t.Fatalf("lignes = %d\n%s", len(lines), out)
	}
	// chaque ligne de courbe contient au moins un caractère braille non vide
	brailleCount := 0
	for _, l := range lines[:8] {
		for _, r := range l {
			if r > 0x2800 && r <= 0x28FF {
				brailleCount++
			}
		}
	}
	if brailleCount == 0 {
		t.Fatalf("aucun point braille:\n%s", out)
	}
	// étiquettes : max sur la 1re ligne, min sur la dernière ligne de courbe
	if !strings.Contains(lines[0], "159") {
		t.Errorf("max absent de la 1re ligne: %q", lines[0])
	}
	if !strings.Contains(lines[7], "100") {
		t.Errorf("min absent de la dernière ligne: %q", lines[7])
	}
	if !strings.Contains(lines[8], "2026-01-01") || !strings.Contains(lines[8], "2026-03-01") {
		t.Errorf("dates absentes: %q", lines[8])
	}
}

func TestBrailleRampGoesUp(t *testing.T) {
	out := Braille(ramp(60), 30, 8)
	lines := strings.Split(out, "\n")
	first := firstBrailleCol(lines[0])  // la fin de la rampe (haut) est à droite
	last := firstBrailleCol(lines[7])   // le début (bas) est à gauche
	if last == -1 || first == -1 || last >= first {
		t.Errorf("rampe non croissante: bas à col %d, haut à col %d\n%s", last, first, out)
	}
}

func firstBrailleCol(line string) int {
	for i, r := range []rune(line) {
		if r > 0x2800 && r <= 0x28FF {
			return i
		}
	}
	return -1
}

func TestBrailleFlatAndEmpty(t *testing.T) {
	if out := Braille(nil, 30, 8); out != "" {
		t.Errorf("série vide: %q", out)
	}
	flat := []perf.Point{{Date: d("2026-01-01"), Value: 50}, {Date: d("2026-01-02"), Value: 50}}
	out := Braille(flat, 10, 4)
	if out == "" || !strings.Contains(out, "50") {
		t.Errorf("série plate:\n%s", out)
	}
}

func TestFormatCompact(t *testing.T) {
	for v, want := range map[float64]string{
		1234567.0: "1.23M",
		473890.0:  "473.9k",
		1500.0:    "1.5k",
		999.0:     "999.00",
		-4230.5:   "-4.2k",
	} {
		if got := formatCompact(v); got != want {
			t.Errorf("formatCompact(%v) = %q, attendu %q", v, got, want)
		}
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/chart/` → FAIL - undefined: Braille.

- [ ] **Step 3: implémenter**

`internal/chart/braille.go`:

```go
// Package chart renders value curves: braille for the terminal, SVG for the
// web. Pure string builders, no I/O.
package chart

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"finador/internal/perf"
)

// Braille draws points as a width×height cell chart (each braille cell holds
// 2×4 dots), with min/max labels and the date range underneath.
func Braille(points []perf.Point, width, height int) string {
	if len(points) == 0 {
		return ""
	}
	lo, hi := bounds(points)
	if hi == lo {
		hi = lo + 1 // série plate : on lui donne de l'épaisseur
	}
	cols, rows := width*2, height*4
	grid := make([][]bool, rows)
	for i := range grid {
		grid[i] = make([]bool, cols)
	}
	// y en dots, 0 = haut
	yOf := func(v float64) int {
		y := int(math.Round((hi - v) / (hi - lo) * float64(rows-1)))
		return min(max(y, 0), rows-1)
	}
	prevY := -1
	for x := range cols {
		idx := x * (len(points) - 1) / max(cols-1, 1)
		y := yOf(points[idx].Value)
		grid[y][x] = true
		if prevY >= 0 { // relie les colonnes pour les pentes raides
			for yy := min(prevY, y); yy <= max(prevY, y); yy++ {
				grid[yy][x] = true
			}
		}
		prevY = y
	}

	labels := []string{formatCompact(hi)}
	for range height - 2 {
		labels = append(labels, "")
	}
	if height > 1 {
		labels = append(labels, formatCompact(lo))
	}
	labelW := 0
	for _, l := range labels {
		labelW = max(labelW, len(l))
	}

	var b strings.Builder
	for row := range height {
		fmt.Fprintf(&b, "%*s ", labelW, labels[row])
		for col := range width {
			var bits rune
			for dy := range 4 {
				for dx := range 2 {
					if grid[row*4+dy][col*2+dx] {
						bits |= brailleBit(dx, dy)
					}
				}
			}
			b.WriteRune(0x2800 + bits)
		}
		b.WriteByte('\n')
	}
	from, to := points[0].Date.String(), points[len(points)-1].Date.String()
	gap := max(width-len(from)-len(to), 1)
	fmt.Fprintf(&b, "%*s %s%s%s\n", labelW, "", from, strings.Repeat(" ", gap), to)
	return b.String()
}

// brailleBit maps a (dx, dy) dot to its bit in the braille block.
func brailleBit(dx, dy int) rune {
	bits := [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	return bits[dy][dx]
}

func bounds(points []perf.Point) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, p := range points {
		lo, hi = math.Min(lo, p.Value), math.Max(hi, p.Value)
	}
	return lo, hi
}

// formatCompact shortens big numbers for axis labels: 473.9k, 1.23M.
func formatCompact(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return trimZero(strconv.FormatFloat(v/1e6, 'f', 2, 64)) + "M"
	case a >= 1e3:
		return trimZero(strconv.FormatFloat(v/1e3, 'f', 1, 64)) + "k"
	default:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
}

func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
```

NOTE : vérifier les attentes de TestFormatCompact à la main : 1234567/1e6 = 1.234567 →
"1.23" → "1.23M" ✓ ; 473890/1e3 = 473.89 → 'f' 1 → "473.9" → "473.9k" ✓ ; 1500/1e3 =
1.5 → "1.5k" ✓ ; −4230.5/1e3 = −4.2305 → "-4.2k" ✓. La rampe de 60 jours va de
2026-01-01 à 2026-03-01 (59 jours plus tard) - vérifier avec AddDays(59).

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/chart/ -v && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/chart
git commit -m "feat(chart): courbes braille pour le terminal"
```

---

### Task C4: chart - renderer SVG (pour la phase D)

**Files:**
- Create: `internal/chart/svg.go`
- Test: `internal/chart/svg_test.go`

SVG autonome (attributs inline, zéro CSS externe, zéro JS) : axes, 4 lignes de grille
horizontales étiquetées, une polyline par série (brut, net…), remplissage léger sous la
première série, dates aux coins. Sortie déterministe (FormatFloat partout).

- [ ] **Step 1: tests qui échouent**

`internal/chart/svg_test.go`:

```go
package chart

import (
	"strings"
	"testing"

	"finador/internal/perf"
)

func TestSVGStructure(t *testing.T) {
	gross := Line{Label: "brut", Color: "#0a7d4b", Points: ramp(60)}
	net := Line{Label: "net", Color: "#888888", Points: ramp(60)}
	out := SVG([]Line{gross, net}, 800, 300)

	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"`, `viewBox="0 0 800 300"`,
		`<polyline`, `stroke="#0a7d4b"`, `stroke="#888888"`,
		"2026-01-01", "2026-03-01", // dates aux coins
		"159", "100", // étiquettes d'échelle (max/min)
		"brut", "net", // légende
		"</svg>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q absent du SVG:\n%s", want, out[:min(len(out), 600)])
		}
	}
	if strings.Count(out, "<polyline") != 2 {
		t.Errorf("polylines = %d, attendu 2", strings.Count(out, "<polyline"))
	}
	// pas de NaN ni d'Inf dans les coordonnées
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s dans le SVG", bad)
		}
	}
}

func TestSVGEmpty(t *testing.T) {
	if out := SVG(nil, 800, 300); out != "" {
		t.Errorf("SVG vide: %q", out)
	}
	if out := SVG([]Line{{Label: "x", Points: nil}}, 800, 300); out != "" {
		t.Errorf("SVG sans points: %q", out)
	}
}

func TestSVGDeterministic(t *testing.T) {
	lines := []Line{{Label: "brut", Color: "#0a7d4b", Points: ramp(30)}}
	if SVG(lines, 800, 300) != SVG(lines, 800, 300) {
		t.Error("sortie SVG non déterministe")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/chart/` → FAIL - undefined: SVG, Line.

- [ ] **Step 3: implémenter**

`internal/chart/svg.go`:

```go
package chart

import (
	"fmt"
	"math"
	"strings"

	"finador/internal/perf"
)

// Line is one curve of an SVG chart.
type Line struct {
	Label  string
	Color  string
	Points []perf.Point
}

const (
	padL, padR, padT, padB = 64, 16, 18, 34
)

// SVG renders self-contained markup: inline attributes only, no CSS, no JS.
// The first line gets a light area fill; the scale covers every line.
func SVG(lines []Line, w, h int) string {
	lines = nonEmpty(lines)
	if len(lines) == 0 {
		return ""
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	maxN := 0
	for _, l := range lines {
		llo, lhi := bounds(l.Points)
		lo, hi = math.Min(lo, llo), math.Max(hi, lhi)
		maxN = max(maxN, len(l.Points))
	}
	if hi == lo {
		hi = lo + 1
	}
	plotW, plotH := float64(w-padL-padR), float64(h-padT-padB)
	x := func(i, n int) float64 { return float64(padL) + float64(i)/float64(max(n-1, 1))*plotW }
	y := func(v float64) float64 { return float64(padT) + (hi-v)/(hi-lo)*plotH }
	f := func(v float64) string { return fmt.Sprintf("%.1f", v) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" font-family="ui-monospace,monospace" font-size="11">`+"\n", w, h)
	// grille + étiquettes d'échelle
	for i := range 4 {
		gv := lo + (hi-lo)*float64(3-i)/3
		gy := y(gv)
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%d" y2="%s" stroke="#dddddd" stroke-width="1"/>`+"\n",
			f(float64(padL)), f(gy), w-padR, f(gy))
		fmt.Fprintf(&b, `<text x="%d" y="%s" text-anchor="end" fill="#666666">%s</text>`+"\n",
			padL-6, f(gy+4), formatCompact(gv))
	}
	// aire sous la première série
	first := lines[0]
	var area strings.Builder
	for i, p := range first.Points {
		fmt.Fprintf(&area, "%s,%s ", f(x(i, len(first.Points))), f(y(p.Value)))
	}
	fmt.Fprintf(&b, `<polygon points="%s%s,%s %s,%s" fill="%s" fill-opacity="0.07"/>`+"\n",
		area.String(),
		f(x(len(first.Points)-1, len(first.Points))), f(float64(padT)+plotH),
		f(x(0, len(first.Points))), f(float64(padT)+plotH),
		first.Color)
	// courbes
	for _, l := range lines {
		var pts strings.Builder
		for i, p := range l.Points {
			fmt.Fprintf(&pts, "%s,%s ", f(x(i, len(l.Points))), f(y(p.Value)))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="1.8"/>`+"\n",
			strings.TrimSpace(pts.String()), l.Color)
	}
	// légende + dates
	lx := padL
	for _, l := range lines {
		fmt.Fprintf(&b, `<rect x="%d" y="4" width="10" height="3" fill="%s"/><text x="%d" y="10" fill="#444444">%s</text>`+"\n",
			lx, l.Color, lx+14, l.Label)
		lx += 14 + 8*len(l.Label) + 16
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666666">%s</text>`+"\n", padL, h-8, first.Points[0].Date)
	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="end" fill="#666666">%s</text>`+"\n",
		w-padR, h-8, first.Points[len(first.Points)-1].Date)
	b.WriteString("</svg>\n")
	return b.String()
}

func nonEmpty(lines []Line) []Line {
	var out []Line
	for _, l := range lines {
		if len(l.Points) > 0 {
			out = append(out, l)
		}
	}
	return out
}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/chart/ -v && go test ./...` → PASS. gofmt/vet silencieux.
Sanity visuel : `go run` un petit main jetable (NON committé) qui écrit le SVG d'une
rampe dans /tmp/test.svg et vérifier qu'il s'ouvre (`qlmanage -p /tmp/test.svg` ou
simplement contrôler le markup) ; supprimer le fichier après.

- [ ] **Step 5: commit**

```bash
git add internal/chart
git commit -m "feat(chart): renderer SVG autonome multi-courbes"
```

---

### Task C5: cli - commande perf (périodes + métriques)

**Files:**
- Create: `internal/cli/perf.go`
- Modify: `internal/cli/cli.go` (AddCommand += perfCmd)
- Test: `internal/cli/cli_test.go` (ajout)

Sortie :

```
PEA Zephyr - performance (EUR), évalué au 2026-06-05
PÉRIODE   TWR       XIRR
1j        +0.00%    -
5j        +1.82%    -
1m        +2.00%    -
3m        +2.00%    -
ytd       +2.00%    -
1a        +2.00%    -
an-1      -         -
origine   +2.00%    +5.07%

CAGR +5.10%   vol 4.05%   Sharpe 1.26   Sortino 1.71   (rf 0.0%)
max drawdown −0.00% - aucun
```

(Les nombres ci-dessus sont illustratifs SAUF ceux affirmés par le test : TWR origine
+2.00 % avec la fixture fake. Le XIRR des fenêtres < 30 jours s'affiche « - » : annualiser
un rendement d'un jour n'a pas de sens.)

Règles : `--to` fixe la date d'évaluation (défaut aujourd'hui) - les périodes sont
relatives à cette date ; `--from` avec `--to` ajoute une ligne « fenêtre » au tableau ;
le taux sans risque vient de `config risk-free` (« 2.4% ») ; les métriques (CAGR, vol,
Sharpe, Sortino, maxDD) sont calculées sur la série depuis l'origine jusqu'à `--to`.
Une période dont le début précède l'origine du ledger affiche les valeurs depuis
l'origine (et « origine » couvre tout). Si la portée n'a aucune transaction → erreur
propre « aucune transaction ».

- [ ] **Step 1: test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestPerfEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")
	run(t, db, "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "perf", "--to", "2026-06-05")
	// série : 5000 plat jusqu'au 1er juin (l'achat est neutre), puis
	// 06-05 : 10×560 − 500 = 5100 → TWR origine = +2.00 %
	for _, want := range []string{"PÉRIODE", "TWR", "XIRR", "origine", "+2.00%", "CAGR", "Sharpe", "Sortino", "max drawdown"} {
		if !strings.Contains(out, want) {
			t.Errorf("perf: %q manquant dans:\n%s", want, out)
		}
	}
	// XIRR des fenêtres courtes : tiret
	if !strings.Contains(out, "-") {
		t.Errorf("tiret XIRR absent:\n%s", out)
	}

	// portée inexistante → erreur propre
	if _, err := tryRun(t, db, "perf", "nimporte"); err == nil {
		t.Fatal("portée inconnue acceptée")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/cli/ -run TestPerfEndToEnd` → FAIL - unknown command "perf".

- [ ] **Step 3: implémenter**

Dans `internal/cli/cli.go`, AddCommand += `perfCmd(a)`.

`internal/cli/perf.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func perfCmd(a *app) *cobra.Command {
	var ccy, from, to string
	cmd := &cobra.Command{
		Use:   "perf [portée]",
		Short: "Rendements (TWR, XIRR) par période et métriques de risque",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := portfolio.ParseScope(b, ref)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			evalTo, err := dateOrToday(to)
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			fx := market.Converter{FX: b.Market.FX}

			// série complète depuis l'origine : base des métriques et des périodes
			res, err := portfolio.Series(b, scope, domain.Date{}, evalTo, display, fx)
			if err != nil {
				return err
			}
			if len(res.Points) < 2 {
				return errors.New("pas assez d'historique pour mesurer une performance")
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintf(cmd.OutOrStdout(), "%s - performance (%s), évalué au %s\n", scope.Label, display, evalTo)
			fmt.Fprintln(w, "PÉRIODE\tTWR\tXIRR")
			for _, name := range perf.Names() {
				pf, pt, err := perf.PeriodRange(name, evalTo)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", name, twrCell(res, pf, pt), xirrCell(res, pf, pt))
			}
			origin := res.Points[0].Date
			fmt.Fprintf(w, "origine\t%s\t%s\n", twrCell(res, origin, evalTo), xirrCell(res, origin, evalTo))
			if from != "" {
				wf, err := domain.ParseDate(from)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "fenêtre\t%s\t%s\n", twrCell(res, wf, evalTo), xirrCell(res, wf, evalTo))
			}
			w.Flush()

			rf := riskFree(b)
			returns := perf.DailyReturns(res.Points, res.Flows)
			twrTotal := perf.TWR(res.Points, res.Flows)
			days := int(res.Points[len(res.Points)-1].Date.Time().Sub(origin.Time()).Hours() / 24)
			fmt.Fprintf(cmd.OutOrStdout(), "\nCAGR %s   vol %s   Sharpe %.2f   Sortino %.2f   (rf %s)\n",
				pct(perf.CAGR(twrTotal, days)), pct(perf.Vol(returns)),
				perf.Sharpe(returns, rf), perf.Sortino(returns, rf), pct(rf))
			dd := perf.MaxDrawdown(res.Points)
			if dd.Depth < 0 {
				rec := "non récupéré"
				if dd.Recovered != nil {
					rec = "récupéré le " + dd.Recovered.String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "max drawdown %s (%s → %s, %s)\n", pct(dd.Depth), dd.Peak, dd.Trough, rec)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "max drawdown - aucun")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&from, "from", "", "début d'une fenêtre libre AAAA-MM-JJ")
	cmd.Flags().StringVar(&to, "to", "", "date d'évaluation AAAA-MM-JJ (défaut : aujourd'hui)")
	return cmd
}

// window slices the series to [from, to]; clamped to available history.
func window(res portfolio.SeriesResult, from, to domain.Date) ([]perf.Point, []perf.Flow) {
	var pts []perf.Point
	for _, p := range res.Points {
		if p.Date.Before(from) || to.Before(p.Date) {
			continue
		}
		pts = append(pts, perf.Point{Date: p.Date, Value: p.Gross})
	}
	var flows []perf.Flow
	for _, fl := range res.Flows {
		if fl.Date.Before(from) || to.Before(fl.Date) || !from.Before(fl.Date) {
			continue // les flux du jour de base sont dans V0
		}
		flows = append(flows, perf.Flow{Date: fl.Date, Amount: fl.Amount})
	}
	return pts, flows
}

func twrCell(res portfolio.SeriesResult, from, to domain.Date) string {
	pts, flows := window(res, from, to)
	if len(pts) < 2 {
		return "-"
	}
	return pct(perf.TWR(pts, flows))
}

// xirrCell: windows shorter than 30 days print "-" (annualizing a daily move
// is meaningless).
func xirrCell(res portfolio.SeriesResult, from, to domain.Date) string {
	if to.Time().Sub(from.Time()).Hours() < 30*24 {
		return "-"
	}
	pts, flows := window(res, from, to)
	if len(pts) < 2 || pts[0].Value <= 0 {
		return "-"
	}
	cfs := []perf.Flow{{Date: pts[0].Date, Amount: -pts[0].Value}}
	for _, fl := range flows {
		cfs = append(cfs, perf.Flow{Date: fl.Date, Amount: -fl.Amount})
	}
	cfs = append(cfs, perf.Flow{Date: pts[len(pts)-1].Date, Amount: pts[len(pts)-1].Value})
	r, err := perf.XIRR(cfs)
	if err != nil {
		return "-"
	}
	return pct(r)
}

func pct(x float64) string {
	return strconv.FormatFloat(x*100, 'f', 2, 64) + "%"
}

// riskFree reads config "risk-free" ("2.4%"), defaulting to zero.
func riskFree(b *domain.Book) float64 {
	s := strings.TrimSuffix(strings.TrimSpace(b.Config["risk-free"]), "%")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v / 100
}
```

NOTE : `pct` n'ajoute pas de signe « + » - le test attend "+2.00%". Adapter : pour les
cellules TWR/XIRR utiliser un format signé (`%+.2f%%` via
`fmt.Sprintf("%+.2f%%", x*100)`) et garder `pct` non signé pour CAGR/vol/rf. Définir
deux helpers : `pctSigned` et `pct`. Vérifier la cohérence avec le test (le test cherche
"+2.00%").

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/cli/ -run TestPerfEndToEnd -v && go test ./...` → PASS.

- [ ] **Step 5: commit**

```bash
git add internal/cli
git commit -m "feat(cli): perf - TWR/XIRR par période, CAGR, vol, Sharpe, Sortino, max drawdown"
```

---

### Task C6: cli - commande chart + finition de phase

**Files:**
- Create: `internal/cli/chart.go`
- Modify: `internal/cli/cli.go` (AddCommand += chartCmd)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestChartEndToEnd(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "deposit", "PEA Zephyr", "5000", "2026-01-10")
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
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/cli/ -run TestChartEndToEnd` → FAIL - unknown command "chart".

- [ ] **Step 3: implémenter**

Dans `internal/cli/cli.go`, AddCommand += `chartCmd(a)`.

`internal/cli/chart.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

func chartCmd(a *app) *cobra.Command {
	var ccy, from, to string
	var net bool
	var width, height int
	cmd := &cobra.Command{
		Use:   "chart [portée]",
		Short: "Courbe d'évolution de la valeur, dans le terminal",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			b := f.Book
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			scope, err := portfolio.ParseScope(b, ref)
			if err != nil {
				return err
			}
			display, err := currencyOr(ccy, displayCurrency(b))
			if err != nil {
				return err
			}
			fromD := domain.Date{}
			if from != "" {
				if fromD, err = domain.ParseDate(from); err != nil {
					return err
				}
			}
			toD, err := dateOrToday(to)
			if err != nil {
				return err
			}
			ensureDisplayFX(cmd, a, f, display)
			res, err := portfolio.Series(b, scope, fromD, toD, display, market.Converter{FX: b.Market.FX})
			if err != nil {
				return err
			}
			pts := make([]perf.Point, len(res.Points))
			label := "brut"
			for i, p := range res.Points {
				v := p.Gross
				if net {
					v, label = p.Net, "net"
				}
				pts[i] = perf.Point{Date: p.Date, Value: v}
			}
			last := pts[len(pts)-1]
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s, %s) - dernier point : %s\n",
				scope.Label, label, display, money(last.Value, display))
			fmt.Fprint(cmd.OutOrStdout(), chart.Braille(pts, width, height))
			return nil
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : config currency, sinon EUR)")
	cmd.Flags().StringVar(&from, "from", "", "début AAAA-MM-JJ (défaut : origine)")
	cmd.Flags().StringVar(&to, "to", "", "fin AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().BoolVar(&net, "net", false, "courbe nette d'impôt latent")
	cmd.Flags().IntVar(&width, "width", 70, "largeur en caractères")
	cmd.Flags().IntVar(&height, "height", 12, "hauteur en lignes")
	return cmd
}
```

- [ ] **Step 4: vérifier le succès - toute la phase**

Run: `gofmt -l . && go vet ./... && go test ./... -count=1` → tout vert.

Smoke binaire (hors ligne après cache impossible ici : utiliser des relevés) :
```bash
go build -trimpath -o bin/finador ./cmd/finador
export FINADOR_PASSWORD=demo
B="./bin/finador --db /tmp/demo-c.fin --no-keychain --offline"
$B init
$B account add "Livret"
$B cash set Livret 10000 --at 2026-01-01
$B cash set Livret 10400 --at 2026-04-01
$B cash set Livret 10900 --at 2026-06-01
$B chart --to 2026-06-09
$B perf --to 2026-06-09
rm -f /tmp/demo-c.fin* bin/finador
```
Expected: une courbe braille en escalier croissante, un tableau perf avec TWR positif
sur « origine », aucune erreur, aucun accès réseau.

- [ ] **Step 5: commit + tag**

```bash
git add internal/cli
git commit -m "feat(cli): chart - courbe braille de la valeur, brut ou net"
git tag phase-c
```


