package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/renamer"
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

func TestReadTokenFromEnvFileCommonDotenvSyntax(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "spaces around assignment", content: "ANNICT_ACCESS_TOKEN = token-with-spaces\n", want: "token-with-spaces"},
		{name: "double quoted", content: "ANNICT_ACCESS_TOKEN=\"quoted-token\"\n", want: "quoted-token"},
		{name: "single quoted export", content: "export ANNICT_ACCESS_TOKEN='exported-token'\n", want: "exported-token"},
		{name: "skip empty assignment", content: "ANNICT_ACCESS_TOKEN=\nANNICT_ACCESS_TOKEN=fallback\n", want: "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := readTokenFromEnvFile(path)
			if !ok || got != tt.want {
				t.Errorf("readTokenFromEnvFile() = %q, %v; want %q, true", got, ok, tt.want)
			}
		})
	}
}

func TestExitCodeForFailures(t *testing.T) {
	for _, tt := range []struct {
		failed int
		want   int
	}{
		{failed: 0, want: 0},
		{failed: 1, want: 1},
		{failed: 10, want: 1},
	} {
		if got := exitCodeForFailures(tt.failed); got != tt.want {
			t.Errorf("exitCodeForFailures(%d) = %d, want %d", tt.failed, got, tt.want)
		}
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

func TestCollectFiles_RejectsUnsupportedSingleFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("not a recording"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := collectFiles(file, false); err == nil {
		t.Error("collectFiles() should reject an unsupported explicit file")
	}
}

func TestCollectFiles_RejectsExplicitSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional privileges on Windows")
	}

	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.mp4")
	link := filepath.Join(dir, "link.mp4")
	if err := os.WriteFile(realFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatal(err)
	}

	if _, err := collectFiles(link, false); err == nil {
		t.Error("collectFiles() should reject an explicitly targeted symlink")
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

func TestCollectFiles_DirectorySkipsSymlinkedRecordings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional privileges on Windows")
	}

	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.mp4")
	link := filepath.Join(dir, "link.mp4")
	if err := os.WriteFile(realFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != realFile {
		t.Errorf("collectFiles() = %v, want only regular file %s", files, realFile)
	}
}

func TestCollectFiles_RecursiveWalkErrorIsReturned(t *testing.T) {
	dir := t.TempDir()
	walkErr := errors.New("permission denied")
	walker := func(root string, fn fs.WalkDirFunc) error {
		return fn(filepath.Join(root, "blocked"), nil, walkErr)
	}

	files, err := collectFilesWithWalker(dir, true, walker)
	if !errors.Is(err, walkErr) {
		t.Fatalf("collectFilesWithWalker() error = %v, want wrapped %v", err, walkErr)
	}
	if files != nil {
		t.Errorf("collectFilesWithWalker() files = %v, want nil on incomplete traversal", files)
	}
}

func TestValidateConfidenceThreshold(t *testing.T) {
	for _, value := range []int{0, 50, 90, 100} {
		if err := validateConfidenceThreshold(value); err != nil {
			t.Errorf("validateConfidenceThreshold(%d) error = %v, want nil", value, err)
		}
	}
	for _, value := range []int{-1, 101} {
		if err := validateConfidenceThreshold(value); err == nil {
			t.Errorf("validateConfidenceThreshold(%d) error = nil, want range error", value)
		}
	}
}

func TestValidateTargetArgs(t *testing.T) {
	if err := validateTargetArgs([]string{"recordings"}); err != nil {
		t.Errorf("validateTargetArgs() error = %v, want nil for one target", err)
	}
	for _, args := range [][]string{nil, {"one", "two"}} {
		if err := validateTargetArgs(args); err == nil {
			t.Errorf("validateTargetArgs(%v) error = nil, want argument-count error", args)
		}
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

func TestProcessFileFetchesProgramsOnlyForSelectedWork(t *testing.T) {
	var programWorkIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
				{"node":{"annictId":1,"title":"作品","seasonName":"SUMMER","seasonYear":2026,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"第一話"}}]}}},
				{"node":{"annictId":2,"title":"作品","seasonName":"SUMMER","seasonYear":2025,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":201,"number":1,"sortNumber":1,"title":"第一話"}}]}}}
			]}}}`)
		case "/programs":
			programWorkIDs = append(programWorkIDs, r.URL.Query().Get("filter_work_ids"))
			fmt.Fprint(w, `{"programs":[{"id":1,"started_at":"2026-08-01T12:00:00+09:00","episode":{"id":101}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品 第1話「第一話・拡大版」 (20260801).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql")
	result := processFile(
		file,
		client,
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil {
		t.Fatalf("processFile() error = %v", result.Error)
	}
	if len(programWorkIDs) != 1 || programWorkIDs[0] != "1" {
		t.Errorf("program API work IDs = %v, want only selected work [1]", programWorkIDs)
	}
}

func TestProcessFileSkipsProgramsWhenConfidenceAlreadyMeetsThreshold(t *testing.T) {
	programRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
				{"node":{"annictId":1,"title":"作品","seasonName":"SUMMER","seasonYear":2026,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"第一話"}}]}}}
			]}}}`)
		case "/programs":
			programRequests++
			fmt.Fprint(w, `{"programs":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品 第1話「第一話」 (20260801).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql")
	result := processFile(
		file,
		client,
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil {
		t.Fatalf("processFile() error = %v", result.Error)
	}
	if programRequests != 0 {
		t.Errorf("program API requests = %d, want 0 after confidence already met threshold", programRequests)
	}
}

func loadFixtureJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return result
}

// TestEndToEndPipeline runs a realistic recording filename through the
// actual parser -> matcher -> renamer pipeline (unlike the matcher
// package's own tests, which hand-construct RecordingMetadata rather than
// parsing it), guarding against a parser change silently breaking matching
// even though each package's own unit tests still pass in isolation.
func TestEndToEndPipeline(t *testing.T) {
	filename := "花ざかりの君たちへ 第2期 ep．7「ずっとそばにいたいから」 (20260813).mp4"

	meta, err := parser.ParseFilename(filename)
	if err != nil {
		t.Fatalf("ParseFilename() error = %v", err)
	}

	workResp := loadFixtureJSON[annict.WorksResponse](t, "testdata/hanakimi-work.json")
	epResp := loadFixtureJSON[annict.EpisodesResponse](t, "testdata/hanakimi-episodes.json")
	progResp := loadFixtureJSON[annict.ProgramsResponse](t, "testdata/hanakimi-programs.json")

	episodesByWork := map[int][]annict.Episode{4168: epResp.Episodes}
	programsByWork := map[int][]annict.Program{4168: progResp.Programs}

	result := matcher.Match(meta, workResp.Works, episodesByWork, programsByWork)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence < matcher.AutoRenameThreshold {
		t.Fatalf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, matcher.AutoRenameThreshold, result.Reasons)
	}

	newPath, err := renamer.BuildPath("/recordings/"+filename, result)
	if err != nil {
		t.Fatalf("BuildPath() error = %v", err)
	}
	wantPath := "/recordings/花ざかりの君たちへ 第2期/花ざかりの君たちへ 第2期 #7 「ずっとそばにいたいから」.mp4"
	if newPath != wantPath {
		t.Errorf("BuildPath() = %q, want %q", newPath, wantPath)
	}
}
