package cache

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// memEntry is one in-memory cache entry.
type memEntry struct {
	data      []byte
	expiresAt int64 // Unix seconds
}

// Cache stores:
//   - items and metadata: pure in-memory (sync.Map) — fast, no persistence needed
//   - artwork: SQLite on disk — large blobs worth persisting across restarts
type Cache struct {
	db          *sql.DB
	ttlItems    time.Duration
	ttlMetadata time.Duration
	ttlArtwork  time.Duration
	memItems    sync.Map // key: serverKey+"\x00"+parentID → memEntry
	memMeta     sync.Map // key: serverKey+"\x00"+itemID   → memEntry
}

func cacheDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "MediaFS")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "mediafs")
}

// Open opens (or creates) the cache database (artwork only).
func Open(ttlItems, ttlMetadata, ttlArtwork time.Duration) (*Cache, error) {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "cache.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

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

// --- Items (in-memory only) ---

func (c *Cache) GetItems(serverKey, parentID string, dest interface{}) (bool, error) {
	key := serverKey + "\x00" + parentID
	now := time.Now().Unix()
	if v, ok := c.memItems.Load(key); ok {
		e := v.(memEntry)
		if e.expiresAt > now {
			return true, json.Unmarshal(e.data, dest)
		}
		c.memItems.Delete(key)
	}
	return false, nil
}

func (c *Cache) StoreItems(serverKey, parentID string, items interface{}) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	key := serverKey + "\x00" + parentID
	c.memItems.Store(key, memEntry{data: data, expiresAt: time.Now().Add(c.ttlItems).Unix()})
	return nil
}

// --- Metadata (in-memory only) ---

func (c *Cache) GetMetadata(serverKey, itemID string, dest interface{}) (bool, error) {
	key := serverKey + "\x00" + itemID
	now := time.Now().Unix()
	if v, ok := c.memMeta.Load(key); ok {
		e := v.(memEntry)
		if e.expiresAt > now {
			return true, json.Unmarshal(e.data, dest)
		}
		c.memMeta.Delete(key)
	}
	return false, nil
}

func (c *Cache) StoreMetadata(serverKey, itemID string, meta interface{}) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	key := serverKey + "\x00" + itemID
	c.memMeta.Store(key, memEntry{data: data, expiresAt: time.Now().Add(c.ttlMetadata).Unix()})
	return nil
}

// --- Artwork (SQLite — persisted across restarts) ---

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
	prefix := serverKey + "\x00"
	for _, m := range []*sync.Map{&c.memItems, &c.memMeta} {
		m.Range(func(k, _ interface{}) bool {
			if s, ok := k.(string); ok && len(s) > len(prefix) && s[:len(prefix)] == prefix {
				m.Delete(k)
			}
			return true
		})
	}
	_, err := c.db.Exec(`DELETE FROM artwork WHERE server_key=?`, serverKey)
	return err
}

// InvalidateItems drops only the item listings for a server (e.g. after library refresh).
func (c *Cache) InvalidateItems(serverKey string) {
	prefix := serverKey + "\x00"
	c.memItems.Range(func(k, _ interface{}) bool {
		if s, ok := k.(string); ok && len(s) > len(prefix) && s[:len(prefix)] == prefix {
			c.memItems.Delete(k)
		}
		return true
	})
}

func (c *Cache) Close() error {
	return c.db.Close()
}

