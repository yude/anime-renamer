package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
)

const defaultTTL = 7 * 24 * time.Hour

// Cache provides JSON file-based caching for Annict API responses.
type Cache struct {
	dir     string
	ttl     time.Duration
	enabled bool
	mu      sync.RWMutex
}

// New creates a new Cache. dir is the cache directory.
func New(dir string) *Cache {
	return &Cache{
		dir:     dir,
		ttl:     defaultTTL,
		enabled: true,
	}
}

// NewWithTTL creates a cache with a custom TTL.
func NewWithTTL(dir string, ttl time.Duration) *Cache {
	return &Cache{
		dir:     dir,
		ttl:     ttl,
		enabled: true,
	}
}

// NewDisabled creates a Cache that never reads or writes to disk. All Get
// methods report a miss and all Set methods are no-ops. Used for --no-cache.
func NewDisabled(dir string) *Cache {
	return &Cache{
		dir: dir,
		ttl: defaultTTL,
	}
}

type cacheEntry[T any] struct {
	Data     T         `json:"data"`
	CachedAt time.Time `json:"cached_at"`
}

// GetWork retrieves a cached work by title.
func (c *Cache) GetWork(title string) (*annict.Work, bool) {
	if !c.enabled {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	path := filepath.Join(c.dir, "works.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var entries map[string]cacheEntry[annict.Work]
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false
	}

	entry, ok := entries[title]
	if !ok {
		return nil, false
	}

	if time.Since(entry.CachedAt) > c.ttl {
		return nil, false
	}

	return &entry.Data, true
}

// SetWork caches a work by title.
func (c *Cache) SetWork(title string, work *annict.Work) error {
	if !c.enabled {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	path := filepath.Join(c.dir, "works.json")
	entries := make(map[string]cacheEntry[annict.Work])

	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &entries)
	}

	entries[title] = cacheEntry[annict.Work]{
		Data:     *work,
		CachedAt: time.Now(),
	}

	data, err = json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

// GetEpisodes retrieves cached episodes for a work ID.
func (c *Cache) GetEpisodes(workID int) ([]annict.Episode, bool) {
	if !c.enabled {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	path := filepath.Join(c.dir, fmt.Sprintf("episodes_%d.json", workID))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var entry cacheEntry[[]annict.Episode]
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}

	if time.Since(entry.CachedAt) > c.ttl {
		return nil, false
	}

	return entry.Data, true
}

// SetEpisodes caches episodes for a work ID.
func (c *Cache) SetEpisodes(workID int, episodes []annict.Episode) error {
	if !c.enabled {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	path := filepath.Join(c.dir, fmt.Sprintf("episodes_%d.json", workID))
	entry := cacheEntry[[]annict.Episode]{
		Data:     episodes,
		CachedAt: time.Now(),
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

// Clear removes all cached files.
func (c *Cache) Clear() error {
	return os.RemoveAll(c.dir)
}
