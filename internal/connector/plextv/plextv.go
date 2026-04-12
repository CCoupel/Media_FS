// Package plextv provides a Plex connector that authenticates via plex.tv and
// auto-discovers the best server URL (plex.direct > relay, HTTPS > HTTP).
// It delegates all media operations to an inner plex.Client once connected.
package plextv

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
	_ "github.com/CCoupel/Media_FS/internal/connector/plex" // ensure plex connector is registered
)

func init() {
	connector.Register(config.ConnectorPlexTV, func() connector.MediaConnector {
		return &Client{}
	})
}

// ServerInfo describes a Plex Media Server available on plex.tv.
type ServerInfo struct {
	Name      string `json:"name"`
	MachineID string `json:"machine_id"`
	URL       string `json:"url"` // best resolved connection URL
}

// ListServers authenticates on plex.tv and returns all available Plex servers.
// apiKey (plex.tv account token) takes precedence over username/password.
func ListServers(apiKey, username, password string) ([]ServerInfo, error) {
	token := apiKey
	if token == "" {
		var err error
		token, err = signIn(username, password)
		if err != nil {
			return nil, err
		}
	}
	resources, err := fetchResources(token)
	if err != nil {
		return nil, err
	}
	servers := make([]ServerInfo, 0, len(resources))
	for _, r := range resources {
		bestURL := ""
		if best := pickBestConnection(r.Connections); best != nil {
			bestURL = best.URI
		}
		servers = append(servers, ServerInfo{
			Name:      r.Name,
			MachineID: r.ClientIdentifier,
			URL:       bestURL,
		})
	}
	return servers, nil
}

// Client resolves a Plex server via plex.tv then wraps a plex.Client.
type Client struct {
	inner connector.MediaConnector
}

func (c *Client) Connect(cfg config.ServerConfig) error {
	if cfg.MachineID == "" {
		return fmt.Errorf("plextv connect: machine_id is required (run 'mediafs plex-servers' to list available servers)")
	}

	// Step 1: obtain a plex.tv auth token
	var globalToken string
	if cfg.APIKey != "" {
		log.Printf("[plextv] using provided plex.tv token")
		globalToken = cfg.APIKey
	} else if cfg.Password != "" {
		log.Printf("[plextv] authenticating on plex.tv for user %q", cfg.Username)
		tok, err := signIn(cfg.Username, cfg.Password)
		if err != nil {
			return fmt.Errorf("plextv connect: %w", err)
		}
		globalToken = tok
		log.Printf("[plextv] plex.tv sign-in OK")
	} else {
		return fmt.Errorf("plextv connect: no api_key or password provided")
	}

	// Step 2: list servers from plex.tv and find the target
	resources, err := fetchResources(globalToken)
	if err != nil {
		return fmt.Errorf("plextv connect: %w", err)
	}
	log.Printf("[plextv] plex.tv returned %d server(s)", len(resources))

	var target *plexResource
	for i := range resources {
		log.Printf("[plextv]   server %q (id=%s)", resources[i].Name, resources[i].ClientIdentifier)
		if strings.EqualFold(resources[i].ClientIdentifier, cfg.MachineID) {
			target = &resources[i]
		}
	}
	if target == nil {
		return fmt.Errorf("plextv connect: server with machine_id %q not found on plex.tv", cfg.MachineID)
	}
	log.Printf("[plextv] found server %q", target.Name)

	// Step 3: resolve best connection URL
	bestConn := pickBestConnection(target.Connections)
	if bestConn == nil {
		return fmt.Errorf("plextv connect: no usable connection for server %q", target.Name)
	}
	log.Printf("[plextv] best connection: %s (local=%v relay=%v)", bestConn.URI, bestConn.Local, bestConn.Relay)

	// Step 4: prefer per-server token (works for shared servers); fall back to global token
	serverToken := target.AccessToken
	if serverToken == "" {
		serverToken = globalToken
	}

	// Step 5: delegate to the inner plex.Client
	inner := connector.New(config.ConnectorPlex)
	if inner == nil {
		return fmt.Errorf("plextv connect: plex connector not registered")
	}
	if err := inner.Connect(config.ServerConfig{
		URL:    bestConn.URI,
		APIKey: serverToken,
	}); err != nil {
		return fmt.Errorf("plextv connect: %w", err)
	}
	c.inner = inner
	return nil
}

