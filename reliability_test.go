package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
)

func TestUnfetchedWorkCandidateCannotBecomeNegativeEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","episodesCount":1,"episodes":{"edges":[{"node":{"annictId":11,"number":1,"title":"はじまり"}}]}}},{"node":{"annictId":2,"title":"作品","episodesCount":1,"episodes":{"edges":[]}}}]}}}`)
			return
		}
		http.Error(w, "cannot verify second candidate", 400)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "作品 #1 「はじまり」.mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := processFile(file, annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql"), cache.NewDisabled(t.TempDir()), make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string), false, false, 90, "")
	if r.Error == nil || !strings.Contains(r.Error.Error(), "cannot verify all work candidates") || r.Renamed {
		t.Fatalf("accepted incomplete evidence: %+v", r)
	}
	if data, _ := os.ReadFile(file); string(data) != "recording" {
		t.Fatal("unverified source was moved or modified")
	}
}

func TestRelatedEpisodeFailureDoesNotPoisonMemoryCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":2,"title":"作品 第2期","episodesCount":1,"episodes":{"edges":[]}}}]}}}`)
			return
		}
		requests++
		if requests == 1 {
			http.Error(w, "temporary verification failure", 400)
			return
		}
		fmt.Fprint(w, `{"episodes":[{"id":21,"number":1,"title":"はじまり"}]}`)
	}))
	defer server.Close()
	client := annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql")
	wc, ec := make(map[string][]annict.Work), make(map[int][]annict.Episode)
	c := cache.NewDisabled(t.TempDir())
	if _, err := searchRelatedWorks(client, c, "作品", wc, ec); err == nil {
		t.Fatal("first lookup must fail")
	}
	works, err := searchRelatedWorks(client, c, "作品", wc, ec)
	if err != nil || len(works) != 1 || len(ec[2]) != 1 || requests != 2 {
		t.Fatalf("failed lookup was cached: %+v, %v, requests=%d", works, err, requests)
	}
}

func TestDotenvRelativeTargetSearchesActualParents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ANNICT_ACCESS_TOKEN=parent-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got := loadTokenFromDotenv("recording.mp4"); got != "parent-token" {
		t.Fatalf("relative target token=%q", got)
	}
}
