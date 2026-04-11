package vfs

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/CCoupel/Media_FS/internal/cache"
	"github.com/CCoupel/Media_FS/internal/connector"
	"github.com/CCoupel/Media_FS/internal/downloader"
	"github.com/CCoupel/Media_FS/pkg/nfo"
)

// MountedServer groups a connector with its server key and cached libraries.
type MountedServer struct {
	Key       string // "user@alias"
	Conn      connector.MediaConnector
	Libraries []connector.Library
}

// MediaFS implements fuse.FileSystemInterface.
// One instance serves all mounted servers under a single mount point.
type MediaFS struct {
	fuse.FileSystemBase

	mu       sync.RWMutex
	servers  map[string]*MountedServer // key → server
	cache    *cache.Cache
	dl       *downloader.Downloader

	// open file handles: handle → streamURL + current offset
	handles map[uint64]*fileHandle
	nextFH  uint64
	handlesMu sync.Mutex
}

type fileHandle struct {
	streamURL  string
	fileSize   int64
	serverKey  string
	nfoContent []byte // non-nil: virtual .nfo file; serve from slice, no HTTP call
}

// New creates a MediaFS instance.
func New(c *cache.Cache, dl *downloader.Downloader) *MediaFS {
	return &MediaFS{
		servers: make(map[string]*MountedServer),
		cache:   c,
		dl:      dl,
		handles: make(map[uint64]*fileHandle),
	}
}

// AddServer registers a server under the filesystem.
func (fs *MediaFS) AddServer(s *MountedServer) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.servers[s.Key] = s
}

// RemoveServer unregisters a server.
func (fs *MediaFS) RemoveServer(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.servers, key)
}

// --- fuse.FileSystemInterface ---

func (fs *MediaFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	parts := splitPath(path)

	switch len(parts) {
	case 0: // root "/"
		stat.Mode = fuse.S_IFDIR | 0555
		return 0
	case 1: // "user@server" folder
		fs.mu.RLock()
		_, ok := fs.servers[parts[0]]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFDIR | 0555
		return 0
	}

	// Virtual .nfo sidecar: check before general resolution
	if strings.HasSuffix(parts[len(parts)-1], ".nfo") {
		item, err := fs.resolveNFOItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFREG | 0444
		stat.Size = 2048 // conservative estimate; exact size served at Read
		return 0
	}

	// Virtual artwork sidecar
	if _, _, ok := splitArtworkName(parts[len(parts)-1]); ok {
		item, artType, err := fs.resolveArtworkItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFREG | 0444
		if data, ok, _ := fs.cache.GetArtwork(parts[0], item.ID, string(artType)); ok {
			stat.Size = int64(len(data))
		} else {
			stat.Size = 51200 // 50 KB estimate before first fetch
		}
		return 0
	}

	// Deeper paths: resolve via connector
	item, err := fs.resolveItem(parts)
	if err != nil {
		return -fuse.ENOENT
	}
	if item == nil {
		return -fuse.ENOENT
	}

	if item.IsFolder {
		stat.Mode = fuse.S_IFDIR | 0555
	} else {
		stat.Mode = fuse.S_IFREG | 0444
		stat.Size = item.FileSize
		if item.DateAdded != "" {
			if t, err := time.Parse(time.RFC3339Nano, item.DateAdded); err == nil {
				stat.Mtim.Sec = t.Unix()
				stat.Mtim.Nsec = int64(t.Nanosecond())
			}
		}
	}
	return 0
}

