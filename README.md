# finador

Suivi de patrimoine personnel, chiffré, en un seul binaire Go — façon Finary ou
portfolio Yahoo Finance, mais à vous : vos données vivent dans **un seul fichier
chiffré** sur votre machine, utilisables aussi bien **en ligne de commande qu'en web**.

```
$ finador value --net
patrimoine au 2026-06-10
LIGNE       BRUT           IMPÔT       NET
actions     18050.00 EUR   361.20 EUR  17688.80 EUR
immo        450000.00 EUR  9000.00 EUR 441000.00 EUR
liquidités  18010.00 EUR   0.00 EUR    18010.00 EUR
TOTAL       486060.00 EUR  9469.20 EUR 476590.80 EUR
```

## Ce que ça fait

- **Un fichier, chiffré** : tout l'état (comptes, transactions, cache de cours) tient
  dans un fichier `.fin` scellé par Argon2id + AES-256-GCM. Mot de passe demandé au
  lancement, mémorisable dans le Keychain macOS par terminal (TTL 12 h, `finador lock`
  pour purger).
- **Enveloppes fiscales** : chaque compte (« PEA BforBank », « CTO IBKR », « PER »)
  porte sa règle — `gains:17.2%` (la plus-value au-delà des versements est taxée) ou
  `value:20%` (tout le contenu est taxé). Partout, finador affiche **brut, impôt
  latent estimé, net** — y compris sur les courbes.
- **Tout actif** : titres cotés (cours Yahoo Finance, dividendes automatiques, FX
  croisés par l'USD), liquidités par relevés, et biens quelconques (« Maison à
  Achères ») par estimations datées.
- **Vraies mesures de rendement** : TWR (la performance de la stratégie, flux
  neutralisés) et XIRR (ce que votre argent a réellement rapporté) par périodes
  (1j, 5j, 1m, 3m, ytd, 1a, an-1, origine), plus CAGR, volatilité, Sharpe, Sortino,
  max drawdown.
- **Courbes partout** : braille dans le terminal, SVG dans le web — brut ou net.
- **Portées uniformes** : toute commande accepte la même portée — rien (tout le
  patrimoine), un groupe hiérarchique (`actions/monde`), une enveloppe, un actif.

## Compiler

Go ≥ 1.26, rien d'autre (pur Go : pas de CGo, pas de toolchain JS).

```sh
go build -trimpath -o bin/finador ./cmd/finador
```

## Démarrer

```sh
finador init                                          # crée ~/.finador.fin (ou --db chemin)
finador account add "PEA BforBank" --tax gains:17.2%
finador account add "CTO IBKR"     --tax gains:30%
finador account add "Livret A"

finador asset add CW8.PA --id cw8 --group actions/monde   # résolu via Yahoo (nom, devise)
finador add cw8 10 @550 2026-06-01 --account "PEA BforBank"
finador deposit "PEA BforBank" 5000 2026-01-10        # apport externe (base fiscale, XIRR)
finador cash set "Livret A" 11250                     # relevé de solde

finador asset add "Maison à Achères" --kind property --group immo
finador asset set maison-a-acheres 450000 --account "Livret A"

finador value --net          # valeur brut / impôt latent / net
finador perf actions         # TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD
finador chart --net          # courbe braille dans le terminal
finador serve                # http://127.0.0.1:8451 — le web complet, zéro JS
```

