# Audit complet de finador du 5 novembre 2026

## Conclusion

L'application présente des défauts confirmés de conservation du journal, de
synchronisation et de calcul. La suite existante passe dans un environnement
normalisé, mais ne couvre pas plusieurs scénarios qui perdent une modification,
réintroduisent une suppression ou affichent un résultat financier incorrect.

**32 constats : 16 P1, 13 P2 et 3 P3.** Les P1 concernent la perte ou l'altération
de données, la sécurité des écritures et des exports, et des montants ou des
performances faux. Aucun correctif n'a été appliqué. Seul ce rapport est ajouté
au dépôt.

Les priorités sont la fusion et la synchronisation (A01 à A06), la protection
des écritures web et des scripts exportés (A07 à A10), puis les erreurs
financières et la course web (A11 à A16). Certaines anomalies reproduisent
exactement un algorithme prescrit par le format public : elles nécessitent une
décision commune avec le client Android, pas seulement un correctif local.

## Périmètre et méthode

Révision examinée : `0917c5cc775c9ef98a28e811c04b6635dc206e9d`, branche `master`.
Environnement de vérification : macOS arm64, Go 1.27.3. Le module déclare Go
1.26.0 et dépend de pofo v0.2.90.

La revue porte sur `domain`, `portfolio`, `perf`, `market`, `chart`, `store`,
`remote`, `keyring`, `paths`, l'importeur `ibkr`, les commandes CLI, les handlers
et templates web, le README, la spécification du format et les tests associés.
Un même relecteur indépendant a effectué successivement une passe stockage et
synchronisation, puis une passe imports et exports. Ses reproductions ont été
relancées par le coordinateur.

Les expériences utilisent uniquement des livres inventés, des fichiers chiffrés
temporaires et des sources de marché simulées. Les tests supplémentaires sont
injectés avec `go test -overlay` depuis le répertoire temporaire : aucun fichier
Go du dépôt n'est modifié. Un contrôle manuel du vrai binaire a aussi interrogé
les sources de cours sur un identifiant public, sans utiliser de portefeuille
réel.

Les montants des exemples et le taux de calcul de 20 % sont fictifs. Ce taux est
un paramètre de test du moteur, sans prétention à décrire une fiscalité en
vigueur. L'audit examine les règles implémentées, pas leur conformité fiscale.

Niveaux :

- **P1** : correction prioritaire avant de considérer les parcours concernés
  fiables, avec perte de données, altération, sécurité ou résultat financier faux.
- **P2** : comportement incorrect dans un cas particulier, défaut de validation,
  contrat d'interface non tenu ou information indispensable absente.
- **P3** : faiblesse de vérification, de documentation ou de confidentialité des
  métadonnées, sans perte de journal démontrée dans le scénario examiné.

## Constats vérifiés

### A01 P1 La fusion oublie les suppressions et ressuscite des transactions

Source : `internal/store/merge.go:176`, `docs/FORMAT.md:626`.

Quand une suppression gagne l'élection, `Merge` n'émet aucun enregistrement pour
cette entité. La connaissance de sa suppression disparaît du journal fusionné.

Reproduction : deux copies contiennent la transaction T ; A la supprime après
sa création ; fusionner B dans A, puis refaire cette même fusion. La première
fusion conserve la suppression, la seconde restaure la transaction de B sans
conflit. Attendu : T reste supprimée après chaque fusion. Les comptes, actifs et
labels soumis au même mécanisme sont également concernés.

Preuve : `TestAuditMergeDeleteIdempotence` échoue. La spécification prescrit
elle-même l'omission du tombstone, tout en promettant l'idempotence à la ligne
633. C'est un défaut de conception du contrat partagé. Une correction doit
conserver la connaissance des suppressions et vérifier les fusions répétées
avec des copies anciennes.

### A02 P1 La compaction transforme une ancienne donnée en écriture récente

Source : `internal/store/store.go:218`, `docs/FORMAT.md:603`.

`Compact` reconstruit les seules entités vivantes et remplace tous leurs `ts`
par l'instant de compaction. Une ancienne copie compactée peut donc battre une
véritable correction plus récente de l'autre copie. Les suppressions sont
également oubliées.

Reproduction : A garde la version initiale de T ; B corrige T un jour plus tard ;
compacter A, puis fusionner B. Résultat : le contenu périmé de A gagne, car sa
date de compaction est la plus récente. Attendu : la correction de B gagne.

Preuve : `TestAuditCompactPreservesEditOrder` échoue. Le format documente cette
réattribution des dates : la résolution doit préserver la chronologie logique
des modifications et être coordonnée avec les autres lecteurs.

### A03 P1 Une ouverture peut associer un ancien contenu à une nouvelle empreinte disque

Source : `internal/store/store.go:78`, `internal/store/store.go:101`.

`Open` lit les octets, dérive les clés, déchiffre et charge le cache, puis relève
seulement ensuite `(size, mtime)`. Une sauvegarde concurrente entre ces étapes
peut lui faire associer l'empreinte du nouveau fichier au contenu ancien.
L'ancien lecteur passe alors le contrôle de concurrence et écrase la sauvegarde
intervenue pendant son ouverture.

Reproduction déterministe : suspendre A dans le chargement de son cache, après
lecture du journal ; B ajoute une configuration et sauvegarde ; laisser A finir
son ouverture, puis sauvegarder une autre configuration avec A. La configuration
de B disparaît sans `ErrConcurrent`.

Preuve : `TestAuditOpenStampMatchesReadSnapshot` échoue. Un FIFO temporaire sert
uniquement de point de synchronisation du test ; la fenêtre existe aussi pendant
le déchiffrement normal. L'empreinte doit identifier les octets effectivement lus.