func (fs *MediaFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) int {

	fill(".", nil, 0)
	fill("..", nil, 0)

	parts := splitPath(path)

	if len(parts) == 0 {
		// Root: list all server keys
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		for key := range fs.servers {
			fill(key, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0555}, 0)
		}
		return 0
	}

	if len(parts) == 1 {
		// List libraries for this server
		fs.mu.RLock()
		srv, ok := fs.servers[parts[0]]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.ENOENT
		}
		for _, lib := range srv.Libraries {
			fill(lib.Name, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0555}, 0)
		}
		return 0
	}

	// List items under a library/folder
	items, err := fs.listItems(parts)
	if err != nil {
		return -fuse.EIO
	}
	for _, it := range items {
		st := &fuse.Stat_t{}
		if it.IsFolder {
			st.Mode = fuse.S_IFDIR | 0555
		} else {
			st.Mode = fuse.S_IFREG | 0444
			st.Size = it.FileSize
		}
		fill(it.Name, st, 0)

		// Inject virtual sidecar files (.nfo + artwork)
		base := strings.TrimSuffix(it.Name, extensionOf(it.Name))
		if !it.IsFolder {
			fill(base+".nfo", &fuse.Stat_t{Mode: fuse.S_IFREG | 0444, Size: 1024}, 0)
		}
		for _, artFile := range connector.ArtworkFilenames(it.Type) {
			fill(base+"-"+artFile, &fuse.Stat_t{Mode: fuse.S_IFREG | 0444, Size: 51200}, 0)
		}
	}
	return 0
}

func (fs *MediaFS) Open(path string, flags int) (int, uint64) {
	if flags&fuse.O_RDWR != 0 || flags&fuse.O_WRONLY != 0 {
		return -fuse.EPERM, ^uint64(0)
	}

	parts := splitPath(path)
	if len(parts) < 2 {
		return -fuse.ENOENT, ^uint64(0)
	}

	// Virtual artwork sidecar
	if _, _, ok := splitArtworkName(parts[len(parts)-1]); ok {
		item, artType, err := fs.resolveArtworkItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT, ^uint64(0)
		}
		data, err := fs.fetchArtwork(parts[0], item, artType)
		if err != nil {
			return -fuse.EIO, ^uint64(0)
		}
		if data == nil {
			return -fuse.ENOENT, ^uint64(0) // 404 from server
		}
		fs.handlesMu.Lock()
		fh := fs.nextFH
		fs.nextFH++
		fs.handles[fh] = &fileHandle{
			nfoContent: data,
			fileSize:   int64(len(data)),
		}
		fs.handlesMu.Unlock()
		return 0, fh
	}

	// Virtual .nfo sidecar
	if strings.HasSuffix(parts[len(parts)-1], ".nfo") {
		item, err := fs.resolveNFOItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT, ^uint64(0)
		}
		content, err := fs.generateNFO(parts[0], item)
		if err != nil {
			return -fuse.EIO, ^uint64(0)
		}
		fs.handlesMu.Lock()
		fh := fs.nextFH
		fs.nextFH++
		fs.handles[fh] = &fileHandle{
			nfoContent: content,
			fileSize:   int64(len(content)),
		}
		fs.handlesMu.Unlock()
		return 0, fh
	}

	item, err := fs.resolveItem(parts)
	if err != nil || item == nil || item.IsFolder {
		return -fuse.ENOENT, ^uint64(0)
	}

	srv := fs.serverForKey(parts[0])
	if srv == nil {
		return -fuse.ENOENT, ^uint64(0)
	}

	streamURL, err := srv.Conn.GetStreamURL(item.ID)
	if err != nil {
		return -fuse.EIO, ^uint64(0)
	}

	fs.handlesMu.Lock()
	fh := fs.nextFH
	fs.nextFH++
	fs.handles[fh] = &fileHandle{
		streamURL: streamURL,
		fileSize:  item.FileSize,
		serverKey: parts[0],
	}
	fs.handlesMu.Unlock()

	return 0, fh
}

