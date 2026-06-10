# Finador phase D — application web : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `finador serve` — l'application web complète, zéro JavaScript : dashboard (valeur nette en manchette, courbe SVG, répartition, perfs par périodes), vues de portée, transactions (saisie/édition/suppression), import CSV, refresh. Single binary via go:embed.

**Architecture:** `internal/web` = `Server` (http.ServeMux Go 1.22, `sync.RWMutex` autour du `store.File`, save atomique après chaque mutation, redirect 303), templates `html/template` + CSS embarqués, rendu des courbes par `chart.SVG`, données par `portfolio.Value/Series/ParseScope` et `perf.Report`. La CLI gagne `serve`. Les handlers sont minces : parse → moteur → render.

**Tech Stack:** Go 1.26 stdlib (net/http, html/template, embed). Aucune ressource externe (pas de fonts distantes, pas de CDN — app de patrimoine privée).

## Direction artistique (issue du skill frontend-design — à respecter à la lettre)

**Concept : « le journal confidentiel de votre patrimoine »** — une feuille financière
à l'ancienne, composée comme un rapport annuel des années 70 : papier crème, encre
quasi-noire, manchette en petites capitales espacées, filets doubles, chiffres en
monospace tabulaire, pointillés de sommaire entre libellé et montant.

- **Palette (variables CSS)** : `--papier:#f7f3ea` (fond), `--papier-2:#efe9db` (panneaux),
  `--encre:#1c1914` (texte), `--encre-2:#5d564a` (secondaire), `--filet:#c9c0ad`
  (hairlines), `--vert:#1e6e4e` (gains), `--garance:#a3332e` (pertes),
  `--ocre:#9a6b1f` (avertissements ≈). PAS de blanc pur, PAS de violet, PAS d'ombres
  portées floues.
- **Typo** : titres et manchette en serif old-style **sans requête réseau** :
  `font-family:"Iowan Old Style","Palatino Linotype",Palatino,"URW Palladio L",Georgia,serif`.
  TOUS les chiffres en monospace tabulaire :
  `font-family:ui-monospace,"SF Mono","Cascadia Code",Menlo,Consolas,monospace` avec
  `font-variant-numeric:tabular-nums`. Étiquettes de sections en petites capitales
  espacées (`letter-spacing:.14em; text-transform:uppercase; font-size:.72rem`).
- **Manchette** : bandeau encre pleine page en haut (`background:var(--encre)`,
  texte papier) : « FINADOR » petites caps espacées à gauche, la date française du jour
  au centre (« mercredi 10 juin 2026 »), nav à droite (Patrimoine · Transactions ·
  Import) en petites caps soulignées au survol.
- **Le héros** : la valeur nette du patrimoine composée comme un titre de une —
  serif, `font-size:clamp(2.6rem,6vw,4.2rem)`, chiffres français (« 1 234 567,89 € »),
  surmontée d'un double filet (`border-top:3px double var(--encre)`) et du libellé
  petites caps « patrimoine net d'impôt latent », flanquée du brut et de l'impôt
  latent en petite ligne dessous, avec note de bas de page † quand TaxNote existe.
- **Typographie française des nombres** : fonctions de template dédiées — milliers
  séparés par espace fine insécable U+202F, virgule décimale, symbole € précédé
  d'espace insécable U+00A0 ; pourcentages « +2,00 % » (espace fine avant %) ;
  négatifs en `--garance`, positifs des deltas en `--vert`.
- **Tables ledger** : en-têtes petites caps avec filet bas 1px, lignes séparées par
  hairlines `--filet`, montants alignés à droite en tabular-nums, survol
  `background:var(--papier-2)`. Répartition par groupe : pointillés de sommaire entre
  libellé et montant (`.leader{border-bottom:2px dotted var(--filet)}` en flex), plus
  une barre de part fine (div pleine `--encre` de width:N%).
- **Courbe** : le SVG de chart.SVG encadré d'un simple filet (`border:1px solid
  var(--filet); background:#fffdf6`), légende « brut » encre / « net » vert.
  Couleurs passées au renderer : brut `#1c1914`, net `#1e6e4e`.
- **Formulaires** : fieldsets « bordereaux » — `border:1px solid var(--encre)`,
  legend petites caps ; inputs monospace sur fond `#fffdf6`, focus
  `outline:2px solid var(--encre)` ; bouton submit « tampon » : bloc encre, texte
  papier petites caps espacées, hover inversé (papier/encre, filet encre).
- **Motion (CSS uniquement, sobre)** : à l'arrivée, sections en
  `animation:apparait .5s ease both` avec `animation-delay` étagé (0/.06/.12s…) —
  fondu + translation 6px ; respect de `prefers-reduced-motion:reduce` (désactivée).
- **Avertissements** : lignes « ≈ … » en `--ocre`, italique, petites tailles ;
  flash de succès (import) en bandeau `--vert` pâle avec filet vert.

**Conventions phase D** (héritées A/B/C) : TDD strict (httptest), gofmt/vet silencieux,
messages français, commits exacts, pas de binaire committé, tests sans réseau (Source
factice injectée). Sécurité : bind 127.0.0.1 par défaut, avertissement explicite sinon ;
tout POST mutateur suivi de `Save()` sous verrou puis redirect 303 ; `html/template`
échappe tout par défaut (ne JAMAIS utiliser template.HTML sur une donnée utilisateur —
seule exception : le SVG produit par chart.SVG, généré par nous et étiqueté échappé).

---

### Task D1: web — squelette (Server, rendu, base + CSS, dashboard minimal, serve)

**Files:**
- Create: `internal/web/server.go`, `internal/web/render.go`,
  `internal/web/templates/base.html`, `internal/web/templates/dashboard.html`,
  `internal/web/templates/error.html`, `internal/web/static/style.css`,
  `internal/cli/serve.go`
- Modify: `internal/cli/cli.go` (AddCommand += serveCmd)
- Test: `internal/web/server_test.go`, `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

`internal/web/server_test.go`:

```go
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/store"
)

// fakeSource : données déterministes, jamais de réseau dans les tests web.
type fakeSource struct{}

func (fakeSource) Resolve(context.Context, string) (market.SymbolInfo, error) {
	return market.SymbolInfo{}, domain.ErrNotFound
}

func (fakeSource) Daily(_ context.Context, sym string, _ domain.Date) (market.DailyData, error) {
	day := func(s string) domain.Date { d, _ := domain.ParseDate(s); return d }
	if sym == "CW8.PA" {
		return market.DailyData{Currency: domain.EUR, Closes: []domain.PricePoint{
			{Date: day("2026-06-01"), Close: 550}, {Date: day("2026-06-05"), Close: 560},
		}}, nil
	}
	return market.DailyData{}, domain.ErrNotFound
}