### A04 P1 Une sauvegarde locale réussie peut être perdue avant son marquage dirty

Source : `internal/cli/cli.go:507`, `internal/remote/sync.go:268`.

Le chemin d'écriture sauvegarde d'abord le journal, puis appelle `AfterWrite`.
Le drapeau persistant `Dirty` n'est posé qu'à l'intérieur de cette seconde étape.
Un arrêt entre les deux laisse donc une modification sauvegardée marquée propre.
Le prochain `sync` peut remplacer le fichier par la copie distante ancienne.

Reproduction : `ForWrite`, modification et sauvegarde locale réussie, interruption
avant `AfterWrite`, puis `Sync`. Résultat : les octets locaux modifiés sont
remplacés par l'ancienne version distante, sans fusion.

Preuve : `TestAuditSavedWriteSurvivesRestartBeforeAfterWrite` échoue. L'intention
d'écriture et la conservation du contenu local doivent être garanties avant
qu'un pull puisse écraser la copie.

### A05 P1 Un push peut effacer dirty alors qu'une autre sauvegarde a eu lieu

Source : `internal/remote/sync.go:275`, `internal/remote/sync.go:284`.

`pushCopy` lit les octets à téléverser, effectue l'appel réseau, puis efface
`Dirty` sans vérifier que la copie locale contient toujours ces mêmes octets.
Une autre commande peut avoir sauvegardé entre la lecture et la réponse réseau.

Reproduction : le faux backend effectue une seconde sauvegarde locale pendant
le push de la première. `AfterWrite` revient avec succès et un état propre ; un
`Sync` ultérieur restaure les octets du premier push et perd la seconde écriture.

Preuve : `TestAuditConcurrentSaveDuringPush` échoue. Ce scénario est distinct
de A04 : il survient alors que `Dirty` a déjà été posé et couvre toute la durée
de la requête. La coordination doit couvrir la copie, l'état et les écritures
concurrentes, pas uniquement la sauvegarde du journal.

### A06 P1 Deux branches distantes partagent la même copie de travail

Source : `internal/remote/sync.go:49`.

La clé de la copie et de son état incorpore le propriétaire, le dépôt et le
chemin, mais pas `GitHub.Branch`. Changer de branche réutilise le journal local,
le SHA de base, le dernier pull et le drapeau dirty de l'autre branche.

Reproduction : préparer une écriture dirty pour la branche A, puis sélectionner
B avec le même blob de départ. `ForWrite` pousse les modifications de A vers B.
Attendu : chaque branche conserve sa copie et son état indépendants. Même sans
écriture pending, la fenêtre de fraîcheur peut servir le contenu de A après le
changement vers B.

Preuve : `TestAuditBranchIsolation` échoue. Inclure la branche dans l'identité
nécessite aussi de traiter explicitement les anciennes copies locales.

### A07 P1 Les écritures web ne vérifient pas l'origine des requêtes

Source : `internal/web/server.go:283`, `internal/web/accounts.go:51`.

Les routes POST ne contrôlent ni `Origin`, ni les indications Fetch Metadata,
ni un jeton anti-CSRF. La liaison sur loopback et l'absence de cookies ne
constituent pas une autorisation d'écriture.

Reproduction au niveau HTTP : envoyer à `/accounts` un formulaire encodé en
`application/x-www-form-urlencoded`, avec `Origin: https://untrusted.example`
et `Sec-Fetch-Site: cross-site`. Résultat : HTTP 303 et compte effectivement
sauvegardé. Attendu : rejet avant toute mutation.

La même sonde confirme qu'un GET portant `Host: untrusted.example:8451` reçoit
HTTP 200 et les comptes. Cela laisse aussi ouverte la protection serveur contre
le DNS rebinding. L'acceptation par le handler est démontrée ; une exploitation
complète depuis un navigateur n'a pas été exécutée et dépend notamment de ses
protections d'accès au réseau local.

Preuve : `TestAuditCrossOriginMutation`. Prévoir des contrôles d'origine des
écritures et une validation des hôtes attendus, en conservant le modèle local.

### A08 P1 Le script exporté interprète des données comme des commandes shell

Source : `internal/cli/export_script.go:179`, `internal/cli/export_script.go:58`,
`internal/cli/export_script.go:140`.

`shq` utilise `strconv.Quote`, qui produit une chaîne Go et non un argument POSIX
sûr. Les substitutions `$()` et les backticks restent actives dans les guillemets
doubles. Un saut de ligne sans les autres caractères recherchés peut même rester
non cité. L'ISIN est émis sans citation et une note de statement cash reçoit un
seul préfixe `#`, même lorsqu'elle contient plusieurs lignes.

Reproduction inoffensive : le texte `literal $(printf AUDIT_SUBSTITUTED)` devient,
après passage par `shq` et `/bin/sh`, `literal AUDIT_SUBSTITUTED`. La sonde confirme
aussi l'exécution d'une substitution par backticks. Attendu : les caractères
restent littéraux. Une note ou un nom importé peut donc devenir du code lors du
rejeu demandé par l'utilisateur.

Preuve : `TestAuditScriptShellQuoting`. Corriger toutes les sorties du générateur,
pas seulement les noms, et tester le résultat avec un vrai shell.

### A09 P1 La recette du script peut rejouer dans le journal d'origine

Source : `internal/cli/export_script.go:23`, `internal/cli/export_script.go:35`.

L'en-tête recommande `finador --db new.fin init && sh this-file.sh`, mais les
commandes générées n'ont ni `--db` ni sélection de destination. L'option de
`init` ne s'applique pas aux processus lancés par le script : ils utilisent le
journal par défaut. Le shell continue après les erreurs de création des comptes
déjà présents et peut ajouter les transactions une seconde fois.

