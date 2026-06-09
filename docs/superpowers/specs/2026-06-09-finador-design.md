# Finador — design

*2026-06-09 — spec validée pendant la session de brainstorming.*

Outil personnel de suivi de patrimoine (façon Finary / portfolio Yahoo Finance), en Go,
**single binary**, utilisable indifféremment en ligne de commande et en web, données dans
**un seul fichier chiffré**. Objectif : suivre la valeur globale du patrimoine (brute et
**nette d'impôt latent**) et le rendement réel des poches qui le composent, dans la durée.

## 1. Décisions actées

| Sujet | Décision |
|---|---|
| Prix de marché | API non-officielle Yahoo Finance, intégrée, derrière une interface `PriceSource` pluggable |
| Rendement | TWR **et** XIRR (les deux répondent à des questions différentes) |
| Dividendes | Récupération automatique depuis Yahoo (`events=div`), saisie manuelle possible |
| Graphiques web | SVG généré côté serveur, **zéro JavaScript** |
| CLI | Sous-commandes riches, avec **spf13/cobra** |
| Stockage | Snapshot chiffré en mémoire : `en-tête ‖ AES-256-GCM(gzip(JSON))`, pas de SQLite |
| Fiscalité | Règle fiscale par enveloppe : `TaxOnGains` (plus-value taxée) ou `TaxOnValue` (tout taxé) |
| Enveloppes | Entité de premier rang, nom libre (« PEA Zephyr », « CTO IBKR »), multi-comptes par actif |
| Groupes | Hiérarchiques, en chemin (`actions/us/tech`), agrégeables par préfixe |
| Transactions | Éditables (ID stable, edit/rm) ; tout l'état dérivé se recalcule depuis le ledger |
| Compilation | Go 1.26 seul, pur Go (pas de CGo, pas de transpileur JS), `go build` suffit |

## 2. Architecture

```
finador/
├── cmd/finador/main.go     # wiring + racine cobra
└── internal/
    ├── domain/      # types métier : Account, Asset, Transaction, Money, Currency, Date — zéro dépendance
    ├── store/       # conteneur chiffré : Open/Save du snapshot, versions & migrations
    ├── keyring/     # mot de passe : prompt sans écho + cache Keychain macOS (par tty, TTL)
    ├── market/      # interface PriceSource + impl. Yahoo (clôtures, FX, dividendes) + politique de cache
    ├── portfolio/   # moteur : rejeu du ledger, positions, valorisation à date T, séries, agrégats, impôt latent
    ├── perf/        # maths pures sur séries : TWR, XIRR, CAGR, vol, Sharpe, Sortino, max drawdown
    ├── chart/       # modèle Series + renderers : chart/term (braille) et chart/svg
    ├── cli/         # cobra.Commands minces qui appellent portfolio/
    └── web/         # http.Server, html/template + CSS via go:embed, handlers minces
```

**Règle de dépendance.** `domain` ne dépend de rien. `portfolio` et `perf` ne dépendent que
de `domain` et reçoivent les prix par interface — testables sans réseau. `cli` et `web` sont
deux façades minces sur le même moteur : toute capacité CLI existe en web et réciproquement.

**Dépendances externes** (courtes, volontairement) :

| Lib | Rôle |
|---|---|
| `spf13/cobra` | CLI : sous-commandes, help, complétion shell |
| `samber/lo` | collections expressives |
| `shopspring/decimal` | quantités et montants du ledger (exacts) ; les maths de perf restent en `float64` |
| `golang.org/x/crypto` | Argon2id (AES-GCM est dans la stdlib) |
| `golang.org/x/term` | saisie du mot de passe sans écho |

Pas de lib de chart : le SVG et le braille sont rendus à la main (code court, golden-testé).
Keychain via `/usr/bin/security` en `os/exec` : zéro CGo.

## 3. Modèle de données

### Enveloppes et fiscalité

```go
type TaxMode uint8 // TaxNone | TaxOnGains | TaxOnValue

type TaxRule struct {
    Mode TaxMode
    Rate decimal.Decimal // ex. 0.172, 0.30, 0.20
}

type Account struct {
    ID       AccountID // slug : "pea-zephyr", "cto-ibkr"
    Name     string    // libellé libre : "PEA Zephyr", "CTO IBKR"
    Currency Currency  // devise de référence du compte
    Tax      TaxRule
}
```

- **`TaxOnGains`** (PEA 17,2 %, CTO 30 %, AV…) : l'apport n'est pas taxé, la plus-value l'est.
  `impôt = max(0, valeur − base) × taux` avec **base = Σ versements − Σ retraits** de
  l'enveloppe (mécanique PEA/AV). Pour un compte au cash non suivi (cf. § cash), la base est
  `Σ achats − Σ produits de vente`.
- **`TaxOnValue`** (PER déduit à l'entrée…) : tout le contenu est taxable. `impôt = valeur × taux`.

Partout où une valeur s'affiche : **brut, impôt latent estimé, net** — y compris les courbes
(la base fiscale est datée, la courbe nette est donc exacte dans le temps). La valeur nette
par *groupe* (qui croise plusieurs enveloppes) applique le taux de l'enveloppe position par
position — approximation documentée, exacte quand l'enveloppe est liquidée en bloc.

Le slug **et** le nom libre sont acceptés partout où une enveloppe est attendue (CLI, CSV, web).

### Actifs

```go
type AssetKind uint8 // Security | Property — le cash n'est pas un actif, il est porté par le compte

type Asset struct {
    ID       AssetID   // slug stable : "cw8", "maison-acheres"
    Kind     AssetKind
    Name     string    // "Amundi MSCI World", "Maison à Achères"
    Ticker   string    // Security : symbole Yahoo ("CW8.PA")
    ISIN     string    // optionnel ; résolu en ticker via la recherche Yahoo
    Aliases  []string  // noms libres acceptés partout où un actif est attendu
    Currency Currency  // devise de cotation
    Group    string    // chemin hiérarchique : "actions/monde", "alternatif/managed-futures"
}
```

- **Security** : valorisé `quantité détenue × clôture(date) × FX(date)`.
- **Property** (« Maison à Achères ») : estimations datées, fonction en escalier.
- Le **cash** n'est pas un actif : c'est un attribut de chaque compte (cf. ci-dessous).
- Le **groupe** vit sur l'actif (la stratégie ne dépend pas de l'enveloppe qui loge la ligne) ;
  toute commande accepte un préfixe de chemin : `finador value actions` agrège le sous-arbre.

### Ledger

La seule source de vérité ; tout état dérivé (positions, PRU, bases fiscales, séries) se
recalcule par rejeu. Les transactions sont **éditables** : ID stable, `tx edit`/`tx rm` ;
l'édition est sans danger précisément parce que tout se recalcule.

```go
type TxKind uint8 // Buy | Sell | Dividend | Fee | Deposit | Withdraw | Statement

type Transaction struct {
    ID       TxID
    Date     domain.Date     // jour civil, sans heure ni fuseau
    Account  AccountID
    Asset    AssetID         // vide pour les mouvements de cash pur du compte
    Kind     TxKind
    Quantity decimal.Decimal // Buy/Sell ; signée par le Kind
    Amount   Money           // total ; la CLI accepte @prix-unitaire et calcule le total
    Note     string
}
```

### Sémantique du cash — une règle

Dans un compte, les trades bougent le cash : Buy débite, Sell et Dividend créditent, Fee
débite — un achat est **neutre en valeur**, comme dans la réalité. `Statement` ancre un solde
constaté à une date (et absorbe les écarts entre relevés). `Deposit`/`Withdraw` marquent les
flux externes (ils alimentent la base fiscale et le XIRR).

**Cash non suivi** : tant qu'un compte n'a aucun Statement/Deposit/Withdraw, son cash est
réputé non suivi (≡ 0) et chaque Buy/Sell y est traité comme un apport/retrait externe pour
la performance et la base fiscale. Aucune configuration : le comportement découle des données.

### Import CSV

Colonnes par en-tête, ordre libre : `date, kind, account, asset, quantity, price, amount,
currency, group, note` — `price` (unitaire) **ou** `amount` (total), l'autre se déduit. Les
actifs et comptes inconnus sont créés à la volée. Import **idempotent** : chaque ligne reçoit
un hash de contenu, les doublons sont ignorés au ré-import.

### Dividendes automatiques

Récupérés de Yahoo (`events=div`) et **dérivés à la volée** (pas matérialisés dans le ledger) :
`quantité détenue à l'ex-date × montant`, crédités au cash du compte s'il est suivi, sinon
comptés comme revenu externe dans la performance. Si un actif a au moins un `Dividend` manuel,
l'automatique est désactivé pour cet actif (pas de double compte). Montants bruts — la retenue
à la source n'est pas modélisée en v1 (approximation documentée).

## 4. Stockage : le fichier `.fin`

```
┌────────────────────────────────────────────┐
│ magic "FINADOR1" · version (1 octet)       │
│ params Argon2id (time, mem, threads) · sel │
│ nonce AES-GCM (régénéré à chaque save)     │
├────────────────────────────────────────────┤
│ AES-256-GCM( gzip( JSON{                   │
│   accounts, assets, transactions,          │
│   prices, dividends, fx, config            │
│ }))                                        │
└────────────────────────────────────────────┘
```

- KDF **Argon2id** (défauts : time=3, mem=64 Mio, threads=min(4, NumCPU), clé 32 o, sel 16 o),
  paramètres stockés dans l'en-tête pour pouvoir les renforcer plus tard.
- Ouverture : mdp → clé → déchiffrement → structs en RAM. Sauvegarde : JSON → gzip → chiffre →
  write tmp → fsync → **rename atomique**, l'ancienne version devient `.bak`.
- Un mauvais mot de passe et un fichier altéré sont indistinguables par construction (échec
  d'authentification GCM) — le message d'erreur l'explique.
- Le **cache de prix vit dans le fichier chiffré** : la liste des tickers détenus est une
  métadonnée sensible. Volumétrie attendue : quelques Mo avant gzip — réécriture intégrale
  à chaque save, négligeable à cette échelle.
- L'octet de version permet les migrations de schéma futures.

### Mot de passe & Keychain (macOS)

À la première saisie, finador propose de mémoriser le mot de passe dans le Keychain
(`/usr/bin/security`, zéro CGo) : une entrée par (fichier, tty) avec horodatage, re-demande
après TTL (12 h par défaut, configurable), `finador lock` purge, `--no-keychain` pour refuser.
Hors macOS : saisie à chaque exécution, ou `FINADOR_PASSWORD` pour le scripting (documenté
comme moins sûr).

## 5. Marché : `market/`

```go
type PriceSource interface {
    Resolve(ctx context.Context, query string) (SymbolInfo, error)      // ticker, ISIN, nom
    Daily(ctx context.Context, symbol string, from Date) (Series, error) // clôtures + dividendes
    FxDaily(ctx context.Context, pair string, from Date) (Series, error)
}
```

- Implémentation Yahoo : `query1.finance.yahoo.com/v8/finance/chart/{sym}?interval=1d&events=div`
  (historique + dividendes), endpoint `v1/finance/search` pour résoudre ISIN et noms.
  User-Agent navigateur, retries avec backoff, throttling poli.
- FX : paires Yahoo (`EURUSD=X`…), croisement par l'USD pour les devises tierces.
- **Cache** : rafraîchi automatiquement quand une commande a besoin de données plus fraîches
  que la veille ; `finador refresh` force ; `--offline` interdit le réseau (on sert le cache).
- Jours sans cotation (week-ends, fonds à cotation espacée) : report de la dernière clôture
  connue ; une valeur est signalée « stale » au-delà de 5 jours.

## 6. Moteur : valorisation & performance

**Valorisation.** `Value(scope, date, devise)` — titres = quantité × clôture × FX ; cash et
biens = escalier des relevés/estimations. Devise d'affichage EUR par défaut, `--ccy USD`.
Affichage brut ou net d'impôt latent (`--net`).

**Portées uniformes.** Le même `scope` partout (valeur, perf, courbes, web) : vide (tout le
patrimoine), groupe ou préfixe (`actions/us`), enveloppe (« PEA Zephyr »), ou actif.

**Performance.**
- **TWR** : chaînage des rendements quotidiens `r_t = (V_t − F_t) / V_{t−1}` (flux externes en
  début de jour). Périodes : 1j, 2j, 5j, 7j, 1m, 3m, YTD, 1an, année civile précédente, depuis
  l'origine, `--from/--to` libre. Les **flux externes sont relatifs à la portée** : pour le
  patrimoine entier ce sont les Deposit/Withdraw ; pour un groupe, une enveloppe ou un actif,
  tout ce qui y entre ou en sort (achats, ventes, apports) est un flux.
- **XIRR** : taux annualisé money-weighted, résolu par bissection (robuste), sur tout
  l'historique ou une fenêtre (la valeur de départ devient le flux initial).

**Métriques** (sur les rendements quotidiens TWR de la portée, en devise d'affichage) : CAGR,
volatilité annualisée (√252), Sharpe et Sortino (taux sans risque plat configurable,
`finador config set risk-free 2.4%`, défaut 0), max drawdown avec dates pic→creux et durée de
récupération. Fonctions pures dans `perf/` (`[]Return → float64`), testées contre des valeurs
de référence calculées indépendamment.

## 7. CLI

```
finador init                                   # crée le .fin (mdp demandé 2×)
finador account add "PEA Zephyr" --tax gains:17.2%
finador account add "PER Linxea"   --tax value:20%
finador asset  add CW8.PA --group actions/monde
finador asset  set maison-acheres 450000 --at 2026-06-01
finador add    cw8 10 @550 2026-06-01 --account "PEA Zephyr"   # qty<0 = vente
finador cash   set "CTO IBKR" 12500
finador deposit|withdraw "PEA Zephyr" 5000 2026-01-10
finador tx     list|edit|rm [id]
finador import transactions.csv
finador value  [scope] [--ccy USD] [--net] [--at 2026-01-01]
finador perf   [scope] [--from --to]           # périodes + CAGR, vol, Sharpe, Sortino, maxDD
finador chart  [scope] [--net] [--from --to]   # courbe braille dans le terminal
finador refresh
finador serve  [--addr 127.0.0.1:8451] [--db ./my.fin]
finador lock
finador config set|get [clé] [valeur]
```

Sorties : tableaux `text/tabwriter` alignés, couleurs ANSI sobres (vert/rouge pour les
variations), courbes braille via `chart/term`. Exit codes : 0 ok, 1 erreur d'usage, 2 interne.

## 8. Web

`finador serve` déverrouille en terminal puis sert sur `127.0.0.1` (pas d'auth web : local par
défaut ; avertissement explicite si on binde une autre interface). **Zéro JavaScript** : pages
`html/template`, formulaires POST/redirect, courbes SVG inline rendues par `chart/svg`, CSS
écrit main — le tout embarqué via `go:embed`.

- `/` — dashboard : valeur totale brut/net, variation du jour, courbe SVG, répartition par
  groupe, perfs par périodes ;
- `/group/{path}`, `/account/{id}`, `/asset/{id}` — la « vue de portée » : valeur, courbe,
  métriques, lignes détenues, transactions ;
- `/tx` — liste + formulaires d'ajout/édition/suppression ; `/import` — upload CSV ;
  bouton refresh (POST).

En mode serve, le store est protégé par un `sync.RWMutex` ; chaque mutation sauvegarde
atomiquement le fichier.

## 9. Erreurs

Erreurs sentinelles dans `domain` (`ErrAssetNotFound`, `ErrAmbiguousRef`, `ErrBadPassword`…),
wrapping `%w` systématique, pas de panic au-delà des frontières de package. La CLI traduit en
messages courts ; le web en pages d'erreur propres (400/404/500).

## 10. Tests

`go test ./...`, sans réseau :

| Package | Stratégie |
|---|---|
| `perf/` | golden values calculées indépendamment (XIRR de tableurs connus, Sharpe vérifiable à la main) |
| `store/` | aller-retour chiffré, mauvais mdp, fichier altéré (échec GCM), migrations de version |
| `market/` | fixtures JSON Yahoo enregistrées, servies par `httptest` ; politique de cache |
| `portfolio/` | scénarios table-driven : cash suivi/non suivi, multi-devises, bases fiscales, dividendes |
| `chart/` | golden files SVG et braille |
| `web/`, `cli/` | bout-en-bout sur `.fin` temporaire avec `PriceSource` factice |

## 11. Hors scope v1 (évolutions naturelles)

Comparaison à un benchmark ; séries de taux sans risque (au lieu du taux plat) ; imports
courtiers natifs (exports CSV) ; dashboard TUI ; retenue à la source sur dividendes et
fiscalité fine (abattements de durée immobilier, tranches) ; charts interactifs (uPlot
vendoré) ; synchronisation multi-machines. L'architecture (PriceSource pluggable, moteur
indépendant des façades, en-tête de fichier versionné) les accueille sans refonte.
