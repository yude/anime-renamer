package matcher

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/parser"
)

func loadFixture[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return result
}

func TestMatchExact(t *testing.T) {
	workResp := loadFixture[annict.WorksResponse](t, "../../testdata/hanakimi-work.json")
	epResp := loadFixture[annict.EpisodesResponse](t, "../../testdata/hanakimi-episodes.json")
	progResp := loadFixture[annict.ProgramsResponse](t, "../../testdata/hanakimi-programs.json")

	meta := &parser.RecordingMetadata{
		WorkTitle:     "花ざかりの君たちへ 第2期",
		EpisodeNumber: 7,
		Subtitle:      "ずっとそばにいたいから",
		RecordedDate:  time.Date(2026, 8, 13, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}

	episodesByWork := map[int][]annict.Episode{
		4168: epResp.Episodes,
	}
	programsByWork := map[int][]annict.Program{
		4168: progResp.Programs,
	}

	result := Match(meta, workResp.Works, episodesByWork, programsByWork)
	if result == nil {
		t.Fatal("Match returned nil")
	}

	if result.Work == nil {
		t.Fatal("Work is nil")
	}
	if result.Work.Title != "花ざかりの君たちへ 第2期" {
		t.Errorf("Work.Title = %q, want %q", result.Work.Title, "花ざかりの君たちへ 第2期")
	}

	if result.Episode == nil {
		t.Fatal("Episode is nil")
	}
	if result.Episode.Number == nil || int(*result.Episode.Number) != 7 {
		t.Errorf("Episode.Number = %v, want 7", result.Episode.Number)
	}
	if result.Episode.Title != "ずっとそばにいたいから" {
		t.Errorf("Episode.Title = %q, want %q", result.Episode.Title, "ずっとそばにいたいから")
	}

	if result.Confidence < AutoRenameThreshold {
		t.Errorf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
}

func TestMatchNoWork(t *testing.T) {
	meta := &parser.RecordingMetadata{
		WorkTitle:     "存在しない作品",
		EpisodeNumber: 1,
	}

	result := Match(meta, nil, nil, nil)
	if result != nil {
		t.Errorf("Match returned non-nil for no works, got confidence %d", result.Confidence)
	}
}

func TestMatchMultipleWorksWithSeason(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品A", SeasonName: "2026-summer"},
		{ID: 2, Title: "作品A", SeasonName: "2024-fall"},
	}

	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品A",
		EpisodeNumber: 1,
		RecordedDate:  time.Date(2026, 7, 15, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}

	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), Title: "ep1"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence < 40 {
		t.Errorf("Confidence = %d, should have narrowed by season (reasons: %v)", result.Confidence, result.Reasons)
	}
}

func TestMatchMultipleWorksNoSeason(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品A"},
		{ID: 2, Title: "作品A"},
	}

	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品A",
		EpisodeNumber: 1,
	}

	result := Match(meta, works, nil, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence != 0 {
		t.Errorf("Confidence = %d, want 0 for multiple works", result.Confidence)
	}
	if len(result.Reasons) == 0 {
		t.Error("Expected reasons for confidence 0")
	}
}

