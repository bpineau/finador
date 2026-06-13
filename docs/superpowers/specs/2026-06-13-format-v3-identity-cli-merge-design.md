# Finador — format v3 : identité random + timestamps, discipline comptes, CLI noun-first, format ouvert, merge

*2026-06-13 — spec validée en brainstorming. Run autonome : pas de relecture demandée.*

Étend le format append-only v2 (`2026-06-13-format-append-log-design.md`). **Pré-release, aucun
utilisateur réel** → le format évolue librement, **aucune migration**, `demo.fin` régénéré. La
version du fichier passe de **2 à 3** (le schéma des enregistrements change : ids + timestamps).

## 0. Objectifs

1. **Identité robuste au merge et indépendante des implémentations** : chaque entité a un id
   **random** (plus de slug ni d'entier incrémental), généré localement, sans collision
   inter-machines, sans algorithme partagé à réimplémenter.
2. **Timestamps** : chaque enregistrement porte un horodatage précis → ordre d'apparition, et
   socle du futur merge (last-writer-wins).
3. **Comptes en déclaration obligatoire** : zéro création de compte à la volée (anti-typo).
4. **CLI claire et intuitive** (usage espacé) : **noun-first**, exemples dans chaque `-h`.
5. **Format ouvert et intensément documenté (anglais)** pour des implémentations alternatives
   (ex. Android natif).
6. **Merge** explicite pour réconcilier des saisies faites sur des machines désynchronisées.

## 1. Décisions actées

| Sujet | Décision |
|---|---|
| Identité | Id **random trié-par-temps** pour comptes, actifs, transactions. Remplace slug (comptes/actifs) **et** `TxID uint64` (transactions). |
| Format de l'id | `base32-crockford( ms_unix[6 o big-endian] ‖ random[8 o] )`, minuscule, sans padding (~22 car.), **triable lexicographiquement par date de création**. |
| Résolution | Noms / alias / tickers restent les **poignées humaines** ; références stockées en id ; affichage résout id→nom. Résolution par **préfixe** partout (comme les SHA courts de git). |
| Timestamp | Champ `ts` (RFC3339 nanosecondes) sur **chaque** enregistrement (create/edit/delete). |
| Enveloppe | Uniforme : `{"k":…,"ts":…,"d":{…}}` ; l'`id` de l'entité vit dans `d`. |
| `LastTxID` | **Supprimé** (l'allocation incrémentale disparaît). |
| Version | En-tête `v:3` ; un lecteur **refuse** un `v` inconnu. |
| Extensibilité | Champs inconnus d'un `k` connu **ignorés** (ajout additif sans bump) ; **nouveau `k` ⇒ bump de version** (jamais d'enregistrement ignoré silencieusement). |
| Comptes | **Déclaration obligatoire** : plus de création à la volée ; transaction sur compte inconnu **rejetée** (web + import). `account rm` + contrôle de références. `--alias` dès `account add`. |
| Actifs | **Gardent** la création à la volée (résolution ticker/Yahoo filtre les typos ; l'import en a besoin). |
| CLI | **Noun-first** : `account`, `asset`, `cash` groupent leurs verbes ; **bloc `Example` sur chaque commande** ; groupes top-level. |
| Doc | Section **Recipes** du README + **`docs/FORMAT.md`** (spec d'implémentation anglais + vecteurs de test). |
| Merge | Commande `finador merge <autre.fin>` : union + LWW par `ts` + prompt de conflit. |
| Crypto/framing | **Inchangés** vs v2 (AES-256-GCM par enregistrement, AAD chaînée `hash(en-tête)‖seq‖prevTag`, ligne de tête, sous-clés HKDF, sidecar). Seul le schéma du `d` et l'enveloppe évoluent. |

## 2. Identité & schéma d'enregistrement (v3)

**Id random trié-par-temps.** Généré à la création d'une entité :
`id = lower(base32crockford( uint48be(unixMillis) ‖ rand(8 o) ))`. 14 octets → ~22 caractères.
Le préfixe temporel rend les ids triables chronologiquement ; les 64 bits aléatoires garantissent
l'unicité inter-machines. **Aucune dépendance externe** (rng + base32 maison). Pourquoi pas un
slug : un slug obligerait chaque implémentation alternative à reproduire `Slugify` à l'identique ;
un id random n'a aucune dépendance de ce genre.

**Enveloppe uniforme** (clair une fois déchiffré, un enregistrement = une ligne base64) :

```
{"k":"acct",     "ts":"2026-06-13T18:22:09.481204Z", "d":{"id":"01h2…","name":"PEA BforBank","ccy":"EUR","tax":{…},"aliases":["pea"]}}
{"k":"asset",    "ts":"…",                            "d":{"id":"01h2…","kind":1,"name":"Amundi MSCI World","ticker":"CW8.PA","ccy":"EUR","group":"equities/world"}}
{"k":"tx",       "ts":"…",                            "d":{"id":"01h2…","date":"2024-01-20","account":"<acct-id>","asset":"<asset-id>","kind":1,"qty":"20","amount":{"amount":"9000","ccy":"EUR"}}}
{"k":"tx-edit",  "ts":"…",                            "d":{"id":"<même tx-id>", …}}
{"k":"acct-del", "ts":"…",                            "d":{"id":"<acct-id>"}}
```

`k` ∈ `acct | acct-del | asset | asset-del | config | tx | tx-edit | tx-del`. Le rejeu (fold)
applique create/upsert/delete par id, comme en v2 ; `LastTxID` n'est plus dérivé.

**Références.** Une transaction stocke les **ids** de son compte et de son actif. L'affichage
(CLI/web) résout id→nom. La saisie utilise nom/alias/ticker/**préfixe d'id** (`tx edit 01h2k` ok).

**Ordre.** Les transactions s'affichent par `Date` métier (inchangé) ; `ts` sert de départage et
de clé d'ordre pour le merge.

**Extensibilité (le « format ouvert »).** L'en-tête porte `v:3`. Règles, documentées dans
`FORMAT.md` :
- Un lecteur **doit refuser** un `v` qu'il ne supporte pas.
- Dans une version donnée, un lecteur **doit ignorer les champs inconnus** d'un `k` connu →
  on peut **ajouter un champ optionnel sans bump** (rétro/avant-compatible).
- Un **nouveau `k`** (ou un changement de sémantique) **exige un bump de version** : un ledger
  financier ne doit **jamais** ignorer en silence un enregistrement qu'il ne comprend pas.

## 3. Discipline comptes / actifs

- `portfolio.EnsureAccount` devient un **resolve strict** : compte inconnu ⇒ erreur explicite
  « unknown account "X" — declare it first with `finador account add` ». Concerne le formulaire
  web (`web/tx.go`) **et** l'import CSV (`portfolio/import.go`).
- `EnsureAsset` **inchangé** (création à la volée conservée pour les actifs).
- **`account rm <ref>`** (+ `domain.RemoveAccount`) : refuse si des transactions référencent le
  compte (symétrie avec `asset rm`).
- **`account add --alias`** (répétable), comme `asset add`.
- Les alias illimités existent déjà (`account edit --add-alias/--rm-alias`) — conservés.

## 4. CLI noun-first

Trois noms = les trois choses manipulées ; on part du nom, l'aide enseigne le reste.

```
SETUP
  finador init
  finador account  add | list | edit | rm            envelopes (PEA, CTO, banque…)
  finador asset    add | list | edit | rm            déclarer titres & biens
  finador config   …

RECORD
  finador asset    buy | sell                         titre coté : quantité + prix
  finador asset    dividend | fee                     revenu / frais sur un titre
  finador asset    set                                valeur constatée d'un bien / non-coté
  finador cash     deposit | withdraw                 cash externe entrant/sortant d'une envelope
  finador cash     set                                solde constaté d'une envelope
  finador tx       list | edit | rm                   corriger n'importe quelle ligne passée

VIEW
  finador value | perf | chart

SYNC & OPS
  finador import | refresh | merge | compact | lock | serve
```

Migration des commandes actuelles : `add`→`asset buy`, `sell`→`asset sell`, `deposit`→
`cash deposit`, `withdraw`→`cash withdraw`, `cash set`→inchangé, `asset set`→inchangé ; nouvelles
`asset dividend`, `asset fee`. Top-level regroupé via les **groupes cobra** (Setup/Record/View/
Sync & Ops).

**Règle « lequel utiliser » (martelée dans la doc ET chaque `-h`)** :

| Tu veux… | Commande | Perf |
|---|---|---|
| Acheter/vendre un titre coté (quantité + prix) | `asset buy/sell` | trade, base de coût |
| Dividende reçu / frais payés sur un titre | `asset dividend/fee` | revenu / coût |
| Constater la **valeur** d'un bien / non-coté | `asset set` | écart vs précédent = **performance** |
| Faire **entrer/sortir** du cash externe d'une envelope | `cash deposit/withdraw` | **apport/retrait** (neutre) |
| Constater le **solde** d'une envelope | `cash set` | écart vs précédent = **performance** |

Crux à expliciter partout : **`deposit/withdraw` = flux externe (apport, neutre)** vs **`set` =
valeur/solde constaté (l'écart = performance)**.

**Exemples obligatoires** : chaque commande renseigne le champ cobra `Example` (visible dans
`-h`), pour un usage espacé sans mémoire.

## 5. Recipes (README, anglais)

Séquences complètes à documenter :

- **Bien immobilier acheté** : `account add "Patrimoine immo"` → `asset add "Appart Lyon" --kind
  property` → `asset set "Appart Lyon" 250000 --account "Patrimoine immo" --at <date>` (1ʳᵉ valo =
  acquisition) → revalorisations ultérieures `asset set …`.
- **Bien vendu** (cash **découplé**, fidèle à la pratique) : `asset set "Appart Lyon" 0 --at
  <date-vente>` (clôture) ; **plus tard, sur le compte réel** : `cash set "Compte X" <solde> --at
  <date-encaissement>` (ou `cash deposit`). Note honnête : entre les deux, le patrimoine reflète
  l'argent « en transit » (bien à 0, cash pas encore constaté). On **ne supprime pas** l'historique.
- **Cash** : déclarer `cash set <account> <solde>` ; enlever une déclaration erronée `tx rm <id>` ;
  « plus de cash » `cash set <account> 0`.
- **Titre** : `asset buy <ref> <qty> @<prix>` / `asset sell …`.
- **Corriger le passé** : `tx list` → `tx edit <id-prefix> …` / `tx rm <id-prefix>`.

## 6. `docs/FORMAT.md` (anglais, niveau implémentation)

Permet un portage indépendant (Android/Kotlin…). Contenu :
- **Crypto** : Argon2id (params + encodage), HKDF-SHA256 (chaînes `info` littérales
  `finador-ledger-v2`/`finador-cache-v2`), AES-256-GCM.
- **Framing** : schéma JSON de l'en-tête, ligne d'enregistrement `base64(nonce‖ciphertext)`,
  **octets exacts de l'AAD** (`sha256(en-tête)‖uint64be(seq)‖prevTag`), chaînage, ligne de tête
  authentifiée (compte + tag final).
- **Enregistrements** : tous les `k` + schéma JSON de `d`, format des **ids** et de `ts`.
- **Rejeu** (fold), règles d'append / diff-on-save, format du **sidecar** cache.
- **Politique de version & d'extensibilité** (§2).
- **Vecteurs de test** : passphrase + fichier exemple + `Book` décodé attendu, pour auto-validation
  d'une réimplémentation.
- Primitives volontairement universelles (AES-256-GCM, Argon2id, HKDF-SHA256, base64, gzip, JSON
  UTF-8) → implémentables sur Android/iOS/web.

## 7. `finador merge <autre.fin>`

Réconcilie deux journaux d'un **même** portefeuille (même passphrase ; chaque fichier a son
salt/sa clé). Notre chaîne d'intégrité interdisant de coller deux journaux à la main, le merge est
une commande qui **re-scelle** un journal unifié :
1. Ouvre les deux fichiers (déchiffre chacun avec sa clé).
2. **Union** des enregistrements ; pour une même entité, applique les éditions **par ordre de
   `ts`** (le dernier gagne).
3. **Conflit** = deux éditions de la **même entité**, touchant **le(s) même(s) champ(s)**, au
   **même instant**, avec des **valeurs différentes** → **détecté**, et l'utilisateur **choisit**
   laquelle conserver (prompt ; en non-interactif, échec explicite listant les conflits).
4. Re-scelle un journal minimal unifié sous l'en-tête du fichier cible (nouveaux nonces, nouvelle
   chaîne) ; `.bak` conservé.

Cas non concurrents (ajouts/suppressions/éditions d'entités **différentes**) : union triviale,
sans perte — c'est le bénéfice principal des ids random.

## 8. Phasage

1. **Identité + timestamps (v3)** : id random partout, `ts` par enregistrement, enveloppe, bump
   v3, drop `LastTxID`, résolution par préfixe, affichage id→nom. Régénérer `demo.fin`.
2. **Discipline comptes** : EnsureAccount strict, rejet web/import, `account rm`, `account add
   --alias`.
3. **CLI noun-first** : restructuration + `Example` partout + groupes + descriptions + règle.
4. **Doc** : README Recipes + `docs/FORMAT.md` + vecteurs de test.
5. **`finador merge`**.

## 9. Critères de réussite

1. Comptes/actifs/transactions portent un id random ~22 car. ; aucun slug ni entier incrémental
   subsistant dans le format ; l'affichage montre des **noms**, la saisie accepte nom/alias/ticker/
   **préfixe d'id**.
2. Chaque enregistrement porte un `ts` ; round-trip et rejeu préservent ids et `ts`.
3. Une transaction sur un **compte inconnu** est **rejetée** (CLI, web, import) ; un actif inconnu
   reste créable à la volée.
4. `finador -h` se lit en groupes ; **chaque commande montre un exemple** dans son `-h`.
5. `account rm` refuse un compte référencé ; `account add --alias` fonctionne.
6. README Recipes couvre bien immo (achat/revalo/vente cash-découplée), cash, titres, correction.
7. `docs/FORMAT.md` permet une réimplémentation (vecteurs de test inclus) ; lecteur refuse un `v`
   inconnu, ignore les champs inconnus d'un `k` connu, rejette un `k` inconnu.
8. `finador merge` : union sans perte pour entités distinctes ; LWW par `ts` ; conflit même-instant/
   même-champ détecté et tranché par l'utilisateur. Suite complète verte, vet + lint propres.
