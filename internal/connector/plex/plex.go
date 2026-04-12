package plex

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
)

func init() {
	connector.Register(config.ConnectorPlex, func() connector.MediaConnector {
		return &Client{}
	})
}

const plexPageSize = 500

// Client implements connector.MediaConnector for Plex Media Server.
type Client struct {
	baseURL       string
	token         string
	http          *http.Client
	movieSections map[string]bool // libID → true for movie sections
}

func (c *Client) Connect(cfg config.ServerConfig) error {
	c.baseURL = strings.TrimRight(cfg.URL, "/")
	c.http = &http.Client{Timeout: 15 * time.Second}

	if cfg.APIKey != "" {
		log.Printf("[plex] connecting %s via API key", cfg.URL)
		c.token = cfg.APIKey
		if err := c.Ping(); err != nil {
			return fmt.Errorf("plex connect: %w", err)
		}
		log.Printf("[plex] API key auth OK")
	} else if cfg.Password != "" {
		log.Printf("[plex] connecting %s via password for user %q", cfg.URL, cfg.Username)
		token, err := authenticateByPassword(cfg.Username, cfg.Password)
		if err != nil {
			return fmt.Errorf("plex connect: %w", err)
		}
		c.token = token
		if err := c.Ping(); err != nil {
			return fmt.Errorf("plex connect: %w", err)
		}
		log.Printf("[plex] password auth OK")
	} else {
		return fmt.Errorf("plex connect: no api_key or password provided")
	}
	return nil
}

