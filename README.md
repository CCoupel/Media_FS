# Media_FS

Mount your Jellyfin, Emby, or Plex library as a real drive letter on Windows or a mountpoint on Linux — no sync, no local copy, streams directly.

```
Z:\
├── Jellyfin-Home\
│   ├── Films\
│   │   ├── Inception (2010)\
│   │   │   └── Inception (2010).mkv
│   │   └── ...
│   └── Séries\
│       └── ...
└── Emby-NAS\
    └── ...
```

Open any file in VLC, mpv, or Windows Media Player. Media_FS handles HTTP range requests and parallel chunk downloads transparently.

## Prerequisites

| Platform | Requirement |
|---|---|
| Windows | [WinFSP](https://winfsp.dev) ≥ 1.x — provides the virtual filesystem driver |
| Linux | `libfuse3-dev` — FUSE3 development package |

## Installation (Windows)

1. Install [WinFSP](https://winfsp.dev/rel/) (User mode file system driver).
2. Download the latest `mediafs-vX.Y.Z-windows-amd64.exe` from [Releases](https://github.com/CCoupel/Media_FS/releases).
3. Run it — a system tray icon appears.
4. Click **Configuration** in the tray menu to add your servers.

## Installation (Linux)

```bash
sudo apt-get install -y libfuse3-dev   # Debian/Ubuntu
```

Download the latest `mediafs-vX.Y.Z-linux-amd64` binary from [Releases](https://github.com/CCoupel/Media_FS/releases), make it executable, and run it.

## Quick start

1. **Add a server** in the configuration UI (right-click the tray icon → Configuration).
2. Set the type (Jellyfin / Emby), the URL, and your API key or username+password.
3. Click **Test** to verify the connection, then **Save**.
4. The drive mounts automatically. A green play icon in the tray confirms everything is OK.

## Configuration file

The configuration is stored in YAML at:

| Platform | Path |
|---|---|
| Windows | `%APPDATA%\Media_FS\config.yaml` |
| Linux | `~/.config/Media_FS/config.yaml` |

Example:

```yaml
mount:
  drive_letter: "Z"      # Windows — ignored on Linux
  mount_point: "/mnt/mediafs"  # Linux — ignored on Windows

servers:
  - alias: Jellyfin-Home
    type: jellyfin
    url: http://192.168.1.10:8096
    username: cyril
    api_key: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    enabled: true

download:
  parallel_chunks: 4
  chunk_size_mb: 16
  read_ahead_window_mb: 64
  read_ahead_windows: 2
  max_cache_mb: 1024
  cache_ttl_min: 60
```

## CLI commands

```
mediafs                        # start with system tray (default)
mediafs mount [key...]         # headless mount
mediafs status                 # ping configured servers
mediafs refresh [key...]       # invalidate metadata cache
mediafs help                   # show usage
```

## Build from source

CGO is required (cgofuse links against WinFSP / libfuse).

### Windows

Requirements:
- [WinFSP](https://winfsp.dev/rel/) installed at `C:\Program Files (x86)\WinFsp\`
- [MinGW-w64](https://www.mingw-w64.org/) with `gcc` at `C:\mingw64\bin\`

WinFSP headers must be staged once to a path without spaces (ld doesn't handle spaces in `-L`):

```powershell
New-Item -ItemType Directory -Force C:\winfsp\inc\fuse, C:\winfsp\lib
Copy-Item "C:\Program Files (x86)\WinFsp\inc\fuse\*" C:\winfsp\inc\fuse\
Copy-Item "C:\Program Files (x86)\WinFsp\lib\winfsp-x64.lib" C:\winfsp\lib\libwinfsp.a
```

Then build (Git Bash or WSL):

```bash
export PATH="/c/mingw64/bin:$PATH"
CGO_ENABLED=1 \
  CGO_CFLAGS="-IC:/winfsp/inc/fuse" \
  CGO_LDFLAGS="-LC:/winfsp/lib -lwinfsp" \
  go build -o dist/mediafs.exe ./cmd/mediafs
```

### Linux

```bash
sudo apt-get install -y libfuse-dev gcc   # fuse2 — cgofuse v1.6.0 uses pkg-config: fuse
CGO_ENABLED=1 go build -o dist/mediafs ./cmd/mediafs
```

### Tests & lint

```bash
go test ./...
golangci-lint run
```

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for a full description of the package layout and data flow.

## License

MIT — see [LICENSE](LICENSE).
