# Finador phase H - v0.5 : onglet Assets avec sparklines

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Demande utilisateur : un onglet web « Assets » - la vue groupes→actifs
DÉPLOYÉE (pas de pliage), une ligne par actif sans retour à la ligne, trois
sparklines de valorisation de la position (1W, 1M, 1Y - « day » impossible avec des
clôtures quotidiennes, décision D13), et en bout de ligne le montant BRUT puis NET
détenu. Esthétique inchangée : tableau ledger dense du thème, sparklines SVG inline
aux couleurs du thème (vert si la fenêtre monte, garance si elle baisse, encre si
plat).

**Architecture:** `chart.Sparkline` (polyline nue, sans axes). La page `/assets`
réutilise le moteur existant : par actif, `ParseScope(id)` → `portfolio.Value`
(brut/impôt/net position) et UNE `portfolio.Series` sur 365 jours dont on tranche
les fenêtres 1W/1M/1Y. Sections = chemins de groupe COMPLETS (pas seulement le
segment de tête), triées par valeur décroissante, en-tête cliquable vers
`/group/...` avec sous-totaux. Le cash n'apparaît pas (pas un actif). Onglet
« Assets » dans la manchette (base.html - toutes les pages).

**Conventions inchangées** (TDD, gofmt/vet, anglais, zéro JS, pas de binaire,
tests sans réseau, RLock en lecture).

**Performance acceptée :** N actifs × (1 Value + 1 Series 365j) sous RLock -
échelle personnelle (<40 actifs, <quelques milliers de tx) : dizaines de ms.

---

### Task H1: chart - Sparkline

**Files:**
- Create: rien (ajout à `internal/chart/svg.go` ou nouveau `internal/chart/spark.go` - préférer `spark.go`)
- Test: `internal/chart/spark_test.go`

- [ ] **Step 1: tests qui échouent**

`internal/chart/spark_test.go`:

```go
package chart

import (
	"strings"
	"testing"
)

func TestSparkline(t *testing.T) {
	out := Sparkline(ramp(30), 90, 24, "#1e6e4e")
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 90 24"`,
		`<polyline`, `stroke="#1e6e4e"`, `fill="none"`, `</svg>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing in:\n%s", want, out)
		}
	}
	// pas d'axes, pas de texte, pas de NaN
	for _, bad := range []string{"<text", "<line", "NaN", "Inf"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s should not appear in a sparkline", bad)
		}
	}
}

func TestSparklineDegenerate(t *testing.T) {
	if out := Sparkline(nil, 90, 24, "#000"); out != "" {
		t.Errorf("empty input: %q", out)
	}
	if out := Sparkline(ramp(1), 90, 24, "#000"); out != "" {
		t.Errorf("single point: %q", out)
	}
	// série plate : rendue sans division par zéro
	flat := ramp(5)
	for i := range flat {
		flat[i].Value = 42
	}
	if out := Sparkline(flat, 90, 24, "#000"); !strings.Contains(out, "<polyline") {
		t.Errorf("flat series should render: %q", out)
	}
}

func TestSparklineDeterministic(t *testing.T) {
	if Sparkline(ramp(15), 90, 24, "#000") != Sparkline(ramp(15), 90, 24, "#000") {
		t.Error("non deterministic")
	}
}
```

- [ ] **Step 2: vérifier l'échec** - FAIL (Sparkline inconnu).

- [ ] **Step 3: implémenter**

`internal/chart/spark.go`:

