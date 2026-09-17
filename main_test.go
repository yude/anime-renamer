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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/renamer"
	"github.com/yude/anime-renamer/internal/syobocal"
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

func TestCollectFiles_AcceptsM2TSRecording(t *testing.T) {
	file := filepath.Join(t.TempDir(), "recording.m2ts")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
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

func TestDirectoryTitleHint(t *testing.T) {
	for _, tt := range []struct {
		name        string
		file        string
		parsedTitle string
		want        string
		wantOK      bool
	}{
		{name: "different parent is a hint", file: "/recordings/Charlotte/file.mp4", parsedTitle: "ヴァイスシュヴァルツ劇場 Charlotte", want: "Charlotte", wantOK: true},
		{name: "normalized equal parent is redundant", file: "/recordings/作品/file.mp4", parsedTitle: "作品", wantOK: false},
		{name: "relative file has no useful parent", file: "file.mp4", parsedTitle: "作品", wantOK: false},
		{name: "filesystem root is not a title", file: "/file.mp4", parsedTitle: "作品", wantOK: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := directoryTitleHint(tt.file, tt.parsedTitle)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("directoryTitleHint() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
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

func TestProcessFileSkipsEpisodesForUnrelatedSearchResults(t *testing.T) {
	var episodeWorkIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			// An empty GraphQL result exercises the REST fuzzy-search fallback.
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[]}}}`)
		case "/works":
			fmt.Fprint(w, `{"works":[
				{"id":1,"title":"作品","episodes_count":1},
				{"id":2,"title":"無関係な番組","episodes_count":1}
			]}`)
		case "/episodes":
			workID := r.URL.Query().Get("filter_work_id")
			episodeWorkIDs = append(episodeWorkIDs, workID)
			if workID != "1" {
				t.Errorf("requested episodes for unrelated work %q", workID)
			}
			fmt.Fprint(w, `{"episodes":[{"id":101,"number":1,"sort_number":1,"title":"第一話"}]}`)
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil {
		t.Fatalf("processFile() error = %v", result.Error)
	}
	if len(episodeWorkIDs) != 1 || episodeWorkIDs[0] != "1" {
		t.Errorf("episode API work IDs = %v, want only matching work [1]", episodeWorkIDs)
	}
}

func TestProcessFileResolvesNumberlessSubtitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","seasonName":"SPRING","seasonYear":2022,"episodesCount":3,"episodes":{"edges":[
				{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"はじまり"}},
				{"node":{"annictId":102,"number":2,"sortNumber":2,"title":"再会"}},
				{"node":{"annictId":103,"number":3,"sortNumber":3,"title":"旅立ち"}}
			]}}}
		]}}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品「再会」 (20220414).mp4")
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed {
		t.Fatalf("processFile() = %+v, want episode 2 preview", result)
	}
	if result.WorkTitle != "作品" || result.EpisodeNum != 2 {
		t.Errorf("processFile() result = %+v, want 作品 episode 2", result)
	}
}

func TestProcessFileSafelySkipsUnresolvedNumberlessSubtitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[]}}}`)
		case "/works":
			fmt.Fprint(w, `{"works":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品「未知の回」 (20220414).mp4")
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil || result.SkipReason == "" || result.Previewed {
		t.Fatalf("processFile() = %+v, want safe skip", result)
	}
}

func TestProcessFileUsesDirectoryTitleAfterEmptyFilenameSearch(t *testing.T) {
	graphqlRequests := 0
	restWorkRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			graphqlRequests++
			if graphqlRequests == 1 {
				fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[]}}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
				{"node":{"annictId":1,"title":"Charlotte","seasonName":"SUMMER","seasonYear":2015,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"我他人を思う"}}]}}}
			]}}}`)
		case "/works":
			restWorkRequests++
			fmt.Fprint(w, `{"works":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := filepath.Join(t.TempDir(), "Charlotte")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ヴァイスシュヴァルツ劇場 アニメ Charlotte 第一話.mp4")
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil {
		t.Fatalf("processFile() error = %v", result.Error)
	}
	if result.WorkTitle != "Charlotte" || result.EpisodeNum != 1 {
		t.Errorf("processFile() result = %+v, want Charlotte episode 1", result)
	}
	if graphqlRequests != 2 || restWorkRequests != 1 {
		t.Errorf("requests: graphql=%d REST works=%d, want 2 and 1", graphqlRequests, restWorkRequests)
	}
}

func TestProcessFileRetriesExplicitRelatedSeasonAfterMissingEpisode(t *testing.T) {
	graphqlRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		graphqlRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","seasonName":"WINTER","seasonYear":2025,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"第一話"}}]}}},
			{"node":{"annictId":2,"title":"作品 第2期","seasonName":"WINTER","seasonYear":2026,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":201,"number":13,"sortNumber":1,"title":"再会"}}]}}}
		]}}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品 #13「再会」.mp4")
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil || result.WorkTitle != "作品 第2期" || result.EpisodeNum != 13 || !result.Previewed {
		t.Fatalf("processFile() = %+v, want related season episode 13 preview", result)
	}
	if graphqlRequests != 2 {
		t.Errorf("GraphQL requests = %d, want initial search plus one related-season retry", graphqlRequests)
	}
}

func TestProcessFileRetriesRelatedWorkAfterSubtitleMismatch(t *testing.T) {
	graphqlRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		graphqlRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","seasonName":"SPRING","seasonYear":1989,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":100,"title":"旧作"}}]}}},
			{"node":{"annictId":2,"title":"作品 (Netflixオリジナル)","seasonName":"AUTUMN","seasonYear":2023,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":201,"number":1,"sortNumber":100,"title":"新作"}}]}}}
		]}}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品(2023) #01「新作」.mp4")
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil || result.WorkTitle != "作品 (Netflixオリジナル)" || result.EpisodeNum != 1 || !result.Previewed {
		t.Fatalf("processFile() = %+v, want stronger related-work match", result)
	}
	if graphqlRequests != 2 {
		t.Errorf("GraphQL requests = %d, want initial search plus one related-work retry", graphqlRequests)
	}
}

func TestProcessFileNoEpisodeDoesNotContactAnnict(t *testing.T) {
	testProcessFileSkippedWithoutAnnict(t, "作品 総集編 (20260801).mp4", "no supported single episode number")
}

func TestProcessFileResolvesDateOnlyFromUniqueSyobocalSchedule(t *testing.T) {
	annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","syobocalTid":6373,"episodesCount":2,"episodes":{"edges":[
				{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"はじまり"}},
				{"node":{"annictId":102,"number":2,"sortNumber":2,"title":"つづき"}}
			]}}}
		]}}}`)
	}))
	defer annictServer.Close()

	syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("TID"); got != "6373" {
			t.Errorf("Syobocal TID = %q, want 6373", got)
		}
		fmt.Fprint(w, `<ProgLookupResponse><ProgItems><ProgItem><PID>10</PID><TID>6373</TID><StTime>2022-09-23 01:00:00</StTime><EdTime>2022-09-23 01:30:00</EdTime><Count>2</Count><Deleted>0</Deleted><Warn>0</Warn><ChID>5</ChID><STSubTitle>つづき</STSubTitle></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
	}))
	defer syobocalServer.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品 (20220923).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL+"/graphql")
	result := processFile(
		file,
		client,
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
		&processingContext{syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL)},
	)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 2 {
		t.Fatalf("processFile() = %+v, want date-only episode 2 preview", result)
	}
}

func TestProcessFileResolvesCompositeSubtitlePartFromUniqueDate(t *testing.T) {
	annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","syobocalTid":6373,"episodesCount":1,"episodes":{"edges":[
				{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"冬の訪れです。／不良です。"}}
			]}}}
		]}}}`)
	}))
	defer annictServer.Close()

	syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<ProgLookupResponse><ProgItems><ProgItem><PID>10</PID><TID>6373</TID><StTime>2022-04-07 01:00:00</StTime><EdTime>2022-04-07 01:30:00</EdTime><Count>1</Count><Deleted>0</Deleted><Warn>0</Warn><ChID>5</ChID><STSubTitle>冬の訪れです。</STSubTitle></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
	}))
	defer syobocalServer.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品「コミュ４４ 冬の訪れです。」 (20220407).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := processFile(
		file,
		annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL+"/graphql"),
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
		&processingContext{syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL)},
	)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 1 {
		t.Fatalf("processFile() = %+v, want date-proven composite subtitle episode 1", result)
	}
}

