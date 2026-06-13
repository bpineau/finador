# Finador — données de marché multi-sources (fallback type portfodor)

*2026-06-13 — run autonome (user AFK ~4h). Décisions consignées ici et dans DECISIONS.md ; pas de relecture.*

## 0. Objectif

finador ne sait coter que via Yahoo. Beaucoup d'**actifs atypiques** (fonds OPCVM/SICAV FR/LU
par **ISIN**) n'y sont pas. Objectif : **coter ces actifs via des sources tierces** quand Yahoo
ne les a pas, **aussi bien que `../portfodor/`** — en particulier `LU0131510165` et `LU1111111111`.

## 1. Constats (étude portfodor + faisabilité live)

- **portfodor `pkg/marketdata` est 100 % stdlib** : JSON (`encoding/json`), CSV (`encoding/csv`),
  scraping HTML en `regexp` + `html.UnescapeString`. **Aucune dépendance** — finador reste pur-stdlib.
- **Financial Times = la source des fonds FR/LU**, et **vérifié joignable d'ici** :
  - Search : `GET https://markets.ft.com/data/searchapi/searchsecurities?query={ISIN}` → JSON
    `data.security[]` `{name, symbol "LU…:EUR", xid, isPrimary}`. On choisit le non-GBX, sinon le
    premier avec `xid`. Le `xid` est l'id porteur.
  - Chart : `POST https://markets.ft.com/data/chartapi/series` (`Content-Type: application/json`),
    body `{"days":N,"dataPeriod":"Day","dataInterval":1,"timeServiceFormat":"JSON","returnDateType":"ISO8601","elements":[{"Type":"price","Symbol":"<xid>"}]}` →
    `Dates[]` (ISO `2006-01-02T15:04:05`) ‖ `Elements[0].ComponentSeries[]` (Type=="Close" → `Values[]`),
    `Elements[0].Currency`. `days = max(jours depuis from + 2, 2)`. Closes nil/≤0 et dates < from ignorées.
  - **Live OK** : LU1111111111 → close 241,79 € (2026-06-11, xid 118135654) ; LU0131510165 → 940,74 € (xid 8542). Devise EUR.
- **Yahoo** : 429 dur d'ici (mais marche chez l'utilisateur) — reste le provider primaire pour les tickers.
- **Morningstar via Boursorama** (fallback défensif, **non vérifiable d'ici** — l'endpoint NAV renvoie `[]`) :
  - Boursorama : `GET https://www.boursorama.com/recherche/ajax?query={ISIN}` (header
    `X-Requested-With: XMLHttpRequest`) → scrape regex `/bourse/(?:opcvm|trackers)/cours/(0P[0-9A-Za-z]+)/` → id Morningstar `0P…`.
  - Morningstar : `GET https://tools.morningstar.fr/api/rest.svc/timeseries_price/ok91jeenoo?id={0P…}&idtype=Morningstar&frequency=daily&startDate={YYYY-MM-DD}&outputType=COMPACTJSON` →
    `[[epoch_ms, value], …]`.
- **Selia `990000000000`** : code interne AMF (FCPE/PEE), pas un ISIN coté — **couvert par aucun provider** (FT/Boursorama renvoient vide), portfodor non plus. → reste en `asset set` manuel (documenté). Une source France-spécifique (AMF GECO) serait un chantier séparé, hors scope.
- **Stooq** : ticker-only + challenge JS d'ici → non pertinent pour les fonds. (Porté éventuellement plus tard ; pas prioritaire.)

## 2. Design

**Référence d'instrument.** Le fetch passe désormais une `Ref{Symbol, ISIN string}` (au lieu d'un
simple `symbol`) : les providers tickers utilisent `Symbol`, les providers fonds utilisent `ISIN`.

