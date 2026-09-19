package dateinfer

import (
	"strings"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/syobocal"
)

func number(value float64) *float64 { return &value }

func TestResolveUnique(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 9, 23, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{
		{ID: 101, Number: number(11), Title: "伝えたいので"},
		{ID: 102, Number: number(12), Title: "勝って伝えたいので"},
	}
	programs := []syobocal.Program{
		{PID: 1, Count: 12, ChannelID: 5, StartedAt: date.Add(time.Hour), Subtitle: "勝って伝えたいので"},
		{PID: 2, Count: 12, ChannelID: 8, StartedAt: date.Add(2 * time.Hour), Subtitle: "勝って伝えたいので"},
		{PID: 3, Count: 11, ChannelID: 9, StartedAt: date.Add(3 * time.Hour), Warn: true},
	}

	episode, reason := ResolveUnique(date, episodes, programs)
	if episode == nil || episode.ID != 102 {
		t.Fatalf("ResolveUnique() = %+v, %q; want episode 102", episode, reason)
	}
}

func TestResolveUniqueFailsClosed(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 9, 23, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{
		{ID: 101, Number: number(11), Title: "第十一話"},
		{ID: 102, Number: number(12), Title: "第十二話"},
	}
	base := syobocal.Program{PID: 1, Count: 12, StartedAt: date.Add(time.Hour), Subtitle: "第十二話"}

	tests := []struct {
		name     string
		programs []syobocal.Program
		episodes []annict.Episode
		want     string
	}{
		{name: "no rows", want: "no usable schedule"},
		{name: "deleted only", programs: []syobocal.Program{{Count: 12, StartedAt: date.Add(time.Hour), Deleted: true}}, want: "no usable schedule"},
		{name: "previous day overlap", programs: []syobocal.Program{{Count: 12, StartedAt: date.Add(-time.Minute)}}, want: "no usable schedule"},
		{name: "different counts", programs: []syobocal.Program{base, {PID: 2, Count: 11, StartedAt: date.Add(2 * time.Hour)}}, want: "ambiguous"},
		{name: "missing Annict episode", programs: []syobocal.Program{base}, episodes: episodes[:1], want: "maps to 0"},
		{name: "duplicate Annict number", programs: []syobocal.Program{base}, episodes: []annict.Episode{episodes[1], {ID: 999, Number: number(12)}}, want: "maps to 2"},
		{name: "subtitle conflict", programs: []syobocal.Program{{PID: 1, Count: 12, StartedAt: date.Add(time.Hour), Subtitle: "別の話"}}, episodes: episodes, want: "conflicts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidateEpisodes := tt.episodes
			if candidateEpisodes == nil {
				candidateEpisodes = episodes
			}
			episode, reason := ResolveUnique(date, candidateEpisodes, tt.programs)
			if episode != nil || !strings.Contains(reason, tt.want) {
				t.Fatalf("ResolveUnique() = %+v, %q; want nil and substring %q", episode, reason, tt.want)
			}
		})
	}
}

func TestResolveCorroboratedWarnedWithSubtitle(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 7, 24, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{{ID: 113, Number: number(13), Title: "なかなかうまくいかないねぇ"}}
	programs := []syobocal.Program{
		{PID: 1, Count: 13, ChannelID: 6, StartedAt: date.Add(time.Hour), Subtitle: "なかなかうまくいかないねぇ", Warn: true},
		{PID: 2, Count: 13, ChannelID: 67, StartedAt: date.Add(2 * time.Hour), Subtitle: "なかなかうまくいかないねぇ", Warn: true},
	}

	episode, reason := ResolveCorroboratedWarnedWithSubtitle(date, episodes, programs, "")
	if episode == nil || episode.ID != 113 || !strings.Contains(reason, "across 2 channels") {
		t.Fatalf("ResolveCorroboratedWarnedWithSubtitle() = %+v, %q; want episode 113", episode, reason)
	}
}