func day(t *testing.T, s string) domain.Date {
	t.Helper()
	d, err := domain.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// testServer construit un store réel en /tmp avec un livre représentatif.
func testServer(t *testing.T) (*Server, *store.File) {
	t.Helper()
	f, err := store.Create(filepath.Join(t.TempDir(), "web.fin"), "test")
	if err != nil {
		t.Fatal(err)
	}
	b := f.Book
	if err := b.AddAccount(&domain.Account{ID: "pea", Name: "PEA Zephyr", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	pea, _ := b.Account("pea")
	pea.Tax, _ = domain.ParseTaxRule("gains:17.2%")
	if err := b.AddAsset(&domain.Asset{ID: "cw8", Kind: domain.Security, Name: "Amundi MSCI World",
		Ticker: "CW8.PA", Currency: domain.EUR, Group: "actions/monde"}); err != nil {
		t.Fatal(err)
	}
	b.Add(domain.Transaction{Date: day(t, "2026-01-10"), Account: "pea", Kind: domain.Deposit,
		Amount: domain.Money{Amount: dec("5000"), Currency: domain.EUR}})
	b.Add(domain.Transaction{Date: day(t, "2026-06-01"), Account: "pea", Asset: "cw8", Kind: domain.Buy,
		Quantity: dec("10"), Amount: domain.Money{Amount: dec("5500"), Currency: domain.EUR}})
	b.Market.Price("cw8").Merge([]domain.PricePoint{
		{Date: day(t, "2026-06-01"), Close: 550}, {Date: day(t, "2026-06-05"), Close: 560},
	})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f, fakeSource{}, true) // offline : pas de refresh auto en test
	return srv, f
}

func get(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestDashboard(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d\n%s", code, body)
	}
	for _, want := range []string{
		"FINADOR",                       // manchette
		"patrimoine",                    // libellé héros
		"5", "100",                      // 5 100,00 € (formatage français, espaces fines)
		"€",                             // devise suffixée
		"style.css",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q manquant", want)
		}
	}
	// le formatage français exact : espace fine U+202F entre milliers, virgule
	if !strings.Contains(body, "5 100,00") {
		t.Errorf("montant français manquant (5\\u202f100,00):\n%s", excerpt(body))
	}
}

func TestStyleSheet(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/style.css")
	if code != http.StatusOK || !strings.Contains(body, "--papier") || !strings.Contains(body, "--encre") {
		t.Fatalf("style.css = %d, palette absente", code)
	}
}

func TestNotFound(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/nimporte/quoi")
	if code != http.StatusNotFound || !strings.Contains(body, "introuvable") {
		t.Fatalf("404 = %d\n%s", code, body)
	}
}

func excerpt(s string) string {
	if len(s) > 800 {
		return s[:800]
	}
	return s
}
```

Ajouter à `internal/cli/cli_test.go` :

```go
func TestServeRefusesOfflineBindWarning(t *testing.T) {
	db := newDB(t)
	// pas de listen réel : on vérifie seulement la validation des flags
	if _, err := tryRun(t, db, "serve", "--addr", "pas-une-adresse"); err == nil {
		t.Fatal("adresse invalide acceptée")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/web/ ./internal/cli/` → FAIL — undefined: NewServer / unknown command "serve".

- [ ] **Step 3: implémenter**

`internal/web/server.go`:

```go
// Package web is the zero-JavaScript façade of finador: server-rendered
// html/template pages over the same portfolio engine as the CLI, all assets
// embedded. The encrypted file is shared behind a RWMutex; every mutation
// saves atomically then redirects (303).
package web

import (
	"net/http"
	"sync"

	"finador/internal/market"
	"finador/internal/store"
)

type Server struct {
	mu      sync.RWMutex
	file    *store.File
	source  market.Source
	offline bool
}

func NewServer(f *store.File, src market.Source, offline bool) *Server {
	return &Server{file: f, source: src, offline: offline}
}

// Handler routes the five views. Mutating routes are POST-only.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /style.css", s.stylesheet)
	mux.HandleFunc("GET /", s.notFound)
	return mux
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, http.StatusNotFound, "page introuvable : "+r.URL.Path)
}
```

`internal/web/render.go`:

```go
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"finador/internal/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/style.css
var styleCSS []byte

var funcs = template.FuncMap{
	"frMoney":  frMoney,
	"frPct":    frPct,
	"frDate":   frDate,
	"frDelta":  frDelta,
	"signe":    signe,
}

var templates = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))

// render executes a page template inside base.html.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, page, data); err != nil {
		fmt.Fprintf(w, "<!-- erreur de rendu: %v -->", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	s.render(w, status, "error.html", map[string]any{"Message": msg, "Title": "erreur"})
}

func (s *Server) stylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(styleCSS)
}

// frMoney typesets an amount the French way: thin no-break thousands,
// decimal comma, currency symbol after a no-break space.
func frMoney(v float64, ccy domain.Currency) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64(v*100+0.5) % 100
	digits := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s,%02d %s", b.String(), cents, symbol(ccy))
	if neg {
		return "−" + out
	}
	return out
}

func symbol(c domain.Currency) string {
	switch c {
	case domain.EUR:
		return "€"
	case domain.USD:
		return "$"
	case "GBP":
		return "£"
	}
	return string(c)
}

// frPct: « +2,00 % » (espace fine avant %).
func frPct(x float64) string {
	s := fmt.Sprintf("%+.2f", x*100)
	return strings.ReplaceAll(s, ".", ",") + " %"
}

func signe(x float64) string {
	switch {
	case x > 0:
		return "pos"
	case x < 0:
		return "neg"
	}
	return ""
}

func frDelta(x float64) string { return frPct(x) }

var frMonths = [...]string{"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre"}
var frDays = [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}

// frDate: « mercredi 10 juin 2026 ».
func frDate(d domain.Date) string {
	t := d.Time()
	return fmt.Sprintf("%s %d %s %d", frDays[int(t.Weekday())], d.Day, frMonths[int(d.Month)-1], d.Year)
}

var _ = time.Now // (gardé si besoin d'horodatage ultérieur)
```

`internal/web/templates/base.html` — le squelette commun (header défini une fois,
chaque page définit `title` et `main`) :

```html
{{define "base"}}<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{block "title" .}}finador{{end}}</title>
<link rel="stylesheet" href="/style.css">
</head>
<body>
<header class="manchette">
  <a class="marque" href="/">Finador</a>
  <span class="date-du-jour">{{frDate .Aujourdhui}}</span>
  <nav>
    <a href="/">Patrimoine</a>
    <a href="/tx">Transactions</a>
    <a href="/import">Import</a>
  </nav>
</header>
<main>
{{block "main" .}}{{end}}
</main>
<footer class="pied">finador — vos données restent dans votre fichier chiffré.</footer>
</body>
</html>{{end}}
```

`internal/web/templates/dashboard.html` (version D1 minimale — la courbe, la
répartition et les perfs arrivent en D2) :

```html
{{define "dashboard.html"}}{{template "base" .}}{{end}}
{{define "title"}}patrimoine — finador{{end}}
{{define "main"}}
<section class="heros">
  <p class="libelle">patrimoine net d'impôt latent</p>
  <p class="chiffre-une {{signe .Val.Net}}">{{frMoney .Val.Net .Val.Currency}}</p>
  <p class="sous-titre">
    brut {{frMoney .Val.Gross .Val.Currency}} · impôt latent {{frMoney .Val.Tax .Val.Currency}}{{if .Val.TaxNote}}<sup>†</sup>{{end}}
  </p>
  {{if .Val.TaxNote}}<p class="note-bas">† {{.Val.TaxNote}}</p>{{end}}
  {{range .Val.Stale}}<p class="approx">≈ {{.}}</p>{{end}}
</section>
{{end}}
```

NOTE base/page : avec `ParseFS`, tous les fichiers partagent un espace de noms ;
`base.html` ne définit que "base" et des blocks ; CHAQUE page redéfinit "title" et
"main". html/template écrase les blocks au fil du parse — pour que chaque page ait
SES blocks, parser chaque page dans son PROPRE clone : remplacer la variable
`templates` par une map construite dans un init :

```go
var pages = map[string]*template.Template{}

func init() {
	baseSrc := template.Must(template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/base.html"))
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "base.html" {
			continue
		}
		clone := template.Must(baseSrc.Clone())
		pages[name] = template.Must(clone.ParseFS(templateFS, "templates/"+name))
	}
}
```

et `render` exécute `pages[page].ExecuteTemplate(w, "base", data)`. Adapter le code
ci-dessus en conséquence (c'est la manière idiomatique de faire de l'héritage de
templates en Go).

`internal/web/handlers.go` (créer aussi, pour le dashboard D1) :

```go
package web

