# Décisions prises en autonomie — à reviewer

Run autonome du 2026-06-09 (nuit). Chaque décision difficile rencontrée pendant
l'implémentation est consignée ici : contexte, options, choix retenu, et comment revenir
dessus si vous préférez autrement.

## D1 — Branche de travail et merge final

**Contexte :** le workflow subagent-driven exige de ne pas implémenter sur `main`.
**Choix :** tout est implémenté sur `feat/finador-v1`. À la complétion du projet (fin de
phase D + revue finale), la branche est mergée dans `main` et taguée `v0.1.0`, pour que
vous vous réveilliez avec un projet fini sur `main`. **Alternative si vous préférez :**
`git reset --hard <sha-avant-merge>` sur main, la branche reste intacte.

## D2 — Modèles des sous-agents

**Choix :** implémenteurs et revue de conformité spec en Sonnet (les tasks du plan
contiennent le code complet : travail mécanique), revue qualité en Opus, revue finale de
chaque phase par le modèle le plus capable. Rationale : coût/vitesse d'un run de nuit sans
sacrifier la qualité aux points de contrôle.

## D3 — Plans des phases B/C/D

**Choix :** écrits par le contrôleur (cette session) juste avant chaque phase, en suivant
les conventions du plan A (TDD, code complet par étape), pour coller au code réellement
produit. L'UI web de la phase D passe par le skill frontend-design comme demandé.

## D4 — Mot de passe en argv de security(1)

**Contexte :** le cache Keychain passe par `/usr/bin/security add-generic-password -w <payload>`
(zéro CGo). Le payload (expiry + mot de passe) transite donc brièvement en argv, visible dans
`ps` pendant quelques millisecondes. La revue sécurité a vérifié que `security` n'offre pas de
lecture stdin non-interactive propre. **Choix :** trade-off assumé pour un outil personnel
mono-utilisateur (la frontière de confiance est déjà la session utilisateur). **Alternative si
refusé :** lib Keychain CGo (casse la contrainte pur-Go), ou pas de cache du tout
(`--no-keychain` existe déjà).

## D5 — Keychain sans demande de consentement

**Contexte :** la spec §4 dit que finador « propose de mémoriser » le mot de passe ; en
pratique l'implémentation mémorise systématiquement sauf `--no-keychain`. Une question
interactive aurait compliqué le scripting et les tests. **Choix :** mémorisation par défaut,
opt-out par flag. **Alternative si refusé :** ajouter une question o/N à la première saisie.

## D6 — Backlog accepté en l'état (revue finale phase A)

