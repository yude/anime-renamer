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