import (
	"net/http"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

// pageData carries what base.html needs plus the page payload.
type pageData struct {
	Aujourdhui domain.Date
	Val        portfolio.Valuation
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	scope, err := portfolio.ParseScope(b, "")
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	today := domain.Today()
	val, err := portfolio.Value(b, scope, today, displayCurrency(b), market.Converter{FX: b.Market.FX})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, http.StatusOK, "dashboard.html", pageData{Aujourdhui: today, Val: val})
}

// displayCurrency mirrors the CLI rule: config "currency" else EUR.
func displayCurrency(b *domain.Book) domain.Currency {
	if c, err := domain.ParseCurrency(b.Config["currency"]); err == nil {
		return c
	}
	return domain.EUR
}
```

`internal/web/static/style.css` — LE design (complet, ~260 lignes) :

```css
/* finador — le journal confidentiel de votre patrimoine.
   Papier crème, encre, filets : une feuille financière à l'ancienne. */
:root {
  --papier: #f7f3ea;
  --papier-2: #efe9db;
  --encre: #1c1914;
  --encre-2: #5d564a;
  --filet: #c9c0ad;
  --vert: #1e6e4e;
  --garance: #a3332e;
  --ocre: #9a6b1f;
  --serif: "Iowan Old Style", "Palatino Linotype", Palatino, "URW Palladio L", Georgia, serif;
  --mono: ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace;
}
* { box-sizing: border-box; }
html { background: var(--papier); }
body {
  margin: 0 auto; max-width: 64rem; padding: 0 1.25rem 4rem;
  color: var(--encre); font-family: var(--serif); line-height: 1.45;
  font-feature-settings: "onum" 1;
}

/* ---- manchette ---- */
.manchette {
  display: flex; align-items: baseline; gap: 1.5rem;
  background: var(--encre); color: var(--papier);
  margin: 0 -1.25rem; padding: .8rem 1.5rem;
}
.marque {
  color: inherit; text-decoration: none; font-variant: small-caps;
  letter-spacing: .22em; font-size: 1.3rem;
}
.date-du-jour { flex: 1; text-align: center; font-style: italic; font-size: .85rem; color: #cfc8b8; }
.manchette nav { display: flex; gap: 1.1rem; }
.manchette nav a {
  color: inherit; text-decoration: none; font-variant: small-caps;
  letter-spacing: .14em; font-size: .85rem;
}
.manchette nav a:hover { text-decoration: underline; text-underline-offset: .3em; }

/* ---- sections, apparitions ---- */
main > section { margin-top: 2.2rem; animation: apparait .5s ease both; }
main > section:nth-child(2) { animation-delay: .06s; }
main > section:nth-child(3) { animation-delay: .12s; }
main > section:nth-child(4) { animation-delay: .18s; }
@keyframes apparait { from { opacity: 0; transform: translateY(6px); } }
@media (prefers-reduced-motion: reduce) { main > section { animation: none; } }

h2, .libelle {
  font-variant: small-caps; letter-spacing: .14em; text-transform: lowercase;
  font-size: .78rem; color: var(--encre-2); font-weight: 600; margin: 0 0 .4rem;
}
section > h2 { border-bottom: 1px solid var(--filet); padding-bottom: .35rem; }

/* ---- héros ---- */
.heros { border-top: 3px double var(--encre); padding-top: 1rem; }
.chiffre-une {
  font-family: var(--serif); font-size: clamp(2.6rem, 6vw, 4.2rem);
  margin: .1em 0; letter-spacing: -.01em; font-variant-numeric: oldstyle-nums;
}
.sous-titre { font-family: var(--mono); font-size: .85rem; color: var(--encre-2); margin: 0; }
.note-bas { font-size: .75rem; font-style: italic; color: var(--encre-2); }
.approx { color: var(--ocre); font-style: italic; font-size: .8rem; margin: .15rem 0; }
.pos { color: var(--encre); }
.neg { color: var(--garance); }
.delta.pos { color: var(--vert); }
.delta.neg { color: var(--garance); }

/* ---- tables ledger ---- */
table { width: 100%; border-collapse: collapse; font-size: .9rem; }
th {
  text-align: left; font-variant: small-caps; letter-spacing: .12em;
  font-weight: 600; font-size: .72rem; color: var(--encre-2);
  border-bottom: 1px solid var(--encre); padding: .3rem .5rem;
}
td { padding: .42rem .5rem; border-bottom: 1px solid var(--filet); }
tr:hover td { background: var(--papier-2); }
td.nombre, th.nombre { text-align: right; font-family: var(--mono); font-variant-numeric: tabular-nums; }
td.actions { text-align: right; }

/* ---- répartition : pointillés de sommaire + barres ---- */
.repartition { list-style: none; margin: 0; padding: 0; }
.repartition li { display: flex; align-items: baseline; gap: .6rem; padding: .34rem 0; }
.repartition .leader { flex: 1; border-bottom: 2px dotted var(--filet); transform: translateY(-4px); }
.repartition .montant { font-family: var(--mono); font-variant-numeric: tabular-nums; }
.part { height: 3px; background: var(--encre); margin-top: .2rem; }

/* ---- courbe ---- */
figure.courbe { margin: 0; border: 1px solid var(--filet); background: #fffdf6; padding: .6rem; }
figure.courbe svg { display: block; width: 100%; height: auto; }

/* ---- grille du dashboard ---- */
.deux-colonnes { display: grid; grid-template-columns: 2fr 1fr; gap: 2rem; align-items: start; }
@media (max-width: 50rem) { .deux-colonnes { grid-template-columns: 1fr; } }

/* ---- formulaires : bordereaux ---- */
fieldset {
  border: 1px solid var(--encre); background: var(--papier-2);
  padding: 1rem 1.2rem; margin: 0 0 1.5rem;
}
legend {
  font-variant: small-caps; letter-spacing: .14em; font-size: .78rem;
  padding: 0 .5rem; background: var(--papier); border: 1px solid var(--encre);
}
label { display: block; font-size: .75rem; color: var(--encre-2); margin: .6rem 0 .15rem; font-variant: small-caps; letter-spacing: .1em; }
input, select, textarea {
  font-family: var(--mono); font-size: .9rem; color: var(--encre);
  background: #fffdf6; border: 1px solid var(--filet); padding: .35rem .5rem; width: 100%;
}
input:focus, select:focus { outline: 2px solid var(--encre); outline-offset: 1px; }
.champs { display: grid; grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr)); gap: .8rem; }
button {
  font-family: var(--serif); font-variant: small-caps; letter-spacing: .18em;
  background: var(--encre); color: var(--papier); border: 1px solid var(--encre);
  padding: .5rem 1.4rem; cursor: pointer; margin-top: 1rem; font-size: .9rem;
}
button:hover { background: var(--papier); color: var(--encre); }
button.lien { background: none; border: none; color: var(--garance); padding: 0; margin: 0; letter-spacing: 0; font-size: .8rem; text-decoration: underline; }

