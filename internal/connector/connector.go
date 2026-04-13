package connector

import (
	"strings"

	"github.com/CCoupel/Media_FS/internal/config"
)

// illegalChars maps characters that are illegal in filesystem names (Windows
// NTFS forbids all of them; Linux/FUSE forbids only '/' and NUL) to visually
// similar Unicode fullwidth equivalents so titles remain readable.
var illegalChars = strings.NewReplacer(
	"/", "∕",  // U+2215 DIVISION SLASH   — path separator on all OSes
	"\\", "＼", // U+FF3C FULLWIDTH REVERSE SOLIDUS
	":", "：",  // U+FF1A FULLWIDTH COLON
	"*", "＊",  // U+FF0A FULLWIDTH ASTERISK
	"?", "？",  // U+FF1F FULLWIDTH QUESTION MARK
	"\"", "＂", // U+FF02 FULLWIDTH QUOTATION MARK
	"<", "＜",  // U+FF1C FULLWIDTH LESS-THAN SIGN
	">", "＞",  // U+FF1E FULLWIDTH GREATER-THAN SIGN
	"|", "｜",  // U+FF5C FULLWIDTH VERTICAL LINE
)

// sanitizeName replaces characters illegal in filesystem names with
// visually-similar Unicode fullwidth equivalents.
func sanitizeName(s string) string {
	return illegalChars.Replace(s)
}

// ArtworkType represents the type of artwork to retrieve.
type ArtworkType string

const (
	ArtworkPoster    ArtworkType = "Primary"
	ArtworkFanart    ArtworkType = "Backdrop"
	ArtworkThumb     ArtworkType = "Thumb"
	ArtworkBanner    ArtworkType = "Banner"
)

// ItemType represents the kind of media item.
type ItemType string

const (
	ItemTypeMovie      ItemType = "Movie"
	ItemTypeSeries     ItemType = "Series"
	ItemTypeSeason     ItemType = "Season"
	ItemTypeEpisode    ItemType = "Episode"
	ItemTypeMusicAlbum ItemType = "MusicAlbum"
	ItemTypeAudio      ItemType = "Audio"
	ItemTypeFolder     ItemType = "CollectionFolder"
)

// Library represents a top-level media library on the server.
type Library struct {
	ID   string
	Name string
	Type ItemType
}

// MediaItem is a file or folder within a library.
type MediaItem struct {
	ID        string
	ParentID  string
	Name      string
	Type      ItemType
	IsFolder  bool
	FileSize       int64  // 0 for folders
	DateAdded      string // ISO 8601 from server, used for mtime
	Extension      string // file extension with leading dot, e.g. ".mkv" (empty for folders)
	ProductionYear int    // not available from GetItems; kept for future use
	PremiereDate   string // not available from GetItems; kept for future use
}

// VFSName returns the name as it appears in the virtual filesystem.
// For media files it appends the extension (e.g. "Inception (2010).mkv").
// For folders the plain Name is returned.
// Characters illegal in filesystem names (e.g. '/', ':' on Windows) are
// replaced with Unicode fullwidth equivalents so every title is a valid entry.
func (m MediaItem) VFSName() string {
	if !m.IsFolder && m.Extension != "" {
		return sanitizeName(m.Name) + m.Extension
	}
	return sanitizeName(m.Name)
}

// ItemMetadata holds the full metadata for a media item.
type ItemMetadata struct {
	ID            string
	Name          string
	Type          ItemType
	Year          int
	Overview      string
	Rating        float64
	Genres        []string
	Directors     []string
	FileSize      int64
	RunTimeTicks  int64 // Jellyfin/Emby ticks (100ns units)
	DateAdded     string
	ExternalIDs   map[string]string // "imdb" → "tt1375666", etc.
	SeasonNumber  int
	EpisodeNumber int
	PremiereDate  string // ISO 8601 release date, used for birthtim
}

// MediaConnector is the interface all server connectors must implement.
type MediaConnector interface {
	// Connect initialises the connector and authenticates with the server.
	Connect(cfg config.ServerConfig) error

	// Ping checks that the server is reachable and the credentials are valid.
	Ping() error

	// GetLibraries returns the top-level media libraries for the authenticated user.
	GetLibraries() ([]Library, error)

	// GetItems returns the direct children of parentID within a library.
	// Pass parentID = "" to list the library root.
	GetItems(libraryID, parentID string) ([]MediaItem, error)

	// GetItemMetadata returns full metadata for a single item.
	GetItemMetadata(itemID string) (ItemMetadata, error)

	// GetArtworkURL returns the URL to download a specific artwork type.
	GetArtworkURL(itemID string, artType ArtworkType) (string, error)

	// GetStreamURL returns the direct HTTP URL for streaming the media file.
	// The URL must support HTTP Range requests.
	GetStreamURL(itemID string) (string, error)

	// GetFileSize returns the exact file size in bytes.
	GetFileSize(itemID string) (int64, error)
}

// ArtworkFilenames returns the virtual sidecar filenames for a given item type.
// Each name is intended to be prefixed with the item's base name, e.g. "Inception-poster.jpg".
func ArtworkFilenames(t ItemType) []string {
	switch t {
	case ItemTypeMovie:
		return []string{"poster.jpg", "fanart.jpg"}
	case ItemTypeSeries:
		return []string{"poster.jpg", "fanart.jpg", "banner.jpg"}
	case ItemTypeSeason:
		return []string{"poster.jpg"}
	case ItemTypeEpisode:
		return []string{"thumb.jpg"}
	case ItemTypeMusicAlbum:
		return []string{"folder.jpg", "fanart.jpg"}
	default:
		return nil
	}
}

// ArtworkTypeForFilename maps a sidecar filename to the ArtworkType used by the connector.
// Returns false if the filename is not a known artwork sidecar.
func ArtworkTypeForFilename(filename string) (ArtworkType, bool) {
	switch filename {
	case "poster.jpg", "folder.jpg":
		return ArtworkPoster, true
	case "fanart.jpg":
		return ArtworkFanart, true
	case "thumb.jpg":
		return ArtworkThumb, true
	case "banner.jpg":
		return ArtworkBanner, true
	default:
		return "", false
	}
}

// StreamHeadersProvider is an optional interface for connectors that require
// custom HTTP headers on streaming Range requests (e.g. Plex client identification).
// The VFS checks for this interface on Open and passes the headers to the downloader.
type StreamHeadersProvider interface {
	GetStreamHeaders() map[string]string
}

// Factory is a function that creates a new connector instance.
type Factory func() MediaConnector

var registry = map[config.ConnectorType]Factory{}

// Register adds a connector factory for a given server type.
func Register(t config.ConnectorType, f Factory) {
	registry[t] = f
}

// New creates a new connector for the given server type.
// Returns nil if the type is not registered.
func New(t config.ConnectorType) MediaConnector {
	f, ok := registry[t]
	if !ok {
		return nil
	}
	return f()
}
