package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/matcher"
)

func writeEnvFile(t *testing.T, dir, token string) {
	t.Helper()
	content := "ANNICT_ACCESS_TOKEN=" + token + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTokenFromDotenv_TargetIsDirectory(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, "dir-token")

	if got := loadTokenFromDotenv(dir); got != "dir-token" {
		t.Errorf("loadTokenFromDotenv(%q) = %q, want %q", dir, got, "dir-token")
	}
}

func TestLoadTokenFromDotenv_TargetIsFileUsesParentDir(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, "file-token")

	file := filepath.Join(dir, "recording.mp4")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := loadTokenFromDotenv(file); got != "file-token" {
		t.Errorf("loadTokenFromDotenv(%q) = %q, want %q", file, got, "file-token")
	}
}

func TestLoadTokenFromDotenv_SearchesParentDirectories(t *testing.T) {
	root := t.TempDir()
	writeEnvFile(t, root, "parent-token")

	sub := filepath.Join(root, "recordings")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := loadTokenFromDotenv(sub); got != "parent-token" {
		t.Errorf("loadTokenFromDotenv(%q) = %q, want %q", sub, got, "parent-token")
	}
}

func TestLoadTokenFromDotenv_NoEnvFileTerminates(t *testing.T) {
	// Regression test: must terminate instead of looping forever when no
	// .env is found all the way up to the filesystem root.
	dir := t.TempDir()
	if got := loadTokenFromDotenv(dir); got != "" {
		t.Errorf("loadTokenFromDotenv(%q) = %q, want empty string", dir, got)
	}
}

func TestCollectFiles_SingleFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles(file, false)
	if err != nil {
		t.Fatalf("collectFiles() error = %v", err)
	}
	if len(files) != 1 || files[0] != file {
		t.Errorf("collectFiles() = %v, want [%s]", files, file)
	}
}

func TestCollectFiles_DirectoryNonRecursive(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.mp4", "b.ts", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "c.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles(dir, false)
	if err != nil {
		t.Fatalf("collectFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Errorf("collectFiles() returned %d files, want 2 (subdir and .txt excluded): %v", len(files), files)
	}
}

func TestCollectFiles_DirectoryRecursive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles(dir, true)
	if err != nil {
		t.Fatalf("collectFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Errorf("collectFiles(recursive) returned %d files, want 2: %v", len(files), files)
	}
}

func TestEpisodesComplete(t *testing.T) {
	tests := []struct {
		name     string
		work     annict.Work
		episodes []annict.Episode
		want     bool
	}{
		{
			name:     "fully covered",
			work:     annict.Work{EpisodesCount: 2},
			episodes: []annict.Episode{{ID: 1}, {ID: 2}},
			want:     true,
		},
		{
			name:     "truncated by GraphQL's 100-episode cap",
			work:     annict.Work{EpisodesCount: 150},
			episodes: make([]annict.Episode, 100),
			want:     false,
		},
		{
			name:     "unknown episode count must not be trusted",
			work:     annict.Work{EpisodesCount: 0},
			episodes: []annict.Episode{{ID: 1}},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := episodesComplete(tt.work, tt.episodes); got != tt.want {
				t.Errorf("episodesComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveConfidenceThreshold(t *testing.T) {
	tests := []struct {
		name          string
		explicitValue int
		explicitlySet bool
		dryRun        bool
		want          int
	}{
		{
			name: "normal run, no explicit flag, uses AutoRenameThreshold",
			want: matcher.AutoRenameThreshold,
		},
		{
			name:   "dry-run, no explicit flag, uses the more lenient DryRunThreshold",
			dryRun: true,
			want:   matcher.DryRunThreshold,
		},
		{
			name:          "explicit --confidence always wins, even under --dry-run",
			explicitValue: 55,
			explicitlySet: true,
			dryRun:        true,
			want:          55,
		},
		{
			name:          "explicit --confidence wins in a normal run too",
			explicitValue: 99,
			explicitlySet: true,
			want:          99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveConfidenceThreshold(tt.explicitValue, tt.explicitlySet, tt.dryRun); got != tt.want {
				t.Errorf("resolveConfidenceThreshold(%d, %v, %v) = %d, want %d",
					tt.explicitValue, tt.explicitlySet, tt.dryRun, got, tt.want)
			}
		})
	}
}

func TestGetProgramsCachesPerDateNotJustPerWork(t *testing.T) {
	// Regression test: a shared programsCache keyed only by workID would
	// let the second file's query for the same work silently reuse the
	// first file's program list, even though the two files have entirely
	// different recorded dates and therefore different (since, until)
	// windows — breaking findMatchingProgram's date-based matching for
	// every file after the first one of a given work in a batch.
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"programs":[{"id":%d,"started_at":"2026-08-01T00:00:00+09:00"}]}`, requestCount)
	}))
	defer server.Close()

	client := annict.NewClientWithBaseURL("token", server.URL)
	pc := make(map[programsCacheKey][]annict.Program)

	jst := time.FixedZone("JST", 9*60*60)
	date1 := time.Date(2026, 8, 1, 0, 0, 0, 0, jst)
	date2 := time.Date(2026, 8, 8, 0, 0, 0, 0, jst)
	workID := 1

	if _, err := getPrograms(client, workID, date1, pc); err != nil {
		t.Fatal(err)
	}
	if _, err := getPrograms(client, workID, date2, pc); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Errorf("requests = %d, want 2 (one per distinct date for the same work)", requestCount)
	}

	// Re-fetching an already-seen (workID, date) pair must hit the cache.
	if _, err := getPrograms(client, workID, date1, pc); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Errorf("requests = %d after re-fetching a cached (workID, date), want still 2", requestCount)
	}
}
