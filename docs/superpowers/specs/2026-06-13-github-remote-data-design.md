# Finador - données dans un dépôt privé GitHub (modèle remote, optionnel)

*2026-06-13 - spec validée en brainstorming (design approuvé). Run autonome : pas de relecture demandée.*

## 0. Objectif

Permettre que le fichier `.fin` **vive dans un dépôt privé GitHub**, synchronisé
automatiquement, utilisable depuis plusieurs machines - l'usage réel visé par l'auteur
(cf. `roadmap-and-github-data-model`). Le mode **fichier local reste le défaut et le
fallback** ; GitHub est **opt-in**. Tout le travail de format précédent (append-log,
ids random, timestamps, `merge`) était le socle de ce modèle.

**Sécurité :** le dépôt ne contient que le `.fin` **chiffré** (AES-256-GCM) ; même si le
repo fuite, le contenu reste opaque. Le token GitHub est un **fine-grained PAT scopé à ce
seul dépôt** (*Contents: R/W*). Le **cache marché reste local** (sidecar, non synchronisé,
régénérable) - seul le grand-livre voyage.

## 1. Décisions actées

| Sujet | Décision |
|---|---|
| Modes | `local` (défaut/fallback) et `github` (opt-in). Même `store` derrière. |
| Adresse | Conf **externe** `~/.config/finador/config.json` (hors fichier chiffré : il faut la localisation avant de déchiffrer). Précédence : `--db <path>` > `FINADOR_DB` > conf. |
| Format conf | **JSON** (stdlib, zéro dépendance ; éditable à la main), géré par `finador remote …`. |
| Auth | **Fine-grained PAT** scopé au repo (*Contents R/W*), dans le **Keychain macOS** (paquet `keyring`), entrée longue durée. Résolution : `GITHUB_TOKEN` > Keychain > prompt (puis stocké). |
| Transport | **API Contents GitHub** en HTTPS pur (pas de binaire `git`, pas de clone), derrière une interface `Backend`. `GET` (contenu+`sha`), `PUT` (contenu+message+`sha`). Chaque `PUT` = un commit → historique + petits deltas append-log. |
| Sync lecture | Pull si la copie locale date de **> 1h** (seuil configurable `readPullAfter`), sinon copie locale. |
| Sync écriture | **Pull juste avant** (toujours) → mutation → **push juste après**. |
| `finador sync` | Force-pull (+ push des changements locaux non poussés). |
| Conflit | `PUT` rejeté (`sha` distant bougé) → re-`GET` distant → **`finador merge`** (union + LWW par `ts` + prompt) → re-`PUT`. |
| Hors-ligne | **Souple** : l'écriture réussit en local, marque « non poussé » + avertit, **pousse au prochain accès en ligne** (merge si divergence). |
| Cache marché | **Local, non synchronisé** (inchangé). |
| `store` | **Inchangé** : opère toujours sur une copie de travail locale. |

## 2. Adressage & configuration (mode)

`~/.config/finador/config.json` (créé/édité par les commandes ; `os.UserConfigDir()`):
```json
{
  "source": "github",
  "github": { "owner": "bpineau", "repo": "finador-data", "path": "portfolio.fin", "branch": "main" },
  "readPullAfter": "1h"
}
```
- **Résolution de la source par invocation** : si `--db <path>` ou `FINADOR_DB` est présent
  → **mode local** sur ce chemin (fallback explicite). Sinon la conf : `source=="github"` →
  mode remote ; sinon (ou conf absente) → mode local sur `~/.finador.fin`.
- Commandes : `finador remote set <owner>/<repo> [--path portfolio.fin] [--branch main]`
  (écrit la conf, `source=github`) ; `finador remote off` (`source=local`) ;
  `finador remote show` (affiche mode, repo, état de sync - jamais le token).

## 3. Authentification

- **Fine-grained PAT** créé par l'utilisateur sur GitHub, scopé au dépôt, permission
  *Contents: Read and write* (réponse à la question « clé scopée à ce seul repo » : oui).
