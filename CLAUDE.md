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

```bash
# Windows (requires WinFSP installed + gcc via mingw)
CGO_ENABLED=1 GOOS=windows go build -o dist/mediafs.exe ./cmd/mediafs

# Linux (requires libfuse3-dev)
CGO_ENABLED=1 GOOS=linux go build -o dist/mediafs ./cmd/mediafs

# Run (mounts all configured servers)
./dist/mediafs mount

# Run with system tray (default mode)
./dist/mediafs
```

## Tests & Lint

```bash
go test ./...                          # all tests
go test ./internal/connector/...       # connector tests only
go test -run TestJellyfin ./...        # single test by name
golangci-lint run                      # lint
```

## Module

Module path: `mediafs` — update to match the GitHub remote once the repo is published.

## Key constraints

- **Read-only**: never propagate write operations to remote servers (`EPERM` / `STATUS_ACCESS_DENIED`)
- **CGO required**: WinFSP and libfuse bindings cannot be avoided
- Pure-Go SQLite (`modernc.org/sqlite`) to avoid double CGO complexity
