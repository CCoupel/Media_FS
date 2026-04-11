# Architecture

## Package layout

```
cmd/mediafs/          Entry point — CLI parsing, tray launch              issue: #15
internal/
  config/             Config struct, YAML load/save, server registry       issue: #8
  connector/          MediaConnector interface + per-server implementations
    jellyfin/         Jellyfin REST connector                              issue: #3
    emby/             Emby adapter (wraps jellyfin, overrides auth)        issue: #4
    plex/             Plex connector (P3)                                  issue: #14
  vfs/                WinFSP/FUSE filesystem                              issues: #1 #2 #5
  cache/              In-memory item/metadata cache + SQLite artwork cache issue: #12
  downloader/         Parallel HTTP Range downloader + shared window cache issue: #6
    downloader.go     Downloader struct, fetchChunk, singleflight dedup
    readahead.go      ReadAheadReader — per-file prefetch coordinator
    window_cache.go   WindowCache — shared LRU cache, TTL, eviction
  tray/               System tray icon + menu (fyne-io/systray)            issue: #7
  webui/              Local HTTP config UI, opened in browser              issue: #7
pkg/
  nfo/                NFO XML generation (Kodi-compatible)                 issue: #10
assets/
  icons/              .ico files per connector type (embedded)             issue: #16
  tray/               Tray icon variants (idle, active, error)             issue: #16
```

## Data flow — directory listing

```
Explorer readdir("Z:\cyril@HomeServer\Films")
  → vfs.Readdir()
    → cache.GetItems(serverKey, parentID)   ← L1 hit: sync.Map, return immediately
    → [miss] connector.GetItems()           ← HTTP call to Jellyfin/Emby API
      → cache.StoreItems()                  ← store in sync.Map (TTL 5 min)
    → build []FileInfo (name, size, isDir)
    → inject virtual sidecars (.nfo, artwork filenames)
```

## Data flow — file read (streaming)

```
VLC opens "Z:\cyril@HomeServer\Films\Inception.mkv"
  → vfs.Open()
    → connector.GetStreamURL()  → streamURL with auth token
    → downloader.NewReadAheadReader(streamURL, fileSize, windowSizeMB, maxActive)
      → WindowCache.GetOrCreate(window 0)   → goroutine: fetch in background
      → WindowCache.GetOrCreate(window 1)   → goroutine: fetch in background  } parallel
      → WindowCache.GetOrCreate(last window) → goroutine: fetch in background }
    → returns fileHandle{reader: ReadAheadReader}

VLC issues Read(offset=0, length=65536)
  → vfs.Read()
    → ReadAheadReader.ReadAt(buf, offset=0)
      → find/create window for offset → already in WindowCache (hit or wait)
      → <-w.done  (block until window data ready)
      → copy from window data at (offset - window.Start)
      → if offset >= 75% of window:
          WindowCache.GetOrCreate(current + maxActive * windowSize) ← keep pipeline full
    → return bytes to VLC

VLC issues Read(offset=fileSize-N, …)  ← index seek (MP4 moov / MKV cues)
  → last window already in WindowCache → immediate return, no wait
```

## Data flow — file copy (parallel download)

```
Explorer copies Inception.mkv to D:\
  → OS issues sequential Read() calls for the full file
  → each Read() goes through ReadAheadReader → WindowCache
    → windows fetched in parallel (N chunks per window via goroutines)
    → singleflight deduplicates concurrent identical chunk requests
    → chunks assembled in order within each window
  → sequential reads are served from the in-memory window buffer
```

## WindowCache — shared prefetch cache

`internal/downloader/window_cache.go`

**Key design decisions:**

| Decision | Rationale |
|---|---|
| Windows survive `Release()` | Re-opening a file (e.g. seek back, second client) gets data from cache instantly |
| Shared across all ReadAheadReaders | Two clients opening the same file share the same fetched data |
| LRU eviction on size limit | Bounded memory regardless of how many files are open |
| TTL sweep (1-min ticker) | Prevents stale data from accumulating when files are not re-read |
| `singleflight` on fetchChunk | Two concurrent reads for the same byte range share a single HTTP request |
| `SetLimits()` at runtime | Config changes (max MB, TTL) apply without restarting the process |

**Window lifecycle:**

```
GetOrCreate(url, start) ──→ WindowFetching ──→ WindowReady
                                          └──→ WindowError (removed from cache; next open retries)

sweeper (every 1 min): remove WindowReady windows where lastAccess > TTL
evict():               remove LRU WindowReady windows when dataBytes > maxBytes
PurgeByAge(frac):      remove WindowReady windows where elapsed/TTL >= frac
```

**Pre-fetch strategy at file open** (per `NewReadAheadReader`):
1. Windows `0 … maxActive-1` launched in parallel
2. Last window (end of file) launched immediately — media players read it for the index (MP4 moov atom, MKV cues, AVI index)
3. At 75% through window N → launch window `N + maxActive` to keep the pipeline full

## Cache layers

```
Request type       L1 (in-process)         L2 (disk)       TTL
─────────────────────────────────────────────────────────────────
Directory listing  sync.Map                —               5 min (default)
Item metadata      sync.Map                —               1 h   (default)
Artwork (blobs)    —                       SQLite          24 h  (default)
Media windows      WindowCache (LRU)       —               2 h   (default, 2 GB max)
```

**Why sync.Map for items/metadata, not SQLite?**
Items and metadata change rarely and are small (JSON, a few KB). An in-memory map avoids serialization overhead on every readdir. SQLite was benchmarked as the bottleneck for fast Explorer navigation — removing it reduced readdir latency by ~10×.

**Why SQLite only for artwork?**
Artwork blobs (50 KB – 2 MB each) are worth persisting across restarts. Re-fetching 100 posters on every app launch would be slow. Items and metadata are cheap to re-fetch on restart.

## Virtual files (generated in-memory, never written to server)

| File | Generated by | Issue |
|---|---|---|
| `*.nfo` | `pkg/nfo` — XML from item metadata | [#10](https://github.com/CCoupel/Media_FS/issues/10) |
| `poster.jpg`, `fanart.jpg` | Proxied from connector.GetArtworkURL() | [#11](https://github.com/CCoupel/Media_FS/issues/11) |
| `desktop.ini` | `internal/vfs` — sets folder icon per connector type | [#16](https://github.com/CCoupel/Media_FS/issues/16) |

## Config + tray interaction

```
mediafs starts
  → config.Load() reads config.yaml
  → Downloader.Init() creates WindowCache (maxBytes, TTL from config)
  → tray.Start() shows icon in system tray
  → for each enabled server: connector.Connect() + vfs.Mount()
  → tray menu: status per server, "Configuration" opens localhost webui
  → webui onSave: WindowCache.SetLimits() + config.yaml write (hot-reload)
```

## WebUI — Activity tab

Served at `GET /api/monitor` → `downloader.MonitorData`:

```json
{
  "files":       [ FileStatus{fh, path, file_size, window_size, windows[]} ],
  "cache":       { "data_mb", "max_mb", "count", "ttl_min" },
  "all_windows": [ WindowStatus{start, end, state, last_access_ago_sec, expires_in_sec} ],
  "heap_inuse_mb": 142
}
```

`all_windows` is sorted oldest-first (by `last_access_ago_sec` desc) so the UI can render the age bar left→right without client-side sorting.

Purge endpoints:
- `POST /api/monitor/purge-cache` `{"min_age_frac": 0.0|0.5|0.9}` — global selective eviction
- `POST /api/monitor/purge-file` `{"fh": N}` — drop all windows for one open file
