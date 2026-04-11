# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Media_FS — Virtual filesystem (WinFSP on Windows, FUSE on Linux) that exposes Jellyfin/Emby/Plex libraries as a mountable drive navigable from Windows Explorer or a Linux file manager.

Full functional specs: [SPECS.md](SPECS.md)

## Docs (load when relevant)

| File | When to read |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package layout, data flow, key design decisions |
| [docs/CONNECTORS.md](docs/CONNECTORS.md) | Adding or modifying a media server connector |
| [docs/VFS.md](docs/VFS.md) | WinFSP / FUSE filesystem implementation details |

## Build & Run

CGO is required (cgofuse links against WinFSP / libfuse).

### Windows

Prérequis installés sur cette machine :
- **WinFSP** : `C:\Program Files (x86)\WinFsp\` (v2.1.25156)
- **MinGW-w64 (gcc)** : `C:\mingw64\bin\`

Les headers WinFSP doivent être stagés dans un chemin sans espaces avant de builder (opération ponctuelle — à refaire si WinFSP est réinstallé) :

```powershell
# Staging WinFSP (une seule fois)
New-Item -ItemType Directory -Force C:\winfsp\inc\fuse, C:\winfsp\lib
Copy-Item "C:\Program Files (x86)\WinFsp\inc\fuse\*" C:\winfsp\inc\fuse\
Copy-Item "C:\Program Files (x86)\WinFsp\lib\winfsp-x64.lib" C:\winfsp\lib\libwinfsp.a
```

```bash
# Build (bash / Git Bash)
export PATH="/c/mingw64/bin:$PATH"
CGO_ENABLED=1 \
  CGO_CFLAGS="-IC:/winfsp/inc/fuse" \
  CGO_LDFLAGS="-LC:/winfsp/lib -lwinfsp" \
  go build -o dist/mediafs.exe ./cmd/mediafs
```

### Linux

```bash
sudo apt-get install -y libfuse3-dev gcc
CGO_ENABLED=1 go build -o dist/mediafs ./cmd/mediafs
```

### Run

```bash
./dist/mediafs          # mode tray (défaut)
./dist/mediafs mount    # headless — monte tous les serveurs configurés
```

## Tests & Lint

```bash
go test ./...                          # all tests
go test ./internal/connector/...       # connector tests only
go test -run TestJellyfin ./...        # single test by name
golangci-lint run                      # lint
```

## Module & Repo

- Module path: `github.com/CCoupel/Media_FS`
- GitHub: https://github.com/CCoupel/Media_FS
- Issues / backlog: https://github.com/CCoupel/Media_FS/issues
- Marketing site: https://ccoupel.github.io/Media_FS/

## Skills disponibles

| Commande | Rôle |
|---|---|
| `/marketing` | Régénère et déploie le site GitHub Pages (`gh-pages` branch) |
| `/dev-issue <N>` | Workflow complet de développement pour une issue (analyse → branch → workshop → plan → test → dev → build → QA → review → PR) |

## Versioning

Semantic Versioning — version courante dans `cmd/mediafs/version.go`.

| Bump | Quand |
|---|---|
| `patch` (0.0.x) | Bug fix, amélioration mineure sans nouvelle API |
| `minor` (0.x.0) | Nouvelle feature, nouvelle commande CLI, nouveau connecteur |
| `major` (x.0.0) | Changement breaking (interface connector, format config.yaml) |

Branches : `feat/issue-{N}-{slug}` ou `fix/issue-{N}-{slug}`
Tags : créés à la merge de PR (`v0.2.0`, etc.)

## Backlog

16 issues actives organisées en 5 épiques :

| Épique | Issues | Priorité |
|---|---|---|
| [#17 Epic P0 — Core VFS](https://github.com/CCoupel/Media_FS/issues/17) | #1 #2 #3 #4 #5 #6 #7 #8 | MVP |
| [#18 Epic P1.1 — Métadonnées](https://github.com/CCoupel/Media_FS/issues/18) | #9 #10 #11 #12 | Post-MVP |
| [#19 Epic P1.2 — Shell Properties](https://github.com/CCoupel/Media_FS/issues/19) | #13 | Post-MVP |
| [#20 Epic P3 — Plex](https://github.com/CCoupel/Media_FS/issues/20) | #14 | Future |
| [#21 Epic Infrastructure](https://github.com/CCoupel/Media_FS/issues/21) | #15 #16 | Transverse |

## Key constraints

- **Read-only**: never propagate write operations to remote servers (`EPERM` / `STATUS_ACCESS_DENIED`)
- **CGO required**: WinFSP and libfuse bindings cannot be avoided
- Pure-Go SQLite (`modernc.org/sqlite`) to avoid double CGO complexity
