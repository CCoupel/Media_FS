package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
)

func init() {
	connector.Register(config.ConnectorJellyfin, func() connector.MediaConnector {
		return &Client{}
	})
}

// Client implements connector.MediaConnector for Jellyfin (and Emby via subtype).
type Client struct {
	baseURL    string
	apiKey     string
	userID     string
	authHeader string // header name: X-MediaBrowser-Token (Jellyfin) or X-Emby-Token (Emby)
	http       *http.Client
}

// SetAuthHeader overrides the authentication header name.
// Used by the Emby adapter to switch to X-Emby-Token.
func (c *Client) SetAuthHeader(header string) {
	c.authHeader = header
}

func (c *Client) Connect(cfg config.ServerConfig) error {
	c.baseURL = cfg.URL
	if c.authHeader == "" {
		c.authHeader = "X-MediaBrowser-Token" // Emby overrides this before calling Connect
	}
	c.http = &http.Client{Timeout: 15 * time.Second}

	if cfg.APIKey != "" {
		log.Printf("[jellyfin] connecting %s via API key, username=%q", cfg.URL, cfg.Username)
		c.apiKey = cfg.APIKey
		userID, err := c.resolveUserID(cfg.Username)
		if err != nil {
			return fmt.Errorf("jellyfin connect: %w", err)
		}
		c.userID = userID
		log.Printf("[jellyfin] API key auth OK, userID=%s", userID)
	} else if cfg.Password != "" {
		log.Printf("[jellyfin] connecting %s via password for user %q", cfg.URL, cfg.Username)
		token, userID, err := c.authenticateByPassword(cfg.Username, cfg.Password)
		if err != nil {
			return fmt.Errorf("jellyfin connect: %w", err)
		}
		c.apiKey = token
		c.userID = userID
		log.Printf("[jellyfin] password auth OK, userID=%s", userID)
	} else {
		return fmt.Errorf("jellyfin connect: no api_key or password provided")
	}
	return nil
}

