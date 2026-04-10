# Connectors

## Interface

All connectors implement `connector.MediaConnector` defined in `internal/connector/connector.go`.

```go
type MediaConnector interface {
    Connect(cfg config.ServerConfig) error
    Ping() error
    GetLibraries() ([]Library, error)
    GetItems(libraryID, parentID string) ([]MediaItem, error)
    GetItemMetadata(itemID string) (ItemMetadata, error)
    GetArtworkURL(itemID string, artType ArtworkType) (string, error)
    GetStreamURL(itemID string) (string, error)
    GetFileSize(itemID string) (int64, error)
}
```

## Jellyfin / Emby — issues [#3](https://github.com/CCoupel/Media_FS/issues/3) [#4](https://github.com/CCoupel/Media_FS/issues/4)

Jellyfin and Emby share the same REST API structure (Emby is the upstream, Jellyfin is a fork). The Jellyfin connector is the base implementation; the Emby connector wraps it, overriding only the auth header (`X-Emby-Token` vs `X-MediaBrowser-Token`).

Key endpoints used:
- `GET /Users/{userId}/Views` — top-level libraries
- `GET /Users/{userId}/Items?ParentId={id}` — children of a folder
- `GET /Items/{itemId}` — full metadata
- `GET /Items/{itemId}/Images/{imageType}` — artwork (returns redirect URL)
- `GET /Videos/{itemId}/stream` — video stream (supports HTTP Range)
- `GET /Audio/{itemId}/stream` — audio stream

Auth: pass API key as `X-MediaBrowser-Token` (Jellyfin) or `X-Emby-Token` (Emby) header.

## Adding a new connector

1. Create `internal/connector/{name}/connector.go`
2. Implement `connector.MediaConnector`
3. Register in `internal/connector/registry.go`: add to `connectorFactories` map
4. Add connector type constant to `config.ConnectorType`
5. Add icon to `assets/icons/{name}.ico`
6. Add to tray icon map in `internal/tray/tray.go`

## Plex (P3) — issue [#14](https://github.com/CCoupel/Media_FS/issues/14)

Plex uses a different auth model (`X-Plex-Token`) and different endpoint structure. Implement as a standalone connector without reusing the Jellyfin base.

Key endpoints:
- `GET /library/sections` — libraries
- `GET /library/sections/{id}/all` — all items in a library
- `GET /library/metadata/{id}` — item metadata
- `GET /library/parts/{id}/file?X-Plex-Token={token}` — direct stream