Mineurs reportés volontairement, à reprendre si gênants : pas de `asset edit/rm` (la
prévention de doublons rend l'état non-réparable improbable) ; `config set` ne valide pas
les clés ; pas de verrou inter-processus (à revoir en phase D quand `serve` coexistera avec
la CLI) ; fenêtre de crash entre les deux renames du Save (récupérable via .bak/.tmp) ;
`Statement` sur un titre coté accepté (la sémantique sera fixée par la valorisation en
phase B). Ajouts de la revue finale projet : résolution par préfixe non ambigu (le
spec-exemple « add cw8 » exige un --id explicite aujourd'hui) ; validation de
default-account au moment du config set ; dashboard web à dégrader comme chart quand
le FX manque (aujourd'hui : page d'erreur propre, mais le bouton refresh est sur le
dashboard lui-même).

## D7 — Le fichier .fin reste en version 1 avec le cache marché

**Contexte :** la phase B ajoute prices/fx/dividends dans le JSON du Book. Un binaire
phase A qui réécrirait ce fichier perdrait silencieusement ces champs. **Choix :** pas de
bump de version — ces champs sont des caches refetchables (un `finador refresh` les
reconstruit), et toutes les phases sont livrées ensemble. **Alternative si refusé :**
bump l'octet de version à 2 et refuser les versions inconnues.

## D8 — Première estimation/relevé = adoption (flux), pas performance

**Contexte :** la revue finale de phase C a montré qu'un bien estimé pour la première fois
en cours d'historique (ou un compte ajouté par son premier relevé) faisait exploser le TWR
du patrimoine (+4300 %) : la valeur saute sans flux compensateur. **Choix :** le premier
relevé d'un couple (compte, actif) et le premier relevé de cash d'un compte sont traités
comme des apports externes (adoption) dans les séries de performance ; les relevés suivants
mesurent la performance (intérêts d'un livret, revalorisation d'un bien). Conséquence
assumée : pour un compte alimenté par deposits PUIS réconcilié par un premier relevé,
l'écart de première réconciliation est compté comme apport, pas comme performance.
**Alternative si refusé :** documenter le piège et exiger un deposit initial explicite.

## D9 — Web sans authentification, lié à 127.0.0.1

**Contexte :** la spec §8 prévoit un serveur local sans auth web (le déverrouillage
se fait au lancement, dans le terminal). **Choix :** bind par défaut 127.0.0.1:8451,
avertissement très visible pour tout autre bind, aucun cookie/session. Pas de verrou
inter-processus CLI/serve (D6 backlog) : dernière écriture gagnante, sauvegardes
atomiques — acceptable mono-utilisateur. **Alternative si refusé :** basic auth
optionnelle (--auth user:pass) ou socket Unix.

## D11 — Arborescence web en details/summary natifs, portée d'intersection

**Contexte :** la répartition demandait des niveaux dépliables et des niveaux croisés
cliquables (enveloppe ∩ groupe) sans JavaScript. **Choix :** <details>/<summary>
natifs stylés au thème ; nouvelle portée ByAccountGroup (URL canonique
/account/x/group/y — un motif Go ne supporte pas un segment multiple au milieu, pas
de forme réciproque) ; agrégation des groupes au premier segment dans les arbres ;
« liquidités » non cliquable (pas de page propre). **Alternative si refusé :**
arbre toujours déplié, ou page dédiée au cash par enveloppe.

## D10 — Concurrence inter-processus : verrouillage optimiste

**Contexte :** CLI et serve peuvent écrire le même fichier (D9 : dernière écriture
gagnante). Un verrou exclusif dur bloquerait la CLI pendant toute la durée de serve.
**Choix :** verrouillage optimiste — chaque File retient (taille, mtime ns) à
l'ouverture ; Save vérifie sous flock que le disque n'a pas bougé, sinon
ErrConcurrent (« relancez »). serve, après une écriture CLI, refusera ses propres
sauvegardes jusqu'à redémarrage : préférable à l'écrasement silencieux.
**Alternative si refusé :** verrou dur par processus, ou rechargement automatique
du Book dans serve sur détection de changement.

## D12 — Interface intégralement en anglais (v0.4)

**Contexte :** demande utilisateur — README, CLI et web en anglais ; seul le contenu
fourni par l'utilisateur (noms de groupes/enveloppes/notes) reste libre. **Choix :**
toutes les chaînes visibles traduites (erreurs comprises) ; formats web anglais
(1,234.56 €, Wednesday 10 June 2026) ; noms de périodes anglais (1d…1y, prev-yr,
inception) — ce sont des ENTRÉES CLI, changement cassant assumé ; --by group|account ;
paramètre web ?by=. Les commentaires de code et docs/superpowers/ restent en français
(pas une surface utilisateur). Le README perd ses sections changelog.

## D14 — Création à la volée, retrait « by asset », donut d'allocation (v0.7)

**Création à la volée web :** un compte inconnu saisi dans le formulaire de transaction
naît EUR / taxe `none` ; un actif inconnu naît `security` avec `ticker = saisie` (mêmes
règles que l'import CSV). Un typo crée donc une entité — assumé, car l'import CSV fait
pareil, et `asset rm` / `tx edit` permettent de corriger. L'ambiguïté (préfixe non
unique) est propagée comme erreur 400 sans création.

**Onglet « by asset » retiré :** redondant avec l'onglet Assets qui affiche une ligne
dense par position. `?by=asset` est normalisé silencieusement vers `group` ; `flatAssets`
et son test sont supprimés comme code mort. Les onglets restants sont `by group` et
`by account`.

**Camembert (donut) d'allocation :** SVG serveur rendu par `chart.Pie` (taille 190 px),
parts = groupes de tête + cash agrégés depuis `portfolio.Breakdown`, triés par montant
décroissant avant l'affectation des couleurs. Palette feutrée `chart.PiePalette` cyclée
(huit teintes papier). Légende HTML à droite : pastille couleur, libellé (lien groupe
quand applicable), pourcentage arrondi, montant. Les couleurs venant d'une palette
constante, `Color` est typé `template.CSS` pour éviter l'échappement html/template.
Sections `property` incluses dans les poids (c'est du poids d'allocation).

## D13 — Sparklines 1W/1M/1Y (pas de « day »)

**Contexte :** la demande était day/week/month, mais le cache ne contient que des
clôtures QUOTIDIENNES (interval=1d) : une sparkline « day » n'aurait qu'un point.
**Choix :** fenêtres 1W (8 points), 1M (31), 1Y (série complète), couleur selon la
dérive de la fenêtre. **Alternative si refusé :** récupérer de l'intraday Yahoo
(interval=15m, non caché, casse --offline) ou élargir le modèle de cache.

## D15 — Format v2 : journal append-only, diff-on-save, cache en sidecar

**Contexte :** stocker le `.fin` dans un dépôt git synchronisé multi-machines (usage
séquentiel). Spec : `specs/2026-06-13-format-append-log-design.md`.
**Choix :** (1) grand-livre = journal append-only chiffré, **texte base64, 1 record/ligne**
(un versement = +297 o, 1 ligne ; historique git ~20× plus petit qu'un blob réécrit) ;
(2) **diff-on-save** plutôt qu'un store event-native — l'API de mutation existante reste
intacte, le store calcule le diff vs l'état persisté et n'ajoute que les records utiles,
ré-émettant les lignes inchangées byte-identiques (churn de code minimal) ;
(3) intégrité par **chaînage AAD + ligne de tête authentifiée** (anti-réordre/suppression/
troncature) ; (4) cache marché sorti vers un **sidecar local chiffré** (`os.UserCacheDir()`),
régénérable, hors git ; (5) **abandon total de FINADOR1, aucune migration** (validé : pas
d'utilisateurs réels). **Alternatives si refusé :** store event-native (plus pur, plus
invasif) ; cache laissé dans le fichier en section figée (portable hors-ligne mais
~1,3–3 Mo de croissance git par refresh commité) ; garder FINADOR1 en parallèle.
**Noté pour plus tard :** fallback Stooq quand Yahoo 429/down (cf. `../portfodor/`).

## D16 — Format v3 : ids random + timestamps, comptes déclaratifs, CLI noun-first, format ouvert, merge

**Contexte :** itération de design (2026-06-13) sur le format v2. Spec :
`specs/2026-06-13-format-v3-identity-cli-merge-design.md`. Pré-release, zéro utilisateur → pas de
migration, bump version fichier 2→3, `demo.fin` régénéré.
**Choix :** (1) **id random trié-par-temps** (`base32(ms‖rand)`, ~22 car.) pour comptes/actifs/
transactions — remplace slug ET `TxID uint64` ; raison clé : merge-safe **et** pas d'algorithme
`Slugify` à réimplémenter pour un portage alternatif (Android) ; noms/alias/tickers = poignées de
résolution, résolution par préfixe. (2) **`ts` par enregistrement** (RFC3339 ns) pour l'ordre et le
futur merge. (3) Enveloppe uniforme `{k,ts,d}`, `LastTxID` supprimé. (4) **Comptes en déclaration
obligatoire** (EnsureAccount strict, rejet web/import), `account rm`, `--alias` au create ; actifs
gardent la création à la volée (Q1). (5) **CLI noun-first** (`account|asset|cash` groupent leurs
verbes ; `add`→`asset buy`, `deposit`→`cash deposit`, etc.) avec **bloc `Example` sur chaque
commande** (usage espacé). (6) **`docs/FORMAT.md`** anglais + vecteurs de test + politique
d'extensibilité (refuser `v` inconnu ; ignorer champ inconnu d'un `k` connu ; rejeter `k` inconnu).
(7) **`finador merge`** : union + LWW par `ts` + prompt de conflit même-instant/même-champ.
**Crypto/framing inchangés vs v2.** **Alternatives écartées :** garder les slugs (couple les
implémentations à Slugify) ; `add` à valeur signée pour vendre (implicite, illisible dans un an) ;
verbes top-level plats (ambigus). **Vente d'un bien :** cash découplé (clôture `asset set 0` à la
vente ; `cash set` sur le compte réel plus tard).

## D17 — Labels libres sur les couples (compte, actif)

**Contexte :** un utilisateur veut taguer une *position* — un couple (compte, actif) précis — avec
un nombre quelconque de labels nominaux libres (« le CW8 de mon PEA » → `retraite`, `core`).
Substrat pour un futur reporting de performance par sous-ensemble (hors scope ici : seulement le
modèle, la gestion CLI, l'affichage web). Pré-release : on **étend v3 sans bump** (deux nouveaux
`k` ajoutés avant le gel du format).
**Choix :** **chaque assignation de label est sa propre entité à id random** (`domain.Label{ID,
Account, Asset, Name}`), pas un set par couple. Nouveaux enregistrements `label` (upsert par `id`)
et `label-del` (tombstone par `id`), enveloppe `{k,ts,d}` habituelle. `account` et `asset` sont
tous deux **requis aujourd'hui** ; vide **réservé** à un futur « wildcard » (tous comptes / tous
actifs) — évolution additive, pas de nouveau `k`. **Raison clé :** quantité arbitraire par couple
(plusieurs entités label) **et** deux machines taguant le même couple **fusionnent en union sans
conflit** (chacune ajoute un id distinct) — les labels passent par la **machinerie générique d'id**
du merge (`classOf`/`isTombstone` seulement, `entityID` lit déjà `d.id`). Gestion CLI noun-first
`label add|rm|list` (groupe `setup`) ; affichage web : chips sur les feuilles de l'arbre « by
account » via `Book.LabelsFor(account, asset)` (lecture seule).
**Alternative écartée :** un **set de noms par couple** (`map[(account,asset)][]string`) — clé
composite, et surtout merge plus délicat (fusion de listes intra-entité au lieu d'une union d'ids
triviale) ; rejeté.

