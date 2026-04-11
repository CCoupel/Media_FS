package connector

import "github.com/CCoupel/Media_FS/internal/config"

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
	FileSize  int64  // 0 for folders
	DateAdded string // ISO 8601 from server, used for mtime
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
