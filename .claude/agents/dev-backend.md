---
effort: high
---
# dev-backend — Backend (Go 1.25 / CGO cgofuse)

## Toolchain et commandes

- Go n'est pas dans le PATH du shell WSL : utiliser `/usr/local/go/bin/go` (tests/vet des paquets sans CGO)
  ou la toolchain Windows `"/mnt/c/Program Files/Go/bin/go.exe"` (build complet).
- **CGO obligatoire** pour `internal/vfs` et `cmd/mediafs` (cgofuse → WinFSP / libfuse). libfuse n'est pas installé
  dans WSL : le build complet se fait côté Windows (MinGW `C:\mingw64\bin`, headers WinFSP stagés dans `C:\winfsp\`),
  voir `CLAUDE.md` § Build & Run.
- Paquets testables sans CGO (`CGO_ENABLED=0`) : tout sauf `internal/vfs` et `cmd/mediafs` — même filtre que le job
  `verify` de `.github/workflows/release.yml`.

## Perimetre

- `cmd/mediafs/**`, `internal/**`, `pkg/**`, `tools/**`

## Conventions

- **Read-only** : aucune opération d'écriture propagée vers les serveurs distants (`EPERM` / `STATUS_ACCESS_DENIED`).
- SQLite pur Go (`modernc.org/sqlite`) — ne jamais introduire un driver CGO supplémentaire.
- Version : seule déclaration autorisée dans `internal/version/version.go` (injectée par `-ldflags` en CI) — aucune
  chaîne `vX.Y.Z` codée en dur ailleurs (vérifié par le job `verify`).
- Connecteurs : interface dans `internal/connector/connector.go`, voir `docs/CONNECTORS.md`.