Reproduction : un journal par défaut contient un compte et un dépôt ; exporter,
initialiser une autre destination avec la recette affichée, puis exécuter les
commandes émises. Le journal d'origine finit avec deux dépôts au lieu d'un.

Preuve : `TestAuditScriptRecipePreservesOriginal` échoue. La sélection explicite
de la destination doit couvrir tout le rejeu, avec arrêt sur la première erreur.

### A10 P1 Des mutations web annoncées en échec sont sauvegardées plus tard

Sources : `internal/web/import.go:46`, `internal/web/tx.go:302`,
`internal/web/tx.go:448`, `internal/web/accounts.go:56`.

Le serveur conserve un livre mutable entre les requêtes. L'import CSV écrit
directement dedans et conserve les lignes déjà parsées quand une ligne suivante
échoue. Plusieurs créations, suppressions et éditions de transactions ne
restaurent pas non plus l'état en mémoire si `Save` échoue. Une autre sauvegarde
réussie finit par enregistrer ces opérations pourtant signalées comme échouées.

Deux reproductions :

1. Importer un dépôt fictif de 77 EUR suivi d'une ligne à date invalide. Après
   le message d'erreur, la mémoire contient une transaction de plus et le disque
   aucune. Créer ensuite un compte : le dépôt est finalement persisté.
2. Faire échouer `Save` avec un répertoire temporaire à la place de son fichier
   `.tmp`, puis soumettre un dépôt web. Malgré HTTP 500, la transaction reste
   en mémoire. Lever l'obstacle et créer un compte : elle est enregistrée.

Preuves : `TestAuditFailedImportAtomicity` et
`TestAuditFailedSaveRetainsTransaction`. Les éditions de comptes, d'actifs et le
renommage disposent déjà d'un rollback partiel utile à comparer. Il faut
distinguer l'échec de sauvegarde d'un échec de push après sauvegarde locale :
ce dernier ne justifie pas d'annuler une écriture déjà persistée.

### A11 P1 Une seule saisie manuelle peut masquer plusieurs opérations importées

Source : `internal/importer/ibkr/import.go:294`.

Le garde-fou par défaut recherche à nouveau toutes les transactions manuelles
pour chaque ligne. `GuardSkip` ne consomme pas le candidat reconnu. Deux
événements distincts, avec des identifiants de trade différents, peuvent donc
tous deux être écartés au titre de la même saisie manuelle.

Reproduction : une saisie manuelle d'achat d'une part pour 100 EUR ; le relevé
contient deux achats identiques ce jour-là, avec deux Trade IDs distincts.
Résultat : `Added=0`, `Matched=2`, une seule transaction finale. Attendu : deux
événements représentés, ou une ambiguïté empêchant de les perdre.

Preuve : `TestAuditManualGuardConsumesAtMostOneEvent` échoue. Ce n'est pas la
limite documentée de deux lignes sans identifiant identiques. `--reconcile`
évite ce cas précis en estampillant la première saisie, mais le mode par défaut
doit aussi respecter la cardinalité des événements.

### A12 P1 La saisie web peut créer un actif dans une devise différente de celle choisie

Source : `internal/web/tx.go:362`, `internal/web/tx.go:370`.

Le formulaire parse la devise explicite de l'opération, puis appelle
`EnsureAsset` avec la devise du compte pour créer un nouvel actif. Un achat USD
d'un actif inconnu dans un compte EUR donne un actif déclaré EUR et une
transaction USD. La devise de l'actif pilote ensuite les cours et la valorisation.

Reproduction : compte EUR, formulaire buy d'un nouvel actif, quantité 1,
montant 100, `ccy=USD`. Résultat observé : actif EUR, montant USD. Le chemin CLI
de création utilise bien la devise demandée.

Preuve : `TestAuditNewAssetUsesExplicitCurrency` échoue. Le problème concerne
la création implicite, pas une opération dans une autre devise sur un actif
déjà déclaré, qui peut être parfaitement légitime.

### A13 P1 Modifier le ticker ou la devise conserve un historique incompatible

Sources : `internal/cli/asset.go:259`, `internal/cli/asset.go:269`,
`internal/web/assets.go:259`, `internal/web/assets.go:260`,
`internal/market/refresh.go:43`.

Les éditions modifient l'identité de cotation sans invalider les prix, dividendes
ou proxies mis en cache sous l'ID de l'actif. Les nombres de l'ancienne série
sont immédiatement lus dans la nouvelle devise. Si `FetchedAt` indique déjà
aujourd'hui, le rafraîchissement automatique peut même éviter tout téléchargement.

Reproduction : une part, ancien cours 10 USD, taux fictif 1 EUR = 2 USD ; la
valorisation donne 5 EUR. Modifier ticker et devise vers une nouvelle cotation
EUR, sans nouveau cours. La valorisation affiche alors 10 EUR à partir de
l'ancien nombre USD. Aucun changement de prix n'a été observé.

Preuve : `TestAuditAssetCurrencyEditKeepsOldCache`. Un cache chiffré reste un
cache sémantiquement typé : il faut vérifier son identité de cotation après une
édition, y compris après synchronisation d'une édition faite ailleurs.

### A14 P1 L'ordre de saisie d'un aller-retour peut fabriquer une perte

Source : `internal/portfolio/series.go:339`, `internal/portfolio/series.go:368`.

Le replay accepte une vente saisie avant l'achat compensateur du même jour
et conserve une quantité signée. Mais le flux de vente coté est plafonné à la
quantité positive connue à cet instant : zéro dans ce scénario. L'achat émet
ensuite son flux positif complet. Les positions se compensent, les flux non.