/* ---- flash, pied ---- */
.flash { border: 1px solid var(--vert); color: var(--vert); background: #eaf2ec; padding: .5rem .8rem; font-size: .85rem; }
.flash.erreur { border-color: var(--garance); color: var(--garance); background: #f6eae9; }
.pied { margin-top: 4rem; border-top: 1px solid var(--filet); padding-top: .6rem; font-size: .72rem; color: var(--encre-2); font-style: italic; }
.fil-ariane { font-size: .8rem; color: var(--encre-2); }
.fil-ariane a { color: inherit; }
```

`internal/web/templates/error.html`:

```html
{{define "error.html"}}{{template "base" .}}{{end}}
{{define "title"}}erreur — finador{{end}}
{{define "main"}}
<section class="heros">
  <p class="libelle">erreur</p>
  <p class="chiffre-une neg" style="font-size:2rem">{{.Message}}</p>
  <p><a href="/">← retour au patrimoine</a></p>
</section>
{{end}}
```

NOTE : error.html passe par `render` qui attend les champs de pageData — donner à
renderError un struct dédié comprenant `Aujourdhui` (frDate en a besoin dans base) :
`map[string]any{"Message": msg, "Aujourdhui": domain.Today()}` et dans base.html
utiliser `{{frDate .Aujourdhui}}` — avec une map ça fonctionne aussi. Vérifier que
TOUTES les pages passent un champ Aujourdhui.

`internal/cli/serve.go`:

```go
package cli

import (
	"fmt"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"finador/internal/web"
)

func serveCmd(a *app) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Sert l'application web (mot de passe demandé au lancement)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("adresse %q invalide: %w", addr, err)
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"ATTENTION : %s expose votre patrimoine au-delà de cette machine (aucune authentification web)\n", addr)
			}
			srv := web.NewServer(f, a.marketSource(), a.offline)
			fmt.Fprintf(cmd.OutOrStdout(), "finador sur http://%s\n", addr)
			return http.ListenAndServe(addr, srv.Handler())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8451", "adresse d'écoute")
	return cmd
}
```

Et dans cli.go : AddCommand += `serveCmd(a)`.

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/web/ ./internal/cli/ && go test ./...` → PASS. gofmt/vet
silencieux. Lancer une fois le vrai serveur ~3 s pour vérifier le rendu :
`FINADOR_PASSWORD=demo go run ./cmd/finador --db /tmp/d1.fin --no-keychain --offline init && FINADOR_PASSWORD=demo timeout 3 go run ./cmd/finador --db /tmp/d1.fin --no-keychain --offline serve || true; rm -f /tmp/d1.fin*`

- [ ] **Step 5: commit**

```bash
git add internal/web internal/cli
git commit -m "feat(web): squelette — serveur, rendu, design « journal du patrimoine », serve"
```

---

### Task D2: web — dashboard complet (courbe, répartition, perfs)

**Files:**
- Modify: `internal/web/handlers.go`, `internal/web/templates/dashboard.html`
- Test: `internal/web/server_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

Ajouter à `internal/web/server_test.go`:

```go
func TestDashboardComplete(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		"<svg",           // courbe inline
		"répartition",    // section
		"actions",        // groupe de tête (lié vers /group/actions)
		"/group/actions", // lien de portée
		"liquidités",
		"performance",    // section perfs
		"origine",        // ligne du tableau de périodes
		"TWR",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard: %q manquant", want)
		}
	}
	// les courbes portent les couleurs du thème
	if !strings.Contains(body, "#1c1914") || !strings.Contains(body, "#1e6e4e") {
		t.Error("couleurs de courbe hors thème")
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/web/ -run TestDashboardComplete` → FAIL.

- [ ] **Step 3: implémenter**

`internal/web/handlers.go` — le dashboard devient complet :

```go
package web

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

const (
	couleurEncre = "#1c1914"
	couleurVert  = "#1e6e4e"
)

type part struct {
	Label   string
	URL     string // vide : pas de lien (liquidités, sans groupe)
	Amount  float64
	Percent int
}

type dashData struct {
	Aujourdhui domain.Date
	Val        portfolio.Valuation
	Curve      template.HTML // SVG généré par chart.SVG — jamais de donnée brute utilisateur
	Rows       []perf.Row
	Met        perf.Metrics
	Parts      []part
	Warnings   []string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	scope, _ := portfolio.ParseScope(b, "")
	fx := market.Converter{FX: b.Market.FX}
	ccy := displayCurrency(b)

	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := dashData{Aujourdhui: today, Val: val}

	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "brut", Color: couleurEncre, Points: res.PerfPoints(false)},
			{Label: "net", Color: couleurVert, Points: res.PerfPoints(true)},
		}, 860, 300))
		data.Rows, data.Met = perf.Report(res.PerfPoints(false), res.PerfFlows(), today, riskFreeWeb(b))
		data.Warnings = res.Warnings
	}

	for _, l := range val.Lines {
		p := part{Label: l.Label, Amount: l.Gross}
		if val.Gross > 0 && l.Gross > 0 {
			p.Percent = int(l.Gross/val.Gross*100 + 0.5)
		}
		if l.Label != "liquidités" && l.Label != "(sans groupe)" {
			p.URL = "/group/" + url.PathEscape(l.Label)
		}
		data.Parts = append(data.Parts, p)
	}
	s.render(w, http.StatusOK, "dashboard.html", data)
}

// riskFreeWeb mirrors the CLI rule (config "risk-free", "2.4%").
func riskFreeWeb(b *domain.Book) float64 {
	s := strings.TrimSuffix(strings.TrimSpace(b.Config["risk-free"]), "%")
	if s == "" {
		return 0
	}
	var v float64
	if _, err := fmtSscan(s, &v); err != nil {
		return 0
	}
	return v / 100
}
```

NOTE : `fmtSscan` n'existe pas — utiliser `strconv.ParseFloat(s, 64)` comme dans
cli/perf.go (importer strconv, gérer l'erreur en retournant 0). Mieux : NE PAS
dupliquer — la logique existe déjà dans cli/perf.go (riskFree) ; comme cli ne peut
pas être importé par web, déplacer `riskFree` dans `internal/perf` en tant que
`perf.RiskFreeFromConfig(map[string]string) float64` avec un petit test, et faire
pointer cli/perf.go ET web dessus. Faire ce déplacement proprement (supprimer la
copie de cli).

`internal/web/templates/dashboard.html` (complet) :

```html
{{define "dashboard.html"}}{{template "base" .}}{{end}}
{{define "title"}}patrimoine — finador{{end}}
{{define "main"}}
<section class="heros">
  <p class="libelle">patrimoine net d'impôt latent</p>
  <p class="chiffre-une">{{frMoney .Val.Net .Val.Currency}}</p>
  <p class="sous-titre">
    brut {{frMoney .Val.Gross .Val.Currency}} · impôt latent {{frMoney .Val.Tax .Val.Currency}}{{if .Val.TaxNote}}<sup>†</sup>{{end}}
  </p>
  {{if .Val.TaxNote}}<p class="note-bas">† {{.Val.TaxNote}}</p>{{end}}
  {{range .Val.Stale}}<p class="approx">≈ {{.}}</p>{{end}}
  {{range .Warnings}}<p class="approx">≈ {{.}}</p>{{end}}
</section>

<section class="deux-colonnes">
  <div>
    <h2>évolution</h2>
    {{if .Curve}}<figure class="courbe">{{.Curve}}</figure>{{else}}<p class="approx">pas encore assez d'historique pour tracer une courbe.</p>{{end}}
  </div>
  <div>
    <h2>répartition</h2>
    <ul class="repartition">
      {{range .Parts}}
      <li>
        <span>{{if .URL}}<a href="{{.URL}}">{{.Label}}</a>{{else}}{{.Label}}{{end}}</span>
        <span class="leader"></span>
        <span class="montant">{{frMoney .Amount $.Val.Currency}}</span>
      </li>
      <div class="part" style="width: {{.Percent}}%"></div>
      {{end}}
    </ul>
  </div>
</section>

