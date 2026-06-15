# Finador phase G - v0.4 : interface intégralement en anglais

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Demande utilisateur : « README.md et interface web et cli etc : tout en
anglais (IMPÔT → TAXES, etc) ; seuls des trucs fournis par l'user (groupes, noms
d'enveloppes) peuvent être en français. Aussi, vire les changelogs du README. »
Toutes les chaînes VISIBLES (sorties CLI, messages d'erreur, prompts, en-têtes de
tableaux, templates web, README) passent en anglais. Le contenu utilisateur (noms de
comptes/actifs/groupes/notes) n'est jamais touché.

**Périmètre :**
- EN ANGLAIS : chaînes de sortie, erreurs (`fmt.Errorf`, `errors.New`), prompts,
  helps cobra (Use/Short/Long/flags), en-têtes de tableaux, templates web, marqueurs
  (≈, what-if), noms de périodes (entrée ET sortie), valeurs de flags
  (`--by group|account`), paramètre web (`?by=group|account|asset`), README.
- INCHANGÉ : commentaires de code (pas une surface utilisateur), docs/superpowers/*,
  données utilisateur, le fichier demo.fin, les formats de COMMITS passés.
- Formats web : nombres `1,234.56 €` (virgule de milliers, point décimal, symbole
  suffixé après espace insécable), pourcentages `+2.00%`, date longue
  « Wednesday 10 June 2026 ». Renommer les funcs de template `frMoney/frPct/frDate/
  frDelta/frNum` → `fmtMoney/fmtPct/fmtDate/fmtDelta/fmtNum` (templates mis à jour).
- Chaque task REÉCRIT AUSSI les assertions de tests concernées. Portillons : gofmt,
  vet, `go test ./... -count=1` verts à chaque task ; et le contrôle final :
  AUCUN littéral de chaîne Go avec un caractère accentué hors commentaires dans
  internal/ (vérification G5).

## Glossaire normatif (FR → EN)

Erreurs sentinelles et domaine :
| FR | EN |
|---|---|
| introuvable | not found |
| référence ambiguë | ambiguous reference |
| existe déjà | already exists |
| mot de passe incorrect ou fichier corrompu | wrong password or corrupted file |
| fichier modifié par un autre processus depuis l'ouverture - relancez la commande | file modified by another process since it was opened - retry the command |
| compte / actif / portée | account / asset / scope |
| (candidats : …) | (candidates: …) |
| %s (référence vide) | %s (empty reference) |
| identifiant vide | empty identifier |
| référence %q déjà portée par %s | reference %q already used by %s |
| date %q (attendu AAAA-MM-JJ) | invalid date %q (expected YYYY-MM-DD) |
| devise %q invalide (attendu code ISO à 3 lettres, ex. EUR) | invalid currency %q (expected a 3-letter ISO code, e.g. EUR) |
| règle fiscale %q: attendu none, gains:N%% ou value:N%% | invalid tax rule %q: expected none, gains:N%% or value:N%% |
| taux invalide / taux hors de [0%, 100%] | invalid rate / rate outside [0%, 100%] |
| mode %q inconnu | unknown mode %q |
| type d'actif %q: attendu security ou property | invalid asset kind %q: expected security or property |
| type de transaction %q inconnu | unknown transaction kind %q |
| TxKind %d non défini / AssetKind %d non défini | undefined TxKind %d / undefined AssetKind %d |
| pourcentage %q invalide (attendu 0%% à 100%%) | invalid percentage %q (expected 0%% to 100%%) |
| transaction %d | transaction %d (inchangé) |
| l'actif %s est référencé par la transaction %d - supprimez d'abord ses transactions (finador tx list --asset %s) | asset %s is referenced by transaction %d - delete its transactions first (finador tx list --asset %s) |

Store / keyring / market :
| FR | EN |
|---|---|
| %s n'est pas un fichier finador | %s is not a finador file |
| version %d non gérée (finador trop ancien ?) | unsupported version %d (finador too old?) |
| paramètres Argon2 hors bornes | Argon2 parameters out of bounds |
| %s existe déjà | %s already exists |
| %s n'existe pas - lancez 'finador init' pour le créer | %s does not exist - run 'finador init' to create it |
| contenu illisible | unreadable content |
| Mot de passe : / Confirmez : | Password: / Confirm: |
| les mots de passe diffèrent | passwords do not match |
| mot de passe vide refusé | empty password rejected |
| aucun terminal pour saisir le mot de passe : utilisez FINADOR_PASSWORD | no terminal to type the password: use FINADOR_PASSWORD |
| symbole pour %q | symbol for %q |
| cours de %q | quotes for %q |
| cours de change %s manquant au %s - lancez « finador refresh » | missing %s exchange rate on %s - run 'finador refresh' |
| %s cote en %s mais l'actif est déclaré en %s | %s quotes in %s but the asset is declared in %s |

Portfolio / perf (labels CALCULÉS - jamais les données utilisateur) :
| FR | EN |
|---|---|
| patrimoine (label de la portée All) | portfolio |
| liquidités | cash |
| (sans groupe) | (ungrouped) |
| portée %q (ni groupe, ni compte, ni actif) | unknown scope %q (not a group, account or asset) |
| impôt total calculé par enveloppe ; la ventilation par ligne est approximative | total tax follows the per-account rule; the per-line breakdown is approximate |
| %s: dernier cours au %s | %s: last quote on %s |
| %s: valorisé par relevé du %s | %s: valued from its %s statement |
| %s: aucun cours ni relevé - compté pour 0 | %s: no quote nor statement - counted as 0 |
| hypothèse : %s à %s %s | what-if: %s at %s %s |
| conversion %s→%s impossible - valeur comptée 0 | cannot convert %s→%s - counted as 0 |
| aucune transaction : rien à tracer | no transactions: nothing to plot |
| borne de fin antérieure au début | end date before start date |
| XIRR non défini pour ces flux… | XIRR undefined for these cashflows (no sign change) |
| XIRR: au moins deux flux requis | XIRR: at least two cashflows required |
| période %q inconnue (…) | unknown period %q (1d, 2d, 5d, 7d, 1m, 3m, ytd, 1y, prev-yr) |
| **Noms de périodes (entrée CLI ET affichage)** : 1j 2j 5j 7j 1m 3m ytd 1a an-1 origine fenêtre | **1d 2d 5d 7d 1m 3m ytd 1y prev-yr inception window** |

CLI (helps, sorties, en-têtes) - liste non exhaustive, TOUT y passe :
| FR | EN |
|---|---|
| Suivi de patrimoine chiffré - CLI et web, single binary | Encrypted personal wealth tracker - CLI and web, single binary |
| Créé %s | Created %s |
| Compte %s (%s) créé / Actif %s (%s) créé / Actif %s mis à jour / Actif supprimé | Account %s (%s) created / Asset %s (%s) created / Asset %s updated / Asset deleted |
| %d importée(s), %d ignorée(s) (doublons) | %d imported, %d skipped (duplicates) |
| ligne %d / en-tête CSV / ni amount ni price / colonne account vide / quantité %q / montant %q / prix %q / solde %q / valeur %q | line %d / CSV header / neither amount nor price / empty account column / invalid quantity %q / invalid amount %q / invalid price %q / invalid balance %q / invalid value %q |
| Transaction %d supprimée | Transaction %d deleted |
| identifiant de transaction %q invalide | invalid transaction id %q |
| précisez l'enveloppe avec --account | specify the account with --account |
| quantité %q invalide / quantité requise pour un %s / un %s demande un actif | invalid quantity %q / quantity required for a %s / a %s requires an asset |
| prix manquant : @prix-unitaire ou montant total | missing price: @unit-price or total amount |
| argument %q incompris (attendu @prix, total ou date) | unexpected argument %q (expected @price, total or date) |
| Keychain purgé | Keychain purged |
| avertissement: | warning: |
| cache non sauvegardé | cache not saved |
| refresh impossible en --offline | refresh unavailable in --offline mode |
| %d série(s) rafraîchie(s) | %d series refreshed |
| résolution %q | resolving %q |
| ≈ borne future ramenée à aujourd'hui | ≈ future date clamped to today |
| --what-if %q: attendu actif=prix / prix %q invalide | --what-if %q: expected asset=price / invalid price %q |
| --by %q: attendu groupe ou enveloppe | --by %q: expected group or account - **les VALEURS deviennent `group` / `account`** |
| --exclude %s | --exclude %s (inchangé) |
| (hors %s) | (excluding %s) |
| adresse %q invalide | invalid address %q |
| ATTENTION : %s expose votre patrimoine au-delà de cette machine (aucune authentification web) | WARNING: %s exposes your portfolio beyond this machine (no web authentication) |
| finador sur http://%s - Ctrl-C pour arrêter / arrêt… | finador on http://%s - Ctrl-C to stop / shutting down… |
| En-têtes : ID NOM DEVISE FISCALITÉ | ID NAME CURRENCY TAX |
| ID TYPE NOM TICKER GROUPE DEVISE ALIAS RETENUE | ID TYPE NAME TICKER GROUP CURRENCY ALIASES WITHHOLDING |
| ID DATE TYPE COMPTE ACTIF QTÉ MONTANT NOTE | ID DATE TYPE ACCOUNT ASSET QTY AMOUNT NOTE |
| LIGNE BRUT IMPÔT NET / LIGNE VALEUR / TOTAL | LINE GROSS TAX NET / LINE VALUE / TOTAL |
| PÉRIODE TWR XIRR | PERIOD TWR XIRR |
| %s - performance (%s), évalué au %s | %s - performance (%s), as of %s |
| max drawdown %s (%s → %s, récupéré le %s / non récupéré) / max drawdown - aucun | max drawdown %s (%s → %s, recovered on %s / not recovered) / max drawdown - none |
| (brut, %s) - dernier point : %s | (gross, %s) - last point: %s - idem net |
| vs réel : brut %+.2f … · net … | vs actual: gross %+.2f … · net … |
| %s au %s (en-tête value) | %s - %s |
| Tous les helps cobra (Short/Long/flags) | anglais, mêmes informations |

Web (templates + render) :
| FR | EN |
|---|---|
| nav : Patrimoine / Transactions / Import | Overview / Transactions / Import |
| patrimoine net d'impôt latent | net worth, after estimated tax |
| brut … · impôt latent … | gross … · estimated tax … |
| évolution / répartition / performance / composition / transactions récentes | history / allocation / performance / holdings / recent transactions |
| onglets : par groupe · par enveloppe · par actif (`?par=`) | by group · by account · by asset (**paramètre `?by=group|account|asset`**) |
| nouvelle écriture / bordereau / corriger l'écriture [N] / ledger | new entry / entry slip / edit entry [N] / ledger |
| date / type / compte / actif (optionnel) / quantité / montant total / devise (optionnel) / note (labels de formulaire) | date / kind / account / asset (optional) / quantity / total amount / currency (optional) / note |
| Enregistrer / Enregistrer la correction / Importer / Rafraîchir les cours / édit. / suppr. | Save / Save changes / Import / Refresh quotes / edit / delete |
| import CSV (titre) + texte d'aide colonnes | CSV import + help text in English |
| fichier de transactions / aucun fichier reçu / fichier trop volumineux (10 Mo maximum) | transactions file / no file received / file too large (10 MB max) |
| sauvegarde impossible : %s | could not save: %s |
| hors ligne : refresh impossible | offline: cannot refresh quotes |
| page introuvable / portée introuvable / compte introuvable / transaction introuvable / identifiant invalide / erreur | page not found / unknown scope / unknown account / transaction not found / invalid id / error |
| ← retour au patrimoine / fil d'ariane « patrimoine » | ← back to overview / breadcrumb "overview" |
| finador - vos données restent dans votre fichier chiffré. | finador - your data stays in your encrypted file. |
| pas encore assez d'historique (pour tracer une courbe). | not enough history yet (to plot a curve). |
| Formats : frMoney→fmtMoney `1,234.56 €` (séparateur virgule, décimal point, U+00A0+symbole) ; frPct→fmtPct `+2.00%` (sans espace) ; frDate→fmtDate « Wednesday 10 June 2026 » ; frNum→fmtNum `1.26` ; signe inchangé | |

---

### Task G1: domain, store, keyring, market - erreurs et prompts en anglais

**Files:** tous les .go de `internal/domain`, `internal/store`, `internal/keyring`,
`internal/market` (sources ET tests).

- [ ] Step 1 : balayer chaque littéral de chaîne visible (errors.New, fmt.Errorf,
  prompts, payloads de messages) selon le glossaire. Les commentaires restent.
- [ ] Step 2 : mettre à jour les assertions de tests qui vérifient ces chaînes
  (`strings.Contains`, `err.Error()`…). Les tests de FORMAT (wire JSON, braille)
  ne changent pas.
- [ ] Step 3 : `gofmt -l . && go vet ./... && go test ./... -count=1` - vert. Les
  suites cli/web/portfolio peuvent casser si elles assertent ces chaînes : les
  corriger AUSSI dans ce task (le dépôt reste vert à chaque commit).
- [ ] Step 4 : commit `git commit -m "i18n: domain, store, keyring and market speak English"`

### Task G2: portfolio, perf - labels, marqueurs, périodes en anglais

**Files:** `internal/portfolio/*.go`, `internal/perf/*.go` (sources ET tests).

- [ ] Step 1 : glossaire - labels calculés (portfolio→`portfolio`, cash, (ungrouped)),
  TaxNote, marqueurs stale/what-if/conversion, erreurs de Series/Scope.
- [ ] Step 2 : périodes : renommer dans `periods.go` (PeriodRange + Names) :
  `1d 2d 5d 7d 1m 3m ytd 1y prev-yr` ; lignes `inception` et `window` dans
  report/cli. Tests de périodes réécrits.
- [ ] Step 3 : adapter TOUTES les assertions cassées (perf_test, value_test,
  series_test, scope_test, breakdown_test, cli_test, server_test) dans ce task.
- [ ] Step 4 : portillons verts ; commit
  `git commit -m "i18n: portfolio and perf labels, English period names"`

### Task G3: cli - helps, sorties, en-têtes en anglais

**Files:** `internal/cli/*.go` (sources ET tests), `cmd/finador/main.go` (préfixe
d'erreur `finador:` inchangé).

- [ ] Step 1 : glossaire - tous les cobra Use/Short/Long/flags, messages imprimés,
  en-têtes de tableaux, `--by group|account` (valeurs ANGLAISES, erreur adaptée),
  `(excluding …)`, en-têtes value/perf/chart, messages serve/refresh/init/lock.
- [ ] Step 2 : tests cli réécrits (beaucoup de Contains).
- [ ] Step 3 : portillons verts ; commit `git commit -m "i18n: the CLI speaks English"`

### Task G4: web - templates, formats et handlers en anglais

**Files:** `internal/web/**` (sources, templates, tests).

- [ ] Step 1 : renommer les funcs de template (`fmtMoney/fmtPct/fmtDate/fmtDelta/
  fmtNum`) et passer aux formats anglais : fmtMoney = virgule de milliers, point
  décimal, U+00A0 + symbole (`1,234.56 €`) ; fmtPct `+2.00%` ; fmtDate
  « Wednesday 10 June 2026 » (tableaux anglais des jours/mois). Le signe moins
  typographique U+2212 est conservé.
- [ ] Step 2 : tous les templates selon le glossaire ; le paramètre `?par=` devient
  `?by=` avec valeurs `group|account|asset` (handlers + liens + onglets) ; messages
  des handlers (flash/erreurs) en anglais.
- [ ] Step 3 : tests web réécrits (formats, libellés, ?by=). Le test frMoney des
  bornes de retenue devient fmtMoney avec « 1,000.00 € » etc.
- [ ] Step 4 : portillons verts + `go test -race ./internal/web/` ; commit
  `git commit -m "i18n: the web app speaks English, English number and date formats"`

### Task G5: README anglais sans changelog + finition

- [ ] Step 1 : RÉÉCRIRE README.md intégralement en anglais : pitch, features, build,
  Getting started (commandes vérifiées contre le binaire !), CSV import, web, config,
  data model & security, known limits. SUPPRIMER toute section changelog/«
  Nouveautés ». Pas de section history.
- [ ] Step 2 : DECISIONS.md - D12 (en français, c'est le journal) : interface
  anglaise, formats anglais, périodes renommées, `--by group|account`, `?by=`,
  commentaires de code et docs inchangés.
- [ ] Step 3 : contrôle d'exhaustivité : `grep -rn '"[^"]*[éèêàçôûîÉÈÀÇ]' internal/ cmd/ --include='*.go'`
  ne doit retourner AUCUN littéral hors commentaire (vérifier manuellement les faux
  positifs : les commentaires en fin de ligne contenant des guillemets). Idem
  `grep -rn '[éèêàçô]' internal/web/templates/ internal/web/static/`.
- [ ] Step 4 : portillons + smoke binaire (init/account/asset/add/value/perf/chart
  offline en anglais ; serve + curl : dashboard anglais) ; coller la sortie.
- [ ] Step 5 : commit `git commit -m "docs: English README without changelog - v0.4 complete"` + `git tag phase-g`
