package main

import (
	"errors"
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
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/syobocal"
)

type courProgramClient struct {
	programs []syobocal.Program
	err      error
}

func (c courProgramClient) GetPrograms(_ int, _ time.Time) ([]syobocal.Program, error) {
	return c.programs, c.err
}

func TestSharedCourSchedule(t *testing.T) {
	type fixture struct {
		meta     *parser.RecordingMetadata
		works    []annict.Work
		episodes map[int][]annict.Episode
		client   courProgramClient
	}
	for _, tt := range []struct {
		name   string
		change func(*fixture)
		wantOK bool
	}{
		{name: "exact continuous number and subtitle", wantOK: true},
		{name: "missing schedule subtitle", change: func(f *fixture) { f.client.programs[0].Subtitle = "" }},
		{name: "conflicting schedule subtitle", change: func(f *fixture) { f.client.programs[0].Subtitle = "別の話" }},
		{name: "warning only", change: func(f *fixture) { f.client.programs[0].Warn = true }},
		{name: "deleted row", change: func(f *fixture) { f.client.programs[0].Deleted = true }},
		{name: "different date", change: func(f *fixture) { f.client.programs[0].StartedAt = f.meta.RecordedDate.AddDate(0, 0, 1) }},
		{name: "unknown episode count", change: func(f *fixture) { f.works[1].EpisodesCount = 0 }},
		{name: "incomplete episode list", change: func(f *fixture) { f.works[1].EpisodesCount = 2 }},
		{name: "overlapping cour numbers", change: func(f *fixture) { f.episodes[1][0].Number = f.episodes[2][0].Number }},
		{name: "unrelated sequel", change: func(f *fixture) { f.works[1].Title = "作品 第2期" }},
		{name: "first cour is not a continuation", change: func(f *fixture) { f.works[1].Title = "作品 第1クール" }},
		{name: "multiple base works", change: func(f *fixture) { f.works[1].Title = "作品 (2026)" }},
		{name: "invalid TID", change: func(f *fixture) { f.works[1].SyobocalTID = "invalid" }},
		{name: "different explicit TID", change: func(f *fixture) { f.works[1].SyobocalTID = "200" }},
		{name: "base TID missing", change: func(f *fixture) { f.works[0].SyobocalTID = "" }},
		{name: "missing Annict subtitle", change: func(f *fixture) { f.episodes[2][0].Title = "" }},
		{name: "conflicting file subtitle", change: func(f *fixture) { f.meta.Subtitle = "別の話" }},
		{name: "ambiguous schedule", change: func(f *fixture) {
			row := f.client.programs[0]
			row.Count, row.Subtitle = 1, "第一話"
			f.client.programs = append(f.client.programs, row)
		}},
		{name: "one empty subtitle among agreeing rows", change: func(f *fixture) {
			row := f.client.programs[0]
			row.Subtitle = ""
			f.client.programs = append(f.client.programs, row)
		}},
		{name: "API failure", change: func(f *fixture) { f.client.err = errors.New("unavailable") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			one, two := 1.0, 2.0
			date := time.Date(2026, 10, 4, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
			f := fixture{
				meta: &parser.RecordingMetadata{WorkTitle: "作品", RecordedDate: date},
				works: []annict.Work{
					{ID: 1, Title: "作品", SyobocalTID: "100", EpisodesCount: 1},
					{ID: 2, Title: "作品 第2クール", EpisodesCount: 1},
				},
				episodes: map[int][]annict.Episode{
					1: {{ID: 101, Number: &one, Title: "第一話"}},
					2: {{ID: 201, Number: &two, Title: "第二話"}},
				},
				client: courProgramClient{programs: []syobocal.Program{{TID: 100, ChannelID: 71, Count: 2, Subtitle: "第二話", StartedAt: date.Add(23 * time.Hour).UTC()}}},
			}
			if tt.change != nil {
				tt.change(&f)
			}
			result, err := matchDateOnlyAcrossWorks(f.meta, f.works, f.episodes, cache.NewDisabled(t.TempDir()), f.client, nil)
			if !tt.wantOK {
				if result != nil || err == nil {
					t.Fatalf("result=%+v, error=%v; want safe rejection", result, err)
				}
				return
			}
			if err != nil || result == nil || result.Work.ID != 2 || result.Episode.ID != 201 || result.Confidence != matcher.AutoRenameThreshold {
				t.Fatalf("result=%+v, error=%v; want continuation episode", result, err)
			}
			if f.works[1].SyobocalTID != "" || f.episodes[2][0].WorkID != 0 {
				t.Fatal("recovery mutated original Annict metadata")
			}
		})
	}
}

func TestProcessFileSharedCourSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"刃牙道","syobocalTid":7943,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":101,"number":13,"title":"実戦屋"}}]}}},
			{"node":{"annictId":2,"title":"刃牙道 第2クール","episodesCount":1,"episodes":{"edges":[{"node":{"annictId":201,"number":14,"title":"ぴくる"}}]}}}
		]}}}`)
	}))
	defer server.Close()
	date := time.Date(2026, 10, 4, 23, 30, 0, 0, time.FixedZone("JST", 9*60*60))
	dir := t.TempDir()
	file := filepath.Join(dir, "アニメ 刃牙道 (20261004).mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := processFile(
		file,
		annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql"), cache.NewDisabled(dir),
		make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string),
		true, false, matcher.AutoRenameThreshold, "",
		&processingContext{syobocalClient: courProgramClient{programs: []syobocal.Program{{TID: 7943, ChannelID: 71, Count: 14, Subtitle: "ぴくる", StartedAt: date}}}},
	)
	if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.WorkTitle != "刃牙道 第2クール" || result.EpisodeNum != 14 {
		t.Fatalf("processFile() = %+v; want verified cour episode 14", result)
	}
}