## D18 — Données dans un dépôt privé GitHub (modèle remote optionnel)

**Contexte :** l'usage réel visé (data `.fin` dans un repo privé GitHub, multi-machines). Spec :
`specs/2026-06-13-github-remote-data-design.md`. Design approuvé en brainstorming.
**Choix :** (1) deux modes — `local` (défaut/fallback) et `github` (opt-in) ; précédence
`--db`/`FINADOR_DB` > conf. (2) Localisation en **conf externe** `~/.config/finador/config.json`
(**JSON stdlib, pas de dépendance YAML**) — externe car nécessaire avant de déchiffrer le fichier.
(3) Auth = **fine-grained PAT** scopé au seul repo (*Contents R/W*), au **Keychain** (`keyring`
étendu `GetSecret/PutSecret` longue durée), override `GITHUB_TOKEN`. (4) Transport = **API Contents
GitHub** HTTPS pur (pas de `git`/clone), `GET`(contenu+`sha`)/`PUT`(contenu+msg+`sha`) → chaque push
= un commit (historique + petits deltas append-log) ; interface `Backend` comme seam. (5) Sync :
**lecture** pull si copie > 1h (configurable), **écriture** pull-avant + push-après, `finador sync`
force. (6) **Conflit** distant → re-fetch + **`merge`** (le paiement des ids random + `ts`) → re-push.
(7) **Hors-ligne souple** : écriture locale + `dirty` + push différé. (8) Cache marché **reste
local**, seul le grand-livre chiffré voyage ; `store` **inchangé** (copie de travail locale).
**Alternatives écartées :** `git` shellé / go-git (lourd pour un seul petit fichier — Contents API
suffit) ; YAML pour la conf (dépendance, surdimensionné pour 3 clés) ; hors-ligne strict (refus —
moins souple, et `merge` couvre la divergence).