func (c *Client) Ping() error {
	resp, err := c.get("/System/Info/Public", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) GetLibraries() ([]connector.Library, error) {
	resp, err := c.get(fmt.Sprintf("/Users/%s/Views", c.userID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Items []struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	libs := make([]connector.Library, len(result.Items))
	for i, item := range result.Items {
		libs[i] = connector.Library{
			ID:   item.ID,
			Name: item.Name,
			Type: collectionTypeToItemType(item.CollectionType),
		}
		log.Printf("[jellyfin] library %d: %q (id=%s collectionType=%s)", i, item.Name, item.ID, item.CollectionType)
	}
	return libs, nil
}

func (c *Client) GetItems(libraryID, parentID string) ([]connector.MediaItem, error) {
	parent := libraryID
	if parentID != "" {
		parent = parentID
	}

	params := url.Values{
		"ParentId": {parent},
		"Fields":   {"BasicSyncInfo,MediaSources"},
		"SortBy":   {"SortName"},
		"SortOrder": {"Ascending"},
	}
	resp, err := c.get(fmt.Sprintf("/Users/%s/Items", c.userID), params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Items []struct {
			ID           string `json:"Id"`
			ParentID     string `json:"ParentId"`
			Name         string `json:"Name"`
			Type         string `json:"Type"`
			IsFolder     bool   `json:"IsFolder"`
			DateCreated  string `json:"DateCreated"`
			MediaSources []struct {
				Size int64 `json:"Size"`
			} `json:"MediaSources"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	items := make([]connector.MediaItem, len(result.Items))
	for i, it := range result.Items {
		var size int64
		if len(it.MediaSources) > 0 {
			size = it.MediaSources[0].Size
		}
		items[i] = connector.MediaItem{
			ID:        it.ID,
			ParentID:  it.ParentID,
			Name:      it.Name,
			Type:      connector.ItemType(it.Type),
			IsFolder:  it.IsFolder,
			FileSize:  size,
			DateAdded: it.DateCreated,
		}
	}
	return items, nil
}

func (c *Client) GetItemMetadata(itemID string) (connector.ItemMetadata, error) {
	resp, err := c.get(fmt.Sprintf("/Items/%s", itemID), url.Values{
		"Fields": {"Genres,People,ExternalUrls,ProviderIds,DateCreated"},
	})
	if err != nil {
		return connector.ItemMetadata{}, err
	}
	defer resp.Body.Close()

	var raw struct {
		ID                 string            `json:"Id"`
		Name               string            `json:"Name"`
		Type               string            `json:"Type"`
		ProductionYear     int               `json:"ProductionYear"`
		Overview           string            `json:"Overview"`
		CommunityRating    float64           `json:"CommunityRating"`
		Genres             []string          `json:"Genres"`
		RunTimeTicks       int64             `json:"RunTimeTicks"`
		DateCreated        string            `json:"DateCreated"`
		IndexNumber        int               `json:"IndexNumber"`
		ParentIndexNumber  int               `json:"ParentIndexNumber"`
		ProviderIDs        map[string]string `json:"ProviderIds"`
		People []struct {
			Name string `json:"Name"`
			Type string `json:"Type"`
		} `json:"People"`
		MediaSources []struct {
			Size int64 `json:"Size"`
		} `json:"MediaSources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return connector.ItemMetadata{}, err
	}

	var directors []string
	for _, p := range raw.People {
		if p.Type == "Director" {
			directors = append(directors, p.Name)
		}
	}
	var fileSize int64
	if len(raw.MediaSources) > 0 {
		fileSize = raw.MediaSources[0].Size
	}

	return connector.ItemMetadata{
		ID:            raw.ID,
		Name:          raw.Name,
		Type:          connector.ItemType(raw.Type),
		Year:          raw.ProductionYear,
		Overview:      raw.Overview,
		Rating:        raw.CommunityRating,
		Genres:        raw.Genres,
		Directors:     directors,
		FileSize:      fileSize,
		RunTimeTicks:  raw.RunTimeTicks,
		DateAdded:     raw.DateCreated,
		ExternalIDs:   raw.ProviderIDs,
		EpisodeNumber: raw.IndexNumber,
		SeasonNumber:  raw.ParentIndexNumber,
	}, nil
}

func (c *Client) GetArtworkURL(itemID string, artType connector.ArtworkType) (string, error) {
	u := fmt.Sprintf("%s/Items/%s/Images/%s?api_key=%s", c.baseURL, itemID, artType, c.apiKey)
	return u, nil
}

func (c *Client) GetStreamURL(itemID string) (string, error) {
	u := fmt.Sprintf("%s/Videos/%s/stream?static=true&api_key=%s", c.baseURL, itemID, c.apiKey)
	return u, nil
}

func (c *Client) GetFileSize(itemID string) (int64, error) {
	meta, err := c.GetItemMetadata(itemID)
	if err != nil {
		return 0, err
	}
	return meta.FileSize, nil
}

// --- internal helpers ---

func (c *Client) get(path string, params url.Values) (*http.Response, error) {
	if params == nil {
		params = url.Values{}
	}
	// api_key query param: universally supported by Emby and Jellyfin
	params.Set("api_key", c.apiKey)
	u := c.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	// Set both header styles for maximum compatibility
	req.Header.Set(c.authHeader, c.apiKey)
	req.Header.Set("Authorization",
		fmt.Sprintf(`MediaBrowser Client="MediaFS", Device="MediaFS", DeviceId="mediafs-cli", Version="1.0", Token="%s"`, c.apiKey))

	log.Printf("[http] GET %s (auth header: %s)", path, c.authHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[http] GET %s → %d: %s", path, resp.StatusCode, body)
		return nil, fmt.Errorf("%s → HTTP %d: %s", path, resp.StatusCode, body)
	}
	return resp, nil
}

// authenticateByPassword calls POST /Users/AuthenticateByName and returns (token, userID).
func (c *Client) authenticateByPassword(username, password string) (string, string, error) {
	body, _ := json.Marshal(map[string]string{"Username": username, "Pw": password})
	req, err := http.NewRequest("POST", c.baseURL+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization",
		`MediaBrowser Client="MediaFS", Device="MediaFS", DeviceId="mediafs-cli", Version="1.0"`)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("auth failed (%d): %s", resp.StatusCode, b)
	}

	var result struct {
		AccessToken string `json:"AccessToken"`
		User        struct {
			ID string `json:"Id"`
		} `json:"User"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	if result.AccessToken == "" {
		return "", "", fmt.Errorf("empty access token in response")
	}
	return result.AccessToken, result.User.ID, nil
}

// resolveUserID resolves a username to a Jellyfin user ID using GET /Users.
// Jellyfin API keys have admin access and can call this endpoint.
// If username is not found, falls back to the first user in the list.
func (c *Client) resolveUserID(username string) (string, error) {
	resp, err := c.get("/Users", nil)
	if err != nil {
		return "", fmt.Errorf("GET /Users failed (API key must have admin access): %w", err)
	}
	defer resp.Body.Close()

	var users []struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return "", err
	}
	log.Printf("[jellyfin] /Users returned %d users", len(users))
	for _, u := range users {
		if strings.EqualFold(u.Name, username) {
			log.Printf("[jellyfin] matched user %q → id=%s", username, u.ID)
			return u.ID, nil
		}
	}
	if len(users) > 0 {
		log.Printf("[jellyfin] user %q not found, using first: %s (id=%s)", username, users[0].Name, users[0].ID)
		return users[0].ID, nil
	}
	return "", fmt.Errorf("no users found on server")
}

func collectionTypeToItemType(ct string) connector.ItemType {
	switch ct {
	case "movies":
		return connector.ItemTypeMovie
	case "tvshows":
		return connector.ItemTypeSeries
	case "music":
		return connector.ItemTypeMusicAlbum
	default:
		return connector.ItemTypeFolder
	}
}
