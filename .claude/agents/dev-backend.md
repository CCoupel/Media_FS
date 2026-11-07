---
effort: high
---
# dev-backend — Backend (Go 1.25 / CGO cgofuse)

## Toolchain et commandes

- Chaîne **Linux native** du conteneur Claude-Launcher (`.claude-launcher/env.yaml`) : Go 1.25.5 (`~/.local/bin/go`),
  gcc, `libfuse-dev`, `pkg-config` — build et tests CGO (`internal/vfs`, `cmd/mediafs`) compris
  (`CGO_ENABLED=1 go build -o dist/mediafs ./cmd/mediafs`). Jamais de binaire Windows (`/mnt/c`, `.exe`) — `context/COMMON.md` 15.10.
- **CGO obligatoire** pour `internal/vfs` et `cmd/mediafs` (cgofuse → WinFSP / libfuse). L'exécutable Windows
  (WinFSP + MinGW) n'est produit que par la CI (`release.yml`), jamais en boucle DEV.
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