<section>
  <h2>performance</h2>
  {{if .Rows}}
  <table>
    <tr><th>période</th><th class="nombre">TWR</th><th class="nombre">XIRR</th></tr>
    {{range .Rows}}
    <tr>
      <td>{{.Name}}</td>
      <td class="nombre delta {{if .HasTWR}}{{signe .TWR}}{{end}}">{{if .HasTWR}}{{frPct .TWR}}{{else}}—{{end}}</td>
      <td class="nombre delta {{if .HasXIRR}}{{signe .XIRR}}{{end}}">{{if .HasXIRR}}{{frPct .XIRR}}{{else}}—{{end}}</td>
    </tr>
    {{end}}
  </table>
  <p class="sous-titre">
    CAGR {{frPct .Met.CAGR}} · vol {{frPct .Met.Vol}} · Sharpe {{printf "%.2f" .Met.Sharpe}} · Sortino {{printf "%.2f" .Met.Sortino}}
    {{if lt .Met.Drawdown.Depth 0.0}} · max drawdown {{frPct .Met.Drawdown.Depth}}{{end}}
  </p>
  {{else}}<p class="approx">pas encore assez d'historique.</p>{{end}}
</section>
{{end}}
```

NOTE template : `<div class="part">` à l'intérieur d'un `<ul>` n'est pas valide —
mettre la barre DANS le `<li>` (wrapper le contenu) :

```html
      <li>
        <div style="width:100%">
          <div style="display:flex;align-items:baseline;gap:.6rem">
            <span>{{if .URL}}<a href="{{.URL}}">{{.Label}}</a>{{else}}{{.Label}}{{end}}</span>
            <span class="leader"></span>
            <span class="montant">{{frMoney .Amount $.Val.Currency}}</span>
          </div>
          <div class="part" style="width: {{.Percent}}%"></div>
        </div>
      </li>
```
et alléger `.repartition li` (le flex passe dans le div interne). Adapter le CSS si
nécessaire — le rendu doit rester : libellé, pointillés, montant, fine barre de part.
ATTENTION html/template : `style="width: {{.Percent}}%"` avec un int est accepté
(CSS context) — vérifier qu'aucun warning d'échappement n'apparaît dans le rendu.

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/web/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/web internal/perf internal/cli
git commit -m "feat(web): dashboard complet — courbe brut/net, répartition, perfs par périodes"
```

---

### Task D3: web — vues de portée (/group, /account, /asset)

**Files:**
- Create: `internal/web/scope.go`, `internal/web/templates/scope.html`
- Modify: `internal/web/server.go` (routes)
- Test: `internal/web/server_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

```go
func TestScopeViews(t *testing.T) {
	srv, _ := testServer(t)
	for path, want := range map[string][]string{
		"/group/actions":      {"actions", "<svg", "Amundi MSCI World", "performance"},
		"/group/actions/monde": {"actions/monde"},
		"/account/pea":        {"PEA Zephyr", "liquidités", "transactions récentes"},
		"/asset/cw8":          {"Amundi MSCI World", "PEA Zephyr"},
	} {
		code, body := get(t, srv, path)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", path, code)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: %q manquant", path, w)
			}
		}
	}
	// portée inconnue → 404 propre
	if code, body := get(t, srv, "/asset/inexistant"); code != http.StatusNotFound || !strings.Contains(body, "introuvable") {
		t.Errorf("scope inconnue = %d\n%s", code, excerpt(body))
	}
}
```

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/web/ -run TestScopeViews` → FAIL (404 partout).

- [ ] **Step 3: implémenter**

Routes dans server.go :

```go
	mux.HandleFunc("GET /group/{ref...}", s.scopePage)
	mux.HandleFunc("GET /account/{ref}", s.scopePage)
	mux.HandleFunc("GET /asset/{ref}", s.scopePage)
```

`internal/web/scope.go`:

```go
package web

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"cmp"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

type txRow struct {
	ID      domain.TxID
	Date    domain.Date
	Kind    string
	Account string
	Asset   string
	Qty     string
	Amount  string
	Note    string
}

type scopeData struct {
	Aujourdhui domain.Date
	Label      string
	Val        portfolio.Valuation
	Curve      template.HTML
	Rows       []perf.Row
	Met        perf.Metrics
	Warnings   []string
	Txs        []txRow
}

func (s *Server) scopePage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	today := domain.Today()
	ref := r.PathValue("ref")

	scope, err := portfolio.ParseScope(b, ref)
	if err != nil {
		status := http.StatusNotFound
		if !errors.Is(err, domain.ErrNotFound) {
			status = http.StatusBadRequest
		}
		s.renderError(w, status, "portée introuvable : "+ref)
		return
	}
	fx := market.Converter{FX: b.Market.FX}
	ccy := displayCurrency(b)
	val, err := portfolio.Value(b, scope, today, ccy, fx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := scopeData{Aujourdhui: today, Label: scope.Label, Val: val}
	if res, err := portfolio.Series(b, scope, domain.Date{}, today, ccy, fx); err == nil && len(res.Points) >= 2 {
		data.Curve = template.HTML(chart.SVG([]chart.Line{
			{Label: "brut", Color: couleurEncre, Points: res.PerfPoints(false)},
			{Label: "net", Color: couleurVert, Points: res.PerfPoints(true)},
		}, 860, 280))
		data.Rows, data.Met = perf.Report(res.PerfPoints(false), res.PerfFlows(), today, perf.RiskFreeFromConfig(b.Config))
		data.Warnings = res.Warnings
	}
	data.Txs = scopeTxs(b, scope, 15)
	s.render(w, http.StatusOK, "scope.html", data)
}

// scopeTxs lists the scope's most recent ledger lines, newest first.
func scopeTxs(b *domain.Book, scope portfolio.Scope, limit int) []txRow {
	txs := portfolio.Sorted(b)
	slices.Reverse(txs)
	var out []txRow
	for _, t := range txs {
		if len(out) == limit {
			break
		}
		if !txInScope(b, scope, t) {
			continue
		}
		row := txRow{ID: t.ID, Date: t.Date, Kind: t.Kind.String(),
			Account: accountName(b, t.Account), Qty: t.Quantity.String(),
			Amount: t.Amount.String(), Note: t.Note}
		if t.Asset != "" {
			if asset, err := b.Asset(string(t.Asset)); err == nil {
				row.Asset = asset.Name
			}
		}
		out = append(out, row)
	}
	return out
}

func txInScope(b *domain.Book, scope portfolio.Scope, t *domain.Transaction) bool {
	acc, err := b.Account(string(t.Account))
	if err != nil {
		return false
	}
	if t.Asset == "" {
		return scope.Kind == portfolio.All || (scope.Kind == portfolio.ByAccount && acc.ID == scope.Account.ID)
	}
	asset, err := b.Asset(string(t.Asset))
	if err != nil {
		return false
	}
	_ = cmp.Or("") // (supprimer si inutilisé)
	return scopeHasAsset(scope, acc, asset)
}

func accountName(b *domain.Book, id domain.AccountID) string {
	if acc, err := b.Account(string(id)); err == nil {
		return acc.Name
	}
	return string(id)
}
```

NOTE : `scope.hasAsset` est non exporté dans portfolio — EXPORTER proprement :
ajouter dans `internal/portfolio/scope.go` :

```go
// HasAsset reports whether the (account, asset) position belongs to the scope.
func (s Scope) HasAsset(acc *domain.Account, asset *domain.Asset) bool { return s.hasAsset(acc, asset) }

// HasCash reports whether the account's cash belongs to the scope.
func (s Scope) HasCash(acc *domain.Account) bool { return s.hasCash(acc) }
```

et utiliser `scope.HasAsset(...)` dans web (supprimer le hack `cmp.Or`). Les champs
`Scope.Kind/Account/Asset` sont déjà exportés.

`internal/web/templates/scope.html`:

