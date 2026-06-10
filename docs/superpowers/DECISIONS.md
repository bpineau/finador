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
phase B).

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