Reproduction : cash stable de 100 EUR, vendre puis acheter une part le même jour,
au même cours constant de 10 EUR. Valeurs journalières : 100, 100, 100. Flux
enregistré : seulement +10 EUR. Résultat TWR : **-9,090909 %** ; attendu : 0 %.

Preuve : `TestAuditSellBeforeBuyFlows`. Il faut garantir une lecture cohérente
des flux et des quantités pour toute la journée, ou refuser explicitement un
historique qu'on ne sait pas interpréter. Ce défaut est dans les entrées données
à pofo, pas dans la formule de TWR de pofo.

### A15 P1 Les arbres et exports ne conservent pas le total net exact

Sources : `internal/portfolio/breakdown.go:17`, `internal/portfolio/export.go:167`,
`internal/cli/perftree.go:81`.

`Value` applique la règle du compte au total, tandis que les arbres additionnent
les nets calculés position par position. L'approximation des lignes est prévue,
mais le `TOTAL` et les lignes d'enveloppe prennent eux aussi cette somme sans
avertissement fiscal. `Notes` ne transmet que les estimations de cours.

Reproduction : règle fictive `gains:20%`, deux parts valant chacune 100 EUR,
achetées respectivement 50 et 150 EUR. Le compte n'a aucun gain global :
`Value.Net=200`. L'arbre taxe seulement la ligne gagnante et affiche
`TOTAL net=190`, sans note. Attendu : total exact 200, ou indication explicite
qu'il s'agit d'une somme d'estimations différente du total exact.

Preuve : `TestAuditTreeTotalTax`. Sont concernés `value --tree`, `export --tree`,
`perf --tree` et les agrégats construits sur ce breakdown. Les lignes CSV sont
elles aussi des estimations par actif ; leurs nets ne constituent pas un total
de compte exact.

### A16 P1 Une page web de lecture écrit sous un verrou de lecture

Sources : `internal/web/server.go:250`, `internal/domain/marketdata.go:110`,
`internal/web/scope.go:55`.

`quoteNote` appelle `MarketData.Price`, qui crée paresseusement la map ou la
série absente. Or les pages d'actifs tiennent seulement `RLock`. Deux lectures
simultanées d'actifs sans cours peuvent donc écrire dans la même map, pendant
que d'autres chemins la lisent.

Reproduction : livre avec seize actifs sans cache ; demander simultanément
leurs pages `/asset/<id>` en mode offline. Le détecteur de courses signale des
lectures et écritures concurrentes dans `MarketData.Price`, `quoteNote` et
`portfolio.Prices`. Attendu : aucune mutation de la structure sous `RLock`.

Preuve : `go test -race` avec `TestAuditColdConcurrentRead` échoue. Les tests
de race existants passent parce qu'ils n'exercent pas ce cache froid concurrent.
La course expose notamment au crash de map concurrente ; aucune corruption de
journal par cette course n'a été démontrée.

### A17 P2 Deux initialisations concurrentes peuvent toutes deux réussir

Source : `internal/store/store.go:59`, `internal/store/store.go:116`.

`Create` vérifie l'existence avant la dérivation des clés et hors verrou. Deux
appels passent la vérification ; leurs empreintes disque nulles font ensuite
ignorer le contrôle d'antériorité dans `Save`. Le second remplace le premier.

Reproduction : tenir le verrou sidecar, lancer deux `Create` sur le même chemin,
puis libérer le verrou après leur précontrôle. Résultat : deux succès. Attendu :
un seul succès, l'autre appel refusant tout remplacement. Des mots de passe ou
écritures différents rendent le remplacement particulièrement problématique.

Preuve : `TestAuditCreateNeverOverwritesConcurrentCreate` échoue. La promesse
de non-écrasement doit être assurée dans la section sérialisée de création.

### A18 P2 Le mode offline utilise encore le backend de synchronisation

Source : `internal/cli/cli.go:93`, `internal/cli/init.go:56`,
`internal/cli/cli.go:432`, `internal/cli/cli.go:494`.

L'aide promet de ne jamais accéder au réseau, mais le drapeau protège les
sources de cours, pas les appels Fetch/Push du remote. Un `init --offline`
en mode GitHub appelle effectivement Push ; les lectures et écritures normales
passent également par le backend sans tester ce drapeau.

Reproduction avec backend instrumenté : `--offline --no-keychain init` produit
zéro Fetch et un Push. Attendu : aucun appel réseau. Aucun vrai remote n'a été
contacté par la sonde.

Preuve : `TestAuditOfflineRemoteDoesNotAccessBackend` échoue. L'utilisateur doit
pouvoir choisir de rester offline même lorsque sa connexion fonctionne.

### A19 P2 Un cours sans rapport peut tronquer la performance d'un compte cash

Sources : `internal/perf/periods.go:21`, `internal/cli/perf.go:76`.

`CloseAnchor` utilise toutes les séries du livre, sans tenir compte du périmètre
mesuré ni des positions détenues. Le CLI réduit ensuite la série à cette date.
Un ancien actif inutilisé peut ainsi faire ignorer des statements cash récents.

Reproduction : compte cash à 100 EUR le 1er janvier, puis 110 EUR le 4 ; actif
jamais détenu avec un seul cours le 2. Demander `perf Savings --to 2026-01-05`.
Résultat : rapport au 2 janvier avec 0 %, sans mesurer l'évolution du 4.
Attendu : le cours de l'actif hors périmètre ne borne pas ce compte cash.

Preuve : `TestAuditPerfCashScopedAnchor`. Le principe d'ancrage sur une clôture
réelle reste utile pour les titres ; son application doit dépendre du périmètre.

