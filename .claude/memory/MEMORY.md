# Memoire projet — Media_FS

> Source de verite unique au demarrage d'une session (/start-session). Mise a jour par /end-session.

## Etat courant

- **Version** : 1.1.2 (dernier tag) — prochain cycle sur `milestone/vX.Y.Z`
- **Branche** : main (1 commit local memoire non pousse au-dela de origin a12a6a1)
- **Travail en cours** : WIP stashe (`stash@{0}` « WIP pre-init-project 2026-10-31 », `git stash pop`) : connecteurs emby/jellyfin/plex/plextv, vfs, downloader + window_cache, cache, config, tray, webui, cmd/mediafs/main.go, docs/VFS.md, index.html) — anterieur a /init-project, a trier avant le prochain workflow
- **Issues actives** : backlog GitHub (epiques #17 a #21) ; ouvertes : #35 #36 #39 #40 #41 #42
- **Cadrage produit** : pas de docs/CADRAGE.md (init par analyse du code)

## Decisions (session 2026-10-31 — /init-project template v3.29.1)

- Environnements : QUALIF = local (binaire promu dans build/qualif_vX.Y.Z/, lance sur ce poste) ; PROD = GitHub Release reconstruite par release.yml sur tag. Adaptations dans `.claude/agents/environments/deploy.{qualif,prod}.md`
- CI : script unique `scripts/ci.sh` (version check, build/vet/test hors paquets CGO, govulncheck) appele par `ci-branch.yml` (milestone/* + dispatch) et le job verify de release.yml
- govulncheck NON bloquant tant que go.mod < 1.25.7 (issue #42) ; ensuite passer `AUDIT_STRICT=1` par defaut
- Conventions Git du template retenues (milestone/vX.Y.Z) ; /dev-issue supprime au profit de /feature et /bugfix
- /marketing = version template (ancienne version projet supprimee)
- dev-backend surcharge `effort: high` ; infra garde opus/medium

## Regles critiques

- Go absent du PATH WSL : `/usr/local/go/bin/go` (paquets sans CGO) ; build complet cote Windows (WinFSP + MinGW, voir CLAUDE.md)

## Corrections de comportement

(aucune pour le moment)