```go
package chart

import (
	"fmt"
	"math"
	"strings"

	"finador/internal/perf"
)

// Sparkline renders a bare inline curve: one polyline, no axes, no labels.
// Returns "" when there is nothing to draw (fewer than 2 points).
func Sparkline(points []perf.Point, w, h int, color string) string {
	if len(points) < 2 {
		return ""
	}
	w, h = max(w, 20), max(h, 8)
	lo, hi := bounds(points)
	if hi == lo {
		hi = lo + 1 // série plate : trait horizontal au milieu
	}
	pad := 2.0
	x := func(i int) float64 {
		return pad + float64(i)/float64(len(points)-1)*(float64(w)-2*pad)
	}
	y := func(v float64) float64 {
		return pad + (hi-v)/(hi-lo)*(float64(h)-2*pad)
	}
	var pts strings.Builder
	for i, p := range points {
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		fmt.Fprintf(&pts, "%.1f,%.1f ", x(i), y(p.Value))
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" preserveAspectRatio="none">`+
			`<polyline points="%s" fill="none" stroke="%s" stroke-width="1.3"/></svg>`,
		w, h, w, h, strings.TrimSpace(pts.String()), color)
}
```

- [ ] **Step 4: vérifier** - `go test ./internal/chart/ -count=1 && go test ./... -count=1` vert ; gofmt/vet silencieux.

- [ ] **Step 5: commit** - `git add internal/chart && git commit -m "feat(chart): bare inline sparklines"`

---

### Task H2: web - page /assets

**Files:**
- Create: `internal/web/assets.go`, `internal/web/templates/assets.html`
- Modify: `internal/web/server.go` (route), `internal/web/templates/base.html` (nav),
  `internal/web/static/style.css` (table dense nowrap + cellules sparkline)
- Test: `internal/web/server_test.go` (ajout)

Couleur des sparklines : comparer premier et dernier point de la TRANCHE - montée
→ `#1e6e4e` (vert), baisse → `#a3332e` (garance), plat/indéterminé → `#1c1914`.
Fenêtres : 1W = les 8 derniers points (7 jours), 1M = 31 derniers, 1Y = toute la
série (365 j). Actifs à valeur nulle aujourd'hui : omis. Sections = chemin de
groupe COMPLET (ex. `equities/us/tech`), actif sans groupe → `(ungrouped)`,
en-tête cliquable vers `/group/<chemin>` (sauf `(ungrouped)`), sous-totaux brut et
net. Tri : sections par brut décroissant, lignes par brut décroissant.

- [ ] **Step 1: tests qui échouent**

Ajouter à `internal/web/server_test.go`:

```go
func TestAssetsPage(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/assets")
	if code != http.StatusOK {
		t.Fatalf("GET /assets = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{
		"equities/world",          // en-tête de section : chemin complet
		"/group/equities/world",   // cliquable
		"Amundi MSCI World",       // une ligne d'actif
		"/asset/cw8",              // nom cliquable
		"assets-table",            // table dense
		"GROSS", "NET", "1W", "1M", "1Y",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets: %q missing", want)
		}
	}
	// trois sparklines pour l'unique actif valorisé
	if got := strings.Count(body, "<polyline"); got != 3 {
		t.Errorf("polylines = %d, want 3", got)
	}
	// montants brut et net de la position (10×560 = 5600 ; base 5500 → gain 100
	// → tax 17.20 si gains:17.2% → net 5582.80)
	for _, want := range []string{"5,600.00", "5,582.80"} {
		if !strings.Contains(body, want) {
			t.Errorf("/assets amounts: %q missing", want)
		}
	}
	// l'onglet est dans la manchette de toutes les pages
	if _, home := get(t, srv, "/"); !strings.Contains(home, `href="/assets"`) {
		t.Error("nav link /assets missing on dashboard")
	}
}
```

NOTE fixture : `testServer` a un buy de 10 cw8 à 5500 avec deposit 5000 → cash
suivi ; la position vaut 10×560=5600 au dernier close (2026-06-05) **valorisée à
AUJOURD'HUI par report**. La base fiscale POSITION (coût moyen) est 5500 → gain
100 → impôt 17.20 → net 5582.80. Si les montants réels diffèrent (forward-fill,
arrondi), DÉRIVER la vérité à la main depuis la fixture avant d'ajuster quoi que
ce soit, et n'ajuster le test QUE si la dérivation montre l'attendu erroné -
expliquer dans le rapport.

