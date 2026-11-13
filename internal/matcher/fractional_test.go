package matcher

import (
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/parser"
)

func TestFractionalEpisodeUsesPublicLabelBeforeIntegerStorageNumber(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	episodes := map[int][]annict.Episode{1: {{ID: 135, Number: float64Ptr(13), NumberText: "第13.5話", SortNumber: 1350, Title: "特別総集編"}}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeLabel: "13.5", Subtitle: "特別総集編"}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 135 || result.Confidence != AutoRenameThreshold {
		t.Fatalf("Match() = %+v, want exact public special", result)
	}
	if label, ok := MatchResultEpisodeLabel(result); !ok || label != "13.5" {
		t.Fatalf("label = %q, %v", label, ok)
	}
	if number, ok := MatchResultEpisodeNumber(result); ok || number != 0 {
		t.Fatalf("fractional label was coerced to integer: %d, %v", number, ok)
	}
	if number, ok := EpisodeNumber(&episodes[1][0]); ok || number != 0 {
		t.Fatalf("internal Number=13 leaked as public episode: %d, %v", number, ok)
	}
	integer := Match(&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 13, Subtitle: "特別総集編"}, works, episodes, nil)
	if integer != nil && integer.Episode != nil {
		t.Fatalf("integer input matched special: %+v", integer)
	}
}

func TestFractionalMatchingRejectsConflictsAndAmbiguity(t *testing.T) {
	for _, tt := range []struct {
		name     string
		episodes []annict.Episode
		subtitle string
	}{
		{"wrong label", []annict.Episode{{ID: 1, NumberText: "第14.5話", Title: "総集編"}}, "総集編"},
		{"subtitle mismatch", []annict.Episode{{ID: 1, NumberText: "第13.5話", Title: "別の話"}}, "総集編"},
		{"subtitle missing", []annict.Episode{{ID: 1, NumberText: "第13.5話", Title: "総集編"}}, ""},
		{"duplicate label even with differing subtitles", []annict.Episode{{ID: 1, NumberText: "第13.5話", Title: "総集編"}, {ID: 2, NumberText: "第13.5話", Title: "別の話"}}, "総集編"},
		{"numeric and textual fractions disagree", []annict.Episode{{ID: 1, Number: float64Ptr(14.5), NumberText: "第13.5話", Title: "総集編"}}, "総集編"},
		{"descriptive number text is not a public fraction", []annict.Episode{{ID: 1, Number: float64Ptr(13.5), NumberText: "OVA", Title: "総集編"}}, "総集編"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := Match(&parser.RecordingMetadata{WorkTitle: "作品", EpisodeLabel: "13.5", Subtitle: tt.subtitle}, []annict.Work{{ID: 1, Title: "作品"}}, map[int][]annict.Episode{1: tt.episodes}, nil)
			if result != nil {
				t.Fatalf("unsafe fractional match: %+v", result)
			}
		})
	}
}

func TestFractionalLabelsCannotBecomeIntegerSortOrdinals(t *testing.T) {
	for _, text := range []string{"第13.5話", "第13.5話（総集編）"} {
		episode := annict.Episode{ID: 135, NumberText: text, SortNumber: 1, Title: "総集編"}
		if number, ok := EpisodeNumber(&episode); ok || number != 0 {
			t.Errorf("EpisodeNumber(%q) = %d, %v", text, number, ok)
		}
		result := Match(&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: "総集編"}, []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 1}}, map[int][]annict.Episode{1: {episode}}, nil)
		if result != nil && result.Episode != nil {
			t.Fatalf("sort ordinal matched fractional episode: %+v", result)
		}
	}
}

func TestSubtitleCorrectionsAreScopedToWholeTitleWorkAndEpisode(t *testing.T) {
	for _, tt := range []struct {
		work     string
		number   int
		subtitle string
		want     bool
	}{
		{"テムパル～アイテムの力～", 1, "レジェンダリー職業", true},
		{"テムパル～アイテムの力～", 2, "レジェンダリー職業", false},
		{"別作品", 1, "レジェンダリー職業", false},
		{"テムパル～アイテムの力～", 1, "レジェンダリー職業 後編", false},
		{"ガールズ＆パンツァー もっとらぶらぶ作戦です！", 4, "すぱボコりぞーとです！／ミニぺパ？／ひょっこり知波単島であります！／心にバレーを！です！", true},
		{"ガールズ＆パンツァー もっとらぶらぶ作戦です！", 3, "すぱボコりぞーとです！／ミニぺパ？／ひょっこり知波単島であります！／心にバレーを！です！", false},
		{"ガールズ＆パンツァー もっとらぶらぶ作戦です！", 4, "ミニぺパ？", false},
	} {
		meta := &parser.RecordingMetadata{WorkTitle: tt.work, EpisodeNumber: tt.number, Subtitle: tt.subtitle}
		corrected, ok := CorrectMetadataSubtitle(meta)
		if ok != tt.want || meta.Subtitle != tt.subtitle || (ok && corrected.Subtitle == tt.subtitle) {
			t.Errorf("CorrectMetadataSubtitle(%+v) = %+v, %v", meta, corrected, ok)
		}
	}
}