func TestResolveCorroboratedWarnedFailsClosed(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 7, 24, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{{ID: 113, Number: number(13), Title: "第十三話"}}
	base := syobocal.Program{PID: 1, Count: 13, ChannelID: 6, StartedAt: date.Add(time.Hour), Subtitle: "第十三話", Warn: true}

	for _, tt := range []struct {
		name     string
		programs []syobocal.Program
		want     string
	}{
		{name: "one channel", programs: []syobocal.Program{base}, want: "only 1 channel"},
		{name: "same channel repeated", programs: []syobocal.Program{base, {PID: 2, Count: 13, ChannelID: 6, StartedAt: date.Add(2 * time.Hour), Subtitle: "第十三話", Warn: true}}, want: "only 1 channel"},
		{name: "different counts", programs: []syobocal.Program{base, {PID: 2, Count: 12, ChannelID: 67, StartedAt: date.Add(2 * time.Hour), Subtitle: "第十二話", Warn: true}}, want: "ambiguous"},
		{name: "blank subtitle", programs: []syobocal.Program{base, {PID: 2, Count: 13, ChannelID: 67, StartedAt: date.Add(2 * time.Hour), Warn: true}}, want: "no subtitle"},
		{name: "subtitle conflict", programs: []syobocal.Program{base, {PID: 2, Count: 13, ChannelID: 67, StartedAt: date.Add(2 * time.Hour), Subtitle: "別の話", Warn: true}}, want: "conflicts"},
		{name: "mixed clean row", programs: []syobocal.Program{base, {PID: 2, Count: 13, ChannelID: 67, StartedAt: date.Add(2 * time.Hour), Subtitle: "第十三話"}}, want: "contains clean rows"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			episode, reason := ResolveCorroboratedWarnedWithSubtitle(date, episodes, tt.programs, "")
			if episode != nil || !strings.Contains(reason, tt.want) {
				t.Fatalf("ResolveCorroboratedWarnedWithSubtitle() = %+v, %q; want nil and substring %q", episode, reason, tt.want)
			}
		})
	}
}

func TestResolveForChannelDisambiguatesDifferentCounts(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 8, 28, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{
		{ID: 107, Number: number(7), Title: "七"},
		{ID: 108, Number: number(8), Title: "八"},
	}
	programs := []syobocal.Program{
		{PID: 1, Count: 8, ChannelID: 16, StartedAt: date.Add(2 * time.Hour)},
		{PID: 2, Count: 7, ChannelID: 79, StartedAt: date.Add(3 * time.Hour)},
	}
	if episode, _ := ResolveUnique(date, episodes, programs); episode != nil {
		t.Fatalf("ResolveUnique() = %+v, want ambiguous nil", episode)
	}
	episode, reason := ResolveForChannel(date, episodes, programs, 16)
	if episode == nil || episode.ID != 108 || !strings.Contains(reason, "trusted channel 16") {
		t.Fatalf("ResolveForChannel() = %+v, %q; want episode 108", episode, reason)
	}
}

func TestResolveUniqueWithCompositeSubtitlePart(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 4, 7, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{{ID: 101, Number: number(1), Title: "冬の訪れです。／不良です。"}}
	programs := []syobocal.Program{{PID: 1, Count: 1, ChannelID: 5, StartedAt: date.Add(time.Hour), Subtitle: "冬の訪れです。"}}

	episode, reason := ResolveUniqueWithSubtitle(date, episodes, programs, "コミュ４４ 冬の訪れです。")
	if episode == nil || episode.ID != 101 || !strings.Contains(reason, "Annict episode title") {
		t.Fatalf("ResolveUniqueWithSubtitle() = %+v, %q; want episode 101 with composite evidence", episode, reason)
	}
}

func TestResolveUniqueWithCompositeSubtitlePartFailsClosed(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 4, 7, 0, 0, 0, 0, jst)
	episodes := []annict.Episode{{ID: 101, Number: number(1), Title: "決戦（前編）／帰還"}}
	base := syobocal.Program{PID: 1, Count: 1, ChannelID: 5, StartedAt: date.Add(time.Hour), Subtitle: "決戦（前編）"}

	for _, subtitle := range []string{"決戦", "決戦（後編）", "未知の回"} {
		episode, reason := ResolveUniqueWithSubtitle(date, episodes, []syobocal.Program{base}, subtitle)
		if episode != nil || !strings.Contains(reason, "filename subtitle") {
			t.Errorf("ResolveUniqueWithSubtitle(%q) = %+v, %q; want safe rejection", subtitle, episode, reason)
		}
	}
}

func TestAnchorChannel(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2022, 8, 21, 0, 0, 0, 0, jst)
	episode := &annict.Episode{ID: 107, Number: number(7), Title: "七"}
	programs := []syobocal.Program{
		{PID: 1, Count: 7, ChannelID: 16, StartedAt: date.Add(2 * time.Hour), Subtitle: "七"},
		{PID: 2, Count: 6, ChannelID: 79, StartedAt: date.Add(3 * time.Hour), Subtitle: "六"},
	}
	channelID, reason := AnchorChannel(date, episode, programs)
	if channelID != 16 || !strings.Contains(reason, "fingerprints channel 16") {
		t.Fatalf("AnchorChannel() = %d, %q; want channel 16", channelID, reason)
	}

	programs = append(programs, syobocal.Program{PID: 3, Count: 7, ChannelID: 99, StartedAt: date.Add(4 * time.Hour), Subtitle: "七"})
	if channelID, _ := AnchorChannel(date, episode, programs); channelID != 0 {
		t.Fatalf("AnchorChannel() = %d, want ambiguous zero", channelID)
	}
}