```html
{{define "scope.html"}}{{template "base" .}}{{end}}
{{define "title"}}{{.Label}} — finador{{end}}
{{define "main"}}
<p class="fil-ariane"><a href="/">patrimoine</a> → {{.Label}}</p>
<section class="heros">
  <p class="libelle">{{.Label}} — net d'impôt latent</p>
  <p class="chiffre-une">{{frMoney .Val.Net .Val.Currency}}</p>
  <p class="sous-titre">brut {{frMoney .Val.Gross .Val.Currency}} · impôt latent {{frMoney .Val.Tax .Val.Currency}}</p>
  {{range .Val.Stale}}<p class="approx">≈ {{.}}</p>{{end}}
  {{range .Warnings}}<p class="approx">≈ {{.}}</p>{{end}}
</section>

<section>
  <h2>évolution</h2>
  {{if .Curve}}<figure class="courbe">{{.Curve}}</figure>{{else}}<p class="approx">pas encore assez d'historique.</p>{{end}}
</section>

<section>
  <h2>composition</h2>
  <table>
    <tr><th>ligne</th><th class="nombre">brut</th><th class="nombre">impôt</th><th class="nombre">net</th></tr>
    {{range .Val.Lines}}
    <tr><td>{{.Label}}</td><td class="nombre">{{frMoney .Gross $.Val.Currency}}</td>
        <td class="nombre">{{frMoney .Tax $.Val.Currency}}</td><td class="nombre">{{frMoney .Net $.Val.Currency}}</td></tr>
    {{end}}
  </table>
</section>

<section>
  <h2>performance</h2>
  {{if .Rows}}
  <table>
    <tr><th>période</th><th class="nombre">TWR</th><th class="nombre">XIRR</th></tr>
    {{range .Rows}}
    <tr><td>{{.Name}}</td>
      <td class="nombre delta {{if .HasTWR}}{{signe .TWR}}{{end}}">{{if .HasTWR}}{{frPct .TWR}}{{else}}—{{end}}</td>
      <td class="nombre delta {{if .HasXIRR}}{{signe .XIRR}}{{end}}">{{if .HasXIRR}}{{frPct .XIRR}}{{else}}—{{end}}</td></tr>
    {{end}}
  </table>
  <p class="sous-titre">CAGR {{frPct .Met.CAGR}} · vol {{frPct .Met.Vol}} · Sharpe {{printf "%.2f" .Met.Sharpe}} · Sortino {{printf "%.2f" .Met.Sortino}}</p>
  {{end}}
</section>

<section>
  <h2>transactions récentes</h2>
  <table>
    <tr><th>date</th><th>type</th><th>compte</th><th>actif</th><th class="nombre">qté</th><th class="nombre">montant</th></tr>
    {{range .Txs}}
    <tr><td class="nombre">{{.Date}}</td><td>{{.Kind}}</td><td>{{.Account}}</td><td>{{.Asset}}</td>
        <td class="nombre">{{.Qty}}</td><td class="nombre">{{.Amount}}</td></tr>
    {{end}}
  </table>
</section>
{{end}}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/web/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/web internal/portfolio
git commit -m "feat(web): vues de portée — groupe, enveloppe, actif"
```

---

### Task D4: web — transactions (liste, saisie, suppression)

**Files:**
- Create: `internal/web/tx.go`, `internal/web/templates/tx.html`
- Modify: `internal/web/server.go` (routes)
- Test: `internal/web/server_test.go` (ajout)

- [ ] **Step 1: tests qui échouent**

```go
func postForm(t *testing.T, srv *Server, path string, form url.Values) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header().Get("Location")
}

func TestTxListAndAdd(t *testing.T) {
	srv, f := testServer(t)
	code, body := get(t, srv, "/tx")
	if code != http.StatusOK || !strings.Contains(body, "buy") || !strings.Contains(body, "deposit") {
		t.Fatalf("GET /tx = %d\n%s", code, excerpt(body))
	}
	// le formulaire propose comptes et actifs
	for _, want := range []string{"PEA Zephyr", "Amundi MSCI World", "<form", `name="kind"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/tx: %q manquant", want)
		}
	}

	// saisie d'un achat → 303 puis visible, et persisté dans le fichier
	code, body, loc := postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"2"}, "amount": {"1115"}, "note": {"via web"},
	})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("POST /tx = %d → %q\n%s", code, loc, excerpt(body))
	}
	if _, body = get(t, srv, "/tx"); !strings.Contains(body, "via web") {
		t.Error("transaction ajoutée invisible")
	}
	if len(f.Book.Transactions) != 3 {
		t.Errorf("transactions = %d, attendu 3", len(f.Book.Transactions))
	}

	// saisie invalide → 400 avec message, rien d'écrit
	code, body, _ = postForm(t, srv, "/tx", url.Values{
		"date": {"2026-06-03"}, "kind": {"buy"}, "account": {"pea"}, "asset": {"cw8"},
		"qty": {"abc"}, "amount": {"10"},
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "quantité") {
		t.Fatalf("POST invalide = %d\n%s", code, excerpt(body))
	}
	if len(f.Book.Transactions) != 3 {
		t.Error("la saisie invalide a écrit quelque chose")
	}
}

func TestTxDelete(t *testing.T) {
	srv, f := testServer(t)
	id := f.Book.Transactions[0].ID
	code, _, loc := postForm(t, srv, fmt.Sprintf("/tx/%d/delete", id), url.Values{})
	if code != http.StatusSeeOther || loc != "/tx" {
		t.Fatalf("delete = %d → %q", code, loc)
	}
	if len(f.Book.Transactions) != 1 {
		t.Errorf("transactions = %d, attendu 1", len(f.Book.Transactions))
	}
	// id inconnu → 404
	if code, _, _ := postForm(t, srv, "/tx/999/delete", url.Values{}); code != http.StatusNotFound {
		t.Errorf("delete inconnu = %d", code)
	}
}
```

(imports du fichier de test : ajouter "fmt" et "net/url")

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/web/ -run TestTx` → FAIL (404).

- [ ] **Step 3: implémenter**

Routes :

```go
	mux.HandleFunc("GET /tx", s.txPage)
	mux.HandleFunc("POST /tx", s.txCreate)
	mux.HandleFunc("POST /tx/{id}/delete", s.txDelete)
```

