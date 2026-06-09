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
