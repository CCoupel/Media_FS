# dev-issue — Workflow de développement guidé

Lance le workflow complet de développement pour une GitHub issue du projet Media_FS.

**Usage:** `/dev-issue <issue-number>`

---

Exécute les étapes ci-dessous **dans l'ordre**. Les étapes marquées ✋ nécessitent une validation explicite de l'utilisateur avant de continuer. Ne jamais sauter une étape. Signaler immédiatement tout bloquant.

---

## Étape 1 — Analyse & Branche

1. Lire l'issue GitHub : `mcp__plugin_github_github__issue_read` (owner: CCoupel, repo: Media_FS)
2. Lire les fichiers de code impactés (identifiés via les refs de l'issue)
3. Présenter un résumé structuré :
   - **Objectif** : ce que l'issue doit accomplir
   - **Périmètre** : packages/fichiers touchés
   - **Dépendances** : issues ou code dont elle dépend
   - **Risques** : régression potentielle, complexité technique
4. Lire `cmd/mediafs/version.go` pour connaître la version courante
5. Déterminer le type de bump : `patch` (fix/amélioration mineure) | `minor` (nouvelle feature) | `major` (breaking change)
6. Calculer la prochaine version
7. Créer la branche git : `feat/issue-{N}-{slug}` (ou `fix/` pour un bug)
   - Slug = titre issue en kebab-case, 3-5 mots max
   - Exemple : `feat/issue-11-images-sidecar`
8. Présenter le résumé + nom de branche et continuer

---

## Étape 2 — Workshop

1. Lister **tous** les points ambigus, questions techniques, décisions de design non résolues
2. Pour chaque point : formuler la question clairement + proposer une option par défaut avec justification
3. Exemples typiques : format de fichier, comportement sur erreur, choix d'API, naming, thread-safety
4. Si des points bloquants existent : **✋ poser toutes les questions en une seule fois → attendre les réponses**
5. Documenter les décisions prises (elles seront reprises dans la doc et le PR)
6. Si aucun doute : indiquer explicitement "Aucun point bloquant" et continuer sans attendre

---

## Étape 3 — Plan de développement

1. Lister précisément les fichiers à **créer** et à **modifier** avec la raison pour chacun
2. Décrire l'approche d'implémentation étape par étape (pseudo-code ou description)
3. Identifier edge cases et comportements aux limites
4. Vérifier la cohérence avec les contraintes CLAUDE.md (read-only, CGO, pure-Go SQLite)
5. **Présenter le plan → attendre approbation explicite avant d'écrire le moindre code**

---

## Étape 4 — Plan de test

1. Lister les **tests unitaires** à écrire : package, nom de fonction, cas couverts
2. Lister les **tests d'intégration** si applicable
3. Définir la **checklist de validation manuelle** mappée aux critères d'acceptance de l'issue
4. Présenter le plan de test et continuer

Format de la checklist :
```
- [ ] Description du test / comportement attendu
- [ ] ...
```

---

## Étape 5 — Documentation

1. Identifier les fichiers docs/ à mettre à jour :
   - `docs/ARCHITECTURE.md` — si nouveaux packages ou flux de données
   - `docs/CONNECTORS.md` — si nouveau connecteur ou modification d'interface
   - `docs/VFS.md` — si comportement VFS modifié
   - `SPECS.md` — si comportement utilisateur modifié
2. Rédiger les mises à jour directement dans les fichiers
3. Ne documenter que ce qui est déjà décidé — pas de doc spéculative

---

## Étape 6 — Développement

1. Implémenter selon le plan approuvé à l'Étape 3
2. Respecter les décisions du Workshop (Étape 2)
3. Committer par sous-tâche logique :
   ```
   git add <fichiers spécifiques>
   git commit -m "type(scope): description courte"
   ```
4. Types : `feat`, `fix`, `refactor`, `test`, `docs`, `chore`
5. **Ne pas s'écarter du plan sans le signaler à l'utilisateur**

---

## Étape 7 — Build

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
```

- Corriger **toutes** les erreurs avant de continuer
- Ne pas masquer les erreurs CGO avec `CGO_ENABLED=0` si le bug est CGO-related
- Reporter le résultat : ✅ build OK | ❌ erreurs (avec détail)

---

## Étape 8 — QA : Tests & Validation

1. Exécuter les tests :
   ```bash
   go test ./...
   go test -v -run <TestName> ./...   # tests ciblés si applicable
   ```
2. Passer la checklist manuelle définie à l'Étape 4 :
   - ✅ validé
   - ❌ échec (décrire le problème)
   - ⚠️ non testable sans infra (expliquer pourquoi)
3. Si échec : corriger et reboucler sur Étape 7

---

## Étape 9 — Review du code

1. Afficher le diff complet : `git diff main...HEAD`
2. Pour chaque fichier modifié : noter les décisions non-triviales
3. Lister les déviations par rapport au plan initial
4. Signaler : code smell, risque de régression, dette technique introduite
5. Continuer vers le deploy

---

## Étape 10 — Deploy QA

1. Bumper la version dans `cmd/mediafs/version.go` selon le type déterminé à l'Étape 1
2. Commit :
   ```
   git commit -m "chore: bump version to vX.Y.Z"
   ```
3. Push la branche :
   ```
   git push -u origin <branch>
   ```
4. Créer la PR GitHub (`mcp__plugin_github_github__create_pull_request`) :
   - **Titre** : `feat(#N): <titre de l'issue>`
   - **Body** : voir template ci-dessous
5. Retourner l'URL de la PR à l'utilisateur

### Template PR body

```markdown
## Issue
Closes #N — <titre issue>

## Résumé
<2-3 bullet points de ce qui a été fait>

## Décisions du workshop
<décisions prises à l'Étape 2, si non triviales>

## Plan de test
<checklist de validation de l'Étape 4>

## Déviations par rapport au plan
<liste vide si aucune, sinon détailler>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```
