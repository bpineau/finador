# Finador phase I - v0.6 : densité Assets + périodes de courbes

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Retours utilisateur : (1) onglet Assets - sparklines trop espacées qui
tronquent les noms, noms d'actifs à indenter légèrement (les distinguer des groupes),
sections 100 % immobilier en bas (suivre leur valorisation a peu d'intérêt) ;
(2) partout où une courbe s'affiche (dashboard, vues de portée), la durée est fixe -
ajouter un sélecteur DISCRET : liens `1m · 3m · 1y · all` en petites capitales fines
au-dessus de la figure (param `?range=`), défaut `all`. Pas de second calcul : on
TRANCHE la série déjà construite (le tableau de perfs reste sur la série complète).

**Conventions inchangées** (TDD, gofmt/vet, anglais, zéro JS, RLock, pas de binaire).

---

### Task I1: web - densité de l'onglet Assets et sections property en bas

**Files:**
- Modify: `internal/web/assets.go`, `internal/web/static/style.css`
- Test: `internal/web/assets_test.go` (créer), `internal/web/server_test.go` (vérifier)

Changements :
1. Sparklines rendues en 72×20 (au lieu de 90×24) ; cellules `.sparkcell` à 78px de
   large, padding réduit. La colonne nom N'A PLUS de largeur imposée : en
   `table-layout: fixed`, fixer les largeurs des sparkcells (78px) et des `.nombre`
   (11%), laisser `.asset-name` prendre le reste (retirer `width: 34%`, garder
   `max-width: 0` + ellipsis pour le nowrap).
2. Indentation des noms d'actifs : `.assets-table .asset-name { padding-left: 1.1rem; }`
   (les en-têtes de section restent au ras).
3. Tri des sections : extraire un helper `sortSections([]assetSection)` - les
   sections dont TOUTES les lignes sont des biens (`assetSection.PropertyOnly bool`,
   calculé pendant l'assemblage : initialisé à true, `&& asset.Kind == domain.Property`
   à chaque ligne) passent APRÈS toutes les autres ; à l'intérieur de chaque bloc,
   tri par brut décroissant puis nom. Le tri des LIGNES ne change pas.

- [ ] **Step 1: tests qui échouent**

`internal/web/assets_test.go` :

```go
package web

import (
	"testing"

	"finador/internal/domain"
)

func TestSortSectionsPropertyLast(t *testing.T) {
	secs := []assetSection{
		{Group: "realty", Gross: 500000, PropertyOnly: true},
		{Group: "equities", Gross: 10000},
		{Group: "bonds", Gross: 20000},
		{Group: "land", Gross: 900000, PropertyOnly: true},
	}
	sortSections(secs)
	got := []string{secs[0].Group, secs[1].Group, secs[2].Group, secs[3].Group}
	want := []string{"bonds", "equities", "land", "realty"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	_ = domain.Today() // garde l'import si inutilisé ailleurs - retirer si superflu
}
```

(nettoyer l'astuce d'import si domain n'est pas nécessaire)

Ajouter à `internal/web/server_test.go`, dans TestAssetsPage, les assertions CSS :

```go
	// densité : sparkline 72×20, nom indenté
	if !strings.Contains(body, `viewBox="0 0 72 20"`) {
		t.Error("sparklines should be 72x20")
	}
	_, css := get(t, srv, "/style.css")
	for _, want := range []string{".assets-table .asset-name { padding-left:", "width: 78px"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css: %q missing", want)
		}
	}
```

NOTE : adapter les chaînes exactes du CSS si le formatage diffère - l'intention
compte (indentation présente, cellule 78px).

- [ ] **Step 2: échec vérifié** ; **Step 3:** implémenter (assets.go : Sparkline(pts, 72, 20, color),
  champ PropertyOnly, sortSections remplaçant le tri inline des sections) ;
  **Step 4:** `go test ./... -count=1` + `-race ./internal/web/` verts ; serveur réel
  3 s : vérifier visuellement au curl que les noms sont plus longs qu'avant.
- [ ] **Step 5:** commit `feat(web): denser assets rows, indented names, property sections last`

---

### Task I2: web - périodes de courbes discrètes (?range=)

**Files:**
- Modify: `internal/web/handlers.go`, `internal/web/scope.go`,
  `internal/web/templates/dashboard.html`, `internal/web/templates/scope.html`,
  `internal/web/static/style.css`
- Test: `internal/web/server_test.go` (ajout)

Sémantique :
- Param `?range=` ∈ `1m, 3m, 1y, all` (défaut all = inception). Invalide → all.
- La série est construite UNE fois depuis l'origine (comme aujourd'hui) ; la COURBE
  trace `slicePoints(points, from)` (points dont la date ≥ from) ; le tableau de
  perfs et les métriques restent calculés sur la série complète (inchangé).
- from : `1m` = today −1 mois (AddDate), `3m` −3 mois, `1y` −1 an, `all` = zéro.
- Liens : rangée `.ranges` DANS la section history, au-dessus de la figure -
  `1m · 3m · 1y · all`, lien pour les inactifs, `<span class="active-range">` pour
  l'actif. Sur le dashboard, les liens DOIVENT préserver `?by=` et réciproquement
  (les onglets de répartition préservent `?range=`) : construire les URLs avec
  `url.Values` (ordre des clés déterministe par Encode). Sur les vues de portée
  (/group, /account, /asset, intersections), même rangée, URLs relatives simples
  (`?range=3m`).

- [ ] **Step 1: tests qui échouent**

```go
func TestChartRanges(t *testing.T) {
	srv, _ := testServer(t)
	_, full := get(t, srv, "/")
	code, m1 := get(t, srv, "/?range=1m")
	if code != http.StatusOK {
		t.Fatalf("range=1m = %d", code)
	}
	// le sélecteur est présent, l'actif est marqué, les liens préservent by/range
	for _, want := range []string{`class="ranges"`, `active-range`, `range=3m`} {
		if !strings.Contains(m1, want) {
			t.Errorf("?range=1m: %q missing", want)
		}
	}
	// la courbe 1m diffère de la courbe complète (moins de points)
	if strings.Count(m1, ",") >= strings.Count(full, ",") {
		t.Error("1m curve should carry fewer svg points than the full curve")
	}
	// les onglets de répartition préservent le range
	if !strings.Contains(m1, "by=account&range=1m") {
		t.Errorf("tabs should carry the range:\n%s", excerpt(m1))
	}
	// portée : le sélecteur existe aussi
	if _, sc := get(t, srv, "/asset/cw8?range=3m"); !strings.Contains(sc, "active-range") {
		t.Error("scope pages should have the range selector")
	}
	// invalide → all (200, pas d'erreur)
	if code, _ := get(t, srv, "/?range=zz"); code != http.StatusOK {
		t.Errorf("invalid range = %d", code)
	}
}
```

NOTE : le compte de virgules est un proxy du nombre de points SVG - si trop fragile,
comparer la PREMIÈRE date affichée dans le SVG (coin gauche) : full commence à la
première transaction, 1m commence ~il y a un mois. Choisir l'assertion robuste et
l'expliquer.

- [ ] **Step 2: échec vérifié** ; **Step 3:** implémenter :

`handlers.go` / `scope.go` - helpers communs :

```go
// chartRange resolves ?range= into a start date (zero = inception) and its name.
func chartRange(r *http.Request, today domain.Date) (domain.Date, string) {
	switch r.URL.Query().Get("range") {
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), "1m"
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), "3m"
	case "1y":
		return domain.DateOf(today.Time().AddDate(-1, 0, 0)), "1y"
	}
	return domain.Date{}, "all"
}

// slicePoints keeps points dated at or after from (zero from = everything).
func slicePoints(pts []perf.Point, from domain.Date) []perf.Point {
	if from.IsZero() {
		return pts
	}
	for i, p := range pts {
		if !p.Date.Before(from) {
			return pts[i:]
		}
	}
	return nil
}
```

Le dashboard et renderScope tranchent les points passés à chart.SVG (gross ET net),
gardent la série complète pour perf.Report. dashData/scopeData gagnent
`Range string` + `RangeLinks []onglet` (réutiliser le type onglet : Label/URL/Active).
Construction des URLs du dashboard avec url.Values{"by":…, "range":…} en omettant
les valeurs par défaut (by=group, range=all) pour des URLs propres. Les onglets de
répartition existants reçoivent eux aussi le range courant.

Templates - dans la section history, au-dessus de la figure :

```html
    <div class="ranges">
      {{range .RangeLinks}}{{if .Active}}<span class="active-range">{{.Label}}</span>{{else}}<a href="{{.URL}}">{{.Label}}</a>{{end}}{{end}}
    </div>
```

CSS :

```css
.ranges { display: flex; gap: .7rem; justify-content: flex-end; font-size: .72rem;
  font-variant: small-caps; letter-spacing: .12em; margin-bottom: .3rem; color: var(--encre-2); }
.ranges .active-range { font-weight: 600; border-bottom: 2px solid var(--encre); color: var(--encre); }
```

- [ ] **Step 4:** `go test ./... -count=1` + `-race ./internal/web/` verts ; serveur
  réel : curl `/?range=3m`, une vue de portée avec range, vérifier la préservation
  croisée by/range dans les hrefs.
- [ ] **Step 5:** commit `feat(web): quiet chart range links - 1m, 3m, 1y, all`

---

### Task I3: finition

- [ ] README : ajuster la puce web (« charts default to full history with quiet
  1m/3m/1y range links »).
- [ ] Portillons complets + revue + tag `phase-i`.
