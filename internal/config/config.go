package config

import (
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

type ConnectorType string

const (
	ConnectorJellyfin ConnectorType = "jellyfin"
	ConnectorEmby     ConnectorType = "emby"
	ConnectorPlex     ConnectorType = "plex"
)

type ServerConfig struct {
	Alias    string        `yaml:"alias"     json:"alias"`
	Type     ConnectorType `yaml:"type"      json:"type"`
	URL      string        `yaml:"url"       json:"url"`
	Username string        `yaml:"username"  json:"username"`
	APIKey   string        `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Password string        `yaml:"password,omitempty" json:"password,omitempty"`
	Enabled  bool          `yaml:"enabled"   json:"enabled"`
}

type DownloadConfig struct {
	ParallelChunks    int `yaml:"parallel_chunks"`
	ChunkSizeMB       int `yaml:"chunk_size_mb"`
	BufferSizeKB      int `yaml:"buffer_size_kb"`
	ReadAheadWindowMB int `yaml:"read_ahead_window_mb"`
	ReadAheadWindows  int `yaml:"read_ahead_windows"`
	MaxCacheMB        int `yaml:"max_cache_mb"`
	CacheTTLMin       int `yaml:"cache_ttl_min"`
}

type CacheConfig struct {
	TTLItemsSec    int `yaml:"ttl_items_sec"`
	TTLMetaSec     int `yaml:"ttl_metadata_sec"`
	TTLArtworkSec  int `yaml:"ttl_artwork_sec"`
}

type MountConfig struct {
	DriveLetter string `yaml:"drive_letter"` // Windows
	MountPoint  string `yaml:"mount_point"`  // Linux
}

type Config struct {
	Mount    MountConfig    `yaml:"mount"`
	Servers  []ServerConfig `yaml:"servers"`
	Download DownloadConfig `yaml:"download"`
	Cache    CacheConfig    `yaml:"cache"`
}

func DefaultConfig() *Config {
	return &Config{
		Mount: MountConfig{
			DriveLetter: "Z",
			MountPoint:  "/mnt/mediafs",
		},
		Download: DownloadConfig{
			ParallelChunks:    4,
			ChunkSizeMB:       64,
			BufferSizeKB:      256,
			ReadAheadWindowMB: 64,
			ReadAheadWindows:  2,
			MaxCacheMB:        2048,
			CacheTTLMin:       120,
		},
		Cache: CacheConfig{
			TTLItemsSec:   300,
			TTLMetaSec:    3600,
			TTLArtworkSec: 86400,
		},
	}
}

func ConfigDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "MediaFS")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mediafs")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

func Load() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0600)
}

// ServerKey returns the display name used as the VFS root folder name.
// Format: "username@alias"
func (s ServerConfig) ServerKey() string {
	if s.Username != "" {
		return s.Username + "@" + s.Alias
	}
	return s.Alias
}