- [ ] **Step 2: vérifier l'échec** - FAIL (404).

- [ ] **Step 3: implémenter**

Route : `mux.HandleFunc("GET /assets", s.assetsPage)`.

`internal/web/assets.go`:

```go
package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

const (
	sparkUp   = "#1e6e4e"
	sparkDown = "#a3332e"
	sparkFlat = "#1c1914"
)

type assetRow struct {
	Name, URL                 string
	Spark1W, Spark1M, Spark1Y template.HTML
	Gross, Net                float64
}

type assetSection struct {
	Group, URL string
	Gross, Net float64
	Rows       []assetRow
}

type assetsData struct {
	Today    domain.Date
	Ccy      domain.Currency
	Sections []assetSection
	Warnings []string
}

func (s *Server) assetsPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	ccy := displayCurrency(b)
	fx := market.Converter{FX: b.Market.FX}

	data := assetsData{Today: today, Ccy: ccy}
	bySection := map[string]*assetSection{}

	for _, asset := range b.Assets {
		scope, err := portfolio.ParseScope(b, string(asset.ID))
		if err != nil {
			continue
		}
		val, err := portfolio.Value(b, scope, today, ccy, fx)
		if err != nil || val.Gross == 0 {
			continue
		}
		res, err := portfolio.Series(b, scope, today.AddDays(-365), today, ccy, fx)
		if err != nil {
			continue
		}
		pts := res.PerfPoints(false)
		row := assetRow{
			Name:    asset.Name,
			URL:     "/asset/" + url.PathEscape(string(asset.ID)),
			Spark1W: spark(lastN(pts, 8)),
			Spark1M: spark(lastN(pts, 31)),
			Spark1Y: spark(pts),
			Gross:   val.Gross,
			Net:     val.Net,
		}
		g := asset.Group
		if g == "" {
			g = "(ungrouped)"
		}
		sec, ok := bySection[g]
		if !ok {
			sec = &assetSection{Group: g}
			if g != "(ungrouped)" {
				sec.URL = "/group/" + escapeGroup(strings.ToLower(g))
			}
			bySection[g] = sec
		}
		sec.Gross += val.Gross
		sec.Net += val.Net
		sec.Rows = append(sec.Rows, row)
		data.Warnings = append(data.Warnings, res.Warnings...)
	}

	for _, sec := range bySection {
		slices.SortStableFunc(sec.Rows, func(a, b assetRow) int {
			switch {
			case a.Gross > b.Gross:
				return -1
			case a.Gross < b.Gross:
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		data.Sections = append(data.Sections, *sec)
	}
	slices.SortStableFunc(data.Sections, func(a, b assetSection) int {
		switch {
		case a.Gross > b.Gross:
			return -1
		case a.Gross < b.Gross:
			return 1
		}
		return strings.Compare(a.Group, b.Group)
	})
	s.render(w, http.StatusOK, "assets.html", data)
}

// lastN keeps the trailing n points of a daily series.
func lastN(pts []perf.Point, n int) []perf.Point {
	if len(pts) <= n {
		return pts
	}
	return pts[len(pts)-n:]
}

// spark renders a window, colored by its own drift.
func spark(pts []perf.Point) template.HTML {
	if len(pts) < 2 {
		return ""
	}
	color := sparkFlat
	switch first, last := pts[0].Value, pts[len(pts)-1].Value; {
	case last > first:
		color = sparkUp
	case last < first:
		color = sparkDown
	}
	return template.HTML(chart.Sparkline(pts, 90, 24, color))
}
```

NOTE : `escapeGroup` vit dans tree.go (même package) - réutiliser. Les Warnings
sont dédupliqués par construction côté Series ; un même avertissement peut revenir
par actif - dédupliquer ici avec une map avant rendu (petit helper), ordre stable.

`internal/web/templates/assets.html`:

```html
{{define "assets.html"}}{{template "base" .}}{{end}}
{{define "title"}}assets - finador{{end}}
{{define "main"}}
<section>
  <h2>assets</h2>
  {{range .Warnings}}<p class="approx">≈ {{.}}</p>{{end}}
  <table class="assets-table">
    <tr><th>asset</th><th>1W</th><th>1M</th><th>1Y</th><th class="nombre">GROSS</th><th class="nombre">NET</th></tr>
    {{range .Sections}}
    <tr class="section-row">
      <td colspan="4">{{if .URL}}<a href="{{.URL}}">{{.Group}}</a>{{else}}{{.Group}}{{end}}</td>
      <td class="nombre">{{fmtMoney .Gross $.Ccy}}</td>
      <td class="nombre">{{fmtMoney .Net $.Ccy}}</td>
    </tr>
    {{range .Rows}}
    <tr>
      <td class="asset-name"><a href="{{.URL}}">{{.Name}}</a></td>
      <td class="sparkcell">{{.Spark1W}}</td>
      <td class="sparkcell">{{.Spark1M}}</td>
      <td class="sparkcell">{{.Spark1Y}}</td>
      <td class="nombre">{{fmtMoney .Gross $.Ccy}}</td>
      <td class="nombre">{{fmtMoney .Net $.Ccy}}</td>
    </tr>
    {{end}}
    {{end}}
  </table>
</section>
{{end}}
```

`base.html` : la nav devient `Overview · Assets · Transactions · Import`
(lien `<a href="/assets">Assets</a>` en 2e position).

`style.css` - ajouter :

```css
/* ---- assets : lignes denses, pas de retour à la ligne ---- */
.assets-table { table-layout: fixed; }
.assets-table td, .assets-table th { white-space: nowrap; overflow: hidden; }
.assets-table .asset-name { text-overflow: ellipsis; max-width: 0; width: 34%; }
.assets-table .sparkcell { width: 98px; padding: .15rem .25rem; }
.assets-table .sparkcell svg { display: block; }
.assets-table .nombre { width: 12%; }
.assets-table .section-row td {
  font-variant: small-caps; letter-spacing: .1em; font-size: .74rem;
  color: var(--encre-2); border-bottom: 1px solid var(--encre);
  padding-top: .8rem; background: none;
}
.assets-table tr:hover td { background: var(--papier-2); }
.assets-table .section-row:hover td { background: none; }
```

- [ ] **Step 4: vérifier** - `go test ./internal/web/ -count=1 -v -run TestAssetsPage`,
  puis `go test ./... -count=1` + `-race ./internal/web/`. Lancer le vrai serveur
  3 s sur un livre de démo et curl /assets : structure table + 3 sparklines par
  ligne + montants ; vérifier visuellement le nowrap (pas de <br>, une <tr> par
  actif).

- [ ] **Step 5: commit** - `git add internal/web && git commit -m "feat(web): assets tab - dense rows, 1W/1M/1Y sparklines, gross and net"`

---

### Task H3: finition v0.5

- [ ] README : 1 puce dans la section Web (« assets tab: every holding on one
  dense row with 1W/1M/1Y sparklines and gross/net amounts »). DECISIONS.md D13 :

```markdown
## D13 - Sparklines 1W/1M/1Y (pas de « day »)

**Contexte :** la demande était day/week/month, mais le cache ne contient que des
clôtures QUOTIDIENNES (interval=1d) : une sparkline « day » n'aurait qu'un point.
**Choix :** fenêtres 1W (8 points), 1M (31), 1Y (série complète), couleur selon la
dérive de la fenêtre. **Alternative si refusé :** récupérer de l'intraday Yahoo
(interval=15m, non caché, casse --offline) ou élargir le modèle de cache.
```

- [ ] Portillons : gofmt/vet/test + -race web. Smoke binaire : serve sur le demo.fin
  (lecture seule, --offline) et curl /assets - coller un extrait.
- [ ] commit `docs: D13 and README note - v0.5 complete` + `git tag phase-h`.
