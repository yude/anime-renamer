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
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/syobocal"
)

func TestSelectionRecoveryUsesOriginalBroadcastEpisode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"私を喰べたい、ひとでなし","syobocalTid":7561,"episodesCount":2,"episodes":{"edges":[
			{"node":{"annictId":101,"number":1,"title":"死にたがりの彼女は海を待つ"}},
			{"node":{"annictId":104,"number":4,"title":"泡沫の結び目"}}
		]}}}]}}}`)
	}))
	defer server.Close()
	date := time.Date(2026, 10, 11, 1, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	for _, tt := range []struct {
		name   string
		change func([]syobocal.Program) []syobocal.Program
		wantOK bool
	}{
		{name: "selection maps to story episode four", wantOK: true},
		{name: "wrong date", change: func(rows []syobocal.Program) []syobocal.Program {
			rows[0].StartedAt = date.AddDate(0, 0, 1)
			return rows
		}},
		{name: "conflicting channels", change: func(rows []syobocal.Program) []syobocal.Program {
			other := rows[0]
			other.ChannelID, other.Count, other.Subtitle = 128, 1, "死にたがりの彼女は海を待つ"
			return append(rows, other)
		}},
		{name: "unconfirmed warning", change: func(rows []syobocal.Program) []syobocal.Program {
			rows[0].Warn = true
			return rows
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := []syobocal.Program{{TID: 7561, ChannelID: 19, Count: 4, Subtitle: "泡沫の結び目", StartedAt: date}}
			if tt.change != nil {
				rows = tt.change(rows)
			}
			dir := t.TempDir()
			file := filepath.Join(dir, "「私を喰べたい、ひとでなし」セレクション放送 (20261011).mp4")
			if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			result := processFile(file, annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql"), cache.NewDisabled(dir),
				make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string),
				true, false, matcher.AutoRenameThreshold, "", &processingContext{syobocalClient: courProgramClient{programs: rows}})
			if tt.wantOK {
				if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 4 {
					t.Fatalf("processFile() = %+v, want original episode 4", result)
				}
			} else if result.Error != nil || result.SkipReason == "" || result.Previewed {
				t.Fatalf("processFile() = %+v, want safe skip", result)
			}
		})
	}
}