### A20 P2 Un statement seul de titre est accepté mais valorisé à zéro

Sources : `internal/cli/asset.go:174`, `internal/portfolio/replay.go:37`,
`internal/portfolio/value.go:112`, `README.md:1012`.

`asset set` accepte une valorisation de security sans achat préalable. Cependant,
`Holdings` ne crée les positions qu'à partir des achats et ventes, et la seconde
passe de `Value` ne retient que les propriétés. La security déclarée uniquement
par son statement n'apparaît pas dans le patrimoine.

Reproduction au vrai binaire, offline : créer un compte et une security non
cotée, puis `asset set UNLISTED 1234 --at 2026-01-02` et valoriser le lendemain.
Résultat : `TOTAL 0.00 EUR`, sans avertissement nommant le statement ignoré ;
`Series` donne également zéro.

Preuve : `TestAuditStatementOnlySecurity` et contrôle CLI. Si la quantité doit
impérativement être déclarée par un achat, le parcours doit le refuser ou le
signaler clairement, et le README doit présenter ce prérequis. Il ne faut pas
inventer silencieusement une quantité.

### A21 P2 Le CSV générique accepte des achats sans actif ni quantité

Source : `internal/portfolio/import.go:80`.

Le parseur rend l'actif optionnel pour tous les types et laisse la quantité à
zéro sans valider la forme de la transaction. Un achat mal formé est donc
persisté et participe à certaines bases et émissions de flux sans représenter
de position.

Reproduction : importer `date,kind,account,amount,currency` avec une ligne
`2026-01-02,buy,Envelope,99,EUR` dans un compte déclaré. L'import annonce un ajout,
avec `Asset=""` et `Quantity=0`. Attendu : rejet atomique de cette ligne invalide.
Le formulaire web et l'achat CLI exigent déjà un actif et une quantité.

Preuves : `TestAuditCSVRejectsBuyWithoutAssetAndQuantity` échoue et
`TestAuditCSVAcceptsNoAssetNoQuantity` confirme la forme produite. Une validation
commune des invariants de transaction éviterait les divergences de façades.

### A22 P2 Un prix unitaire négatif est écrit tel quel dans le journal

Source : `internal/cli/add.go:83`.

Le total fourni directement est normalisé par `Abs`, mais le chemin `@price`
multiplie le prix signé par la quantité absolue. Un prix négatif non nul passe
la vérification finale et viole l'invariant des montants positifs.

Reproduction au vrai binaire : `asset buy UNLISTED 1 @-25 2026-01-03`. Résultat :
succès, achat avec `Amount=-25 EUR` confirmé par `tx list`. Attendu : erreur de
validation avant sauvegarde. Ce n'est pas le raccourci documenté d'une quantité
négative pour exprimer une vente : le montant du journal doit rester positif.

Preuve : contrôle CLI isolé décrit dans les vérifications. Le défaut doit être
traité avant que ces montants atteignent les bases et les fallbacks à coût.

### A23 P2 Les nombres non finis franchissent la validation

Sources : `internal/domain/rate.go:11`, `internal/cli/value.go:166`,
`internal/store/record.go:37`.

Les comparaisons de borne sur un float ne rejettent pas NaN. `ParsePercent`
accepte `NaN` ; le stockage panique ensuite quand `mustJSON` essaie de le
sérialiser. `parseWhatIf` accepte aussi NaN et l'infini positif.

Reproductions : `asset edit UNLISTED --withholding NaN` termine avec un panic et
le code de sortie 2, au lieu de l'erreur unique attendue ; le formulaire web
d'ajout d'actif provoque le même panic. `value --what-if UNLISTED=NaN` réussit et
imprime des totaux `NaN EUR`.

Preuves : `TestAuditNaNWithholding` et contrôles du vrai binaire. Tester
explicitement le caractère fini des valeurs avant toute mutation ; vérifier
aussi que le serveur ne garde pas un actif invalide après le panic.

### A24 P2 Les vues filtrées peuvent échouer sur une position exclue

Sources : `internal/cli/value.go:82`, `internal/cli/export.go:72`,
`internal/portfolio/export.go:91`, `internal/portfolio/breakdown.go:26`.

Les arbres et exports calculent `Breakdown` de tout le livre, puis filtrent les
lignes. Une conversion manquante pour une position hors périmètre fait donc
échouer une demande qui ne devrait jamais la valoriser.

Reproduction : une position EUR et une position USD, sans taux USD/EUR ; demander
seulement l'actif EUR. `Value` ciblé réussit avec 100 EUR, mais le breakdown
préalable des versions arbre/export échoue sur l'actif USD exclu.

Preuve : `TestAuditBreakdownFailsOutsideScope`. Le filtrage doit intervenir
avant les calculs qui peuvent échouer, comme dans la valorisation ciblée.

### A25 P2 Le téléchargement CSV web efface l'étiquette des estimations

Sources : `internal/web/assets.go:162`, `internal/portfolio/export.go:28`.

`/assets.csv` appelle `AllRows`, dont `AssetRows` recopie les montants de `Value`
sans ses indications d'estimation. Le CSV ne contient aucune colonne de statut.
Le chemin CLI utilise `ScopedRows`, conserve `Note` et l'affiche à côté de l'export.

Reproduction : NAV publiée à 100, proxy portant une estimation à 110, dix parts.
Le CSV web télécharge 1100 EUR sans signaler qu'aucun prix 110 n'a été publié.
La même construction via `ScopedRows` possède une note explicite.

Preuves : `TestAuditWebCSVEstimateLabel` et `TestAuditAllRowsDropsEstimateNote`.
Le contrat impose qu'une estimation soit identifiée partout où elle intervient,
y compris dans le fichier téléchargé, qui peut être lu indépendamment de la page.