func (fs *MediaFS) Read(path string, buf []byte, ofst int64, fh uint64) int {
	fs.handlesMu.Lock()
	h, ok := fs.handles[fh]
	fs.handlesMu.Unlock()
	if !ok {
		return -fuse.EBADF
	}

	// Virtual .nfo: serve from in-memory slice
	if h.nfoContent != nil {
		if ofst >= int64(len(h.nfoContent)) {
			return 0
		}
		return copy(buf, h.nfoContent[ofst:])
	}

	length := int64(len(buf))
	if ofst+length > h.fileSize {
		length = h.fileSize - ofst
	}
	if length <= 0 {
		return 0
	}

	data, err := fs.dl.ReadAt(h.streamURL, nil, ofst, length)
	if err != nil {
		return -fuse.EIO
	}

	return copy(buf, data)
}

func (fs *MediaFS) Release(path string, fh uint64) int {
	fs.handlesMu.Lock()
	delete(fs.handles, fh)
	fs.handlesMu.Unlock()
	return 0
}

// Write operations are rejected — read-only filesystem.
func (fs *MediaFS) Write(path string, buf []byte, ofst int64, fh uint64) int { return -fuse.EPERM }
func (fs *MediaFS) Create(path string, flags int, mode uint32) (int, uint64)  { return -fuse.EPERM, 0 }
func (fs *MediaFS) Mkdir(path string, mode uint32) int                        { return -fuse.EPERM }
func (fs *MediaFS) Unlink(path string) int                                    { return -fuse.EPERM }
func (fs *MediaFS) Rename(oldpath, newpath string) int                        { return -fuse.EPERM }

// --- helpers ---

func splitPath(path string) []string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func extensionOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

func (fs *MediaFS) serverForKey(key string) *MountedServer {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.servers[key]
}

func (fs *MediaFS) resolveItem(parts []string) (*connector.MediaItem, error) {
	// parts[0]=serverKey, parts[1]=libraryName, parts[2..n]=path within library
	srv := fs.serverForKey(parts[0])
	if srv == nil {
		return nil, nil
	}

	// Find library by name
	var lib *connector.Library
	for i := range srv.Libraries {
		if srv.Libraries[i].Name == parts[1] {
			lib = &srv.Libraries[i]
			break
		}
	}
	if lib == nil {
		return nil, nil
	}

	// Walk down the path
	parentID := ""
	var found *connector.MediaItem
	for depth := 2; depth <= len(parts); depth++ {
		items, err := fs.listItemsCached(parts[0], lib.ID, parentID)
		if err != nil {
			return nil, err
		}
		var next *connector.MediaItem
		for i := range items {
			if items[i].Name == parts[depth-1] {
				next = &items[i]
				break
			}
		}
		if next == nil {
			return nil, nil
		}
		found = next
		parentID = next.ID
	}
	return found, nil
}

func (fs *MediaFS) listItems(parts []string) ([]connector.MediaItem, error) {
	srv := fs.serverForKey(parts[0])
	if srv == nil {
		return nil, nil
	}

	var lib *connector.Library
	for i := range srv.Libraries {
		if srv.Libraries[i].Name == parts[1] {
			lib = &srv.Libraries[i]
			break
		}
	}
	if lib == nil {
		return nil, nil
	}

	parentID := ""
	if len(parts) > 2 {
		parent, err := fs.resolveItem(parts[:len(parts)])
		if err != nil || parent == nil {
			return nil, err
		}
		parentID = parent.ID
	}

	return fs.listItemsCached(parts[0], lib.ID, parentID)
}

// splitArtworkName parses a virtual artwork filename, e.g. "Inception-poster.jpg"
// → ("Inception", "poster.jpg", true). Returns ok=false for non-artwork names.
func splitArtworkName(name string) (base, artFile string, ok bool) {
	for _, af := range []string{"poster.jpg", "fanart.jpg", "banner.jpg", "thumb.jpg", "folder.jpg"} {
		suffix := "-" + af
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), af, true
		}
	}
	return "", "", false
}

