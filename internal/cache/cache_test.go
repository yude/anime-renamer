package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
)

func TestWorkRoundTrip(t *testing.T) {
	c := New(t.TempDir())

	if _, ok := c.GetWork("作品"); ok {
		t.Fatal("GetWork() on empty cache should miss")
	}

	work := &annict.Work{ID: 1, Title: "作品"}
	if err := c.SetWork("作品", work); err != nil {
		t.Fatalf("SetWork() error = %v", err)
	}

	got, ok := c.GetWork("作品")
	if !ok {
		t.Fatal("GetWork() should hit after SetWork()")
	}
	if got.ID != work.ID || got.Title != work.Title {
		t.Errorf("GetWork() = %+v, want %+v", got, work)
	}
}

func TestWorkExpiresAfterTTL(t *testing.T) {
	c := NewWithTTL(t.TempDir(), -1*time.Second)

	if err := c.SetWork("作品", &annict.Work{ID: 1, Title: "作品"}); err != nil {
		t.Fatalf("SetWork() error = %v", err)
	}

	if _, ok := c.GetWork("作品"); ok {
		t.Error("GetWork() should miss once the entry is older than the TTL")
	}
}

func TestEpisodesRoundTrip(t *testing.T) {
	c := New(t.TempDir())

	if _, ok := c.GetEpisodes(42); ok {
		t.Fatal("GetEpisodes() on empty cache should miss")
	}

	episodes := []annict.Episode{{ID: 1, Title: "第一話"}, {ID: 2, Title: "第二話"}}
	if err := c.SetEpisodes(42, episodes); err != nil {
		t.Fatalf("SetEpisodes() error = %v", err)
	}

	got, ok := c.GetEpisodes(42)
	if !ok {
		t.Fatal("GetEpisodes() should hit after SetEpisodes()")
	}
	if len(got) != 2 || got[0].Title != "第一話" {
		t.Errorf("GetEpisodes() = %+v, want %+v", got, episodes)
	}

	// A different work ID must not see these episodes.
	if _, ok := c.GetEpisodes(43); ok {
		t.Error("GetEpisodes(43) should miss; only work 42 was cached")
	}
}

func TestEpisodesExpiresAfterTTL(t *testing.T) {
	c := NewWithTTL(t.TempDir(), -1*time.Second)

	if err := c.SetEpisodes(1, []annict.Episode{{ID: 1}}); err != nil {
		t.Fatalf("SetEpisodes() error = %v", err)
	}

	if _, ok := c.GetEpisodes(1); ok {
		t.Error("GetEpisodes() should miss once the entry is older than the TTL")
	}
}

func TestDisabledCacheNeverPersists(t *testing.T) {
	// Use a not-yet-existing subdirectory so a wrongly-created cache dir is
	// distinguishable from t.TempDir()'s own directory, which already exists.
	dir := filepath.Join(t.TempDir(), "cache")
	c := NewDisabled(dir)

	if err := c.SetWork("作品", &annict.Work{ID: 1, Title: "作品"}); err != nil {
		t.Fatalf("SetWork() on disabled cache error = %v", err)
	}
	if _, ok := c.GetWork("作品"); ok {
		t.Error("GetWork() on disabled cache should always miss")
	}

	if err := c.SetEpisodes(1, []annict.Episode{{ID: 1}}); err != nil {
		t.Fatalf("SetEpisodes() on disabled cache error = %v", err)
	}
	if _, ok := c.GetEpisodes(1); ok {
		t.Error("GetEpisodes() on disabled cache should always miss")
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("disabled cache must not create the cache directory")
	}
}

func TestClearRemovesCacheDir(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "anime-renamer"))

	if err := c.SetWork("作品", &annict.Work{ID: 1, Title: "作品"}); err != nil {
		t.Fatalf("SetWork() error = %v", err)
	}
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, ok := c.GetWork("作品"); ok {
		t.Error("GetWork() should miss after Clear()")
	}
}