### A26 P2 Une sparkline mélange des devises quand le change manque

Source : `internal/web/assets.go:380`.

`assetPricePoints` conserve la valeur native lorsque `fx.Convert` échoue, puis
l'ajoute à la courbe destinée à la devise d'affichage. L'échec est masqué et la
signification de la série change suivant les jours disponibles.

Reproduction : deux cours USD 10 et 20, affichage EUR, aucun taux disponible.
La fonction renvoie deux points 10 et 20 au lieu de les omettre ou de signaler
l'indisponibilité. Avec des taux présents seulement sur une partie de l'histoire,
la courbe contient des nombres USD et EUR dans la même série.

Preuve : `TestAuditSparkMissingFX`. Respecter un traitement explicite et uniforme
du change manquant ; le commentaire annonçant des points ignorés ne correspond
pas au code.

### A27 P2 Le générateur de script ne couvre pas toutes les transactions admises

Sources : `internal/cli/export_script.go:125`, `internal/cli/export_script.go:133`,
`internal/cli/asset.go:174`.

Un statement d'actif avec note est exporté avec `asset set ... --note`, alors que
ce sous-ordre n'a pas ce flag. Un frais de compte sans actif devient `asset fee`
avec un argument actif manquant. Les notes de statements cash deviennent de
simples commentaires et ne sont pas reconstruites.

Reproduction : générer le script d'un livre contenant un statement d'actif annoté
et un frais pur de compte valide. Les commandes émises ne satisfont pas le
contrat des commandes qu'elles appellent. Attendu : rejeu complet, ou refus
explicite d'exporter un état que la CLI ne sait pas reconstruire.

Preuve : `TestAuditScriptStatementNotesAndAccountFees` compare le script à la
définition réelle des flags. Cette sonde vérifie la structure du générateur ;
elle ne prétend pas avoir rejoué un shell complet pour ces deux opérations.

### A28 P2 La reconstruction conserve un default-account pointant sur un ancien ID

Source : `internal/cli/export_script.go:31`.

Les configurations sont copiées textuellement, alors que les comptes sont
recréés avec de nouveaux identifiants. Une valeur `default-account` exprimée
par l'ID d'origine devient une référence introuvable dans le livre reconstruit.

Reproduction : configuration désignant un compte par ID ; exporter, puis rejouer
dans la bonne destination isolée. Le rejeu réussit, mais résoudre
`Book.Config["default-account"]` échoue avec `not found`.

Preuve : `TestAuditScriptRewritesDefaultAccountReference` échoue. Exporter une
référence stable dans le livre reconstruit, par exemple le nom résolu, et vérifier
les autres configurations qui pourraient porter des références d'entités.

### A29 P2 La recette d'ancrage fiscal par dépôt n'est pas réalisée par le moteur

Sources : `README.md:160`, `internal/portfolio/value.go:464`,
`internal/portfolio/series.go:604`.

Le README propose de fixer la base exacte d'un ancien compte avec un
`cash deposit`, d'entrer les achats au coût et de laisser le cash se compenser.
Mais `accountBasis` utilise achats moins ventes plus frais, puis le cash déclaré
actuel. Le dépôt historique n'est pas un ancrage indépendant de cette base.
Les achats ne retirent par ailleurs jamais de cash, conformément à D29.

Reproduction fictive : dépôt 100, retrait 100 pour laisser le cash à zéro, achat
déclaré au coût 150 et valeur 150, règle de calcul `gains:20%`. Le moteur prend
150 comme base et calcule zéro, et non une base ancrée à 100 par le dépôt. Sans
retrait compensateur, le dépôt reste simplement ajouté au patrimoine.

Preuve : `TestAuditCashDepositDoesNotSetBasis`. Le défaut vérifié est la recette
et sa garantie d'exactitude. Choisir une vraie représentation des contributions
ou réduire la promesse ; ne pas modifier la convention de cash à l'insu des
autres parcours.

### A30 P3 Le gate dépend du NO_COLOR hérité de l'environnement

Sources : `internal/cli/cli_test.go:865`, `internal/cli/couleur.go:21`.

`TestPerfColors` force `FINADOR_FORCE_COLOR` mais ne neutralise pas `NO_COLOR`.
Or le comportement de production donne, à raison, priorité à `NO_COLOR`.

Reproduction : `NO_COLOR=1 make check` échoue avec `green missing with
FINADOR_FORCE_COLOR`. `env -u NO_COLOR make check` passe. Attendu : les tests
isolent explicitement les variables dont ils vérifient l'effet.

Ce n'est pas un défaut du rendu de production. Le risque est un gate non
reproductible suivant le terminal ou l'agent qui le lance.

### A31 P3 L'adoption copie un nom de fichier dans l'historique distant non chiffré

Source : `internal/cli/remote.go:62`.

Le message de commit d'adoption contient `filepath.Base(src)`. Un nom descriptif
du fichier source est donc transmis en clair à l'historique GitHub, alors que
les messages des mutations ordinaires évitent les noms d'entités.

La transmission est établie par lecture directe du chemin de code ; aucun
téléversement réel n'a été effectué. Un message générique suffirait à identifier
l'opération sans recopier cette métadonnée choisie par l'utilisateur.

### A32 P3 Plusieurs descriptions publiques ne reflètent plus le comportement

Sources : `README.md:826`, `README.md:1107`, `docs/FORMAT.md:590`,
`internal/cli/asset.go:28`.

- Le CSV générique est présenté comme créant les comptes inconnus. En réalité,
  `ResolveAccount` exige une déclaration préalable et les refuse.