// resolveArtworkItem finds the media item and ArtworkType for a virtual artwork path.
func (fs *MediaFS) resolveArtworkItem(parts []string) (*connector.MediaItem, connector.ArtworkType, error) {
	itemBase, artFile, ok := splitArtworkName(parts[len(parts)-1])
	if !ok {
		return nil, "", nil
	}
	artType, ok := connector.ArtworkTypeForFilename(artFile)
	if !ok {
		return nil, "", nil
	}
	parentParts := parts[:len(parts)-1]
	if len(parentParts) < 2 {
		return nil, "", nil
	}
	items, err := fs.listItems(parentParts)
	if err != nil {
		return nil, "", err
	}
	for i := range items {
		base := strings.TrimSuffix(items[i].Name, extensionOf(items[i].Name))
		if base == itemBase {
			return &items[i], artType, nil
		}
	}
	return nil, "", nil
}

// fetchArtwork returns artwork bytes from cache or fetches from the server.
// Returns nil data (no error) when the server responds 404.
func (fs *MediaFS) fetchArtwork(serverKey string, item *connector.MediaItem, artType connector.ArtworkType) ([]byte, error) {
	cacheKey := string(artType)

	if data, ok, _ := fs.cache.GetArtwork(serverKey, item.ID, cacheKey); ok {
		return data, nil
	}

	srv := fs.serverForKey(serverKey)
	if srv == nil {
		return nil, fmt.Errorf("server %q not found", serverKey)
	}
	artURL, err := srv.Conn.GetArtworkURL(item.ID, artType)
	if err != nil {
		return nil, err
	}

	resp, err := fs.dl.HTTPClient.Get(artURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // silently treat 404 as absent
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork %s HTTP %d", artURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = fs.cache.StoreArtwork(serverKey, item.ID, cacheKey, data)
	return data, nil
}

// resolveNFOItem finds the media item whose base name matches a .nfo path.
// e.g. ["cyril@Home", "Films", "Inception.nfo"] → MediaItem for "Inception.mkv"
func (fs *MediaFS) resolveNFOItem(parts []string) (*connector.MediaItem, error) {
	nfoBase := strings.TrimSuffix(parts[len(parts)-1], ".nfo")
	parentParts := parts[:len(parts)-1]
	if len(parentParts) < 2 {
		return nil, nil
	}
	items, err := fs.listItems(parentParts)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if !items[i].IsFolder {
			base := strings.TrimSuffix(items[i].Name, extensionOf(items[i].Name))
			if base == nfoBase {
				return &items[i], nil
			}
		}
	}
	return nil, nil
}

// generateNFO fetches metadata (via cache) and renders the NFO XML.
func (fs *MediaFS) generateNFO(serverKey string, item *connector.MediaItem) ([]byte, error) {
	var meta connector.ItemMetadata
	if ok, _ := fs.cache.GetMetadata(serverKey, item.ID, &meta); !ok {
		srv := fs.serverForKey(serverKey)
		if srv == nil {
			return nil, fmt.Errorf("server %q not found", serverKey)
		}
		var err error
		meta, err = srv.Conn.GetItemMetadata(item.ID)
		if err != nil {
			return nil, err
		}
		_ = fs.cache.StoreMetadata(serverKey, item.ID, meta)
	}
	switch item.Type {
	case connector.ItemTypeSeries:
		return nfo.TVShow(meta)
	case connector.ItemTypeEpisode:
		return nfo.Episode(meta, meta.SeasonNumber, meta.EpisodeNumber)
	default:
		return nfo.Movie(meta)
	}
}

func (fs *MediaFS) listItemsCached(serverKey, libID, parentID string) ([]connector.MediaItem, error) {
	cacheKey := parentID
	if cacheKey == "" {
		cacheKey = libID
	}

	var items []connector.MediaItem
	if ok, err := fs.cache.GetItems(serverKey, cacheKey, &items); ok {
		return items, err
	}

	srv := fs.serverForKey(serverKey)
	if srv == nil {
		return nil, nil
	}

	items, err := srv.Conn.GetItems(libID, parentID)
	if err != nil {
		return nil, err
	}

	_ = fs.cache.StoreItems(serverKey, cacheKey, items)
	return items, nil
}
