package main

import (
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
)

func TestFractionalContentsOnlyReadDedicatedFields(t *testing.T) {
	for _, tt := range []struct {
		name, extended string
		raw            map[string]string
		number         int
		want           bool
		wantErr        bool
	}{
		{name: "dedicated display field", extended: "◇今回の番組内容\n＃3.5\n◇出演者\n誰か", want: true},
		{name: "structured field", raw: map[string]string{"番組内容": "#3.5"}, want: true},
		{name: "synopsis reference", extended: "◇今回の番組内容\n第3話の続き。#3.5の内容も紹介。"},
		{name: "cast is not an episode", extended: "◇出演者\n#3.5"},
		{name: "announcement is not an episode", raw: map[string]string{"おしらせ": "#3.5"}},
		{name: "multiple labels", extended: "◇今回の番組内容\n#3.5 #4.5", wantErr: true},
		{name: "different fields conflict", extended: "◇今回の番組内容\n#3.5", raw: map[string]string{"番組内容": "#4.5"}, wantErr: true},
		{name: "name's episode contradicts field", extended: "◇今回の番組内容\n#3.5", number: 4, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := fractionalRecordingContents(&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: tt.number}, &epgstation.Recording{Extended: tt.extended, RawExtended: tt.raw})
			if (err != nil) != tt.wantErr || (!tt.wantErr && (found != tt.want || (found && got.EpisodeLabel != "3.5"))) {
				t.Fatalf("fractionalRecordingContents() = %+v, %v, %v", got, found, err)
			}
		})
	}
}

func TestVerifiedRecordingSpecialRejectsUnverifiedVariations(t *testing.T) {
	for _, spec := range verifiedRecordingSpecials {
		for _, change := range []string{"valid", "wrong work", "wrong media", "wrong channel", "wrong start", "wrong end", "wrong label", "wrong subtitle"} {
			t.Run(spec.media+"/"+change, func(t *testing.T) {
				start, _ := time.Parse(time.RFC3339, spec.start)
				end, _ := time.Parse(time.RFC3339, spec.end)
				meta := &parser.RecordingMetadata{WorkTitle: spec.title, EpisodeLabel: spec.label, RecordedDate: start}
				record := &epgstation.Recording{ID: 1, ChannelID: 400141, StartAt: start.UnixMilli(), EndAt: end.UnixMilli(), Name: spec.title}
				work := annict.Work{ID: spec.workID, Title: spec.title, Media: spec.media}
				channel := &epgstation.Channel{ID: 400141, Name: spec.channel}
				switch change {
				case "wrong work":
					work.ID++
				case "wrong media":
					work.Media = "unknown"
				case "wrong channel":
					channel.Name = "別局"
				case "wrong start":
					record.StartAt += 60_000
				case "wrong end":
					record.EndAt += 60_000
				case "wrong label":
					meta.EpisodeLabel = "4.5"
				case "wrong subtitle":
					meta.Subtitle = "別内容"
				}
				result, err := matchVerifiedRecordingSpecial(meta, record, work, &processingContext{epgstationClient: historyFixture{record: record, channel: channel}})
				if change == "valid" {
					if err != nil || result == nil || result.Episode != nil || (spec.media == "movie") != result.Standalone {
						t.Fatalf("result=%+v, err=%v", result, err)
					}
				} else if result != nil || err == nil {
					t.Fatalf("unverified variation accepted: %+v, %v", result, err)
				}
			})
		}
	}
}

func TestProcessFractionalAndStandaloneRecording(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
		{"node":{"annictId":17367,"title":"千歳くんはラムネ瓶のなか 第2クール","media":"TV","episodesCount":1,"episodes":{"edges":[{"node":{"annictId":183330,"number":13,"numberText":"第13.5話","title":"特別総集編","sortNumber":1350}}]}}},
		{"node":{"annictId":17354,"title":"死亡遊戯で飯を食う。 44:CLOUDY BEACH","media":"MOVIE","episodesCount":0,"episodes":{"edges":[]}}}
		]}}}`)
	}))
	defer server.Close()
	for _, tt := range []struct {
		filename, want string
		movie          bool
	}{
		{"千歳くんはラムネ瓶のなか 第2クール 第13．5話「特別総集編」 (20261007).mp4", "#13.5", false},
		{"死亡遊戯で飯を食う。44：CLOUDY BEACH (20260928).mp4", "死亡遊戯で飯を食う。 44：CLOUDY BEACH.mp4", true},
	} {
		t.Run(tt.filename, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, tt.filename)
			if err := os.WriteFile(file, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			var runtime *processingContext
			if tt.movie {
				runtime = &processingContext{epgstationClient: historyFixture{record: &epgstation.Recording{ID: 5786, ChannelID: 400141, StartAt: 1790605800000, EndAt: 1790611200000, Name: "死亡遊戯で飯を食う。44:CLOUDY BEACH"}, channel: &epgstation.Channel{ID: 400141, Name: "ＢＳ日テレ"}}}
			}
			result := processFile(file, annict.NewClientWithURLs("token", server.URL, server.URL+"/graphql"), cache.NewDisabled(dir), make(map[string][]annict.Work), make(map[int][]annict.Episode), make(map[programsCacheKey][]annict.Program), make(map[string]string), true, false, matcher.AutoRenameThreshold, "", runtime)
			if result.Error != nil || result.SkipReason != "" || !result.Previewed || !strings.Contains(filepath.Base(result.NewPath), tt.want) {
				t.Fatalf("processFile()=%+v", result)
			}
		})
	}
}
