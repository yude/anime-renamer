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
	"github.com/yude/anime-renamer/internal/tsmeta"
)

func embeddedFixture() *tsmeta.Broadcast {
	start := time.Date(2026, 11, 12, 0, 30, 0, 0, time.FixedZone("JST", 9*60*60))
	return &tsmeta.Broadcast{NetworkID: 4, TransportID: 0x40d0, ServiceID: 141, EventID: 10,
		Name: "作品 第6話「秘密」", Channel: "BS日テレ", StartedAt: start, EndedAt: start.Add(30 * time.Minute)}
}

func TestMergeEmbeddedRecordingRejectsConflicts(t *testing.T) {
	for _, test := range []struct {
		name, file, title, description string
		wantErr                        bool
	}{
		{name: "missing episode and subtitle", file: "作品 (20261112).m2ts"},
		{name: "missing date", file: "作品 第6話「秘密」.ts"},
		{name: "matching complete name", file: "作品 第6話「秘密」 (20261112).m2ts"},
		{name: "unquoted broadcast subtitle", file: "作品 (20261112).m2ts", title: "作品 第6話 秘密"},
		{name: "explicit description header", file: "作品 (20261112).m2ts", title: "作品", description: "#6「秘密」\n第7話もお楽しみに"},
		{name: "bare description header retains name subtitle", file: "作品 (20261112).m2ts", description: "#6"},
		{name: "wrong work", file: "別作品 (20261112).m2ts", wantErr: true},
		{name: "wrong season", file: "作品 Season2 (20261112).m2ts", wantErr: true},
		{name: "wrong episode", file: "作品 第7話 (20261112).m2ts", wantErr: true},
		{name: "wrong subtitle", file: "作品 第6話「別の話」 (20261112).m2ts", wantErr: true},
		{name: "wrong date", file: "作品 (20261111).m2ts", wantErr: true},
		{name: "integer versus fraction", file: "作品 #6.5 (20261112).m2ts", wantErr: true},
		{name: "description contradicts name number", file: "作品 (20261112).m2ts", description: "#7「秘密」", wantErr: true},
		{name: "description contradicts name subtitle", file: "作品 (20261112).m2ts", description: "#6「別の話」", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta, err := parser.ParseFilename(test.file)
			if err != nil {
				t.Fatal(err)
			}
			b := embeddedFixture()
			if test.title != "" {
				b.Name = test.title
			}
			b.Description = test.description
			got, _, err := mergeEmbeddedRecording(meta, b)
			if test.wantErr {
				if err == nil {
					t.Fatalf("conflicting evidence accepted: %+v", got)
				}
			} else if err != nil || got.EpisodeNumber != 6 || got.Subtitle != "秘密" || got.RecordedDate.Format("20060102") != "20261112" {
				t.Fatalf("merge = %+v, %v", got, err)
			}
		})
	}
}

type embeddedScheduleFixture struct {
	programs []syobocal.Program
	channels []syobocal.Channel
	err      error
}

func (s embeddedScheduleFixture) GetPrograms(_ int, _ time.Time) ([]syobocal.Program, error) {
	return s.programs, s.err
}

func (s embeddedScheduleFixture) GetChannels() ([]syobocal.Channel, error) {
	return s.channels, s.err
}

func TestEmbeddedStationAndTimeDisambiguateSchedule(t *testing.T) {
	b := embeddedFixture()
	b.Name = "作品" // No episode in EIT: station and exact interval must prove it.
	for _, test := range []string{"valid", "wrong start", "wrong end", "wrong channel", "warned", "deleted", "missing subtitle", "API error", "duplicate channels"} {
		t.Run(test, func(t *testing.T) {
			meta, _ := parser.ParseFilename("作品 (20261112).m2ts")
			meta, embedded, err := mergeEmbeddedRecording(meta, b)
			if err != nil {
				t.Fatal(err)
			}
			number := float64(6)
			works := []annict.Work{{ID: 1, Title: "作品", SyobocalTID: "100"}}
			ec := map[int][]annict.Episode{1: {{ID: 106, Number: &number, Title: "秘密"}}}
			p := syobocal.Program{ChannelID: 71, Count: 6, StartedAt: b.StartedAt, EndedAt: b.EndedAt, Subtitle: "秘密"}
			fixture := embeddedScheduleFixture{channels: []syobocal.Channel{{ID: 71, Name: "ＢＳ日テレ"}}}
			switch test {
			case "wrong start":
				p.StartedAt = p.StartedAt.Add(time.Minute)
			case "wrong end":
				p.EndedAt = p.EndedAt.Add(time.Minute)
			case "wrong channel":
				p.ChannelID = 99
			case "warned":
				p.Warn = true
			case "deleted":
				p.Deleted = true
			case "missing subtitle":
				p.Subtitle = ""
			case "API error":
				fixture.err = errors.New("unavailable")
			case "duplicate channels":
				fixture.channels = append(fixture.channels, syobocal.Channel{ID: 72, Name: "BS日テレ"})
			}
			fixture.programs = []syobocal.Program{p, {ChannelID: 163, Count: 5, StartedAt: b.StartedAt, EndedAt: b.EndedAt, Subtitle: "別局の話"}}
			result, err := matchEmbeddedSchedule(meta, embedded, works, ec, cache.NewDisabled(t.TempDir()), &processingContext{syobocalClient: fixture})
			if test == "valid" {
				if err != nil || result.Episode.ID != 106 {
					t.Fatalf("match = %+v, %v", result, err)
				}
			} else if err == nil {
				t.Fatalf("unverified broadcast accepted: %+v", result)
			}
		})
	}
}

func TestProcessEmbeddedMetadataAndFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","syobocalTid":100,"episodesCount":1,"episodes":{"edges":[{"node":{"annictId":106,"number":6,"title":"秘密"}}]}}}]}}}`)
		} else {
			fmt.Fprint(w, `{"programs":[]}`)
		}
	}))
	defer server.Close()
	for _, test := range []struct {
		name, filename string
		readErr        error
		disabled       bool
		wantOK         bool
	}{
		{name: "enriches date-only m2ts", filename: "作品 (20261112).m2ts", wantOK: true},
		{name: "enriches missing date ts", filename: "作品 第6話.ts", wantOK: true},
		{name: "conflicting episode stops", filename: "作品 第7話 (20261112).m2ts"},
		{name: "conflicting subtitle stops", filename: "作品 第6話「別の話」 (20261112).m2ts"},
		{name: "unquoted conflicting subtitle stops", filename: "作品 第6話 別の話 (20261112).m2ts"},
		{name: "ambiguous programme stops", filename: "作品 第6話「秘密」 (20261112).m2ts", readErr: tsmeta.ErrAmbiguous},
		{name: "missing SI retains old match", filename: "作品 第6話「秘密」 (20261112).m2ts", readErr: tsmeta.ErrUnavailable, wantOK: true},
		{name: "mp4 never probed", filename: "作品 第6話「秘密」 (20261112).mp4", readErr: tsmeta.ErrAmbiguous, wantOK: true},
		{name: "disabled retains old match", filename: "作品 第6話「秘密」 (20261112).m2ts", disabled: true, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, test.filename)
			if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			reader := func(_ string) (*tsmeta.Broadcast, error) {
				if filepath.Ext(file) == ".mp4" {
					t.Fatal("probed a non-TS recording")
				}
				return embeddedFixture(), test.readErr
			}
			if test.disabled {
				reader = nil
			}
			result := processFile(file, annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql"), cache.NewDisabled(dir),
				make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string), true, false, matcher.AutoRenameThreshold, "",
				&processingContext{tsMetadataReader: reader})
			if test.wantOK {
				if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 6 {
					t.Fatalf("processFile() = %+v", result)
				}
			} else if result.Error != nil || result.SkipReason == "" || result.Previewed {
				t.Fatalf("processFile() = %+v, want safe skip", result)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "recording" {
				t.Fatal("source was changed in dry-run")
			}
		})
	}
}

func TestEmbeddedEpisodeRequiresExplicitUniqueAnnictIdentity(t *testing.T) {
	for _, test := range []string{"valid", "fractional", "wrong Annict subtitle", "empty Annict subtitle", "duplicate labels", "sort ordinal", "cour offset"} {
		t.Run(test, func(t *testing.T) {
			b := embeddedFixture()
			number := float64(6)
			episode := annict.Episode{ID: 106, Number: &number, Title: "秘密"}
			if test == "fractional" {
				b.Name = "作品 #6.5「秘密」"
				episode.NumberText = "第6.5話"
			}
			if test == "wrong Annict subtitle" {
				episode.Title = "別の話"
			}
			if test == "empty Annict subtitle" {
				episode.Title = ""
			}
			if test == "sort ordinal" {
				episode.Number, episode.SortNumber = nil, 6
			}
			if test == "cour offset" {
				number = 1
			}
			meta, _ := parser.ParseFilename("作品 (20261112).m2ts")
			meta, embedded, err := mergeEmbeddedRecording(meta, b)
			if err != nil {
				t.Fatal(err)
			}
			work := annict.Work{ID: 1, Title: "作品"}
			ec := map[int][]annict.Episode{1: {episode}}
			if test == "duplicate labels" {
				duplicate := episode
				duplicate.ID++
				ec[1] = append(ec[1], duplicate)
			}
			result := &matcher.MatchResult{Work: &work, Episode: &episode, OutputEpisodeNumber: 6}
			if test == "fractional" {
				result.OutputEpisodeNumber, result.OutputEpisodeLabel = 0, "6.5"
			}
			err = verifyEmbeddedMatch(meta, embedded, result, []annict.Work{work}, ec, cache.NewDisabled(t.TempDir()), nil)
			if (test == "valid" || test == "fractional") != (err == nil) {
				t.Fatalf("verifyEmbeddedMatch() = %v", err)
			}
		})
	}
}
