package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
)

func TestWorkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

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
	if _, err := os.Stat(filepath.Join(dir, "works.json")); !os.IsNotExist(err) {
		t.Error("SetWork() should not create the legacy shared works.json cache")
	}
}

func TestWorkReadsLegacySharedCache(t *testing.T) {
	dir := t.TempDir()
	cachedAt := time.Now()
	legacy := fmt.Sprintf(`{"作品":{"data":{"id":1,"title":"作品"},"cached_at":%q}}`, cachedAt.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, "works.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := New(dir).GetWork("作品")
	if !ok || got.ID != 1 || got.Title != "作品" {
		t.Errorf("GetWork() legacy result = %+v, %v; want work ID 1", got, ok)
	}
}

func TestConcurrentCacheInstancesDoNotLoseWorks(t *testing.T) {
	dir := t.TempDir()
	const count = 32
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			title := fmt.Sprintf("作品%d", i)
			err := New(dir).SetWork(title, &annict.Work{ID: i + 1, Title: title})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SetWork() error = %v", err)
		}
	}

	c := New(dir)
	for i := 0; i < count; i++ {
		title := fmt.Sprintf("作品%d", i)
		got, ok := c.GetWork(title)
		if !ok || got.ID != i+1 {
			t.Errorf("GetWork(%q) = %+v, %v; want ID %d", title, got, ok, i+1)
		}
	}
}

func TestAtomicWritesLeaveNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	if err := c.SetWork("作品", &annict.Work{ID: 1, Title: "作品"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEpisodes(1, []annict.Episode{{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("atomic cache writes left temp files: %v", leftovers)
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

func TestWorkWithFutureTimestampIsRejected(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	entry := cacheEntry[annict.Work]{
		Data:     annict.Work{ID: 1, Title: "未来の作品"},
		CachedAt: time.Now().Add(time.Hour),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.workPath("未来の作品"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.GetWork("未来の作品"); ok {
		t.Error("GetWork() should reject a cache entry timestamped in the future")
	}
}

func TestOversizedWorkCacheIsRejected(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	path := c.workPath("巨大キャッシュ")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxCacheFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.GetWork("巨大キャッシュ"); ok {
		t.Error("GetWork() should reject an oversized cache file")
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