- Le README de sécurité limite les appels réseau aux endpoints Yahoo. La source
  standard possède des fallbacks FT et Morningstar, et le mode remote utilise
  l'API GitHub. Le périmètre de confidentialité décrit est donc incomplet.
- La section 6.2 du format décrit le déplacement du fichier courant vers `.bak`
  avant remplacement. L'implémentation préfère un hard link pour éviter la
  fenêtre sans fichier, avec repli vers le déplacement si nécessaire.
- Le commentaire de `asset set` dans l'aide générale parle des différences
  d'estimation comme de performance, alors que les re-déclarations de propriétés
  sont neutralisées comme flux externes selon la décision actuelle.

Ces écarts sont établis par comparaison du code avec les textes. A01 et A02 sont
plus graves : dans ces cas, la spécification et le code sont d'accord sur un
algorithme qui viole les garanties annoncées.

## Couverture et limites

| Domaine | Contrôles effectués | Résultat ou limite utile |
|---|---|---|
| Valorisation et replay | Positions, cash déclaratif, statements, bases, fallbacks, devises, filtres et cohérence de vues | A12 à A15, A19 à A26, A29 ; égalité Value/Series seule insuffisante pour valider les flux |
| Performance | Façade pofo, fenêtres, ancrage de clôture, flux, rapport et arbre | A14, A15, A19 ; pas de nouvelle formule financière introduite |
| Marché | Fetch incrémental, contrôle de devise, spot, estimations/proxies, invalidation | A13, A25, A26 ; contrôle manuel live réussi |
| Stockage | Paramètres KDF, sous-clés, authentification du journal et du trailer, replay, diff, sauvegardes, verrous et empreintes | A01 à A03, A17 ; aucun contournement cryptographique établi |
| Synchronisation | Pull/push, dirty, reprise, conflits, branches, adoption et intégration CLI/web | A04 à A06, A18, A31 ; backends simulés uniquement pour les scénarios de conflit |
| Imports | CSV, déduplication, reprise d'import, adoption manuelle, mapping du relevé et garde-fous | A10, A11, A21 ; pas de relevé réel utilisé |
| Exports | CSV, arbre, script, périmètres, métadonnées et commandes de rejeu | A08, A09, A15, A24, A25, A27, A28 |
| Web | Routes, validations, mutations, sauvegarde/erreur, origine/hôte, templates et accès concurrents | A07, A10, A12, A16, A23, A25, A26 ; tests HTTP sans navigateur graphique |
| Secrets et chemins | Clés, cache chiffré, migration, résolution de mot de passe et token | Revue de code et tests ; aucun accès au vrai Keychain ni aux secrets |
| Qualité et dépendances | Formatage, vet, lint, tests, race, build et vulnérabilités connues | Gate normalisé vert ; sondes supplémentaires révélant les défauts ci-dessus |

Points positifs vérifiés : séparation du cache et du journal, clés dérivées par
usage, authentification chaînée avec trailer, sauvegarde atomique, contrôle des
paramètres KDF, conservation des lignes inchangées, sources injectables et tests
de non-régression nombreux. Ces protections ne compensent pas les défauts de
concurrence et de fusion démontrés.

Observations complémentaires établies par lecture, sans reproduction de panne
complète et non comptées comme défauts supplémentaires :

- `internal/web/server.go:190` prend le verrou global avant les appels réseau de
  refresh. La limite de cinq secondes du contexte de page n'interrompt pas
  l'attente de `mu.Lock` derrière un refresh quotidien déjà engagé. Il faut
  mesurer cette latence sous source lente avant de garantir le délai annoncé.
- `internal/cli/cli.go:542` et `internal/web/server.go:113` peuvent éviter le reload
  si une réconciliation a réécrit la copie puis qu'un push échoue. Le disque et
  la mémoire web peuvent alors diverger. Le scénario multi-échec reste à tester.
- `internal/store/cache.go` utilise un `.tmp` partagé sans verrou dédié entre
  processus. Des refreshes concurrents peuvent perdre une écriture de cache ou
  échouer. Le cache est régénérable ; aucune perte du journal n'a été déduite.
- `internal/remote/sync.go:192` remplace les fichiers de copie et d'état par
  rename sans `fsync`, contrairement au stockage du journal. La résistance à
  une coupure d'alimentation de cette couche n'a pas été validée.
- Les pages financières ne posent pas explicitement `Cache-Control: no-store`.
  La confidentialité vis-à-vis du cache navigateur, ainsi que la protection
  contre l'affichage en iframe, méritent une revue dédiée.
- Les rendements à l'intérieur d'une journée sont évalués selon la convention
  documentée des flux à la valeur de marché. Ce n'est pas une mesure complète
  du prix d'exécution ni du résultat de trading intrajournalier.

Limites de l'audit : aucune donnée personnelle, aucun ledger réel, aucun token
et aucun service de production n'ont été utilisés pour construire les
reproductions. Aucune configuration utilisateur n'a été modifiée. Le scanner de
vulnérabilités n'est pas un
audit de l'ensemble des sources de pofo. Le client Android, les plateformes hors
macOS et les politiques de sécurité de chaque navigateur n'ont pas été testés.
Il n'y a pas eu de campagne de fuzzing longue, de coupure électrique réelle,
de benchmark de charge complet ni de test d'intrusion depuis un site distant.

Comportements examinés et laissés hors des défauts :

- Le dernier écrivain gagnant opère sur une entité entière : la fusion de deux
  modifications de champs différents n'est pas promise.
- Le rejeu du script régénère les IDs et fingerprints : cette limite est
  explicitement documentée. A28 concerne une référence que le script conserve
  malgré cette régénération.
