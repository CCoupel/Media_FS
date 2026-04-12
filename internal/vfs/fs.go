package vfs

import (
	"fmt"
	"io"
	"log"
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
	handles   map[uint64]*fileHandle
	nextFH    uint64
	handlesMu sync.Mutex

	// nfoCache stores generated NFO content keyed by VFS path so that
	// Getattr can return the real file size on subsequent calls.
	nfoCache sync.Map // path(string) → []byte
}

type fileHandle struct {
	streamURL   string
	streamHdrs  map[string]string // extra HTTP headers for Range requests (e.g. Plex)
	fileSize    int64
	serverKey   string
	nfoContent  []byte // non-nil: virtual .nfo/.artwork file; served from memory
	path        string
	reader      *downloader.ReadAheadReader // nil for small/virtual files
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
		dirStat(stat)
		return 0
	case 1: // "user@server" folder
		fs.mu.RLock()
		_, ok := fs.servers[parts[0]]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.ENOENT
		}
		dirStat(stat)
		return 0
	case 2: // "user@server/LibraryName" folder
		fs.mu.RLock()
		srv, ok := fs.servers[parts[0]]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.ENOENT
		}
		for _, lib := range srv.Libraries {
			if lib.Name == parts[1] {
				dirStat(stat)
				return 0
			}
		}
		return -fuse.ENOENT
	}

	// Virtual .nfo sidecar: check before general resolution
	if strings.HasSuffix(parts[len(parts)-1], ".nfo") {
		item, err := fs.resolveNFOItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFREG | 0444
		setTimestamps(stat, item.DateAdded, item.PremiereDate, item.ProductionYear)
		if cached, ok := fs.nfoCache.Load(path); ok {
			stat.Size = int64(len(cached.([]byte)))
		} else {
			// Generate eagerly so stat returns the exact size on first call.
			// Open() will serve from cache and skip regeneration.
			content, genErr := fs.generateNFO(parts[0], item)
			if genErr == nil {
				fs.nfoCache.Store(path, content)
				stat.Size = int64(len(content))
			} else {
				stat.Size = 4096 // fallback if server unreachable
			}
		}
		return 0
	}

	// Virtual artwork sidecar
	if _, _, ok := splitArtworkName(parts[len(parts)-1]); ok {
		item, artType, err := fs.resolveArtworkItem(parts)
		if err != nil || item == nil {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFREG | 0444
		setTimestamps(stat, item.DateAdded, item.PremiereDate, item.ProductionYear)
		if data, ok, _ := fs.cache.GetArtwork(parts[0], item.ID, string(artType)); ok {
			stat.Size = int64(len(data))
		} else {
			// Fetch eagerly so stat() returns exact size on first call.
			// Fixes Explorer thumbnail corruption caused by truncated JPEG reads.
			// Open() will serve from the warm cache without re-fetching.
			data, fetchErr := fs.fetchArtwork(parts[0], item, artType)
			if fetchErr == nil && data != nil {
				stat.Size = int64(len(data))
			} else {
				stat.Size = 51200 // fallback if server unreachable or 404
			}
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
		dirStat(stat)
		setTimestamps(stat, item.DateAdded, item.PremiereDate, item.ProductionYear)
	} else {
		stat.Mode = fuse.S_IFREG | 0444
		stat.Size = item.FileSize
		// Use cached metadata for PremiereDate/Year and, when the items listing
		// did not carry a file size (Plex omits it for some entries), for FileSize.
		premiereDate := item.PremiereDate
		productionYear := item.ProductionYear
		var meta connector.ItemMetadata
		if ok, _ := fs.cache.GetMetadata(parts[0], item.ID, &meta); ok {
			premiereDate = meta.PremiereDate
			productionYear = meta.Year
			if stat.Size == 0 && meta.FileSize > 0 {
				stat.Size = meta.FileSize
			}
		} else {
			srv := fs.serverForKey(parts[0])
			if srv != nil {
				go func(srvKey, itemID string) {
					m, err := srv.Conn.GetItemMetadata(itemID)
					if err == nil {
						_ = fs.cache.StoreMetadata(srvKey, itemID, m)
					}
				}(parts[0], item.ID)
			}
		}
		setTimestamps(stat, item.DateAdded, premiereDate, productionYear)
	}
	return 0
}

func (fs *MediaFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) int {

	fill(".", nil, 0)
	fill("..", nil, 0)

	parts := splitPath(path)
	log.Printf("[vfs] Readdir %q (depth=%d)", path, len(parts))

	if len(parts) == 0 {
		// Root: list all server keys
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		log.Printf("[vfs] root: %d servers", len(fs.servers))
		for key := range fs.servers {
			fill(key, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0555, Nlink: 2}, 0)
		}
		return 0
	}

	if len(parts) == 1 {
		// List libraries for this server
		fs.mu.RLock()
		srv, ok := fs.servers[parts[0]]
		fs.mu.RUnlock()
		if !ok {
			log.Printf("[vfs] server %q not found", parts[0])
			return -fuse.ENOENT
		}
		log.Printf("[vfs] server %q: %d libraries", parts[0], len(srv.Libraries))
		for _, lib := range srv.Libraries {
			fill(lib.Name, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0555, Nlink: 2}, 0)
		}
		return 0
	}

	// List items under a library/folder
	items, err := fs.listItems(parts)
	if err != nil {
		log.Printf("[vfs] listItems %q error: %v", path, err)
		return -fuse.EIO
	}
	log.Printf("[vfs] listItems %q → %d items", path, len(items))
	for _, it := range items {
		st := &fuse.Stat_t{}
		if it.IsFolder {
			st.Mode = fuse.S_IFDIR | 0555
			st.Nlink = 2
		} else {
			st.Mode = fuse.S_IFREG | 0444
			st.Size = it.FileSize
		}
		fill(it.VFSName(), st, 0)

		// Inject virtual sidecar files (.nfo + artwork) — only for leaf media
		// files, not for folder items. Movie folders expose their sidecars
		// inside the folder (next to the actual media file).
		base := it.Name // sidecar base = title without extension
		if !it.IsFolder {
			nfoSize := int64(1024)
			if cached, ok := fs.nfoCache.Load(path + "/" + base + ".nfo"); ok {
				nfoSize = int64(len(cached.([]byte)))
			}
			fill(base+".nfo", &fuse.Stat_t{Mode: fuse.S_IFREG | 0444, Size: nfoSize}, 0)
			for _, artFile := range connector.ArtworkFilenames(it.Type) {
				artSize := int64(51200)
				if artType, ok := connector.ArtworkTypeForFilename(artFile); ok {
					if data, cached, _ := fs.cache.GetArtwork(parts[0], it.ID, string(artType)); cached {
						artSize = int64(len(data))
					}
				}
				fill(base+"-"+artFile, &fuse.Stat_t{Mode: fuse.S_IFREG | 0444, Size: artSize}, 0)
			}
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
		if err != nil {
			log.Printf("[vfs] NFO resolveItem error %s: %v", path, err)
			return -fuse.ENOENT, ^uint64(0)
		}
		if item == nil {
			log.Printf("[vfs] NFO item not found: %s", path)
			return -fuse.ENOENT, ^uint64(0)
		}

		var content []byte
		if cached, ok := fs.nfoCache.Load(path); ok {
			content = cached.([]byte)
		} else {
			content, err = fs.generateNFO(parts[0], item)
			if err != nil {
				log.Printf("[vfs] NFO generate error %s: %v", path, err)
				return -fuse.EIO, ^uint64(0)
			}
			fs.nfoCache.Store(path, content)
			log.Printf("[vfs] NFO generated %s (%d bytes)", path, len(content))
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
		log.Printf("[vfs] Open %s: GetStreamURL error: %v", path, err)
		return -fuse.EIO, ^uint64(0)
	}
	log.Printf("[vfs] Open %s size=%d url=%s", path, item.FileSize, streamURL)

	// Collect optional streaming headers (e.g. Plex X-Plex-* identifiers).
	var streamHdrs map[string]string
	if p, ok := srv.Conn.(connector.StreamHeadersProvider); ok {
		streamHdrs = p.GetStreamHeaders()
	}

	var reader *downloader.ReadAheadReader
	if item.FileSize > 0 && fs.dl.ReadAheadWindows > 0 {
		reader = downloader.NewReadAheadReader(
			streamURL, streamHdrs, item.FileSize, fs.dl,
			fs.dl.ReadAheadWindowMB, fs.dl.ReadAheadWindows,
		)
	}

	fs.handlesMu.Lock()
	fh := fs.nextFH
	fs.nextFH++
	fs.handles[fh] = &fileHandle{
		streamURL:  streamURL,
		streamHdrs: streamHdrs,
		fileSize:   item.FileSize,
		serverKey:  parts[0],
		path:       path,
		reader:     reader,
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
	// Only clamp to the known file size when we have a reliable value.
	// When fileSize == 0 (Plex did not report a size), let the HTTP layer
	// determine how many bytes are available rather than returning EOF immediately.
	if h.fileSize > 0 && ofst+length > h.fileSize {
		length = h.fileSize - ofst
		if length <= 0 {
			return 0
		}
	}

	if h.reader != nil {
		n, err := h.reader.ReadAt(buf[:length], ofst)
		if err != nil {
			log.Printf("[vfs] Read %s ofst=%d len=%d: ReadAhead error: %v", h.path, ofst, length, err)
			return -fuse.EIO
		}
		return n
	}

	data, err := fs.dl.ReadAt(h.streamURL, h.streamHdrs, ofst, length)
	if err != nil {
		log.Printf("[vfs] Read %s ofst=%d len=%d: HTTP error: %v", h.path, ofst, length, err)
		return -fuse.EIO
	}
	return copy(buf, data)
}

func (fs *MediaFS) Release(path string, fh uint64) int {
	fs.handlesMu.Lock()
	if h, ok := fs.handles[fh]; ok && h.reader != nil {
		h.reader.Close()
	}
	delete(fs.handles, fh)
	fs.handlesMu.Unlock()
	return 0
}

// OpenFiles returns the status of all open media files (for monitoring).
func (fs *MediaFS) OpenFiles() []downloader.FileStatus {
	fs.handlesMu.Lock()
	defer fs.handlesMu.Unlock()
	var result []downloader.FileStatus
	for fh, h := range fs.handles {
		if h.reader == nil {
			continue
		}
		result = append(result, downloader.FileStatus{
			FH:         fh,
			Path:       h.path,
			FileSize:   h.fileSize,
			WindowSize: h.reader.WindowSize(),
			Windows:    h.reader.Status(),
		})
	}
	return result
}

// PurgeFile drops all prefetch windows for a file handle.
func (fs *MediaFS) PurgeFile(fh uint64) bool {
	fs.handlesMu.Lock()
	h, ok := fs.handles[fh]
	fs.handlesMu.Unlock()
	if !ok || h.reader == nil {
		return false
	}
	h.reader.PurgeAll()
	return true
}

// PurgeWindow drops one prefetch window for a file handle.
func (fs *MediaFS) PurgeWindow(fh uint64, start int64) bool {
	fs.handlesMu.Lock()
	h, ok := fs.handles[fh]
	fs.handlesMu.Unlock()
	if !ok || h.reader == nil {
		return false
	}
	return h.reader.PurgeWindow(start)
}

// Write operations are rejected — read-only filesystem.
func (fs *MediaFS) Write(path string, buf []byte, ofst int64, fh uint64) int { return -fuse.EPERM }
func (fs *MediaFS) Create(path string, flags int, mode uint32) (int, uint64)  { return -fuse.EPERM, 0 }
func (fs *MediaFS) Mkdir(path string, mode uint32) int                        { return -fuse.EPERM }
func (fs *MediaFS) Unlink(path string) int                                    { return -fuse.EPERM }
func (fs *MediaFS) Rename(oldpath, newpath string) int                        { return -fuse.EPERM }

// --- helpers ---

// dirStat fills stat as a read-only directory.
// Nlink=2 tells Windows Explorer the folder may have children (shows expand arrow).
func dirStat(stat *fuse.Stat_t) {
	stat.Mode = fuse.S_IFDIR | 0555
	stat.Nlink = 2
}

// setTimestamps sets stat timestamps from a media item's dates.
// Mtim/Atim/Ctim = dateAdded (DateCreated from server).
// Birthtim = premiereDate if set, else Jan 1 of productionYear, else Mtim.
// Tries RFC3339Nano then RFC3339 to handle Jellyfin/Emby's variable decimal precision.
func setTimestamps(stat *fuse.Stat_t, dateAdded, premiereDate string, productionYear int) {
	parseDate := func(s string) (time.Time, bool) {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}

	t, ok := parseDate(dateAdded)
	if !ok {
		return
	}
	stat.Mtim.Sec = t.Unix()
	stat.Mtim.Nsec = int64(t.Nanosecond())
	stat.Atim = stat.Mtim
	stat.Ctim = stat.Mtim

	if birth, ok := parseDate(premiereDate); ok {
		stat.Birthtim.Sec = birth.Unix()
		stat.Birthtim.Nsec = int64(birth.Nanosecond())
	} else if productionYear > 0 {
		birth := time.Date(productionYear, 1, 1, 0, 0, 0, 0, time.UTC)
		stat.Birthtim.Sec = birth.Unix()
	} else {
		stat.Birthtim = stat.Mtim
	}
}

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

	// Walk down the path: parts[2], parts[3], …
	parentID := ""
	var found *connector.MediaItem
	for i := 2; i < len(parts); i++ {
		items, err := fs.listItemsCached(parts[0], lib.ID, parentID)
		if err != nil {
			return nil, err
		}
		var next *connector.MediaItem
		for j := range items {
			if items[j].VFSName() == parts[i] {
				next = &items[j]
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
		if items[i].IsFolder {
			continue // artwork lives inside the folder, not next to it
		}
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
	// Cache key must be scoped to the library to avoid collisions between
	// Plex section IDs (small integers like "1","2","3") and item ratingKeys
	// that happen to have the same value.
	var cacheKey string
	if parentID == "" {
		cacheKey = libID
	} else {
		cacheKey = libID + "|" + parentID
	}

	var items []connector.MediaItem
	if ok, err := fs.cache.GetItems(serverKey, cacheKey, &items); ok {
		log.Printf("[vfs] cache hit: server=%s lib=%s parent=%s → %d items", serverKey, libID, parentID, len(items))
		return items, err
	}

	srv := fs.serverForKey(serverKey)
	if srv == nil {
		log.Printf("[vfs] listItemsCached: server %q not found", serverKey)
		return nil, nil
	}

	log.Printf("[vfs] fetching items: server=%s lib=%s parent=%s", serverKey, libID, parentID)
	items, err := srv.Conn.GetItems(libID, parentID)
	if err != nil {
		log.Printf("[vfs] GetItems error: server=%s lib=%s parent=%s: %v", serverKey, libID, parentID, err)
		return nil, err
	}
	log.Printf("[vfs] GetItems: server=%s lib=%s parent=%s → %d items", serverKey, libID, parentID, len(items))

	_ = fs.cache.StoreItems(serverKey, cacheKey, items)
	return items, nil
}