## D19 — Données de marché multi-sources (fallback type portfodor)

**Contexte :** finador ne cote que via Yahoo ; les fonds atypiques par ISIN (LU0131510165,
LU1832174962…) y sont absents. Spec : `specs/2026-06-13-multi-source-market-data-design.md`. Run
autonome. Étude de `../portfodor/pkg/marketdata` + test de faisabilité live.
**Choix :** (1) le fetch passe une **`Ref{Symbol, ISIN}`** ; interface `Provider` (Daily +
`ErrNotCovered`) ; un `Multi` (impl. `Source`) chaîne **Yahoo → FT → Morningstar** et renvoie le
1er succès. (2) **Financial Times = provider clé** des fonds FR/LU (search `searchsecurities`→xid,
puis POST `chartapi/series`→NAV quotidien EUR) — **vérifié live** sur les 2 fonds cibles. (3)
**Morningstar via Boursorama** = fallback défensif (Boursorama `recherche/ajax`→id `0P…`, puis
`tools.morningstar.fr/.../timeseries_price`) ; non vérifiable d'ici (endpoint NAV renvoie `[]`) mais
porté best-effort. (4) **Yahoo reste primaire** pour les tickers ; `Resolve` (asset add) inchangé.
(5) **Zéro dépendance** : portfodor est 100 % stdlib (regex pour le HTML) — finador aussi. (6) Cache
sidecar **inchangé** (les séries y vivent déjà) ; pas de cache de résolution (re-search à chaque
refresh, acceptable). `Refresh` « never fails hard » préservé (fallback périmé naturel).
**Hors scope (documenté) :** l'**Eres `990000118919`** (code AMF FCPE/PEE) n'est coté par aucun
provider (portfodor non plus) → `asset set` manuel ; Stooq (ticker-only) ; pin de catalogue xid.
