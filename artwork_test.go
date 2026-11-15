package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/artwork"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/metadata"
)

type artworkWorkFixture struct{ calls int }

func (f *artworkWorkFixture) GetArtwork(context.Context, int) (*annict.ArtworkWork, error) {
	f.calls++
	return &annict.ArtworkWork{ID: 42, Title: "作品", SeasonName: "2026-winter"}, nil
}

func TestArtworkDiscoveryUsesWorkIDsAndSkipsUnknownFolders(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"作品", "作品/duplicate", "IDなし", metadata.BackupDirectory} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, folder, "recording.mp4"), []byte("unchanged video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "作品", "tvshow.nfo"), []byte(`<tvshow><uniqueid type="annict">42</uniqueid></tvshow>`), 0o600); err != nil {
		t.Fatal(err)
	}
	// An episode's Annict ID must never become the show's ID.
	if err := os.WriteFile(filepath.Join(root, "IDなし", "recording.nfo"), []byte(`<episodedetails><uniqueid type="annict">42</uniqueid></episodedetails>`), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs, err := collectArtworkJobs(root, true)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("discovered jobs: %+v, %v", jobs, err)
	}
	fixture := &artworkWorkFixture{}
	if failed := runArtworkJobs(context.Background(), jobs, fixture, true, 0); failed != 0 || fixture.calls != 1 {
		t.Fatalf("unknown folder was looked up or known folder was repeated: %d, calls=%d", failed, fixture.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "作品", "poster.png")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote artwork")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "作品", "recording.mp4")); string(data) != "unchanged video" {
		t.Fatal("artwork discovery changed a recording")
	}
}

func TestArtworkQueuesOnceAndRejectsMixedWorkFolder(t *testing.T) {
	dir := t.TempDir()
	jobs := make(artworkJobs)
	for _, id := range []int{42, 42, 43} {
		jobs.add(dir, id)
	}
	fixture := &artworkWorkFixture{}
	if len(jobs) != 1 || runArtworkJobs(context.Background(), jobs, fixture, false, 0) != 0 || fixture.calls != 0 {
		t.Fatal("mixed work folder was processed")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("mixed work folder received an identity or image")
	}
}

func TestRenameArtworkIsQueuedAfterSuccessfulMatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "作品 #1 「始まり」.mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	number := 1.0
	match := &matcher.MatchResult{Work: &annict.Work{ID: 42, Title: "作品"}, Episode: &annict.Episode{ID: 421, Number: &number, Title: "始まり"}, Confidence: 90}
	jobs := make(artworkJobs)
	result := renameMatchedRecording(file, match, true, "", make(map[string]string), &processingContext{artwork: jobs})
	if result.Error != nil || !result.Previewed || len(jobs) != 1 {
		t.Fatalf("verified rename was not queued for artwork: %+v, jobs=%v", result, jobs)
	}
	for _, job := range jobs {
		if !job.workIDs[42] || job.dir != filepath.Join(dir, "作品") {
			t.Fatalf("wrong artwork identity or folder: %+v", job)
		}
	}
	match.Confidence = 80
	lowConfidence := make(artworkJobs)
	lowConfidence.matched(result.NewPath, match)
	if len(lowConfidence) != 0 {
		t.Fatal("low confidence match generated artwork")
	}
}

func TestArtworkDiscoveryReportsInvalidNFOAndDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	nfo := filepath.Join(dir, "tvshow.nfo")
	if err := os.WriteFile(nfo, []byte(`<tvshow><uniqueid type="annict">42</uniqueid><uniqueid type="annict">43</uniqueid></tvshow>`), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs, err := collectArtworkJobs(nfo, false)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("invalid NFO discovery: %v", err)
	}
	fixture := &artworkWorkFixture{}
	if failed := runArtworkJobs(context.Background(), jobs, fixture, false, 0); failed != 1 || fixture.calls != 0 {
		t.Fatal("looked up a conflicting work ID")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := collectArtworkJobs(link, true); err == nil {
		t.Fatal("followed an artwork target symlink")
	}
	if _, err := os.Stat(filepath.Join(dir, artwork.IdentityFile)); !os.IsNotExist(err) {
		t.Fatal("invalid work received artwork identity")
	}
}
