package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
)

const (
	defaultTTL        = 7 * 24 * time.Hour
	maxCacheFileBytes = 8 << 20
)

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

	path := c.workPath(title)
	data, err := readCacheFile(path)
	if err == nil {
		var entry cacheEntry[annict.Work]
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, false
		}
		if !cacheEntryFresh(entry.CachedAt, c.ttl) {
			return nil, false
		}
		return &entry.Data, true
	}
	if !os.IsNotExist(err) {
		return nil, false
	}

	// Read the pre-v1.1 shared cache format for compatibility. New writes
	// use one file per title to avoid repeatedly decoding and rewriting an
	// ever-growing map and to prevent separate processes losing each
	// other's updates.
	data, err = readCacheFile(filepath.Join(c.dir, "works.json"))
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

	if !cacheEntryFresh(entry.CachedAt, c.ttl) {
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

	if work == nil {
		return fmt.Errorf("cache work: nil work")
	}

	entry := cacheEntry[annict.Work]{
		Data:     *work,
		CachedAt: time.Now(),
	}
	return writeJSONAtomic(c.workPath(title), entry)
}

func (c *Cache) workPath(title string) string {
	hash := sha256.Sum256([]byte(title))
	return filepath.Join(c.dir, fmt.Sprintf("work_%x.json", hash))
}

// GetEpisodes retrieves cached episodes for a work ID.
func (c *Cache) GetEpisodes(workID int) ([]annict.Episode, bool) {
	if !c.enabled {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	path := filepath.Join(c.dir, fmt.Sprintf("episodes_%d.json", workID))
	data, err := readCacheFile(path)
	if err != nil {
		return nil, false
	}

	var entry cacheEntry[[]annict.Episode]
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}

	if !cacheEntryFresh(entry.CachedAt, c.ttl) {
		return nil, false
	}

	return entry.Data, true
}

func cacheEntryFresh(cachedAt time.Time, ttl time.Duration) bool {
	age := time.Since(cachedAt)
	return age >= 0 && age <= ttl
}

func readCacheFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxCacheFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCacheFileBytes {
		return nil, fmt.Errorf("cache file exceeds %d bytes", maxCacheFileBytes)
	}
	return data, nil
}

// SetEpisodes caches episodes for a work ID.
func (c *Cache) SetEpisodes(workID int, episodes []annict.Episode) error {
	if !c.enabled {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	path := filepath.Join(c.dir, fmt.Sprintf("episodes_%d.json", workID))
	entry := cacheEntry[[]annict.Episode]{
		Data:     episodes,
		CachedAt: time.Now(),
	}

	return writeJSONAtomic(path, entry)
}

// Clear removes all cached files.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return os.RemoveAll(c.dir)
}

// writeJSONAtomic writes a complete cache entry to a unique temporary file
// and then replaces the destination. Readers therefore see either the old
// complete JSON or the new complete JSON, never a partially written file.
func writeJSONAtomic(path string, value any) (err error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if len(data) > maxCacheFileBytes {
		return fmt.Errorf("cache entry exceeds %d bytes", maxCacheFileBytes)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("set cache temp mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write cache temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace cache file: %w", err)
	}
	return nil
}