func TestProcessFileResolvesCompositeSubtitleInExactlyOneRelatedWork(t *testing.T) {
	graphqlRequests := 0
	annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		graphqlRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","syobocalTid":100,"episodesCount":1,"episodes":{"edges":[
				{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"冬の訪れです。／旧作です。"}}
			]}}},
			{"node":{"annictId":2,"title":"作品 第2期","syobocalTid":200,"episodesCount":1,"episodes":{"edges":[
				{"node":{"annictId":201,"number":1,"sortNumber":1,"title":"冬の訪れです。／不良です。"}}
			]}}}
		]}}}`)
	}))
	defer annictServer.Close()

	syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("TID") {
		case "100":
			fmt.Fprint(w, `<ProgLookupResponse><ProgItems></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
		case "200":
			fmt.Fprint(w, `<ProgLookupResponse><ProgItems><ProgItem><PID>20</PID><TID>200</TID><StTime>2022-04-07 01:00:00</StTime><EdTime>2022-04-07 01:30:00</EdTime><Count>1</Count><Deleted>0</Deleted><Warn>0</Warn><ChID>5</ChID><STSubTitle>冬の訪れです。</STSubTitle></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
		default:
			t.Errorf("unexpected Syobocal TID %q", r.URL.Query().Get("TID"))
		}
	}))
	defer syobocalServer.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品「冬の訪れです。」 (20220407).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := processFile(
		file,
		annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL+"/graphql"),
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
		&processingContext{syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL)},
	)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.WorkTitle != "作品 第2期" || result.EpisodeNum != 1 {
		t.Fatalf("processFile() = %+v, want uniquely date-proven related-work episode", result)
	}
	if graphqlRequests != 2 {
		t.Errorf("GraphQL requests = %d, want initial search plus related-work search", graphqlRequests)
	}
}

func TestProcessFileDateOnlyScheduleAmbiguityIsSafeSkip(t *testing.T) {
	annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","syobocalTid":6373,"episodesCount":2,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"title":"一"}},{"node":{"annictId":102,"number":2,"title":"二"}}]}}}]}}}`)
	}))
	defer annictServer.Close()
	syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<ProgLookupResponse><ProgItems><ProgItem><PID>1</PID><TID>6373</TID><StTime>2022-09-23 01:00:00</StTime><EdTime>2022-09-23 01:30:00</EdTime><Count>1</Count><ChID>5</ChID></ProgItem><ProgItem><PID>2</PID><TID>6373</TID><StTime>2022-09-23 02:00:00</StTime><EdTime>2022-09-23 02:30:00</EdTime><Count>2</Count><ChID>8</ChID></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
	}))
	defer syobocalServer.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "作品 (20220923).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := processFile(
		file,
		annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL),
		cache.NewDisabled(filepath.Join(dir, "cache")),
		make(map[string][]annict.Work),
		make(map[int][]annict.Episode),
		make(map[programsCacheKey][]annict.Program),
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
		&processingContext{syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL)},
	)
	if result.Error != nil || result.SkipReason == "" || result.Previewed || !strings.Contains(result.SkipReason, "ambiguous") {
		t.Fatalf("processFile() = %+v, want ambiguous date-only safe skip", result)
	}
}