- Le même contenu de transaction sans identifiant externe distinct peut être
  dédupliqué : limite documentée, contrairement à A11.
- Les sections de relevé non prises en charge et les opérations sur titres sont
  signalées, pas inventées : ce choix n'est pas un défaut d'import.
- Les erreurs de format ou de version de header restent distinctes des échecs
  d'authentification des données chiffrées : comportement de format prévu.
- Le retour à une ancienne version entière authentique ne peut pas être empêché
  par le seul format autonome sans référence de révision externe fiable.
- Le passage du secret encodé dans les arguments de l'utilitaire Keychain est
  un compromis documenté de l'intégration sans CGo. Aucun secret réel n'a été lu.
- L'absence de flock sur certaines plateformes et les limites de taille du
  backend sont déjà annoncées ; elles n'ont pas été présentées comme nouvelles
  vulnérabilités vérifiées.

## Vérifications exécutées

### Gate existant et outils

| Commande | Résultat observé |
|---|---|
| `make check` avec `NO_COLOR=1` hérité | Échec de `TestPerfColors` uniquement ; vet et lint passent ; la cible test arrête le gate avant race |
| `env -u NO_COLOR make check` | Succès : fmt-check, vet, lint, tous les packages de tests et race web/store |
| `make race` | Succès indépendant sur web/store |
| `make build` | Succès, vrai binaire construit |
| `govulncheck ./...` | Zéro vulnérabilité atteignable détectée |
| `govulncheck -show verbose ./...` | Même résultat ; une entrée dans un module requis, sans package importé ni symbole appelé |
| Binaire offline sur livre inventé | Initialisation, comptes, actifs, statements, achats, consultation, what-if et erreurs de validation exercés |
| Binaire online, refresh manuel isolé | `2 series refreshed, 1 quotes` ; le fournisseur indique la dernière séance de l'identifiant public au 4 novembre |

Le scanner, version 1.8.0, a interrogé la base Go mise à jour le 2 novembre 2026.
L'entrée de module est
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), relative à `openpgp` dans
`golang.org/x/crypto`. Le scanner ne trouve aucun package concerné importé par
finador et aucun chemin de symboles vulnérable. Ce n'est pas une vulnérabilité
exploitable de finador démontrée par cet audit.

Deux premiers lancements de `govulncheck` ont échoué sur une entrée du cache Go
manquante et une tentative intermédiaire du gate a échoué au chargement de lint.
Les relances ont abouti sans changement de code, de dépendances ou de cache
manuel. Ces incidents d'outillage ne sont pas comptés comme bugs applicatifs.

### Sondes supplémentaires

Les sondes résident temporairement sous `/private/tmp`, hors suivi Git. Leurs
scénarios et résultats utiles sont décrits dans chaque constat pour ne pas
faire dépendre le rapport de la conservation de ces fichiers temporaires.

```sh
# Huit assertions de sécurité attendue : huit échecs confirmant A01-A06, A17, A18.
go test -overlay /private/tmp/finador-audit-overlay.json \
  ./internal/store ./internal/remote ./internal/cli \
  -run '^TestAudit' -count=1 -timeout=90s

# Cinq assertions attendues : cinq échecs confirmant A09, A11, A12, A21, A28.
go test -overlay /private/tmp/finador-audit-second-overlay.json \
  ./internal/importer/ibkr ./internal/portfolio ./internal/web ./internal/cli \
  -run '^TestAudit' -count=1 -timeout=90s

# Dix-sept sondes d'observation : assertions du comportement défectueux observé.
go test -overlay /private/tmp/finador-audit-20261105/overlay.json \
  ./internal/web ./internal/portfolio ./internal/cli \
  -run '^TestAudit' -v -count=1 -timeout=90s

# Course confirmée sur les GET de pages d'actifs avec cache froid.
go test -race -overlay /private/tmp/finador-audit-20261105/race-overlay.json \
  ./internal/web -run '^TestAuditColdConcurrentRead$' -count=1 -timeout=30s
```

Les succès des sondes d'observation signifient que le comportement décrit a
bien été observé ; ils ne signifient pas que l'application est correcte. Les
treize assertions de sécurité échouent volontairement sur le comportement
attendu de l'application. La sonde race produit `WARNING: DATA RACE` et échoue.
Toutes les reproductions supplémentaires sont absentes des sources compilées
normalement et n'altèrent pas le résultat du gate existant.

Le format stocké n'a pas été modifié. Le sample et les vecteurs KDF sont testés
par la suite existante ; la passerelle Android `crossimpl.sh` n'a pas été
exécutée. Elle sera indispensable pour un changement effectif du contrat de
fusion ou de compaction.

## Priorités proposées

1. **Conserver le journal** : traiter A01 à A06 et A17 ensemble avec des tests
   d'interleavings, de redémarrage et de fusions répétées. Fixer d'abord le
   contrat des tombstones et des dates, puis sa traduction Go/Android.
2. **Fermer les chemins d'altération** : contrôles HTTP A07, génération shell et
   destination A08-A09, mutations atomiques A10, lecture concurrente A16.
3. **Rendre les chiffres cohérents** : A11-A15, puis ancrage de période,
   validation, périmètres, estimations et devises A19-A26. Comparer les résultats
   entre CLI, web, CSV et arbre, en plus de comparer Value et Series.
4. **Rendre le rejeu et les contrats fiables** : A18, A27-A32 ; vérifier le vrai
   shell et le vrai binaire sur des livres inventés, puis mettre à jour les
   recettes et la spécification concernées.

Aucune de ces corrections, aucun re-import et aucune migration de données
utilisateur n'ont été effectués dans le cadre de cet audit.
