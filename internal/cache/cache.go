package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

// Cache stores metadata, item listings, and artwork with per-type TTLs.
type Cache struct {
	db             *sql.DB
	ttlItems       time.Duration
	ttlMetadata    time.Duration
	ttlArtwork     time.Duration
}

func cacheDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "MediaFS")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "mediafs")
}

// Open opens (or creates) the cache database.
func Open(ttlItems, ttlMetadata, ttlArtwork time.Duration) (*Cache, error) {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "cache.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer

	c := &Cache{
		db:          db,
		ttlItems:    ttlItems,
		ttlMetadata: ttlMetadata,
		ttlArtwork:  ttlArtwork,
	}
	return c, c.migrate()
}

func (c *Cache) migrate() error {
	_, err := c.db.Exec(`
		CREATE TABLE IF NOT EXISTS items (
			server_key TEXT NOT NULL,
			parent_id  TEXT NOT NULL,
			data       TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			PRIMARY KEY (server_key, parent_id)
		);
		CREATE TABLE IF NOT EXISTS metadata (
			server_key TEXT NOT NULL,
			item_id    TEXT NOT NULL,
			data       TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			PRIMARY KEY (server_key, item_id)
		);
		CREATE TABLE IF NOT EXISTS artwork (
			server_key TEXT NOT NULL,
			item_id    TEXT NOT NULL,
			art_type   TEXT NOT NULL,
			data       BLOB NOT NULL,
			expires_at INTEGER NOT NULL,
			PRIMARY KEY (server_key, item_id, art_type)
		);
	`)
	return err
}

// --- Items ---

func (c *Cache) GetItems(serverKey, parentID string, dest interface{}) (bool, error) {
	row := c.db.QueryRow(
		`SELECT data FROM items WHERE server_key=? AND parent_id=? AND expires_at>?`,
		serverKey, parentID, time.Now().Unix(),
	)
	var raw string
	if err := row.Scan(&raw); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dest)
}

func (c *Cache) StoreItems(serverKey, parentID string, items interface{}) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(
		`INSERT OR REPLACE INTO items (server_key, parent_id, data, expires_at) VALUES (?,?,?,?)`,
		serverKey, parentID, string(data), time.Now().Add(c.ttlItems).Unix(),
	)
	return err
}

// --- Metadata ---

func (c *Cache) GetMetadata(serverKey, itemID string, dest interface{}) (bool, error) {
	row := c.db.QueryRow(
		`SELECT data FROM metadata WHERE server_key=? AND item_id=? AND expires_at>?`,
		serverKey, itemID, time.Now().Unix(),
	)
	var raw string
	if err := row.Scan(&raw); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dest)
}

func (c *Cache) StoreMetadata(serverKey, itemID string, meta interface{}) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(
		`INSERT OR REPLACE INTO metadata (server_key, item_id, data, expires_at) VALUES (?,?,?,?)`,
		serverKey, itemID, string(data), time.Now().Add(c.ttlMetadata).Unix(),
	)
	return err
}

// --- Artwork ---

func (c *Cache) GetArtwork(serverKey, itemID, artType string) ([]byte, bool, error) {
	row := c.db.QueryRow(
		`SELECT data FROM artwork WHERE server_key=? AND item_id=? AND art_type=? AND expires_at>?`,
		serverKey, itemID, artType, time.Now().Unix(),
	)
	var data []byte
	if err := row.Scan(&data); err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (c *Cache) StoreArtwork(serverKey, itemID, artType string, data []byte) error {
	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO artwork (server_key, item_id, art_type, data, expires_at) VALUES (?,?,?,?,?)`,
		serverKey, itemID, artType, data, time.Now().Add(c.ttlArtwork).Unix(),
	)
	return err
}

// Invalidate removes all cached entries for a given server.
func (c *Cache) Invalidate(serverKey string) error {
	for _, table := range []string{"items", "metadata", "artwork"} {
		if _, err := c.db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE server_key=?`, table), serverKey); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) Close() error {
	return c.db.Close()
}