func TestRefreshWorkMetadataUpgradesPreSyobocalCacheEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","syobocalTid":6373,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"title":"一"}}]}}}]}}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	if err := c.SetWork("作品", &annict.Work{ID: 1, Title: "作品"}); err != nil {
		t.Fatal(err)
	}
	wc := make(map[string][]annict.Work)
	ec := make(map[int][]annict.Episode)
	works, err := refreshWorkMetadata(annict.NewClientWithURLs("token", server.URL, server.URL), c, "作品", wc, ec)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].SyobocalTID != "6373" || len(ec[1]) != 1 {
		t.Fatalf("refreshWorkMetadata() works=%+v episodes=%+v", works, ec)
	}
	if cached, ok := c.GetWork("作品"); !ok || cached.SyobocalTID != "6373" {
		t.Fatalf("refreshed work was not persisted: %+v, %v", cached, ok)
	}
}

func TestProcessFileUsesTwoBatchAnchorsForAmbiguousDate(t *testing.T) {
	annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		episodes := make([]string, 8)
		for i := range episodes {
			number := i + 1
			episodes[i] = fmt.Sprintf(`{"node":{"annictId":%d,"number":%d,"sortNumber":%d,"title":""}}`, 100+number, number, number)
		}
		fmt.Fprintf(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","syobocalTid":6373,"episodesCount":8,"episodes":{"edges":[%s]}}}]}}}`, strings.Join(episodes, ","))
	}))
	defer annictServer.Close()

	syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts := map[string][2]int{
			"20220814_000000-20220815_000000": {6, 5},
			"20220821_000000-20220822_000000": {7, 6},
			"20220828_000000-20220829_000000": {8, 7},
		}
		pair, ok := counts[r.URL.Query().Get("Range")]
		if !ok {
			t.Fatalf("unexpected Range %q", r.URL.Query().Get("Range"))
		}
		date := r.URL.Query().Get("Range")[:8]
		formatted := date[:4] + "-" + date[4:6] + "-" + date[6:]
		fmt.Fprintf(w, `<ProgLookupResponse><ProgItems><ProgItem><PID>1</PID><TID>6373</TID><StTime>%s 02:00:00</StTime><EdTime>%s 02:30:00</EdTime><Count>%d</Count><ChID>16</ChID></ProgItem><ProgItem><PID>2</PID><TID>6373</TID><StTime>%s 02:42:00</StTime><EdTime>%s 03:12:00</EdTime><Count>%d</Count><ChID>79</ChID></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`, formatted, formatted, pair[0], formatted, formatted, pair[1])
	}))
	defer syobocalServer.Close()

	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "作品 第6話 (20220814).mp4"),
		filepath.Join(dir, "作品 第7話 (20220821).mp4"),
		filepath.Join(dir, "作品 (20220828).mp4"),
	}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	batch := newBatchScheduleContext()
	batch.targetTitles[normalize.NormalizeTitleForMatch("作品")] = true
	processing := &processingContext{
		syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL),
		batchSchedule:  batch,
	}
	client := annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL)
	c := cache.NewDisabled(filepath.Join(dir, "cache"))
	workCache := make(map[string][]annict.Work)
	episodesCache := make(map[int][]annict.Episode)
	programsCache := make(map[programsCacheKey][]annict.Program)
	plans := make(map[string]string)
	for _, file := range files[:2] {
		result := processFile(file, client, c, workCache, episodesCache, programsCache, plans, true, false, matcher.AutoRenameThreshold, "", processing)
		if result.Error != nil || result.SkipReason != "" || !result.Previewed {
			t.Fatalf("anchor processFile(%q) = %+v", file, result)
		}
	}
	if channelID, anchors, ok := batch.trustedChannel(1); !ok || channelID != 16 || anchors != 2 {
		t.Fatalf("trustedChannel() = %d, %d, %v; want 16, 2, true", channelID, anchors, ok)
	}
	result := processFile(files[2], client, c, workCache, episodesCache, programsCache, plans, true, false, matcher.AutoRenameThreshold, "", processing)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 8 {
		t.Fatalf("date-only processFile() = %+v, want episode 8 from trusted channel", result)
	}
}