**Interface `Source`** (utilisée par `Refresh` et par l'enrichissement de `asset add`) :
```go
type Ref struct { Symbol, ISIN string }
type Source interface {
    Resolve(ctx, query string) (SymbolInfo, error)             // résolution ticker pour `asset add` (Yahoo search)
    Daily(ctx, ref Ref, from domain.Date) (DailyData, error)   // multi-providers
}
```
**`Provider`** (un fournisseur de série) :
```go
var ErrNotCovered = errors.New("instrument not covered by this provider")
type Provider interface {
    Daily(ctx, ref Ref, from domain.Date) (DailyData, error)   // ErrNotCovered s'il ne sait pas
    Name() string
}
```
- **Yahoo** : Provider (utilise `ref.Symbol` ; `ErrNotCovered` si vide) + `Resolve` (inchangé).
- **FT** : Provider (utilise `ref.ISIN` ; `ErrNotCovered` si vide ; search→xid→chart).
- **Morningstar/Boursorama** : Provider défensif (utilise `ref.ISIN`).
- **`Multi`** (impl. `Source`, défaut via `market.Default()`) : `Resolve`→Yahoo ; `Daily`→**chaîne
  ordonnée [Yahoo, FT, Morningstar]**, renvoie le 1er succès, saute `ErrNotCovered`, sinon la dernière erreur.

**`Refresh`** : construit `Ref{Symbol: asset.Ticker, ISIN: asset.ISIN}` et appelle `src.Daily(ctx, ref, from)`.
Le reste inchangé : merge dans `PriceSeries`, warning si devise fetchée ≠ devise de l'actif,
**ne plante jamais** (réseau KO → warning, cache conservé = fallback périmé naturel).

**Cache** : inchangé — les séries vivent déjà dans le sidecar marché (`MarketData.Prices`), peu
importe la source. Pas de second cache de résolution (FT search est rapide ; on re-résout l'ISIN à
chaque refresh, acceptable ; optimisation « pin xid » possible plus tard).

**Client HTTP** : réutilise le style `yahoo.go` (timeout 15s, UA navigateur, 1-2 retries sur 429/5xx).
FT POST en JSON ; Boursorama header `X-Requested-With`. Erreurs réseau → remontée (le provider suivant prend le relais).

## 3. Périmètre

- **Implémenté** : `Ref`, `Provider`, **FT** (vérifié sur les 2 fonds cibles), **Multi** chaîne
  Yahoo→FT→Morningstar, **Morningstar/Boursorama** (défensif, best-effort).
- **Hors scope (documenté)** : FCPE/PEE par code AMF (Selia) → `asset set` manuel ; Stooq (ticker-only,
  non prioritaire) ; pin de catalogue xid (optimisation).
- **Zéro dépendance** ajoutée.

## 4. Phasage

1. **Refactor `Ref`** : interface `Source.Daily(Ref,...)`, Yahoo, `Refresh`, enrich (`asset add`), tests/fakes. Comportement identique (Yahoo only). Commit vert.
2. **Provider FT** : `internal/market/ft.go` (search+chart, parsing), tests httptest avec fixtures FT réelles. Non câblé.
3. **`Multi` + câblage** : chaîne Yahoo→FT, `market.Default()`, `cli` wire `Default()` au lieu de `NewYahoo()`. Active le fallback FT. **Vérif live LU0131510165/LU1111111111.**
4. **Morningstar/Boursorama** : provider défensif + tests.
5. **Doc** : README (« atypical assets / funds by ISIN »), note sur l'Selia FCPE manuel, DECISIONS.

## 5. Critères de réussite

1. `finador refresh` cote **LU1111111111** et **LU0131510165** via FT (séries NAV EUR) — **vérifié live**.
2. Un actif avec ticker Yahoo continue d'être coté par Yahoo (chaîne : Yahoo d'abord).
3. Un actif sans ticker ni ISIN connu d'aucun provider → pas de plantage (warning, valorisé par statement).
4. Selia FCPE : documenté comme `asset set` manuel.
5. Zéro dépendance ajoutée ; `Refresh` « never fails hard » préservé ; suite verte, vet + lint propres.
