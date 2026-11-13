package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/epgstation"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/syobocal"
)

type historyFixture struct {
	record  *epgstation.Recording
	channel *epgstation.Channel
	err     error
}

func (f historyFixture) FindRecording(_, _ string, _ time.Time) (*epgstation.Recording, error) {
	return f.record, f.err
}

func (f historyFixture) GetChannel(_ int64) (*epgstation.Channel, error) {
	return f.channel, f.err
}

func TestRecordingMetadataOnlyReadsExplicitHeaders(t *testing.T) {
	meta := &parser.RecordingMetadata{WorkTitle: "作品", RecordedDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))}
	for _, tt := range []struct {
		description, title, subtitle string
		label                        string
		number                       int
		wantErr                      bool
	}{
		{description: "第４話　秘密", title: "作品", subtitle: "秘密", number: 4},
		{description: "Chapter.６「秘密」", title: "作品", subtitle: "秘密", number: 6},
		{description: "〜副題〜　＃06「秘密」", title: "作品 〜副題〜", subtitle: "秘密", number: 6},
		{description: "第七話　秘密", title: "作品", subtitle: "秘密", number: 7},
		{description: "前回の第4話から物語が動く。", title: "作品"},
		{description: "番組内容\n第4話「別作品の紹介」", title: "作品"},
		{description: "第4．5話「総集編」", title: "作品", subtitle: "総集編", label: "4.5"},
		{description: "#4,5「結合」", wantErr: true},
		{description: "第4話／第5話", wantErr: true},
		{description: "第0話「前日譚」", wantErr: true},
	} {
		t.Run(tt.description, func(t *testing.T) {
			got, err := recordingMetadata(meta, &epgstation.Recording{Name: "作品", Description: tt.description})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("recordingMetadata() = %+v, want rejection", got)
				}
			} else if err != nil || got.WorkTitle != tt.title || got.Subtitle != tt.subtitle || got.EpisodeNumber != tt.number || got.EpisodeLabel != tt.label {
				t.Fatalf("recordingMetadata() = %+v, %v; want title=%q subtitle=%q number=%d", got, err, tt.title, tt.subtitle, tt.number)
			}
		})
	}
	if _, err := recordingMetadata(meta, &epgstation.Recording{Name: "別作品", Description: "第4話「秘密」"}); err == nil {
		t.Fatal("conflicting program title was accepted")
	}
}

func TestProcessFileEPGStationRecovery(t *testing.T) {
	for _, tt := range []struct {
		name        string
		description string
		channel     string
		startOffset time.Duration
		endOffset   time.Duration
		warn        bool
		lookupErr   bool
		noRecord    bool
		catalogue   string
		wantOK      bool
	}{
		{name: "exact filename channel and time disambiguate episodes", description: "第４話　秘密", wantOK: true},
		{name: "history channel without an episode header", description: "作品紹介", wantOK: true},
		{name: "warning corroborated by explicit episode and subtitle", description: "第４話　秘密", warn: true, wantOK: true},
		{name: "warning lacks independent header", description: "作品紹介", warn: true},
		{name: "warning lacks subtitle", description: "第４話", warn: true},
		{name: "EPG episode contradicts schedule", description: "第３話　秘密"},
		{name: "EPG subtitle contradicts Annict", description: "第４話　別の話"},
		{name: "wrong channel", description: "第４話　秘密", channel: "未確認局"},
		{name: "wrong start time", description: "第４話　秘密", startOffset: time.Minute},
		{name: "wrong end time", description: "第４話　秘密", endOffset: time.Minute},
		{name: "history API failed", lookupErr: true},
		{name: "source recording missing", noRecord: true},
		{name: "catalogue API failed", catalogue: "failure"},
		{name: "catalogue has ambiguous names", catalogue: "ambiguous"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			annictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/graphql" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[{"node":{"annictId":1,"title":"作品","syobocalTid":100,"episodesCount":2,"episodes":{"edges":[{"node":{"annictId":103,"number":3,"title":"別局の回"}},{"node":{"annictId":104,"number":4,"title":"秘密"}}]}}}]}}}`)
			}))
			defer annictServer.Close()
			syobocalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("Command") == "ChLookup" {
					if tt.catalogue == "failure" {
						http.Error(w, "unavailable", 503)
						return
					}
					extra := ""
					if tt.catalogue == "ambiguous" {
						extra = `<ChItem><ChID>72</ChID><ChName>ＢＳ日テレ</ChName></ChItem>`
					}
					fmt.Fprintf(w, `<ChLookupResponse><ChItems><ChItem><ChID>71</ChID><ChName>BS日テレ</ChName></ChItem>%s</ChItems><Result><Code>200</Code></Result></ChLookupResponse>`, extra)
					return
				}
				warn := 0
				if tt.warn {
					warn = 1
				}
				fmt.Fprintf(w, `<ProgLookupResponse><ProgItems>
					<ProgItem><TID>100</TID><ChID>71</ChID><Count>4</Count><StTime>2026-11-01 00:00:00</StTime><EdTime>2026-11-01 00:30:00</EdTime><STSubTitle>秘密</STSubTitle><Warn>%d</Warn></ProgItem>
					<ProgItem><TID>100</TID><ChID>163</ChID><Count>3</Count><StTime>2026-11-01 00:00:00</StTime><EdTime>2026-11-01 00:30:00</EdTime><STSubTitle>別局の回</STSubTitle><Warn>%d</Warn></ProgItem>
					</ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`, warn, warn)
			}))
			defer syobocalServer.Close()
			date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
			history := historyFixture{
				record:  &epgstation.Recording{ID: 6067, ChannelID: 400141, StartAt: date.Add(tt.startOffset).UnixMilli(), EndAt: date.Add(30*time.Minute + tt.endOffset).UnixMilli(), Name: "作品", Description: tt.description},
				channel: &epgstation.Channel{ID: 400141, Name: "ＢＳ日テレ"},
			}
			if tt.channel != "" {
				history.channel.Name = tt.channel
			}
			if tt.lookupErr {
				history.err = errors.New("unavailable")
			}
			if tt.noRecord {
				history.record = nil
			}
			dir := t.TempDir()
			file := filepath.Join(dir, "作品 (20261101).mp4")
			if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			result := processFile(file, annict.NewClientWithURLs("token", annictServer.URL, annictServer.URL+"/graphql"), cache.NewDisabled(dir),
				make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string), true, false, matcher.AutoRenameThreshold, "",
				&processingContext{epgstationClient: history, syobocalClient: syobocal.NewClientWithBaseURL(syobocalServer.URL)})
			if tt.wantOK {
				if result.Error != nil || result.SkipReason != "" || !result.Previewed || result.EpisodeNum != 4 {
					t.Fatalf("processFile() = %+v, want recorded episode 4", result)
				}
			} else if result.Error != nil || result.SkipReason == "" || result.Previewed {
				t.Fatalf("processFile() = %+v, want safe skip", result)
			}
		})
	}
}