func TestBatchScheduleContextRejectsConflictingOrDuplicateAnchors(t *testing.T) {
	batch := newBatchScheduleContext()
	batch.record(1, 16, "2022-08-14:6")
	batch.record(1, 16, "2022-08-14:6")
	if _, anchors, ok := batch.trustedChannel(1); ok || anchors != 0 {
		t.Fatalf("duplicate anchor unexpectedly trusted: anchors=%d ok=%v", anchors, ok)
	}
	batch.record(1, 16, "2022-08-21:7")
	if channel, anchors, ok := batch.trustedChannel(1); !ok || channel != 16 || anchors != 2 {
		t.Fatalf("two anchors not trusted: channel=%d anchors=%d ok=%v", channel, anchors, ok)
	}
	batch.record(1, 79, "2022-08-28:7")
	if _, _, ok := batch.trustedChannel(1); ok {
		t.Fatal("conflicting channel anchors unexpectedly trusted")
	}
}

func TestPrioritizeDateOnlyFiles(t *testing.T) {
	files := []string{
		"/recordings/作品 (20220828).mp4",
		"/recordings/作品 第7話 (20220821).mp4",
		"/recordings/作品 総集編 (20220822).mp4",
	}
	ordered, batch := prioritizeDateOnlyFiles(files)
	want := []string{files[1], files[2], files[0]}
	if !slices.Equal(ordered, want) {
		t.Fatalf("prioritizeDateOnlyFiles() = %v, want %v", ordered, want)
	}
	if !batch.wantsAnchors("作品") {
		t.Fatal("date-only work title was not registered for anchors")
	}
}