- Résolution : `GITHUB_TOKEN` (env) → **Keychain** (entrée longue durée, clé
  `finador:github:<owner>/<repo>`, sans TTL, distincte du mot de passe du fichier) → prompt
  no-echo (puis stockée au Keychain). Le paquet `keyring` gagne `GetSecret/PutSecret(key)`
  sans expiration, à côté du cache TTL existant.
- `finador remote login` : (re)saisir le token. `finador lock` : purge aussi le token.
- Erreur 401/403 → message clair (« token GitHub invalide ou permissions insuffisantes -
  `finador remote login` »), jamais confondu avec hors-ligne.

## 4. Transport - `internal/remote`

Interface (seam pour d'autres hôtes plus tard ; une seule impl réseau ici) :
```go
type Version string // opaque ; sha du blob côté GitHub
var ErrRemoteConflict = errors.New(...) // base sha != courant
var ErrRemoteMissing  = errors.New(...) // fichier/dossier absent (404)

type Backend interface {
    Fetch(ctx context.Context) (data []byte, v Version, err error)            // ErrRemoteMissing si absent
    Push(ctx context.Context, data []byte, base Version, msg string) (Version, error) // ErrRemoteConflict
    Describe() string
}
```
**GitHub Contents API** (`internal/remote/github.go`) :
- `GET /repos/{owner}/{repo}/contents/{path}?ref={branch}` (`Authorization: Bearer <token>`,
  `Accept: application/vnd.github+json`) → `{content: base64, sha}`. 404 → `ErrRemoteMissing`.
- `PUT /repos/{owner}/{repo}/contents/{path}` body `{message, content: base64(data), sha: base
  (omis si création), branch}` → nouveau `sha`. 409/422 sur `sha` périmé → `ErrRemoteConflict`.
- Le contenu transporté est `base64(octets-du-.fin)` ; GitHub stocke les octets bruts (notre
  texte base64) → l'historique git montre les lignes append-log ajoutées (petits deltas).
- **Caveat documenté** : l'endpoint Contents plafonne ~1 Mo. Le `.fin` (grand-livre seul,
  cache marché exclu) reste bien en-dessous ; si un jour il approchait, basculer sur l'API
  Git Blobs (hors scope v1, noté).
