package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	c.apiKey = cfg.APIKey
	c.authHeader = "X-MediaBrowser-Token"
	c.http = &http.Client{Timeout: 15 * time.Second}

	userID, err := c.resolveUserID(cfg.Username)
	if err != nil {
		return fmt.Errorf("jellyfin connect: %w", err)
	}
	c.userID = userID
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
			ID                string `json:"Id"`
			ParentID          string `json:"ParentId"`
			Name              string `json:"Name"`
			Type              string `json:"Type"`
			IsFolder          bool   `json:"IsFolder"`
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
			ID:       it.ID,
			ParentID: it.ParentID,
			Name:     it.Name,
			Type:     connector.ItemType(it.Type),
			IsFolder: it.IsFolder,
			FileSize: size,
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
		ID           string   `json:"Id"`
		Name         string   `json:"Name"`
		Type         string   `json:"Type"`
		ProductionYear int    `json:"ProductionYear"`
		Overview     string   `json:"Overview"`
		CommunityRating float64 `json:"CommunityRating"`
		Genres       []string `json:"Genres"`
		RunTimeTicks int64    `json:"RunTimeTicks"`
		DateCreated  string   `json:"DateCreated"`
		ProviderIDs  map[string]string `json:"ProviderIds"`
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
		ID:           raw.ID,
		Name:         raw.Name,
		Type:         connector.ItemType(raw.Type),
		Year:         raw.ProductionYear,
		Overview:     raw.Overview,
		Rating:       raw.CommunityRating,
		Genres:       raw.Genres,
		Directors:    directors,
		FileSize:     fileSize,
		RunTimeTicks: raw.RunTimeTicks,
		DateAdded:    raw.DateCreated,
		ExternalIDs:  raw.ProviderIDs,
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
	u := c.baseURL + path
	if params != nil {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(c.authHeader, c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("jellyfin %s → %d: %s", path, resp.StatusCode, body)
	}
	return resp, nil
}

func (c *Client) resolveUserID(username string) (string, error) {
	resp, err := c.get("/Users", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var users []struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return "", err
	}
	for _, u := range users {
		if u.Name == username {
			return u.ID, nil
		}
	}
	if len(users) > 0 {
		return users[0].ID, nil
	}
	return "", fmt.Errorf("user %q not found", username)
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
