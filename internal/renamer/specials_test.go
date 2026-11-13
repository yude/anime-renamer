package renamer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/matcher"
)

func TestFractionalAndStandalonePathsPreserveIdentity(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result *matcher.MatchResult
		want   string
	}{
		{"Annict public fractional label", &matcher.MatchResult{Work: &annict.Work{Title: "作品"}, Episode: &annict.Episode{NumberText: "第13.5話", Title: "総集編"}, OutputEpisodeLabel: "13.5"}, "作品 #13.5 「総集編」.mkv"},
		{"verified recording special", &matcher.MatchResult{Work: &annict.Work{Title: "作品"}, RecordingEpisode: &matcher.RecordingEpisode{Label: "3.5", Subtitle: "オーディオコメンタリー"}}, "作品 #3.5 「オーディオコメンタリー」.mkv"},
		{"standalone movie", &matcher.MatchResult{Work: &annict.Work{Title: "作品 44:CLOUDY BEACH", Media: "movie"}, Standalone: true}, "作品 44：CLOUDY BEACH.mkv"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.mkv")
			if err := os.WriteFile(source, []byte("recording"), 0o644); err != nil {
				t.Fatal(err)
			}
			preview := Rename(source, tt.result, true, "")
			if preview.Error != nil || !preview.Previewed || filepath.Base(preview.NewPath) != tt.want {
				t.Fatalf("preview = %+v, want %q", preview, tt.want)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal("dry-run changed the source")
			}
			moved := Rename(source, tt.result, false, "")
			if moved.Error != nil || !moved.Renamed {
				t.Fatalf("rename = %+v", moved)
			}
			body, err := os.ReadFile(moved.NewPath)
			if err != nil || string(body) != "recording" {
				t.Fatalf("recording contents changed: %q, %v", body, err)
			}
			again := Rename(moved.NewPath, tt.result, true, "")
			if again.Error != nil || again.NewPath != moved.NewPath {
				t.Fatalf("rerun nested or renamed the recording: %+v", again)
			}
		})
	}
}

func TestSpecialPathsRejectInvalidIdentity(t *testing.T) {
	for _, result := range []*matcher.MatchResult{
		{Work: &annict.Work{Title: "作品"}, Episode: &annict.Episode{NumberText: "第13.5話"}, OutputEpisodeLabel: "13"},
		{Work: &annict.Work{Title: "作品"}, RecordingEpisode: &matcher.RecordingEpisode{Label: "3.5/4"}},
		{Work: &annict.Work{Title: "作品", Media: "tv"}, Standalone: true},
		{Work: &annict.Work{Title: "作品", Media: "movie"}, Standalone: true, Episode: &annict.Episode{NumberText: "第1話"}},
	} {
		if path, err := BuildPath("source.mp4", result); err == nil {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
}
