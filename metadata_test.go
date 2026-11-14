package main

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/metadata"
	"github.com/yude/anime-renamer/internal/renamer"
)

func metadataFixture(t *testing.T, file string, only, dryRun bool) *renamer.RenameResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"searchWorks":{"edges":[{"node":{"annictId":12,"title":"作品","episodesCount":1,"episodes":{"edges":[{"node":{"annictId":34,"number":1,"numberText":"第1話","sortNumber":100,"title":"はじまり"}}]}}}]}}}`))
	}))
	defer server.Close()
	r := processFile(file, annict.NewClientWithURLs("token", server.URL, server.URL), cache.NewDisabled(t.TempDir()), make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string), dryRun, false, matcher.AutoRenameThreshold, "", &processingContext{metadata: &metadataOptions{mode: "nfo", only: only, season: 1}})
	return r
}

func TestMetadataOnlyPreservesVideoNameContentAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "作品 #1 「はじまり」.mp4")
	original := []byte("recording bytes")
	if err := os.WriteFile(file, original, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(file)
	r := metadataFixture(t, file, true, false)
	if r.Error != nil || r.SkipReason != "" || r.Renamed || r.NewPath != file || r.MetadataWritten != 1 {
		t.Fatalf("metadata-only: %+v", r)
	}
	after, _ := os.Stat(file)
	if data, _ := os.ReadFile(file); !bytes.Equal(data, original) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("metadata-only changed recording")
	}
	if _, err := os.Stat(filepath.Join(dir, "tvshow.nfo")); !os.IsNotExist(err) {
		t.Fatal("shared input directory must not receive one work's tvshow.nfo")
	}
	var nfo struct {
		Title   string `xml:"title"`
		Episode int    `xml:"episode"`
		Season  int    `xml:"season"`
	}
	data, _ := os.ReadFile(metadata.SidecarPath(file))
	if err := xml.Unmarshal(data, &nfo); err != nil || nfo.Title != "はじまり" || nfo.Episode != 1 || nfo.Season != 1 {
		t.Fatalf("NFO: %s (%v)", data, err)
	}
}

func TestRenameWithMetadataAndDryRun(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry-run", false: "write"}[dryRun], func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "作品 第1話 はじまり.mp4")
			if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			r := metadataFixture(t, file, false, dryRun)
			if r.Error != nil || r.SkipReason != "" {
				t.Fatalf("rename with NFO: %+v", r)
			}
			if dryRun {
				if r.MetadataPreviewed != 2 {
					t.Fatalf("previewed %d NFOs, want episode and show", r.MetadataPreviewed)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 1 {
					t.Fatal("dry-run created files or directories")
				}
			} else {
				if r.MetadataWritten != 2 || !r.Renamed {
					t.Fatalf("write: %+v", r)
				}
				if _, err := os.Stat(metadata.SidecarPath(r.NewPath)); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(r.NewPath), "tvshow.nfo")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMetadataBackupsExcludedFromRecursiveCollection(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, metadata.BackupDirectory)
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "do-not-process.mp4"), []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := collectFiles(dir, true)
	if err != nil || len(files) != 0 {
		t.Fatalf("collected backups: %v (%v)", files, err)
	}
}

func TestMetadataAutoFallbackAfterRemuxFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a shell script")
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(err)
		}
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.mp4")
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:r=2", "-t", "1", "-c:v", "mpeg4", fixture)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v: %s", err, data)
	}
	original, _ := os.ReadFile(fixture)
	tools := filepath.Join(dir, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	number := 1.0
	match := &matcher.MatchResult{Work: &annict.Work{ID: 12, Title: "作品"}, Episode: &annict.Episode{ID: 34, Number: &number, Title: "はじまり"}, Confidence: 90}
	for _, mode := range []string{"auto", "embedded"} {
		t.Run(mode, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "作品 #1.mp4")
			if err := os.WriteFile(file, original, 0o644); err != nil {
				t.Fatal(err)
			}
			r := processMatchedMetadata(file, match, false, "", nil, &metadataOptions{mode: mode, only: true, season: 1})
			if mode == "auto" && (r.Error != nil || r.MetadataWritten != 1) {
				t.Fatalf("automatic fallback failed: %+v", r)
			}
			if mode == "embedded" && (r.Error == nil || r.MetadataWritten != 0) {
				t.Fatalf("explicit embedded mode should report failure: %+v", r)
			}
			if data, _ := os.ReadFile(file); !bytes.Equal(data, original) {
				t.Fatal("failed remux modified recording")
			}
			if _, err := os.Stat(metadata.BackupPath(file)); !os.IsNotExist(err) {
				t.Fatal("failed remux created backup")
			}
			if files, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".anime-renamer-*.tmp")); len(files) != 0 {
				t.Fatal("failed remux left temporary media")
			}
		})
	}
}

func TestExistingNFOPreventsOrphanedMetadataOnRename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "作品 #1.mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata.SidecarPath(file), []byte("manual metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := metadataFixture(t, file, false, false)
	if r.Error != nil || r.SkipReason == "" || r.Renamed {
		t.Fatalf("existing NFO should prevent moving its video: %+v", r)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("video moved away from its original sidecar")
	}
}
