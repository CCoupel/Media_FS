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
| `Readdir` | List directory contents |
| `Open` | Resolve path → stream URL, return file handle |
| `Read` | HTTP range request to stream URL |
| `Release` | Close HTTP connection |

All write methods (`Write`, `Create`, `Mkdir`, `Unlink`, `Rename`) return `-fuse.EPERM`.

## Path resolution

```
/cyril@HomeServer/Films/Inception (2010)/Inception.mkv
  → split on "/"
  → [0] = server key "cyril@HomeServer" → look up connector
  → [1] = library name "Films"
  → [2..n-1] = parent folders → resolve to itemID via cache
  → [n] = file name → look up in item list for that parent
```

Path-to-itemID resolution is cached. On cache miss, walk the tree from the nearest cached ancestor.

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

Virtual files (`.nfo`, artwork) report their in-memory size at `Getattr` time.
