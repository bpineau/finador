# Finador - format de stockage v2 : journal append-only + cache sidecar

*2026-06-13 - spec validée en brainstorming. Run autonome : pas de relecture demandée.*

## 0. Objectif et contexte

Le fichier de données doit pouvoir **vivre dans un dépôt git synchronisé** (GitHub),
utilisé séquentiellement depuis plusieurs machines. Trois propriétés visées :

1. **Diff minimal** - un petit changement logique (un versement de liquide) ne change
   qu'une **petite partie** du fichier, pas tout. Git ne doit pas regrossir d'un blob
   complet à chaque commit.
2. **Text-like** - le fichier est du texte (base64), un enregistrement par ligne :
   `git diff` montre « +1 ligne », et git delta-compresse parfaitement l'append-only.
3. **Sécurité intacte** - confidentialité équivalente au format actuel ; il est acceptable
   que les **tailles** des sections soient visibles (un observateur voit que certaines
   parties sont plus grosses que d'autres).

Contrainte utilisateur : **un seul fichier physique** à transporter (le cache marché,
régénérable, est local et ne voyage donc pas - il ne compte pas comme « un fichier de
plus à transporter »).

**Pas d'utilisateurs réels encore : on ABANDONNE totalement le format `FINADOR1`.
Aucune migration. Le format v2 repart de zéro ; `demo.fin` est régénéré.**

### Pourquoi append-only (chiffres mesurés)

Scénario : 100 records à t0 (10 comptes + 50 actifs + 40 tx d'ouverture), puis 10 tx/mois.

| | blob+gzip (actuel) | append-log base64 (v2) |
|---|---|---|
| Fichier à 10 ans (1240 tx) | 16,3 Ko | 374,9 Ko |
| **Historique git cumulé à 10 ans** (1 commit/tx) | **~10,7 Mo** | **~0,4–0,6 Mo** |
| Delta par transaction | réécrit tout le fichier | **+297 o (1 ligne)** |

Le fichier de travail est ~20× plus gros (pas de compression globale + crypto/base64 par
enregistrement), **mais l'historique git est ~20× plus petit et insensible à la fréquence
de commit** : git ne stocke que les lignes ajoutées (delta append-only), alors qu'il ne
peut rien delta-compresser sur un blob chiffré (nonce aléatoire → bytes 100 % différents
à chaque save). 375 Ko après 10 ans reste trivial en absolu.

Le cache marché (séries de prix/FX/dividendes) pèse ~1,3–3 Mo pour 100 titres + 1 FX sur
10 ans et **se réécrit en entier à chaque `refresh`** : laissé dans le fichier synchronisé,
il dominerait la croissance git (~70–150 Mo/an). Étant **régénérable** (re-fetch Yahoo, un
`refresh` ≈ 30–90 s), il sort vers un sidecar local.

## 1. Décisions actées

| Sujet | Décision |
|---|---|
| Grand-livre | **Journal append-only chiffré**, texte base64, **un enregistrement par ligne** |
| Records | Union taguée : `acct`, `asset`, `config`, `tx`, et corrections `tx-edit` / `tx-del` (+ `acct`/`asset` ré-émis pour édition, `*-del` pour suppression) |
| Matérialisation | Le `Book` est une **vue rejouée** depuis le log à l'ouverture |
| Écriture | **Diff-on-save** : l'API de mutation existante (`b.Add`, `b.RemoveTx`, `tx edit`…) est INCHANGÉE ; le store calcule le diff vs l'état persisté et **n'ajoute que les records nécessaires** ; les lignes inchangées sont ré-émises **byte-identiques** |
| Chiffrement | AES-256-GCM par enregistrement, **nonce aléatoire 96 bits propre** ; clé via Argon2id (mêmes paramètres qu'aujourd'hui) puis **sous-clés HKDF** (une pour le log, une pour le cache) |
| Intégrité du log | AAD = `hash(en-tête) ‖ seq ‖ tag-précédent` (**chaînage**) + **ligne de tête authentifiée** (compte + tag de fin) contre la troncature |
| Cache marché | **Sidecar chiffré** dans `os.UserCacheDir()/finador/<id>.cache`, hors du fichier synchronisé, format **source-agnostique** (futur fallback Stooq) |
| Édition du passé | Déjà fonctionnelle (`tx edit`/`tx rm` CLI + web) ; en v2 ces mutations deviennent des records de correction/tombstone. Compaction = commande de maintenance optionnelle |
| Extension | `.fin` conservé (désormais un fichier **texte**) |
| Hors scope | Fusion multi-machines concurrente (usage séquentiel assumé) ; fallback Stooq (noté pour plus tard) |

## 2. Format du fichier grand-livre (`.fin`, texte)

Fichier UTF-8, lignes terminées par `\n`. Trois parties.

### Ligne 1 - en-tête (clair, lisible, JSON)

```json
{"fmt":"finador-ledger","v":2,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"<base64>","id":"<base64-128bits>"}
```

- `t`/`m`/`p` : paramètres Argon2id (passes / mémoire KiB / threads), bornés comme
  aujourd'hui (lecture avant authentification → garde-fous anti-bombe).
- `salt` : 16 octets aléatoires (dérivation de clé).
- `id` : identifiant de fichier aléatoire 128 bits, généré à la création. Sert (a) à
  **nommer le sidecar** de façon déterministe et stable entre machines, (b) à **lier** les
  records à ce fichier précis (anti-greffe d'un autre fichier). L'en-tête est en clair mais
  **authentifié** : son hash entre dans l'AAD de chaque record.

### Lignes 2..N+1 - enregistrements (un par ligne)

```
base64( nonce[12] ‖ AES-256-GCM-Seal(plaintext, AAD) )
```

- `plaintext` = JSON compact : `{"k":"<kind>","d":{…}}`.
  - `k` ∈ {`acct`, `asset`, `config`, `tx`, `tx-edit`, `tx-del`, `acct-del`, `asset-del`}.
  - `d` = la valeur (struct domaine pour create/edit ; `{"id":…}` pour les `*-del`).
- `AAD` = `SHA-256(octets-ligne-1) ‖ uint64be(seq) ‖ prevTag[16]`
  - `seq` : index 1-based du record.
  - `prevTag` : les 16 octets de tag GCM du record précédent (16 zéros pour le premier).
    → **chaînage** : réordonner ou supprimer un record du milieu casse la chaîne au rejeu.
- Clé : **sous-clé `log`** (cf. §4).

### Dernière ligne - tête authentifiée (anti-troncature)

```
base64( nonce[12] ‖ AES-256-GCM-Seal({"count":N,"head":"<base64 tag du record N>"}, AAD_head) )
AAD_head = SHA-256(octets-ligne-1) ‖ "finador-head" ‖ uint64be(N)
```

Réécrite à chaque save (c'est la dernière ligne ; les records précédents restent
byte-identiques). Au chargement, on vérifie `count` == nombre de records et `head` == tag
du dernier record → détecte une **troncature** (suppression de records de fin) ou un compte
falsifié.

### Lecture (Open)

1. Parser la ligne 1 ; dériver la clé (Argon2id + HKDF) - nécessite le mot de passe.
2. Pour chaque ligne de record, en maintenant `seq` et `prevTag`, reconstruire l'AAD et
   `GCM-Open`. **Tout échec d'ouverture → `domain.ErrBadPassword`** (mot de passe faux et
   altération indistinguables, comme aujourd'hui). Décoder le JSON du record.
3. Vérifier la ligne de tête (count + head). Échec → erreur d'intégrité explicite.
4. **Rejouer** les records en ordre pour matérialiser le `Book` (cf. §3).

### Écriture (Save) - diff-on-save, préfixe byte-stable

Le store retient, depuis l'ouverture : la **liste ordonnée des lignes de records**
(chaînes verbatim + record décodé) et un **instantané** de l'état matérialisé. À chaque
`Save()` :

1. Lock sidecar + contrôle de concurrence optimiste (`diskStamp`) - **réutilisés tels quels**.
2. **Diff** `Book` courant vs instantané, par identité stable (Account.ID, Asset.ID,
   TxID, clés de Config) :
   - présent maintenant, absent avant → record **create** (`acct`/`asset`/`config`/`tx`) ;
   - présent des deux côtés mais différent (deep-equal) → record **edit** (`tx-edit`, ou
     `acct`/`asset` ré-émis) ;
   - absent maintenant, présent avant → record **delete** (`tx-del`/`acct-del`/`asset-del`).
   Ordre d'émission dans le batch : `config`, `acct`, `asset`, puis `tx*` (les définitions
   avant les transactions qui les référencent).
3. Sceller **uniquement** les nouveaux records (nonce frais, chaînés sur le dernier tag).
4. Écrire `ligne1 ‖ lignes records (anciennes verbatim + nouvelles) ‖ ligne de tête` via
   **tmp + fsync + rename**, `.bak` rotation - **réutilisés tels quels**.
5. Mettre à jour instantané + liste de lignes.

Comme les lignes anciennes sont ré-émises **à l'octet près**, le préfixe du fichier est
inchangé : git ne voit que les lignes ajoutées + la ligne de tête modifiée.

> `LastTxID` n'est plus persisté : au rejeu, il vaut le **max des id** vus sur tous les
> records `tx` (y compris superséd­és/tombstoned), ce qui garantit l'absence de collision
> d'id même après suppression.

## 3. Records et rejeu

Le `Book` (domain) **perd le champ `Market`** côté persistance log (il reste en mémoire,
peuplé depuis le sidecar - cf. §5). Le rejeu fold les records :

| Record | Effet au rejeu |
|---|---|
| `acct` / `asset` / `config` | upsert par ID/clé |
| `acct-del` / `asset-del` | retire l'entité |
| `tx` | crée la transaction (ID porté par le record) |
| `tx-edit` | remplace la transaction de même ID |
| `tx-del` | tombstone : la transaction de cet ID n'apparaît pas dans le `Book` |

Le moteur `portfolio` recalcule tout le reste depuis le `Book` matérialisé : **aucun
changement** au-delà de la frontière de persistance. C'est l'architecture « registre
immuable, tout est dérivé » déjà en place (`tx.go`).

## 4. Dérivation de clés

```
master   = Argon2id(password, salt, t, m, p, 32)
keyLog   = HKDF-SHA256(master, info="finador-ledger-v2", 32)
keyCache = HKDF-SHA256(master, info="finador-cache-v2", 32)
```

`golang.org/x/crypto/hkdf` (déjà dans l'arbre de deps via x/crypto). Sous-clés distinctes
→ isolation cryptographique entre le log et le cache (aucune inquiétude de nonce
inter-fichiers). Le coût Argon2 (64 MiB) n'est payé qu'**une fois** par ouverture.

## 5. Sidecar cache marché (local, régénérable)

- **Emplacement** : `os.UserCacheDir()/finador/<id>.cache`, où `<id>` vient de l'en-tête du
  grand-livre → même nom logique sur toutes les machines, **physiquement hors du dépôt git**
  (donc rien à gitignorer). Si `UserCacheDir` échoue : repli à côté du `.fin` en `.<nom>.cache`.
- **Format** : simple (non synchronisé, le churn n'a pas d'importance) - petit en-tête
  `{"fmt":"finador-cache","v":2}` ‖ `nonce[12]` ‖ `GCM-Seal(gzip(JSON(MarketData)), keyCache)`.
  Chiffré (le commentaire de `MarketData` rappelle que la liste des tickers est sensible).
- **Source-agnostique** : structure inchangée si un jour les points viennent de Stooq.
- **Cycle de vie** : `Open` charge le log → `Book` (Market vide), puis charge le sidecar et
  peuple `Book.Market` (absent/illisible → Market vide, un `refresh` reconstruit, jamais
  d'erreur dure). Une nouvelle méthode `SaveCache()` écrit le sidecar ; elle est appelée par
  les chemins qui touchent `Book.Market` (`refresh`, `RemoveAsset` qui purge le cache d'un
  actif). `Save()` (log) et `SaveCache()` (sidecar) sont **indépendants**.

## 6. Édition du passé - interface

**Déjà fonctionnelle**, à conserver et documenter, jamais restreinte à une fenêtre récente :

- **CLI** : `finador tx edit <id> [--date --account --asset --qty --total --note --kind]`,
  `finador tx rm <id>` (existent). En v2 ces mutations passent par le diff-on-save → records
  `tx-edit` / `tx-del`. **Doc complète** : section dédiée du README (corriger une saisie,
  changer d'enveloppe, supprimer un doublon, avec exemples), et help cobra à jour.
- **Web** : `txEditSubmit` / `txDelete` existent ; vérifier que la liste des transactions
  expose **édition et suppression pour chaque ligne** (parité CLI/web).
- **Compaction** : nouvelle commande de maintenance `finador compact` - réécrit un log
  minimal propre (drop des superséd­és/tombstones, renumérotation `seq`, nouveaux nonces et
  chaîne). Réécrit tout le fichier (un gros commit ponctuel) ; **optionnelle et rare** (vu
  le faible volume de corrections, souvent jamais nécessaire).

**Conflits** : usage séquentiel assumé. Si deux machines divergent sans s'être
synchronisées, la réconciliation git est manuelle (on garde un fichier) - accepté car rare.

## 7. Impacts sur le code

- **`internal/store`** : réécriture du cœur (`Open`/`Save`/`Create`/header/crypto). Nouveau
  type interne « entry » (ligne verbatim + record décodé), diff-on-save, rejeu, intégrité de
  chaîne + tête, HKDF, `SaveCache()`/chargement sidecar. `diskStamp`, flock, tmp+fsync+rename,
  `.bak` : **réutilisés**.
- **`internal/domain/book.go`** : `Market` n'est plus dans le périmètre persisté par le log
  (le champ reste pour le moteur). `LastTxID` dérivé au rejeu (peut quitter la sérialisation).
- **`internal/market`** : les chemins `refresh` appellent `SaveCache()` au lieu de compter
  sur `Save()` du Book entier.
- **`internal/cli` / `internal/web`** : aucun changement d'API de mutation (diff-on-save
  transparent). Ajouts : `finador compact`, doc README de l'édition, vérif parité web.
- **Tests** : `store_test` réécrit (round-trip log, intégrité, diff-on-save, byte-stabilité
  du préfixe, corruption détectée) ; sidecar testé ; `demo.fin` régénéré.

## 8. Critères de réussite

1. Ajouter une transaction = **une ligne ajoutée** ; `git diff` le confirme ; le préfixe du
   fichier est byte-identique.
2. Round-trip complet (create → mutations → edits → deletes → reopen) reconstruit un `Book`
   identique au modèle attendu.
3. Toute altération (flip d'octet, réordonnancement, suppression, troncature) est **détectée**
   (ErrBadPassword ou erreur d'intégrité), jamais silencieuse.
4. Mot de passe faux et fichier altéré restent indistinguables.
5. Le cache marché vit hors du fichier synchronisé ; sa perte coûte un `refresh`, jamais une
   donnée utilisateur.
6. `tx edit`/`tx rm` corrigent n'importe quelle transaction passée, en CLI (documenté) et web.
```