func (c *Client) Ping() error {
	resp, err := c.get("/", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) GetLibraries() ([]connector.Library, error) {
	resp, err := c.get("/library/sections", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		XMLName     xml.Name `xml:"MediaContainer"`
		Directories []struct {
			Key   string `xml:"key,attr"`
			Type  string `xml:"type,attr"`
			Title string `xml:"title,attr"`
		} `xml:"Directory"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	c.movieSections = make(map[string]bool)
	libs := make([]connector.Library, 0, len(result.Directories))
	for _, d := range result.Directories {
		if d.Key == "" {
			continue
		}
		if d.Type == "movie" {
			c.movieSections[d.Key] = true
		}
		libs = append(libs, connector.Library{
			ID:   d.Key,
			Name: d.Title,
			Type: sectionTypeToItemType(d.Type),
		})
		log.Printf("[plex] library %q (id=%s type=%s)", d.Title, d.Key, d.Type)
	}
	return libs, nil
}

func (c *Client) GetItems(libraryID, parentID string) ([]connector.MediaItem, error) {
	isMovieSection := c.movieSections != nil && c.movieSections[libraryID]

	if parentID != "" {
		// "grp:{k1},{k2},..." — expand group into numbered sub-folders.
		if strings.HasPrefix(parentID, "grp:") {
			return expandMovieGroup(parentID), nil
		}
		// Single movie folder or numbered sub-folder: return the media file.
		if isMovieSection {
			return c.fetchMovieFiles(parentID)
		}
		return c.fetchAllItems(fmt.Sprintf("/library/metadata/%s/children", parentID))
	}

	if !isMovieSection {
		return c.fetchAllItems(fmt.Sprintf("/library/sections/%s/all", libraryID))
	}

	// Movie section root: fetch with GUIDs for IMDB/TMDB disambiguation.
	rawItems, err := c.fetchAllXMLItems(
		fmt.Sprintf("/library/sections/%s/all", libraryID),
		url.Values{"includeGuids": {"1"}},
	)
	if err != nil {
		return nil, err
	}
	return buildMovieFolders(rawItems), nil
}

// buildMovieFolders converts a flat list of movie xmlItems into virtual folder
// MediaItems. Movies with the same folder name are grouped under a single
// "grp:{k1},{k2},..." ID; the VFS can then expand them into numbered sub-folders.
func buildMovieFolders(rawItems []xmlItem) []connector.MediaItem {
	// Preserve insertion order while grouping by folder name.
	type group struct {
		first connector.MediaItem // for timestamps / type
		keys  []string
	}
	var order []string
	groups := make(map[string]*group)

	for _, it := range rawItems {
		if it.RatingKey == "" {
			continue // skip malformed Plex items (e.g. collection placeholders)
		}
		name := movieFolderName(it)
		if _, exists := groups[name]; !exists {
			order = append(order, name)
			mi := mapXMLItem(it)
			groups[name] = &group{first: mi}
		}
		groups[name].keys = append(groups[name].keys, it.RatingKey)
	}

	result := make([]connector.MediaItem, 0, len(order))
	for _, name := range order {
		g := groups[name]
		id := g.keys[0]
		if len(g.keys) > 1 {
			id = "grp:" + strings.Join(g.keys, ",")
		}
		result = append(result, connector.MediaItem{
			ID:             id,
			Name:           name,
			Type:           connector.ItemTypeMovie,
			IsFolder:       true,
			DateAdded:      g.first.DateAdded,
			ProductionYear: g.first.ProductionYear,
		})
	}
	return result
}

// expandMovieGroup turns "grp:{k1},{k2},..." into numbered sub-folder items.
// Each sub-folder has the ratingKey as its ID so fetchMovieFiles resolves it.
func expandMovieGroup(parentID string) []connector.MediaItem {
	keys := strings.Split(strings.TrimPrefix(parentID, "grp:"), ",")
	result := make([]connector.MediaItem, 0, len(keys))
	for i, k := range keys {
		result = append(result, connector.MediaItem{
			ID:       k,
			Name:     strconv.Itoa(i + 1),
			Type:     connector.ItemTypeMovie,
			IsFolder: true,
		})
	}
	return result
}

// fetchMovieFiles fetches a single movie's metadata and returns its media file.
// The item ID is the ratingKey, so GetStreamURL/GetItemMetadata work unchanged.
func (c *Client) fetchMovieFiles(movieID string) ([]connector.MediaItem, error) {
	resp, err := c.get(fmt.Sprintf("/library/metadata/%s", movieID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		XMLName xml.Name  `xml:"MediaContainer"`
		Videos  []xmlItem `xml:"Video"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw.Videos) == 0 {
		log.Printf("[plex] fetchMovieFiles %s: no Video element in metadata response", movieID)
		return nil, nil
	}
	item := mapXMLItem(raw.Videos[0])
	// Ensure the file item carries the same display name as its parent folder
	// so NFO/artwork path resolution (base-name matching) works correctly.
	item.Name = movieFolderName(raw.Videos[0])
	if item.FileSize == 0 {
		log.Printf("[plex] fetchMovieFiles %s (%q): FileSize=0 — Media/Part missing or size absent in XML", movieID, item.Name)
	}
	if item.Extension == "" {
		log.Printf("[plex] fetchMovieFiles %s (%q): Extension empty — container attribute missing", movieID, item.Name)
	}
	return []connector.MediaItem{item}, nil
}

// movieFolderName returns the VFS folder/file base name for a movie.
// Prefers IMDB, then TMDB, then TVDB for disambiguation; falls back to title only.
func movieFolderName(it xmlItem) string {
	for _, g := range it.Guids {
		switch {
		case strings.HasPrefix(g.ID, "imdb://"):
			return fmt.Sprintf("%s {imdb-%s}", it.Title, strings.TrimPrefix(g.ID, "imdb://"))
		case strings.HasPrefix(g.ID, "tmdb://"):
			return fmt.Sprintf("%s {tmdb-%s}", it.Title, strings.TrimPrefix(g.ID, "tmdb://"))
		case strings.HasPrefix(g.ID, "tvdb://"):
			return fmt.Sprintf("%s {tvdb-%s}", it.Title, strings.TrimPrefix(g.ID, "tvdb://"))
		}
	}
	return it.Title
}

func (c *Client) GetItemMetadata(itemID string) (connector.ItemMetadata, error) {
	resp, err := c.get(fmt.Sprintf("/library/metadata/%s", itemID), nil)
	if err != nil {
		return connector.ItemMetadata{}, err
	}
	defer resp.Body.Close()

	var raw struct {
		XMLName xml.Name      `xml:"MediaContainer"`
		Videos  []xmlMetadata `xml:"Video"`
		Dirs    []xmlMetadata `xml:"Directory"`
		Tracks  []xmlMetadata `xml:"Track"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return connector.ItemMetadata{}, err
	}

	var el *xmlMetadata
	switch {
	case len(raw.Videos) > 0:
		el = &raw.Videos[0]
	case len(raw.Dirs) > 0:
		el = &raw.Dirs[0]
	case len(raw.Tracks) > 0:
		el = &raw.Tracks[0]
	default:
		return connector.ItemMetadata{}, fmt.Errorf("no media element for item %s", itemID)
	}

	var year int
	if v, err := strconv.Atoi(el.Year); err == nil {
		year = v
	}
	var rating float64
	if v, err := strconv.ParseFloat(el.Rating, 64); err == nil {
		rating = v
	}
	var runTimeTicks int64
	if d, err := strconv.ParseInt(el.Duration, 10, 64); err == nil {
		runTimeTicks = d * 10_000
	}
	var fileSize int64
	if len(el.Media) > 0 && len(el.Media[0].Parts) > 0 {
		if v, err := strconv.ParseInt(el.Media[0].Parts[0].Size, 10, 64); err == nil {
			fileSize = v
		}
	}

	premiereDate := ""
	if el.OriginallyAvailableAt != "" {
		if t, err := time.Parse("2006-01-02", el.OriginallyAvailableAt); err == nil {
			premiereDate = t.UTC().Format(time.RFC3339)
		}
	}
	dateAdded := ""
	if el.AddedAt != "" {
		if ts, err := strconv.ParseInt(el.AddedAt, 10, 64); err == nil {
			dateAdded = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
	}

	genres := make([]string, 0, len(el.Genres))
	for _, g := range el.Genres {
		if g.Tag != "" {
			genres = append(genres, g.Tag)
		}
	}
	directors := make([]string, 0, len(el.Directors))
	for _, d := range el.Directors {
		if d.Tag != "" {
			directors = append(directors, d.Tag)
		}
	}
	extIDs := make(map[string]string)
	for _, g := range el.Guids {
		switch {
		case strings.HasPrefix(g.ID, "imdb://"):
			extIDs["imdb"] = strings.TrimPrefix(g.ID, "imdb://")
		case strings.HasPrefix(g.ID, "tmdb://"):
			extIDs["tmdb"] = strings.TrimPrefix(g.ID, "tmdb://")
		case strings.HasPrefix(g.ID, "tvdb://"):
			extIDs["tvdb"] = strings.TrimPrefix(g.ID, "tvdb://")
		}
	}

	var episodeNum, seasonNum int
	if v, err := strconv.Atoi(el.Index); err == nil {
		episodeNum = v
	}
	if v, err := strconv.Atoi(el.ParentIndex); err == nil {
		seasonNum = v
	}

	return connector.ItemMetadata{
		ID:            el.RatingKey,
		Name:          el.Title,
		Type:          plexTypeToItemType(el.Type),
		Year:          year,
		PremiereDate:  premiereDate,
		Overview:      el.Summary,
		Rating:        rating,
		Genres:        genres,
		Directors:     directors,
		FileSize:      fileSize,
		RunTimeTicks:  runTimeTicks,
		DateAdded:     dateAdded,
		ExternalIDs:   extIDs,
		EpisodeNumber: episodeNum,
		SeasonNumber:  seasonNum,
	}, nil
}

func (c *Client) GetArtworkURL(itemID string, artType connector.ArtworkType) (string, error) {
	var endpoint string
	switch artType {
	case connector.ArtworkFanart:
		endpoint = "art"
	case connector.ArtworkBanner:
		endpoint = "banner"
	default: // ArtworkPoster, ArtworkThumb
		endpoint = "thumb"
	}
	return fmt.Sprintf("%s/library/metadata/%s/%s?X-Plex-Token=%s", c.baseURL, itemID, endpoint, c.token), nil
}

func (c *Client) GetStreamURL(itemID string) (string, error) {
	resp, err := c.get(fmt.Sprintf("/library/metadata/%s", itemID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var raw struct {
		XMLName xml.Name `xml:"MediaContainer"`
		Videos  []struct {
			Media []struct {
				Parts []struct {
					Key string `xml:"key,attr"`
				} `xml:"Part"`
			} `xml:"Media"`
		} `xml:"Video"`
		Tracks []struct {
			Media []struct {
				Parts []struct {
					Key string `xml:"key,attr"`
				} `xml:"Part"`
			} `xml:"Media"`
		} `xml:"Track"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}

	var partKey string
	if len(raw.Videos) > 0 && len(raw.Videos[0].Media) > 0 && len(raw.Videos[0].Media[0].Parts) > 0 {
		partKey = raw.Videos[0].Media[0].Parts[0].Key
	} else if len(raw.Tracks) > 0 && len(raw.Tracks[0].Media) > 0 && len(raw.Tracks[0].Media[0].Parts) > 0 {
		partKey = raw.Tracks[0].Media[0].Parts[0].Key
	}
	if partKey == "" {
		return "", fmt.Errorf("no Part key found for item %s", itemID)
	}
	// download=1 is required for Plex to serve the raw file with Range support.
	// Without it some PMS versions return HTTP 500 on Range requests.
	return fmt.Sprintf("%s%s?X-Plex-Token=%s&download=1", c.baseURL, partKey, c.token), nil
}

func (c *Client) GetFileSize(itemID string) (int64, error) {
	meta, err := c.GetItemMetadata(itemID)
	if err != nil {
		return 0, err
	}
	return meta.FileSize, nil
}

// GetStreamHeaders returns the HTTP headers Plex requires on streaming Range requests.
// These must accompany every request to the Plex streaming endpoint.
func (c *Client) GetStreamHeaders() map[string]string {
	return map[string]string{
		"X-Plex-Token":             c.token,
		"X-Plex-Product":           "MediaFS",
		"X-Plex-Client-Identifier": "mediafs-cli",
		"X-Plex-Version":           "1.0",
	}
}

// --- XML data types ---

type xmlItem struct {
	RatingKey   string     `xml:"ratingKey,attr"`
	ParentKey   string     `xml:"parentRatingKey,attr"`
	Title       string     `xml:"title,attr"`
	Type        string     `xml:"type,attr"`
	Year        string     `xml:"year,attr"`
	AddedAt     string     `xml:"addedAt,attr"`
	Duration    string     `xml:"duration,attr"`
	Index       string     `xml:"index,attr"`
	ParentIndex string     `xml:"parentIndex,attr"`
	Media       []xmlMedia `xml:"Media"`
	Guids       []xmlGuid  `xml:"Guid"`
}

type xmlMetadata struct {
	RatingKey             string     `xml:"ratingKey,attr"`
	Title                 string     `xml:"title,attr"`
	Type                  string     `xml:"type,attr"`
	Year                  string     `xml:"year,attr"`
	Duration              string     `xml:"duration,attr"`
	AddedAt               string     `xml:"addedAt,attr"`
	OriginallyAvailableAt string     `xml:"originallyAvailableAt,attr"`
	Summary               string     `xml:"summary,attr"`
	Rating                string     `xml:"rating,attr"`
	Index                 string     `xml:"index,attr"`
	ParentIndex           string     `xml:"parentIndex,attr"`
	Media                 []xmlMedia `xml:"Media"`
	Genres                []xmlTag   `xml:"Genre"`
	Directors             []xmlTag   `xml:"Director"`
	Guids                 []xmlGuid  `xml:"Guid"`
}

type xmlMedia struct {
	Container string    `xml:"container,attr"`
	Parts     []xmlPart `xml:"Part"`
}

type xmlPart struct {
	Key  string `xml:"key,attr"`
	Size string `xml:"size,attr"`
}

type xmlGuid struct {
	ID string `xml:"id,attr"`
}

type xmlTag struct {
	Tag string `xml:"tag,attr"`
}

type itemsContainer struct {
	XMLName     xml.Name  `xml:"MediaContainer"`
	TotalSize   int       `xml:"totalSize,attr"`
	Videos      []xmlItem `xml:"Video"`
	Directories []xmlItem `xml:"Directory"`
	Tracks      []xmlItem `xml:"Track"`
}

// --- internal helpers ---

// fetchAllXMLItems fetches all pages from path and returns raw xmlItem values.
// extra params are merged into each page request (e.g. includeGuids=1).
func (c *Client) fetchAllXMLItems(path string, extra url.Values) ([]xmlItem, error) {
	var all []xmlItem
	offset := 0
	for {
		params := url.Values{
			"X-Plex-Container-Start": {strconv.Itoa(offset)},
			"X-Plex-Container-Size":  {strconv.Itoa(plexPageSize)},
		}
		maps.Copy(params, extra)
		resp, err := c.get(path, params)
		if err != nil {
			return nil, err
		}
		var container itemsContainer
		decErr := xml.NewDecoder(resp.Body).Decode(&container)
		resp.Body.Close()
		if decErr != nil {
			return nil, decErr
		}
		items := mergeItems(container)
		all = append(all, items...)
		offset += len(items)
		if len(items) < plexPageSize || (container.TotalSize > 0 && offset >= container.TotalSize) {
			break
		}
	}
	return all, nil
}

func (c *Client) fetchAllItems(path string) ([]connector.MediaItem, error) {
	raw, err := c.fetchAllXMLItems(path, nil)
	if err != nil {
		return nil, err
	}
	result := make([]connector.MediaItem, 0, len(raw))
	for _, it := range raw {
		result = append(result, mapXMLItem(it))
	}
	return result, nil
}

func mergeItems(cont itemsContainer) []xmlItem {
	result := make([]xmlItem, 0, len(cont.Videos)+len(cont.Directories)+len(cont.Tracks))
	result = append(result, cont.Videos...)
	result = append(result, cont.Directories...)
	result = append(result, cont.Tracks...)
	return result
}

func mapXMLItem(it xmlItem) connector.MediaItem {
	isFolder := it.Type == "show" || it.Type == "season" || it.Type == "artist" || it.Type == "album"

	var size int64
	var ext string
	if len(it.Media) > 0 && len(it.Media[0].Parts) > 0 {
		size, _ = strconv.ParseInt(it.Media[0].Parts[0].Size, 10, 64)
		if ct := strings.ToLower(it.Media[0].Container); ct != "" {
			ext = "." + ct
		}
	}

	dateAdded := ""
	if it.AddedAt != "" {
		if ts, err := strconv.ParseInt(it.AddedAt, 10, 64); err == nil {
			dateAdded = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
	}

	year, _ := strconv.Atoi(it.Year)

	return connector.MediaItem{
		ID:             it.RatingKey,
		ParentID:       it.ParentKey,
		Name:           it.Title,
		Type:           plexTypeToItemType(it.Type),
		IsFolder:       isFolder,
		FileSize:       size,
		DateAdded:      dateAdded,
		Extension:      ext,
		ProductionYear: year,
	}
}

func (c *Client) get(path string, params url.Values) (*http.Response, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("X-Plex-Token", c.token)
	u := c.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("X-Plex-Product", "MediaFS")
	req.Header.Set("X-Plex-Client-Identifier", "mediafs-cli")
	req.Header.Set("X-Plex-Version", "1.0")
	req.Header.Set("Accept", "application/xml")

	log.Printf("[plex] GET %s", path)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[plex] GET %s → %d: %s", path, resp.StatusCode, body)
		return nil, fmt.Errorf("%s → HTTP %d: %s", path, resp.StatusCode, body)
	}
	return resp, nil
}

// authenticateByPassword signs in via plex.tv and returns the auth token.
func authenticateByPassword(username, password string) (string, error) {
	form := url.Values{
		"user[login]":    {username},
		"user[password]": {password},
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", "https://plex.tv/users/sign_in.xml",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Plex-Product", "MediaFS")
	req.Header.Set("X-Plex-Client-Identifier", "mediafs-cli")
	req.Header.Set("X-Plex-Version", "1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("plex.tv auth failed (%d): %s", resp.StatusCode, b)
	}

	var result struct {
		XMLName   xml.Name `xml:"user"`
		AuthToken string   `xml:"authToken,attr"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.AuthToken == "" {
		return "", fmt.Errorf("empty authToken in plex.tv response")
	}
	return result.AuthToken, nil
}

func sectionTypeToItemType(t string) connector.ItemType {
	switch t {
	case "movie":
		return connector.ItemTypeMovie
	case "show":
		return connector.ItemTypeSeries
	case "artist":
		return connector.ItemTypeMusicAlbum
	default:
		return connector.ItemTypeFolder
	}
}

func plexTypeToItemType(t string) connector.ItemType {
	switch t {
	case "movie":
		return connector.ItemTypeMovie
	case "show":
		return connector.ItemTypeSeries
	case "season":
		return connector.ItemTypeSeason
	case "episode":
		return connector.ItemTypeEpisode
	case "artist", "album":
		return connector.ItemTypeMusicAlbum
	case "track":
		return connector.ItemTypeAudio
	default:
		return connector.ItemTypeFolder
	}
}