// --- connector.MediaConnector — delegate to inner plex.Client ---

func (c *Client) Ping() error                    { return c.inner.Ping() }
func (c *Client) GetLibraries() ([]connector.Library, error) { return c.inner.GetLibraries() }
func (c *Client) GetItems(libID, parentID string) ([]connector.MediaItem, error) {
	return c.inner.GetItems(libID, parentID)
}
func (c *Client) GetItemMetadata(itemID string) (connector.ItemMetadata, error) {
	return c.inner.GetItemMetadata(itemID)
}
func (c *Client) GetArtworkURL(itemID string, artType connector.ArtworkType) (string, error) {
	return c.inner.GetArtworkURL(itemID, artType)
}
func (c *Client) GetStreamURL(itemID string) (string, error) { return c.inner.GetStreamURL(itemID) }
func (c *Client) GetFileSize(itemID string) (int64, error)   { return c.inner.GetFileSize(itemID) }

// --- plex.tv API ---

const plexTvBase = "https://plex.tv"

type plexTvSignInResponse struct {
	User struct {
		AuthToken string `json:"authToken"`
	} `json:"user"`
}

type plexResource struct {
	ClientIdentifier string           `json:"clientIdentifier"`
	Name             string           `json:"name"`
	Provides         string           `json:"provides"`
	AccessToken      string           `json:"accessToken"`
	Connections      []plexConnection `json:"connections"`
}

type plexConnection struct {
	Protocol string `json:"protocol"`
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
	IPv6     bool   `json:"IPv6"`
}

func signIn(username, password string) (string, error) {
	form := url.Values{
		"user[login]":    {username},
		"user[password]": {password},
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", plexTvBase+"/users/sign_in.json",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Plex-Product", "MediaFS")
	req.Header.Set("X-Plex-Client-Identifier", "mediafs-cli")
	req.Header.Set("X-Plex-Version", "1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("plex.tv sign_in failed (%d): %s", resp.StatusCode, b)
	}

	var result plexTvSignInResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.User.AuthToken == "" {
		return "", fmt.Errorf("empty authToken in plex.tv response")
	}
	return result.User.AuthToken, nil
}

func fetchResources(token string) ([]plexResource, error) {
	u := fmt.Sprintf("%s/api/v2/resources?includeHttps=1&includeRelay=1&X-Plex-Token=%s", plexTvBase, token)
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Product", "MediaFS")
	req.Header.Set("X-Plex-Client-Identifier", "mediafs-cli")
	req.Header.Set("X-Plex-Version", "1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET plex.tv/api/v2/resources failed (%d): %s", resp.StatusCode, b)
	}

	var resources []plexResource
	if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		return nil, err
	}

	// Keep only server resources
	var servers []plexResource
	for _, r := range resources {
		if strings.Contains(r.Provides, "server") {
			servers = append(servers, r)
		}
	}
	return servers, nil
}

// pickBestConnection selects the preferred connection following VirtualLib's priority:
// 1. Non-local, non-relay (plex.direct), HTTPS
// 2. Non-local, relay, HTTPS
// 3. Local, HTTPS
// 4. Any HTTP fallback
// IPv6 connections are excluded.
func pickBestConnection(conns []plexConnection) *plexConnection {
	// Filter out IPv6
	var valid []plexConnection
	for _, c := range conns {
		if !c.IPv6 && c.URI != "" {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return nil
	}

	sort.SliceStable(valid, func(i, j int) bool {
		return connectionScore(valid[i]) > connectionScore(valid[j])
	})
	return &valid[0]
}

// connectionScore returns a priority score (higher = preferred).
// plex.direct non-local HTTPS > relay HTTPS > local HTTPS > HTTP variants
func connectionScore(c plexConnection) int {
	score := 0
	if c.Protocol == "https" {
		score += 4
	}
	if !c.Local {
		score += 2
	}
	if !c.Relay {
		score += 1
	}
	return score
}