`deposit` ≠ `cash set` : un **apport** se saisit avec `deposit`/`withdraw` (il nourrit
la base fiscale et le XIRR) ; `cash set` pose un **solde constaté**, et les écarts entre
relevés comptent comme performance (les intérêts d'un livret, par exemple).

## Import CSV

```sh
finador import transactions.csv
```

Colonnes par en-tête, ordre libre : `date, kind, account, asset, quantity, price,
amount, currency, group, note` — `price` (unitaire) ou `amount` (total), l'autre se
déduit. Comptes et actifs inconnus sont créés à la volée. L'import est **idempotent** :
ré-importer le même fichier n'ajoute aucun doublon. `kind` ∈ buy, sell, deposit,
withdraw, dividend, fee, statement.

## Le web

`finador serve` déverrouille le fichier dans le terminal puis sert l'application sur
`127.0.0.1:8451` : dashboard (valeur nette en manchette, courbe, répartition, perfs),
vues par groupe/enveloppe/actif, saisie et suppression d'écritures, import CSV,
rafraîchissement des cours. Zéro JavaScript, zéro ressource externe — tout est rendu
côté serveur et embarqué dans le binaire. Pas d'authentification web : ne servez pas
au-delà de 127.0.0.1 (un avertissement s'affiche si vous le faites quand même).

## Configuration

```sh
finador config set currency EUR        # devise d'affichage par défaut
finador config set risk-free 2.4%      # taux sans risque pour Sharpe/Sortino
finador config set keychain-ttl 8h     # durée de mémorisation du mot de passe
finador config set default-account pea-bforbank  # enveloppe par défaut des saisies
```

`--offline` (toutes commandes) interdit le réseau et travaille sur le cache ;
`finador refresh` force la mise à jour des cours ; `FINADOR_PASSWORD` fournit le mot
de passe pour le scripting ; `FINADOR_DB` remplace le chemin par défaut.

## Modèle de données & sécurité

- Le **ledger de transactions est la seule source de vérité** : positions, bases
  fiscales et séries se recalculent par rejeu. Les transactions sont éditables
  (`finador tx list/edit/rm`).
- Fichier : `magic ‖ version ‖ paramètres Argon2id ‖ sel ‖ nonce ‖
  AES-256-GCM(gzip(JSON))`, en-tête authentifié (AAD), écriture atomique avec `.bak`.
  Un mauvais mot de passe et un fichier altéré sont indistinguables par construction.
- Le cache de cours vit **dans** le fichier chiffré : la liste de vos tickers est une
  métadonnée sensible.
- Les choix d'implémentation discutables sont consignés dans
  `docs/superpowers/DECISIONS.md` ; la spec et les plans d'implémentation dans
  `docs/superpowers/`.

## Nouveautés v0.2/v0.3

- **Références courtes** : `add cw8` fonctionne si « cw8 » est un préfixe unique d'un
  identifiant, ticker, ISIN, alias ou nom d'actif — plus besoin de l'ID complet.
  Ambiguïté → message listant les candidats.
- **`--exclude`** : toute portée peut écarter un ou plusieurs actifs —
  `perf --exclude btc,ddog`, `value actions --exclude aapl` (actifs uniquement, par
  ticker/ISIN/alias/référence courte).
- **`--by enveloppe`** : `value --by enveloppe` ventile le patrimoine par compte
  (PEA, CTO…) plutôt que par groupe. Disponible aussi dans le web (`/?par=enveloppe`).
- **`--what-if`** : hypothèses jetables sur les prix —
  `value --what-if ddog=280` affiche la valeur hypothétique ET le delta vs réel.
  Les prix sont exprimés dans la devise de cotation de l'actif, jamais persistés.
- **`asset edit` / `asset rm`** : modifier le nom, ticker, ISIN, groupe, devise,
  alias (`--add-alias`, `--rm-alias`) ou la retenue à la source (`--withholding 15%`).
  `asset rm` refuse si des transactions référencent l'actif.
- **`--withholding`** : retenue à la source sur les dividendes automatiques
  (net = brut × (1 − taux)), saisie par `asset edit X --withholding 15%`.
- **Couleurs dans `perf`** : les colonnes TWR/XIRR s'affichent en vert (positif) ou
  rouge (négatif) sur un terminal. Désactivables par `--no-color` ou la variable
  `NO_COLOR`. Forçables en test avec `FINADOR_FORCE_COLOR=1`.
- **Protection contre les écritures concurrentes** : `store.File` retient l'empreinte
  disque (taille + mtime) à l'ouverture ; `Save()` vérifie sous flock qu'un autre
  processus n'a pas modifié le fichier entre-temps — si c'est le cas, l'écriture est
  refusée avec le message « relancez la commande » plutôt qu'écraser silencieusement.

- **Édition des transactions au web** : `GET /tx/{id}/edit` affiche le bordereau
  prérempli ; `POST /tx/{id}/edit` revalide et sauve — l'identifiant et l'empreinte
  d'import sont préservés (une ligne éditée ne revient pas au ré-import suivant).
- **Répartition à 3 modes** : le dashboard propose « par groupe », « par enveloppe »
  et « par actif » — arborescence dépliable `<details>/<summary>` à 3 niveaux, zéro
  JavaScript, chaque niveau croisé (enveloppe ∩ groupe) est cliquable.
- **Liens au thème** : toutes les ancres de l'interface utilisent la palette encre/garance
  du thème ; plus de bleu navigateur par défaut.

## Limites assumées (v1)

Fiscalité par enveloppe approximée position par position dans les ventilations ;
pas de benchmark ; cours Yahoo non officiels.
Voir `docs/superpowers/specs/2026-06-09-finador-design.md` §11.
