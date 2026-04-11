# VFS Layer

## Library

- **Windows**: `github.com/billziss-gh/cgofuse` wrapping WinFSP. Requires WinFSP installed on the target machine. → [issue #1](https://github.com/CCoupel/Media_FS/issues/1)
- **Linux**: same library, wrapping libfuse3. Requires `libfuse3-dev` at build time, `libfuse3` at runtime. → [issue #2](https://github.com/CCoupel/Media_FS/issues/2)

The same Go code runs on both platforms — cgofuse abstracts the OS difference.

## Filesystem struct

`internal/vfs/fs.go` defines `MediaFS` which embeds `fuse.FileSystemBase` and overrides only the methods we need:

| Method | Purpose |
|---|---|
| `Getattr` | File/dir attributes (size, mtime, mode) |
| `Readdir` | List directory contents, inject virtual sidecars |
| `Open` | Resolve path → stream URL, create `ReadAheadReader`, return handle |
| `Read` | Serve bytes from `ReadAheadReader` (window cache) or direct HTTP Range |
| `Release` | Release reader's active window references (data stays in `WindowCache`) |

All write methods (`Write`, `Create`, `Mkdir`, `Unlink`, `Rename`) return `-fuse.EPERM`.

## File handle

```go
type fileHandle struct {
    streamURL  string
    fileSize   int64
    serverKey  string
    nfoContent []byte              // non-nil: virtual .nfo / artwork, served from memory
    path       string
    reader     *downloader.ReadAheadReader  // nil for virtual files
}
```

`reader` is nil for `.nfo` and artwork sidecars (served entirely from memory). For real media files with `fileSize > 0` and `ReadAheadWindows > 0`, a `ReadAheadReader` is created at `Open` time.

## Path resolution

```
/cyril@HomeServer/Films/Inception (2010)/Inception.mkv
  → split on "/"
  → [0] = server key "cyril@HomeServer" → look up connector
  → [1] = library name "Films"
  → [2..n-1] = parent folders → resolve to itemID via cache
  → [n] = file name → look up in item list for that parent
```

Path-to-itemID resolution is cached in `cache.memItems` (sync.Map). On cache miss, walk the tree from the nearest cached ancestor.

## Streaming — ReadAheadReader

`Open` creates a `ReadAheadReader` which coordinates prefetching against the shared `WindowCache`:

```
Open(path)
  → GetStreamURL()
  → NewReadAheadReader(url, fileSize, windowSizeMB, maxActive)
      → launch goroutines: window 0, window 1, …, window (maxActive-1), last window
        all in parallel — last window covers MP4 moov atom / MKV cues
  → store in fileHandle.reader
```

```
Read(offset, buf)
  → ReadAheadReader.ReadAt(buf, offset)
      → find window aligned to offset in WindowCache
      → <-w.done  (block only on the window currently needed)
      → copy bytes from window.data[offset - window.Start :]
      → if offset >= 75% of window:
            WindowCache.GetOrCreate(aligned + maxActive * windowSize)
            ← keeps pipeline always maxActive windows ahead
  → return n bytes
```

```
Release(fh)
  → reader.Close()  ← sets active=nil, releases references
                      window data remains in WindowCache for reuse
  → delete handle
```

**Why `Release` does not drop window data:**
If the same file is re-opened (seek-back, second client, explorer preview), the `WindowCache` serves it instantly from the already-fetched buffers. The `WindowCache`'s LRU eviction and TTL sweep handle memory reclamation.

## Direct fallback

If `reader == nil` (virtual file not caught earlier) or on a `ReadAheadReader` cache error, `vfs.Read` falls back to `downloader.ReadAt` — a single direct HTTP Range request. This is also used for seek positions whose window has not been prefetched yet.

## Virtual files — issues [#10](https://github.com/CCoupel/Media_FS/issues/10) [#11](https://github.com/CCoupel/Media_FS/issues/11) [#16](https://github.com/CCoupel/Media_FS/issues/16)

Files not present in the server's item list but synthesized by Media_FS:

- `*.nfo` — inserted alongside every media item, generated via `pkg/nfo`
- `{name}-poster.jpg`, `{name}-fanart.jpg`, … — artwork, fetched once and cached as SQLite blobs
- `desktop.ini` — injected at the `user@server` level to set the folder icon (Windows only)

Artwork virtual filenames are prefixed with the item base name to avoid collisions within a flat library folder (e.g. `Inception-poster.jpg` next to `Inception.mkv`).

### Artwork naming table

| Item type | Virtual files injected |
|---|---|
| `Movie` | `{name}-poster.jpg`, `{name}-fanart.jpg` |
| `Series` | `{name}-poster.jpg`, `{name}-fanart.jpg`, `{name}-banner.jpg` |
| `Season` | `{name}-poster.jpg` |
| `Episode` | `{name}-thumb.jpg` |
| `MusicAlbum` | `{name}-folder.jpg`, `{name}-fanart.jpg` |

`{name}` = item name without file extension. Artwork is served from `cache.GetArtwork` (SQLite blob, TTL 24 h); on miss, fetched via `connector.GetArtworkURL` and stored. A 404 from the server returns `ENOENT` silently.

## Mount points

- **Windows**: drive letter from config (e.g. `Z:`), mounted via WinFSP
- **Linux**: directory from config (e.g. `/mnt/mediafs`), mounted via FUSE

Multiple `user@server` entries are all served by a single `MediaFS` instance on a single mount point.

## File size reporting

For `.mkv`/`.mp4`/`.flac` etc., size comes from `ItemMetadata.FileSize` (API field). This lets the OS show the correct size before any data is transferred, which is required for Explorer to show progress during copy.

Virtual files (`.nfo`, artwork) report their in-memory size at `Getattr` time. NFO size is estimated at 4096 bytes until the first `Open` generates and caches the real content.

## Monitoring API

`MediaFS` exposes three methods consumed by `webui.Server`:

| Method | Description |
|---|---|
| `OpenFiles() []FileStatus` | All handles with a live `ReadAheadReader`, including window states |
| `PurgeFile(fh) bool` | Drop all `WindowCache` entries for one file handle |
| `PurgeWindow(fh, start) bool` | Drop one specific window for a file handle |
