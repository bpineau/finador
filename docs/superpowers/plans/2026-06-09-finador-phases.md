# Finador — découpage en phases

Spec : `docs/superpowers/specs/2026-06-09-finador-design.md`. Quatre phases, chacune produit
un logiciel utilisable et testé. Chaque phase a (ou aura) son plan détaillé, écrit juste avant
son exécution pour coller au code réellement produit par les phases précédentes.

| Phase | Plan | Contenu | Livrable utilisable |
|---|---|---|---|
| A | `2026-06-09-finador-a-noyau.md` | `domain`, `store`, `keyring`, CLI de saisie (init, account, asset, add, cash, deposit/withdraw, tx, import CSV, config, lock) | Ledger patrimonial chiffré, saisie et import complets |
| B | *(à écrire après A)* | `market` (Yahoo : prix, FX, dividendes, cache), `portfolio` (positions, valorisation, impôt latent), `value`, `refresh`, `--offline` | Valeur du patrimoine brut/net, à toute date, toute portée |
| C | *(à écrire après B)* | `perf` (TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD), `chart/term` + `chart/svg`, `perf`, `chart` | Rendements, métriques et courbes en terminal |
| D | *(à écrire après C)* | `web` (serveur, templates, 5 vues, formulaires, SVG inline), `serve` | L'application web complète, zéro JS |

Règles transverses (toutes phases) : TDD strict, commits fréquents, `go test ./...` sans
réseau, pur Go (pas de CGo), erreurs sentinelles wrappées `%w`.