func TestMatchEpisodeNotFound(t *testing.T) {
	workResp := loadFixture[annict.WorksResponse](t, "../../testdata/hanakimi-work.json")

	meta := &parser.RecordingMetadata{
		WorkTitle:     "花ざかりの君たちへ 第2期",
		EpisodeNumber: 999, // non-existent
	}

	episodesByWork := map[int][]annict.Episode{
		4168: {{ID: 1001, Number: float64Ptr(1), Title: "ep1"}},
	}

	result := Match(meta, workResp.Works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Episode != nil {
		t.Errorf("Episode should be nil for non-existent episode")
	}
	// Should still have work match
	if result.Confidence < 40 {
		t.Errorf("Confidence = %d, should have work match (40+)", result.Confidence)
	}
}

func TestSeasonNarrowing(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "うしろの正面カムイさん", SeasonName: "2026-summer"},
		{ID: 2, Title: "うしろの正面カムイさん", SeasonName: "2024-spring"},
		{ID: 3, Title: "うしろの正面カムイさん", SeasonName: "2023-winter"},
	}

	meta := &parser.RecordingMetadata{
		WorkTitle:     "うしろの正面カムイさん",
		EpisodeNumber: 3,
		Subtitle:      "呪いの人形／隙間女",
		RecordedDate:  time.Date(2026, 7, 22, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}

	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(3), Title: "呪いの人形／隙間女"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Work == nil {
		t.Fatal("Work is nil")
	}
	if result.Work.ID != 1 {
		t.Errorf("Work.ID = %d, want 1 (narrowed by season)", result.Work.ID)
	}
	if result.Episode == nil {
		t.Fatal("Episode is nil")
	}
	if result.Confidence < AutoRenameThreshold {
		t.Errorf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
}

func TestMatchSameSeasonSameTitleNarrowedByEpisode(t *testing.T) {
	// Same title AND same season for both candidates (e.g. a duplicate
	// Annict registration): narrowBySeason alone can't disambiguate, so
	// Match() must fall through to episode-number narrowing.
	works := []annict.Work{
		{ID: 1, Title: "作品", SeasonName: "2026-summer"},
		{ID: 2, Title: "作品", SeasonName: "2026-summer"},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 5,
		RecordedDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(5), Title: "ep5"}},
		2: {{ID: 201, Number: float64Ptr(9), Title: "ep9"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Work == nil || result.Work.ID != 1 {
		t.Errorf("Work = %+v, want work ID 1 narrowed via episode number (reasons: %v)", result.Work, result.Reasons)
	}
}

func TestMatchSameSeasonSameTitleStillAmbiguous(t *testing.T) {
	// Same setup, but the episode number can't disambiguate either
	// (present, identically, in both candidates): must report the
	// ambiguous-match error rather than picking one arbitrarily.
	works := []annict.Work{
		{ID: 1, Title: "作品", SeasonName: "2026-summer"},
		{ID: 2, Title: "作品", SeasonName: "2026-summer"},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 5,
		RecordedDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(5), Title: "ep5"}},
		2: {{ID: 201, Number: float64Ptr(5), Title: "ep5"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence != 0 {
		t.Errorf("Confidence = %d, want 0 for still-ambiguous match (reasons: %v)", result.Confidence, result.Reasons)
	}
}

func TestNarrowByEpisodeNumberFallsBackToSortNumber(t *testing.T) {
	// Regression test: episodes without a Number (only SortNumber set, as
	// Annict sometimes returns for specials) must still be usable to
	// disambiguate between same-titled works, consistent with
	// findMatchingEpisode's own SortNumber fallback.
	works := []annict.Work{
		{ID: 1, Title: "作品A"},
		{ID: 2, Title: "作品B"},
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, SortNumber: 5}},
		2: {{ID: 201, SortNumber: 9}},
	}

	got := narrowByEpisodeNumber(works, 5, episodesByWork)
	if got == nil || got.Work.ID != 1 || got.EpisodeNumber != 5 {
		t.Errorf("narrowByEpisodeNumber() = %+v, want work ID 1 episode 5 via SortNumber fallback", got)
	}
}

func TestMatchMultipleWorksNarrowedByEpisodeSortNumber(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品A"},
		{ID: 2, Title: "作品A"},
	}

	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品A",
		EpisodeNumber: 5,
	}

	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, SortNumber: 5, Title: "ep5"}},
		2: {{ID: 201, SortNumber: 9, Title: "ep9"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Work == nil || result.Work.ID != 1 {
		t.Errorf("Work = %+v, want work ID 1 narrowed via SortNumber (reasons: %v)", result.Work, result.Reasons)
	}
}

func TestMatchMultiCourOffsetFindsCorrectEpisode(t *testing.T) {
	// Regression test: narrowByEpisodeNumber's multi-cour offset heuristic
	// picks the 2nd-cour work when the absolute episode number exceeds the
	// 1st cour's episode count, but Match() must then look up the
	// *offset-adjusted* episode number within that work's own episode list
	// (which restarts at 1), not the original absolute number.
	//
	// Both works must land in candidateWorks together for this heuristic to
	// even run: findMatchingWorks only falls back to substring matching when
	// NO candidate is an exact title match, so the parsed title here is a
	// strict substring of both work titles rather than equal to either.
	baseEpisodes := make([]annict.Episode, 12)
	for i := range baseEpisodes {
		baseEpisodes[i] = annict.Episode{ID: 100 + i + 1, Number: float64Ptr(float64(i + 1)), Title: fmt.Sprintf("base-ep%d", i+1)}
	}
	kourEpisodes := make([]annict.Episode, 8)
	for i := range kourEpisodes {
		kourEpisodes[i] = annict.Episode{ID: 200 + i + 1, Number: float64Ptr(float64(i + 1)), Title: fmt.Sprintf("kour-ep%d", i+1)}
	}

	works := []annict.Work{
		{ID: 1, Title: "作品X"},
		{ID: 2, Title: "作品X 第2クール"},
	}
	episodesByWork := map[int][]annict.Episode{
		1: baseEpisodes,
		2: kourEpisodes,
	}

	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 15, // absolute; offset within the 2nd cour is 15-12=3
		Subtitle:      "kour-ep3",
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Work == nil || result.Work.ID != 2 {
		t.Fatalf("Work = %+v, want the 2nd-cour work (ID 2) (reasons: %v)", result.Work, result.Reasons)
	}
	if result.Episode == nil {
		t.Fatalf("Episode is nil, want offset episode 3 of the 2nd cour (reasons: %v)", result.Reasons)
	}
	if result.Episode.ID != 203 {
		t.Errorf("Episode.ID = %d, want 203 (kour-ep3, offset 15-12=3)", result.Episode.ID)
	}
	if result.Confidence < AutoRenameThreshold {
		t.Errorf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
}

func TestSubtitlePartialMatch(t *testing.T) {
	tests := []struct {
		name   string
		a, b   string
		expect bool
	}{
		{
			name:   "exact match after normalization",
			a:      "サブタイトル",
			b:      "サブタイトル",
			expect: true,
		},
		{
			name:   "one contains the other, both long enough",
			a:      "エピソードタイトル",
			b:      "タイトル",
			expect: true,
		},
		{
			name: "single shared kanji must not count as a match",
			// Regression: a byte-length check here (3 bytes for one kanji)
			// let a single shared character pass the "long enough" guard;
			// counting runes correctly requires more than 2 characters.
			a:      "怪",
			b:      "本当は怖い怪談集",
			expect: false,
		},
		{
			name:   "unrelated subtitles",
			a:      "サブタイトルA",
			b:      "サブタイトルB",
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subtitlePartialMatch(tt.a, tt.b); got != tt.expect {
				t.Errorf("subtitlePartialMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expect)
			}
		})
	}
}

func float64Ptr(f float64) *float64 {
	return &f
}