func TestProcessFileNoMeaningfulContentDoesNotContactAnnict(t *testing.T) {
	testProcessFileSkippedWithoutAnnict(t, "(2022_07_05).mp4", "no meaningful work title or episode")
}

func TestProcessFileAmbiguousEpisodeDoesNotContactAnnict(t *testing.T) {
	testProcessFileSkippedWithoutAnnict(t, "作品 #01,02「第一話 ／ 第二話」.mp4", "cannot represent as one positive integer episode")
}

func TestProcessFileEpisodeZeroDoesNotContactAnnict(t *testing.T) {
	testProcessFileSkippedWithoutAnnict(t, "作品 第0話「前日譚」.mp4", "cannot represent as one positive integer episode")
}

func testProcessFileSkippedWithoutAnnict(t *testing.T, baseName, wantReason string) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, baseName)
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
		make(map[string]string),
		true,
		false,
		matcher.AutoRenameThreshold,
		"",
	)
	if result.Error != nil {
		t.Fatalf("processFile() error = %v, want safe skip", result.Error)
	}
	if !strings.Contains(result.SkipReason, wantReason) {
		t.Errorf("processFile() SkipReason = %q, want substring %q", result.SkipReason, wantReason)
	}
	if requests != 0 {
		t.Errorf("Annict requests = %d, want 0 for a recording without an episode number", requests)
	}
}

func TestProcessFileDetectsBatchDestinationCollisionInDryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","seasonName":"SUMMER","seasonYear":2026,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":1,"sortNumber":1,"title":"第一話"}}]}}}
		]}}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	first := filepath.Join(dir, "作品 第1話「第一話」 (20260801).mp4")
	second := filepath.Join(dir, "作品 #1「第一話」 (20260802).mp4")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	client := annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql")
	c := cache.NewDisabled(filepath.Join(dir, "cache"))
	workCache := make(map[string][]annict.Work)
	episodesCache := make(map[int][]annict.Episode)
	programsCache := make(map[programsCacheKey][]annict.Program)
	plans := make(map[string]string)

	firstResult := processFile(first, client, c, workCache, episodesCache, programsCache, plans, true, false, matcher.AutoRenameThreshold, "")
	if firstResult.Error != nil {
		t.Fatalf("first processFile() error = %v", firstResult.Error)
	}
	secondResult := processFile(second, client, c, workCache, episodesCache, programsCache, plans, true, false, matcher.AutoRenameThreshold, "")
	if secondResult.Error == nil || !strings.Contains(secondResult.Error.Error(), "batch destination collision") {
		t.Fatalf("second processFile() error = %v, want batch destination collision", secondResult.Error)
	}
	if secondResult.NewPath != firstResult.NewPath {
		t.Errorf("collision paths differ: first=%q second=%q", firstResult.NewPath, secondResult.NewPath)
	}
	for _, file := range []string{first, second} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("dry-run changed source %s: %v", file, err)
		}
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
		case "/me/programs":
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
		make(map[string]string),
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
		case "/me/programs":
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
		make(map[string]string),
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
