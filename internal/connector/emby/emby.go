// Package emby provides an Emby connector.
// Emby and Jellyfin share the same REST API; the only difference is the
// authentication header name.  This package re-uses the Jellyfin client
// and overrides the header at Connect time.
package emby

import (
	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
	jf "github.com/CCoupel/Media_FS/internal/connector/jellyfin"
)

func init() {
	connector.Register(config.ConnectorEmby, func() connector.MediaConnector {
		return &Client{}
	})
}

// Client wraps the Jellyfin client with Emby-specific auth.
type Client struct {
	jf.Client
}

func (c *Client) Connect(cfg config.ServerConfig) error {
	if err := c.Client.Connect(cfg); err != nil {
		return err
	}
	// Emby uses X-Emby-Token instead of X-MediaBrowser-Token
	c.Client.SetAuthHeader("X-Emby-Token")
	return nil
}