- Client HTTP avec timeout + 1 retry sur 5xx/429 (comme le client Yahoo). Erreur réseau/DNS →
  remontée comme « hors-ligne » (distincte des 4xx d'auth).

## 5. Couche de synchronisation - `internal/remote/sync.go`

Copie de travail locale : `os.UserCacheDir()/finador/checkout/<hash(owner/repo/path)>.fin`,
plus un **état sidecar** `…<hash>.state.json` : `{ "sha": "<dernier sha distant connu>",
"lastPull": "<RFC3339>", "dirty": false }`. La copie de travail peut contenir des
changements **non poussés** (`dirty=true`) en cas de hors-ligne.

Intégration dans `cli` (autour de `store.Open`/`Save`) :
- **Ouverture lecture** (`open`) : mode local → inchangé. Mode remote → si en ligne et
  `now-lastPull > readPullAfter` (ou `dirty`) : `Fetch`, écrire la copie, MAJ `sha`+`lastPull` ;
  sinon utiliser la copie. Puis `store.Open(copie)`.
- **Mutation** (`mutate`) : mode remote → `Fetch` frais d'abord (MAJ copie+`sha`), puis
  `store.Open(copie)` → `fn` → `Save()` (écrit la copie) → `Push(copie, sha, message)` →
  succès : MAJ `sha`, `dirty=false`. `ErrRemoteConflict` → cf. §6. Hors-ligne → `dirty=true`,
  avertissement « non poussé (hors-ligne) », succès local.
- **`finador sync`** : force `Fetch` ; si `dirty`, push (avec résolution de conflit) ; sinon
  juste rafraîchir. Affiche un résumé.
- Message de commit auto : court et utile, p. ex. `finador: <commande> (<date heure>)`.

## 6. Conflits & hors-ligne

**Conflit au push** (`ErrRemoteConflict`, le distant a bougé) :
1. `Fetch` le distant dans un fichier temporaire.
2. **Réconcilier** via la mécanique de `merge` (union + LWW par `ts` + prompt sur vrai conflit)
   entre la copie locale et le temp distant ; écrire le résultat fusionné dans la copie.
3. Re-`Push` avec le nouveau `sha` distant. Boucle bornée (quelques essais) puis erreur claire.

**Hors-ligne** (souple) : une écriture réussit localement, `dirty=true`, avertissement. Le
prochain accès en ligne (ou `finador sync`) pousse - avec résolution de conflit si le distant a
bougé entre-temps. (`merge` exige la **même passphrase** et le **même `id` de fichier** - vrai
puisque c'est le même grand-livre copié.)

**Premier push** (repo vide / fichier absent) : `Fetch` → `ErrRemoteMissing`. `finador init` en
mode remote crée la copie locale puis `Push` sans `sha` (création). `remote set` sur un repo
contenant déjà un `.fin` → première lecture le récupère.

## 7. Surface CLI

```
finador remote set <owner>/<repo> [--path portfolio.fin] [--branch main]   # passe en mode github
finador remote login                                                        # (re)saisir le token (Keychain)
finador remote show                                                         # mode, repo, état de sync (pas le token)
finador remote off                                                          # repasser en local
finador sync                                                                # force pull (+ push si non poussé)
finador --db <chemin> <cmd>                                                 # forcer le local pour une invocation
```
Toutes les autres commandes : sync transparent selon §5. Groupe d'aide : `remote`/`sync` sous
« Sync & maintenance ».

## 8. Architecture / isolation

- **`internal/remote`** (nouveau) : `backend.go` (interface + erreurs), `github.go` (client
  Contents API), `sync.go` (copie de travail + état + pull/push + conflit), `config.go`
  (lecture/écriture de `~/.config/finador/config.json`, résolution de la source).
- **`internal/keyring`** : `PutSecret/GetSecret(key)` longue durée pour le token (+ `Purge`
  l'inclut).
- **`internal/cli`** : commandes `remote`, `sync` ; `open`/`mutate` deviennent source-aware
  (local inchangé ; remote passe par `internal/remote`). Réutilise `store.Merge` pour les
  conflits.
- **`internal/store`** : **inchangé** (opère sur la copie de travail locale).
- Dépendances : **aucune nouvelle** (net/http, encoding/json, encoding/base64 stdlib).

## 9. Phasage

1. **Config & résolution de source** : `internal/remote/config.go`, commandes
   `remote set/show/off`, précédence `--db`/env/conf. Pas de réseau. (Mode local inchangé.)
2. **Backend GitHub** : `internal/remote/{backend,github.go}` (Fetch/Push, erreurs, retry),
   testé avec `httptest`. Token : `keyring` étendu + `remote login` + `GITHUB_TOKEN`.
3. **Couche sync** : `internal/remote/sync.go` (copie de travail + état), `open`/`mutate`
   source-aware, lecture-si->1h, écriture pull-avant/push-après, `finador sync`.
4. **Conflits + hors-ligne** : intégration `merge`, comportement souple `dirty`, premier push.
5. **Doc + tests d'intégration** : README (section « Use a private GitHub repo »), `FORMAT.md`
   note que le remote n'est qu'un transport du même fichier ; tests bout-en-bout avec un faux
   backend.

## 10. Critères de réussite

1. Mode local **inchangé** (défaut/fallback) ; `--db`/`FINADOR_DB` forcent le local.
2. `remote set` configure GitHub ; le token vit au Keychain (jamais en clair en conf/logs) ;
   `GITHUB_TOKEN` override.
3. Lecture : pull si > 1h sinon cache ; `finador sync` force.
4. Écriture : pull-avant + push-après ; chaque push = un commit (historique visible).
5. Conflit distant → réconcilié via `merge` sans perte ; hors-ligne → écriture locale + push
   différé.
6. Le repo ne contient que le `.fin` **chiffré** ; le cache marché reste local.
7. Erreurs d'auth (401/403) distinctes du hors-ligne ; messages clairs.
8. Aucune dépendance nouvelle ; `store` inchangé ; suite verte, vet + lint propres.