`internal/web/tx.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/shopspring/decimal"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

type txPageData struct {
	Aujourdhui domain.Date
	Txs        []txRow
	Accounts   []*domain.Account
	Assets     []*domain.Asset
	Kinds      []string
	Erreur     string
	Flash      string
}

func (s *Server) txPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.renderTxPage(w, http.StatusOK, r.URL.Query().Get("flash"), "")
}

// renderTxPage est appelé verrou (R ou W) déjà pris.
func (s *Server) renderTxPage(w http.ResponseWriter, status int, flash, erreur string) {
	b := s.file.Book
	all, _ := portfolio.ParseScope(b, "")
	data := txPageData{
		Aujourdhui: domain.Today(),
		Txs:        scopeTxs(b, all, 200),
		Accounts:   b.Accounts,
		Assets:     b.Assets,
		Kinds:      []string{"buy", "sell", "deposit", "withdraw", "dividend", "fee", "statement"},
		Erreur:     erreur,
		Flash:      flash,
	}
	s.render(w, status, "tx.html", data)
}

func (s *Server) txCreate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.file.Book
	tx, err := parseTxForm(b, r)
	if err != nil {
		s.renderTxPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	b.Add(tx)
	if err := s.file.Save(); err != nil {
		s.renderTxPage(w, http.StatusInternalServerError, "", "sauvegarde impossible : "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}

func parseTxForm(b *domain.Book, r *http.Request) (domain.Transaction, error) {
	var zero domain.Transaction
	date, err := domain.ParseDate(r.FormValue("date"))
	if err != nil {
		return zero, err
	}
	kind, err := domain.ParseTxKind(r.FormValue("kind"))
	if err != nil {
		return zero, err
	}
	acc, err := b.Account(r.FormValue("account"))
	if err != nil {
		return zero, err
	}
	tx := domain.Transaction{Date: date, Account: acc.ID, Kind: kind, Note: r.FormValue("note")}

	ccy := acc.Currency
	if ref := r.FormValue("asset"); ref != "" {
		asset, err := b.Asset(ref)
		if err != nil {
			return zero, err
		}
		tx.Asset = asset.ID
		ccy = asset.Currency
	}
	if (kind == domain.Buy || kind == domain.Sell || kind == domain.Dividend) && tx.Asset == "" {
		return zero, fmt.Errorf("un %s demande un actif", kind)
	}
	if q := r.FormValue("qty"); q != "" {
		qty, err := decimal.NewFromString(q)
		if err != nil {
			return zero, fmt.Errorf("quantité %q invalide", q)
		}
		tx.Quantity = qty.Abs()
	}
	if (kind == domain.Buy || kind == domain.Sell) && tx.Quantity.IsZero() {
		return zero, fmt.Errorf("quantité requise pour un %s", kind)
	}
	amount, err := decimal.NewFromString(r.FormValue("amount"))
	if err != nil {
		return zero, fmt.Errorf("montant %q invalide", r.FormValue("amount"))
	}
	if c := r.FormValue("ccy"); c != "" {
		if ccy, err = domain.ParseCurrency(c); err != nil {
			return zero, err
		}
	}
	tx.Amount = domain.Money{Amount: amount.Abs(), Currency: ccy}
	return tx, nil
}

func (s *Server) txDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "identifiant invalide")
		return
	}
	if err := s.file.Book.RemoveTx(domain.TxID(id)); err != nil {
		s.renderError(w, http.StatusNotFound, "transaction introuvable")
		return
	}
	if err := s.file.Save(); err != nil {
		s.renderError(w, http.StatusInternalServerError, "sauvegarde impossible : "+err.Error())
		return
	}
	http.Redirect(w, r, "/tx", http.StatusSeeOther)
}

var _ = slices.Clone[[]int] // (supprimer si slices devient inutilisé)
```

NOTE : retirer les bricolages `var _ =` si l'import n'est pas nécessaire. scopeTxs
accepte déjà un scope All — élargir sa limite à 200 ici.

`internal/web/templates/tx.html`:

```html
{{define "tx.html"}}{{template "base" .}}{{end}}
{{define "title"}}transactions — finador{{end}}
{{define "main"}}
<section>
  <h2>nouvelle écriture</h2>
  {{if .Erreur}}<p class="flash erreur">{{.Erreur}}</p>{{end}}
  {{if .Flash}}<p class="flash">{{.Flash}}</p>{{end}}
  <form method="post" action="/tx">
    <fieldset>
      <legend>bordereau</legend>
      <div class="champs">
        <div><label for="date">date</label><input id="date" name="date" placeholder="AAAA-MM-JJ" value="{{.Aujourdhui}}"></div>
        <div><label for="kind">type</label>
          <select id="kind" name="kind">{{range .Kinds}}<option>{{.}}</option>{{end}}</select></div>
        <div><label for="account">compte</label>
          <select id="account" name="account">{{range .Accounts}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div>
        <div><label for="asset">actif (optionnel)</label>
          <select id="asset" name="asset"><option value=""></option>{{range .Assets}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div>
        <div><label for="qty">quantité</label><input id="qty" name="qty"></div>
        <div><label for="amount">montant total</label><input id="amount" name="amount"></div>
        <div><label for="ccy">devise (optionnel)</label><input id="ccy" name="ccy" placeholder="EUR"></div>
        <div><label for="note">note</label><input id="note" name="note"></div>
      </div>
      <button type="submit">Enregistrer</button>
    </fieldset>
  </form>
</section>

<section>
  <h2>ledger</h2>
  <table>
    <tr><th>id</th><th>date</th><th>type</th><th>compte</th><th>actif</th><th class="nombre">qté</th><th class="nombre">montant</th><th>note</th><th></th></tr>
    {{range .Txs}}
    <tr>
      <td class="nombre">{{.ID}}</td><td class="nombre">{{.Date}}</td><td>{{.Kind}}</td>
      <td>{{.Account}}</td><td>{{.Asset}}</td>
      <td class="nombre">{{.Qty}}</td><td class="nombre">{{.Amount}}</td><td>{{.Note}}</td>
      <td class="actions"><form method="post" action="/tx/{{.ID}}/delete"><button class="lien" type="submit">suppr.</button></form></td>
    </tr>
    {{end}}
  </table>
</section>
{{end}}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/web/ && go test ./...` → PASS. gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/web
git commit -m "feat(web): transactions — ledger, bordereau de saisie, suppression"
```

---

### Task D5: web — import CSV et refresh

**Files:**
- Create: `internal/web/import.go`, `internal/web/templates/import.html`,
  `internal/portfolio/import.go` (logique déplacée depuis cli), `internal/portfolio/import_test.go`
- Modify: `internal/cli/import.go` (délègue), `internal/web/server.go` (routes),
  `internal/web/templates/dashboard.html` (bouton refresh)
- Delete: `internal/cli/import_test.go` (tests déplacés)
- Test: `internal/web/server_test.go` (ajout)

REFACTOR PRÉALABLE (sans changement de comportement) : `importCSV`, `rowToTx`,
`hashTx`, `ensureAccount`, `ensureAsset` vivent dans internal/cli — le web en a
besoin. Les DÉPLACER verbatim dans `internal/portfolio/import.go` en exportant
`func ImportCSV(b *domain.Book, r io.Reader) (added, skipped int, err error)` (les
autres restent non exportés). Déplacer les tests white-box (import_test.go) dans
internal/portfolio en adaptant le nom de package. cli/import.go appelle
`portfolio.ImportCSV`. `currencyOr` est utilisé par rowToTx : la version cli reste,
copier la petite fonction dans portfolio/import.go (non exportée) — pas de cycle.

- [ ] **Step 1: tests qui échouent**

```go
func TestImportUpload(t *testing.T) {
	srv, f := testServer(t)
	code, body := get(t, srv, "/import")
	if code != http.StatusOK || !strings.Contains(body, "multipart/form-data") {
		t.Fatalf("GET /import = %d", code)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("fichier", "txs.csv")
	part.Write([]byte("date,kind,account,asset,quantity,price,amount,currency,group,note\n" +
		"2026-02-01,buy,PEA Zephyr,CW8.PA,3,540,,EUR,actions/monde,import web\n"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /import = %d\n%s", rec.Code, excerpt(rec.Body.String()))
	}
	if len(f.Book.Transactions) != 3 {
		t.Errorf("transactions = %d, attendu 3", len(f.Book.Transactions))
	}
	// le flash de résultat est visible après redirection
	code, body = get(t, srv, rec.Header().Get("Location"))
	if code != http.StatusOK || !strings.Contains(body, "1 importée") {
		t.Errorf("flash absent:\n%s", excerpt(body))
	}
}

func TestRefreshButtonOffline(t *testing.T) {
	srv, _ := testServer(t) // serveur en mode offline
	code, _, loc := postForm(t, srv, "/refresh", url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(loc, "hors+ligne") && !strings.Contains(loc, "hors%20ligne") {
		t.Fatalf("refresh offline = %d → %q", code, loc)
	}
}
```

(imports test : "bytes", "mime/multipart")

- [ ] **Step 2: vérifier l'échec**

Run: `go test ./internal/web/ -run 'TestImport|TestRefresh'` → FAIL.

- [ ] **Step 3: implémenter**

Routes :

```go
	mux.HandleFunc("GET /import", s.importPage)
	mux.HandleFunc("POST /import", s.importUpload)
	mux.HandleFunc("POST /refresh", s.refresh)
```

`internal/web/import.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"net/url"

	"finador/internal/market"
	"finador/internal/portfolio"
)

type importData struct {
	Aujourdhui domain.Date
	Flash      string
	Erreur     string
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.render(w, http.StatusOK, "import.html", importData{
		Aujourdhui: domain.Today(),
		Flash:      r.URL.Query().Get("flash"),
		Erreur:     r.URL.Query().Get("erreur"),
	})
}

func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := r.FormFile("fichier")
	if err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape("aucun fichier reçu"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	added, skipped, err := portfolio.ImportCSV(s.file.Book, file)
	if err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := s.file.Save(); err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape("sauvegarde impossible : "+err.Error()), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d importée(s), %d ignorée(s) (doublons)", added, skipped)
	http.Redirect(w, r, "/import?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline {
		http.Redirect(w, r, "/?erreur="+url.QueryEscape("hors ligne : refresh impossible"), http.StatusSeeOther)
		return
	}
	sum := market.Refresh(r.Context(), s.file.Book, s.source, true)
	if err := s.file.Save(); err != nil {
		http.Redirect(w, r, "/?erreur="+url.QueryEscape("sauvegarde impossible"), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d série(s) rafraîchie(s)", len(sum.Fetched))
	http.Redirect(w, r, "/?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}
```

(import domain manquant : l'ajouter ; le dashboard lit `flash`/`erreur` de la query
et les affiche — ajouter au dashData `Flash, Erreur string` remplis depuis
`r.URL.Query()`, et dans dashboard.html, au-dessus du héros :
`{{if .Flash}}<p class="flash">{{.Flash}}</p>{{end}}{{if .Erreur}}<p class="flash erreur">{{.Erreur}}</p>{{end}}` ;
ajouter aussi le bouton : `<form method="post" action="/refresh"><button type="submit">Rafraîchir les cours</button></form>`
dans la section évolution.)

`internal/web/templates/import.html`:

```html
{{define "import.html"}}{{template "base" .}}{{end}}
{{define "title"}}import — finador{{end}}
{{define "main"}}
<section>
  <h2>import CSV</h2>
  {{if .Flash}}<p class="flash">{{.Flash}}</p>{{end}}
  {{if .Erreur}}<p class="flash erreur">{{.Erreur}}</p>{{end}}
  <form method="post" action="/import" enctype="multipart/form-data">
    <fieldset>
      <legend>fichier de transactions</legend>
      <p class="note-bas">colonnes par en-tête, ordre libre : date, kind, account, asset,
      quantity, price, amount, currency, group, note. Le ré-import ignore les doublons.</p>
      <input type="file" name="fichier" accept=".csv,text/csv">
      <button type="submit">Importer</button>
    </fieldset>
  </form>
</section>
{{end}}
```

- [ ] **Step 4: vérifier le succès**

Run: `go test ./internal/web/ ./internal/cli/ ./internal/portfolio/ && go test ./...`
→ PASS (les tests d'import déplacés tournent dans portfolio, les tests cli
d'intégration TestImportCommand inchangés et verts). gofmt/vet silencieux.

- [ ] **Step 5: commit**

```bash
git add internal/web internal/cli internal/portfolio
git commit -m "feat(web): import CSV et refresh ; ImportCSV déplacé dans portfolio"
```

---

### Task D6: web — finition de phase (arrêt propre, smoke e2e, tag)

**Files:**
- Modify: `internal/cli/serve.go` (arrêt gracieux)
- Modify: `docs/superpowers/DECISIONS.md` (D9)

- [ ] **Step 1: arrêt gracieux**

serveCmd utilise un http.Server avec arrêt sur Ctrl-C :

```go
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("adresse %q invalide: %w", addr, err)
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"ATTENTION : %s expose votre patrimoine au-delà de cette machine (aucune authentification web)\n", addr)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			httpSrv := &http.Server{Addr: addr, Handler: web.NewServer(f, a.marketSource(), a.offline).Handler()}
			errc := make(chan error, 1)
			go func() { errc <- httpSrv.ListenAndServe() }()
			fmt.Fprintf(cmd.OutOrStdout(), "finador sur http://%s — Ctrl-C pour arrêter\n", addr)
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
				fmt.Fprintln(cmd.OutOrStdout(), "\narrêt…")
				shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return httpSrv.Shutdown(shCtx)
			}
		},
```
(imports : context, os, os/signal, syscall, time)

- [ ] **Step 2: portillons complets**

Run: `gofmt -l . && go vet ./... && go test ./... -count=1` → tout vert.

- [ ] **Step 3: smoke e2e contre le vrai serveur**

```bash
go build -trimpath -o bin/finador ./cmd/finador
export FINADOR_PASSWORD=demo
B="./bin/finador --db /tmp/demo-d.fin --no-keychain --offline"
$B init
$B account add "PEA Zephyr" --tax gains:17.2%
$B account add "Livret"
$B cash set Livret 12000 --at 2026-01-05
$B deposit "PEA Zephyr" 5000 2026-01-10
$B asset add "Maison à Achères" --kind property --group immo
$B asset set maison-a-acheres 450000 --at 2026-06-01 --account "PEA Zephyr" 2>/dev/null || \
  $B asset set maison-a-acheres 450000 --at 2026-06-01 --account Livret
./bin/finador --db /tmp/demo-d.fin --no-keychain --offline serve --addr 127.0.0.1:8459 &
SRV=$!; sleep 1
curl -fsS http://127.0.0.1:8459/ | grep -q "patrimoine" || echo "ÉCHEC dashboard"
curl -fsS http://127.0.0.1:8459/style.css | grep -q -- "--papier" || echo "ÉCHEC css"
curl -fsS http://127.0.0.1:8459/tx | grep -q "bordereau" || echo "ÉCHEC tx"
curl -fsS http://127.0.0.1:8459/import | grep -q "multipart" || echo "ÉCHEC import"
curl -fsS http://127.0.0.1:8459/group/immo | grep -q "immo" || echo "ÉCHEC scope"
curl -fsS -X POST -d "date=2026-06-05&kind=deposit&account=livret&amount=100" \
  http://127.0.0.1:8459/tx -o /dev/null -w "%{http_code}\n" | grep -q "303" || echo "ÉCHEC post tx"
kill $SRV; wait $SRV 2>/dev/null
rm -f /tmp/demo-d.fin* bin/finador
```
Expected: aucune ligne ÉCHEC, le serveur s'arrête proprement. Coller la sortie dans
le rapport.

- [ ] **Step 4: décision D9**

Ajouter à DECISIONS.md :

```markdown
## D9 — Web sans authentification, lié à 127.0.0.1

**Contexte :** la spec §8 prévoit un serveur local sans auth web (le déverrouillage
se fait au lancement, dans le terminal). **Choix :** bind par défaut 127.0.0.1:8451,
avertissement très visible pour tout autre bind, aucun cookie/session. Pas de verrou
inter-processus CLI/serve (D6 backlog) : dernière écriture gagnante, sauvegardes
atomiques — acceptable mono-utilisateur. **Alternative si refusé :** basic auth
optionnelle (--auth user:pass) ou socket Unix.
```

- [ ] **Step 5: commit + tag**

```bash
git add internal/cli docs/superpowers/DECISIONS.md
git commit -m "feat(web): arrêt gracieux de serve — la phase D est complète"
git tag phase-d
```