func TestEPGStationConfigFromDotenv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ANNICT_ACCESS_TOKEN=token\nexport EPGSTATION_URL='https://example.test/epg/'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadEnvFromDotenv(dir, "EPGSTATION_URL"); got != "https://example.test/epg/" {
		t.Fatalf("EPGSTATION_URL = %q", got)
	}
	if got := loadTokenFromDotenv(dir); got != "token" {
		t.Fatalf("ANNICT_ACCESS_TOKEN = %q", got)
	}
	if got := loadEnvFromDotenv(dir, "MISSING"); strings.TrimSpace(got) != "" {
		t.Fatalf("missing variable = %q", got)
	}
}

type historyScheduleFixture struct {
	courProgramClient
}

func (historyScheduleFixture) GetChannels() ([]syobocal.Channel, error) {
	return []syobocal.Channel{{ID: 128, Name: "BS11イレブン"}}, nil
}

func TestEPGDescriptionQualifierDisambiguatesWork(t *testing.T) {
	date := time.Date(2026, 10, 5, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	meta := &parser.RecordingMetadata{WorkTitle: "作品", RecordedDate: date}
	number := 1.0
	works := []annict.Work{
		{ID: 1, Title: "作品 ～副題～", SyobocalTID: "100", EpisodesCount: 1},
		{ID: 2, Title: "作品 ～副題～ TVSP"},
		{ID: 3, Title: "作品 ～副題～ 2nd Season"},
	}
	for _, description := range []string{"〜副題〜　＃01「出会い」", "＃01「出会い」"} {
		t.Run(description, func(t *testing.T) {
			record := &epgstation.Recording{ID: 5824, ChannelID: 400211, Name: "作品", StartAt: date.UnixMilli(), EndAt: date.Add(30 * time.Minute).UnixMilli(), Description: description}
			runtime := &processingContext{
				epgstationClient: historyFixture{record: record, channel: &epgstation.Channel{ID: 400211, Name: "ＢＳ１１イレブン"}},
				syobocalClient:   historyScheduleFixture{courProgramClient{programs: []syobocal.Program{{TID: 100, ChannelID: 128, Count: 1, Subtitle: "出会い", StartedAt: date, EndedAt: date.Add(30 * time.Minute), Warn: true}}}},
			}
			wc := map[string][]annict.Work{relatedWorkCachePrefix + "作品 〜副題〜": works, relatedWorkCachePrefix + "作品": works}
			ec := map[int][]annict.Episode{1: {{ID: 101, Number: &number, Title: "出会い"}}}
			result, err := matchEPGStationRecording("作品 (20261005).mp4", meta, nil, cache.NewDisabled(t.TempDir()), wc, ec, runtime)
			if strings.HasPrefix(description, "〜副題〜") {
				if err != nil || result == nil || result.Work.ID != 1 || result.Episode.ID != 101 {
					t.Fatalf("matchEPGStationRecording() = %+v, %v, want exact qualified work", result, err)
				}
			} else if result != nil || err == nil {
				t.Fatalf("unqualified ambiguous title was accepted: %+v, %v", result, err)
			}
		})
	}
}
