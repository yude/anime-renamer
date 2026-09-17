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

func TestMonthToSeasonAndSeasonYear(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	tests := []struct {
		month      time.Month
		wantSeason string
		wantYear   int // for a 2026 date
	}{
		{time.January, "winter", 2026},
		{time.February, "winter", 2026},
		{time.March, "winter", 2026},
		{time.April, "spring", 2026},
		{time.May, "spring", 2026},
		{time.June, "spring", 2026},
		{time.July, "summer", 2026},
		{time.August, "summer", 2026},
		{time.September, "summer", 2026},
		{time.October, "autumn", 2026},
		{time.November, "autumn", 2026},
		// Regression: December was miscategorized as "winter" (falling
		// into the default case), disagreeing with Annict's own 3-month
		// cour convention where autumn runs Oct-Dec. That mismatch made
		// narrowBySeason fail to find the correct work for any December
		// recording, since its computed season string never matched what
		// Annict actually has for an autumn-season work.
		{time.December, "autumn", 2026},
	}

	for _, tt := range tests {
		t.Run(tt.month.String(), func(t *testing.T) {
			if got := monthToSeason(tt.month); got != tt.wantSeason {
				t.Errorf("monthToSeason(%s) = %q, want %q", tt.month, got, tt.wantSeason)
			}

			date := time.Date(2026, tt.month, 15, 0, 0, 0, 0, jst)
			if got := SeasonYearFromMonth(date); got != tt.wantYear {
				t.Errorf("SeasonYearFromMonth(%s 2026) = %d, want %d", tt.month, got, tt.wantYear)
			}
		})
	}
}

func TestSeasonNarrowingForWinterUsesSameCalendarYear(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品", SeasonName: "2026-winter"},
		{ID: 2, Title: "作品", SeasonName: "2025-winter"},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 2,
		RecordedDate:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(2), Title: "ep2"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil || result.Work == nil || result.Work.ID != 1 {
		t.Fatalf("Match() = %+v, want 2026-winter work ID 1", result)
	}
}

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

func TestSeasonNarrowingForDecemberRecording(t *testing.T) {
	// Regression test for the monthToSeason December bug: a December
	// recording must narrow against a work registered in Annict's
	// "autumn" season (Oct-Dec), not fail to match against "winter".
	works := []annict.Work{
		{ID: 1, Title: "作品", SeasonName: "2026-autumn"},
		{ID: 2, Title: "作品", SeasonName: "2020-autumn"},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 10,
		RecordedDate:  time.Date(2026, 12, 5, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(10), Title: "ep10"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Work == nil || result.Work.ID != 1 {
		t.Errorf("Work = %+v, want work ID 1 narrowed by season 2026-autumn (reasons: %v)", result.Work, result.Reasons)
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

func TestMatchDoesNotUseScoringOnlyOrthographyToChooseAWork(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品"},
		{ID: 2, Title: "作品"},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 5,
		Subtitle:      "天才と凡才",
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(5), Title: "天才と凡人"}},
		2: {{ID: 201, Number: float64Ptr(5), Title: "別タイトル"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence != 0 {
		t.Errorf("Confidence = %d, want 0 because scoring-only subtitle variants must not disambiguate works (reasons: %v)", result.Confidence, result.Reasons)
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

	got := narrowByEpisodeNumber(works, 5, "", episodesByWork)
	if got == nil || got.Work.ID != 1 || got.EpisodeNumber != 5 {
		t.Errorf("narrowByEpisodeNumber() = %+v, want work ID 1 episode 5 via SortNumber fallback", got)
	}
}

func TestNarrowByEpisodeNumberUsesUniqueSubtitleAcrossWorks(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品 1st"}, {ID: 2, Title: "作品 2nd"}}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), Title: "はじまり"}},
		2: {{ID: 201, Number: float64Ptr(1), Title: "再会"}},
	}
	got := narrowByEpisodeNumber(works, 1, "再会", episodesByWork)
	if got == nil || got.Work.ID != 2 || got.EpisodeNumber != 1 {
		t.Errorf("narrowByEpisodeNumber() = %+v, want work 2 episode 1", got)
	}
}

func TestNarrowByEpisodeNumberIgnoresTrailingBroadcastPunctuation(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品 前編"}, {ID: 2, Title: "作品 続編"}}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), Title: "正義VS悪"}},
		2: {{ID: 201, Number: float64Ptr(1), Title: "別の始まり"}},
	}
	got := narrowByEpisodeNumber(works, 1, "正義VS悪！", episodesByWork)
	if got == nil || got.Work.ID != 1 || got.EpisodeNumber != 1 {
		t.Errorf("narrowByEpisodeNumber() = %+v, want work 1 after ignoring only terminal punctuation", got)
	}
}

func TestNarrowByEpisodeNumberMapsContinuousNumberByUniqueSubtitle(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品 前半"}, {ID: 2, Title: "作品 後半"}}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(10), Title: "前半最終話"}},
		2: {{ID: 201, Number: float64Ptr(1), Title: "後半開始"}},
	}
	got := narrowByEpisodeNumber(works, 11, "後半開始", episodesByWork)
	if got == nil || got.Work.ID != 2 || got.EpisodeNumber != 1 {
		t.Errorf("narrowByEpisodeNumber() = %+v, want work 2 local episode 1", got)
	}
}

func TestNarrowByEpisodeNumberRejectsSubtitleSharedAcrossWorks(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品 1st"}, {ID: 2, Title: "作品 2nd"}}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), Title: "総集編"}},
		2: {{ID: 201, Number: float64Ptr(1), Title: "総集編"}},
	}
	if got := narrowByEpisodeNumber(works, 1, "総集編", episodesByWork); got != nil {
		t.Errorf("narrowByEpisodeNumber() = %+v, want nil for a shared subtitle", got)
	}
}

func TestFractionalEpisodeNumberDoesNotMatchIntegerInput(t *testing.T) {
	episodes := []annict.Episode{{ID: 101, Number: float64Ptr(7.5), SortNumber: 7, Title: "特別話"}}
	for _, subtitle := range []string{"", "特別話"} {
		if got := findMatchingEpisode(7, subtitle, episodes); got != nil {
			t.Errorf("findMatchingEpisode(7, %q) = %+v, want nil for fractional episode 7.5", subtitle, got)
		}
	}
	if number, ok := EpisodeNumber(&episodes[0]); ok || number != 0 {
		t.Errorf("EpisodeNumber() = %d, %v; want 0, false for fractional number", number, ok)
	}
}

func TestEpisodeNumberUsesNumberTextBeforeInternalSortOrder(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int
	}{
		{text: "第6話", want: 6},
		{text: "第10局", want: 10},
		{text: "＃１３", want: 13},
		{text: "episode 2", want: 2},
		{text: "EPISODE.12", want: 12},
		{text: "SAILING 26", want: 26},
		{text: "RIDE.8", want: 8},
		{text: "第十四話", want: 14},
	} {
		episode := &annict.Episode{NumberText: tt.text, SortNumber: tt.want * 10}
		if got, ok := EpisodeNumber(episode); !ok || got != tt.want {
			t.Errorf("EpisodeNumber(NumberText=%q, SortNumber=%d) = %d, %v; want %d, true", tt.text, episode.SortNumber, got, ok, tt.want)
		}
	}
}

func TestMatchPrefersExplicitEpisodeLabelAcrossWorks(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品"},
		{ID: 2, Title: "作品"},
	}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), NumberText: "#1", Title: "本編"}},
		2: {{ID: 201, Number: float64Ptr(1), NumberText: "#11", Title: "特別編"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil || result.Work == nil || result.Work.ID != 1 {
		t.Fatalf("Match() = %+v, want work 1 selected by its explicit #1 label", result)
	}
	if result.Confidence < AutoRenameThreshold {
		t.Fatalf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
	if got, ok := MatchResultEpisodeNumber(result); !ok || got != 1 {
		t.Fatalf("MatchResultEpisodeNumber() = %d, %v; want 1, true", got, ok)
	}
}

func TestMatchUsesExplicitDisplayNumberWithinWork(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 44}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(5), NumberText: "第44話", Title: "第四十四話"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 101 {
		t.Fatalf("Match() = %+v, want episode 101 selected by 第44話", result)
	}
	if result.Confidence < AutoRenameThreshold {
		t.Fatalf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
	if got, ok := MatchResultEpisodeNumber(result); !ok || got != 44 {
		t.Fatalf("MatchResultEpisodeNumber() = %d, %v; want 44, true", got, ok)
	}
}

func TestMatchRejectsAmbiguousExplicitEpisodeLabels(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}, {ID: 2, Title: "作品"}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(7), NumberText: "#1"}},
		2: {{ID: 201, Number: float64Ptr(8), NumberText: "#1"}},
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil || result.Confidence != 0 {
		t.Fatalf("Match() = %+v, want a safe ambiguous result", result)
	}
}

func TestEpisodeNumberRejectsUnsupportedNumberTextWithoutSortFallback(t *testing.T) {
	episode := &annict.Episode{NumberText: "総集篇", SortNumber: 2150}
	if got, ok := EpisodeNumber(episode); ok || got != 0 {
		t.Errorf("EpisodeNumber() = %d, %v; want 0, false", got, ok)
	}
}

func TestMatchUsesSortNumberForExactDescriptiveSpecial(t *testing.T) {
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 26, Subtitle: "765プロという物語"}
	episodes := map[int][]annict.Episode{
		1: {{ID: 101, NumberText: "特別編", SortNumber: 26, Title: "765プロという物語"}},
	}
	result := Match(meta, []annict.Work{{ID: 1, Title: "作品"}}, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 101 || result.Confidence < AutoRenameThreshold {
		t.Fatalf("Match() = %+v, want exact special episode at confidence >= %d", result, AutoRenameThreshold)
	}
	if got, ok := MatchResultEpisodeNumber(result); !ok || got != 26 {
		t.Fatalf("MatchResultEpisodeNumber() = %d, %v; want 26, true", got, ok)
	}

	wrong := Match(
		&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 26, Subtitle: "別の物語"},
		[]annict.Work{{ID: 1, Title: "作品"}},
		episodes,
		nil,
	)
	if wrong == nil || wrong.Confidence >= AutoRenameThreshold {
		t.Fatalf("Match(wrong subtitle) = %+v, want strict rejection", wrong)
	}
}

func TestEpisodeNumberUsesExplicitLabelForFractionalSpecial(t *testing.T) {
	episode := &annict.Episode{Number: float64Ptr(10.5), NumberText: "第11話", SortNumber: 110}
	if got, ok := EpisodeNumber(episode); !ok || got != 11 {
		t.Errorf("EpisodeNumber() = %d, %v; want 11, true from explicit label", got, ok)
	}

	descriptive := &annict.Episode{Number: float64Ptr(10.5), NumberText: "OVA", SortNumber: 110}
	if got, ok := EpisodeNumber(descriptive); ok || got != 0 {
		t.Errorf("EpisodeNumber(descriptive) = %d, %v; want 0, false", got, ok)
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
	// even run: MatchingWorks only falls back to substring matching when
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

func TestMatchSubtitleWithParentheticalReadingAid(t *testing.T) {
	tests := []struct {
		name         string
		episodeTitle string
		fileSubtitle string
	}{
		{
			name:         "katakana reading omitted from filename",
			episodeTitle: "開幕！裏超闘球(スーパードッジ)大会！",
			fileSubtitle: "開幕!裏超闘球大会!",
		},
		{
			name:         "hiragana reading omitted and spaces added",
			episodeTitle: "超常対決！巨人vs(たい)巨人！",
			fileSubtitle: "超常対決!巨人 vs 巨人!",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workID := i + 1
			works := []annict.Work{{ID: workID, Title: "炎の闘球女 ドッジ弾子"}}
			episodesByWork := map[int][]annict.Episode{
				workID: {{ID: 100 + workID, Number: float64Ptr(float64(7 + i)), Title: tt.episodeTitle}},
			}
			meta := &parser.RecordingMetadata{
				WorkTitle:     "炎の闘球女 ドッジ弾子",
				EpisodeNumber: 7 + i,
				Subtitle:      tt.fileSubtitle,
			}

			result := Match(meta, works, episodesByWork, nil)
			if result == nil {
				t.Fatal("Match returned nil")
			}
			if result.Confidence < AutoRenameThreshold {
				t.Errorf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
			}
			if result.Episode == nil {
				t.Fatalf("Episode is nil (reasons: %v)", result.Reasons)
			}
		})
	}
}

func TestMatchSubtitleDoesNotDropMeaningfulParentheticalQualifier(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(1), Title: "決戦（前編）"}},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "作品",
		EpisodeNumber: 1,
		Subtitle:      "決戦",
	}

	result := Match(meta, works, episodesByWork, nil)
	if result == nil {
		t.Fatal("Match returned nil")
	}
	if result.Confidence >= AutoRenameThreshold {
		t.Errorf("Confidence = %d, want < %d because a meaningful qualifier differs (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
}

func TestSubtitlesEquivalentForScoringToleratesOnlyOneLongSubtitleInsertion(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{a: "零化域のミッシングリンク", b: "零化領域のミッシングリンク", want: true},
		{a: "決戦前編", b: "決戦後編", want: false},
		{a: "とても長いサブタイトル甲", b: "とても長いサブタイトル乙", want: false},
		{a: "長いサブタイトル", b: "長いサブタイトル補足", want: false},
	} {
		if got := subtitlesEquivalentForScoring(tt.a, tt.b); got != tt.want {
			t.Errorf("subtitlesEquivalentForScoring(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
	if subtitlesEquivalent("零化域のミッシングリンク", "零化領域のミッシングリンク") {
		t.Error("subtitlesEquivalent accepted a typo during candidate selection")
	}
}

func TestMatchUsesFileSubtitleForKnownAnnictPlaceholder(t *testing.T) {
	result := Match(
		&parser.RecordingMetadata{WorkTitle: "義妹生活", EpisodeNumber: 12, Subtitle: "tomorrow and tomorrow"},
		[]annict.Work{{ID: 1, Title: "義妹生活"}},
		map[int][]annict.Episode{1: {{ID: 161796, Number: float64Ptr(12), Title: "　　と　　"}}},
		nil,
	)
	if result == nil || result.Confidence < AutoRenameThreshold {
		t.Fatalf("Match() = %+v, want confidence >= %d", result, AutoRenameThreshold)
	}
	if result.FileSubtitle != "tomorrow and tomorrow" {
		t.Fatalf("FileSubtitle = %q, want source subtitle", result.FileSubtitle)
	}
	if episodeTitleUnavailable("と") {
		t.Fatal("a literal title と without placeholder whitespace must remain meaningful")
	}
}

func TestSubtitleScoringAllowsVerifiedRanmaTypoOnly(t *testing.T) {
	if !subtitlesEquivalentForScoring("かんばれムース", "がんばれムース") {
		t.Fatal("verified Annict/EPG variant should match after episode selection")
	}
	if subtitlesEquivalent("かんばれムース", "がんばれムース") {
		t.Fatal("verified scoring variant must not disambiguate candidate episodes")
	}
	if subtitlesEquivalentForScoring("かんばれムース", "がんばれシャンプー") {
		t.Fatal("unlisted subtitle difference must remain a mismatch")
	}
}

func TestMatchRelatedUsesParentheticalWorkYearWithoutRecordingDate(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "らんま1/2", SeasonName: "1989-spring"},
		{ID: 2, Title: "らんま1/2(2024)", SeasonName: "2024-autumn"},
		{ID: 3, Title: "らんま1/2 第2期", SeasonName: "2025-autumn"},
	}
	episodesByWork := map[int][]annict.Episode{
		1: {{ID: 101, Number: float64Ptr(24), Title: "旧作の第二十四話"}},
		2: {{ID: 201, Number: float64Ptr(12), Title: "第1期最終話"}},
		3: {{ID: 301, Number: float64Ptr(24), Title: "かんばれムース"}},
	}
	meta := &parser.RecordingMetadata{
		WorkTitle:     "らんま1／2(2025)",
		EpisodeNumber: 24,
		Subtitle:      "がんばれムース",
	}

	result := MatchRelated(meta, works, episodesByWork, nil)
	if result == nil || result.Work == nil || result.Work.ID != 3 {
		t.Fatalf("MatchRelated() = %+v, want the unique 2025 work", result)
	}
	if result.Confidence < AutoRenameThreshold {
		t.Fatalf("Confidence = %d, want >= %d (reasons: %v)", result.Confidence, AutoRenameThreshold, result.Reasons)
	}
}

func TestSubtitlesEquivalentStripsKomiSegmentNumber(t *testing.T) {
	if !subtitlesEquivalent("メリークリスマス…です。", "コミュ５６ メリークリスマス…です。") {
		t.Fatal("Komi EPG segment number should not hide an otherwise exact subtitle")
	}
	if subtitlesEquivalent("ケーションです。", "コミュニケーションです。") {
		t.Fatal("ordinary word beginning with コミュ must remain meaningful")
	}
}

func TestCompositeSubtitlePartMatch(t *testing.T) {
	for _, tt := range []struct {
		name               string
		complete, observed string
		want               bool
	}{
		{name: "one complete part", complete: "一つ目です。／二つ目です。／三つ目です。", observed: "二つ目です。", want: true},
		{name: "contiguous complete parts", complete: "一つ目です。／二つ目です。／三つ目です。", observed: "一つ目です。／二つ目です。", want: true},
		{name: "Komi segment label", complete: "冬の訪れです。／不良です。", observed: "コミュ４４ 冬の訪れです。", want: true},
		{name: "generic substring", complete: "決戦（前編）／帰還", observed: "決戦", want: false},
		{name: "meaningful qualifier differs", complete: "決戦（前編）／帰還", observed: "決戦（後編）", want: false},
		{name: "noncontiguous parts", complete: "一つ目です。／二つ目です。／三つ目です。", observed: "一つ目です。／三つ目です。", want: false},
		{name: "ordinary punctuation is not a delimiter", complete: "出会い、そして別れ", observed: "出会い", want: false},
		{name: "short part is insufficient", complete: "A／長い題名", observed: "A", want: false},
		{name: "whole title is not partial", complete: "一つ目です。／二つ目です。", observed: "一つ目です。／二つ目です。", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompositeSubtitlePartMatch(tt.complete, tt.observed); got != tt.want {
				t.Errorf("CompositeSubtitlePartMatch(%q, %q) = %v, want %v", tt.complete, tt.observed, got, tt.want)
			}
		})
	}
}

func TestDateProvenSubtitleMatchAcceptsExplicitOtherSummary(t *testing.T) {
	if !DateProvenSubtitleMatch("冬の訪れです。／不良です。", "コミュ４４ 冬の訪れです。 ほか") {
		t.Fatal("DateProvenSubtitleMatch should accept a complete leading part with an explicit other-summary marker")
	}
	if DateProvenSubtitleMatch("決戦（後編）／帰還", "決戦（前編）ほか") {
		t.Fatal("DateProvenSubtitleMatch accepted a meaningfully different summarized part")
	}
}

func TestMatchSubtitlePresentationVariantsReachThreshold(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "Ez Do Dance", file: "EZ DO DANCE"},
		{annict: "顔の無い王 ノーフェイス・メイキング", file: "顔の無い王—ノーフェイス・メイキング—"},
		{annict: "紅い瞳の魔法使い達【ウィザーズ】", file: "紅い瞳の魔法使い達(ウィザーズ)"},
		{annict: "プロジェクトS 史上最悪の道場／ビッチ・パーフェクト／F*CK & FURIOUS", file: "プロジェクトS ～史上最悪の道場～／ビッチ・パーフェクト／F＊CK & FURIOUS"},
		{annict: "友達になってくれませんか／描く派", file: "友達になってくれませんか&描く派"},
		{annict: "前代未聞です！", file: "前代未聞です"},
		{annict: "\u200bI'll come back for you", file: "I'll come back for you"},
		{annict: "ただ才あらば用いる", file: "ただ才あらは゛用いる"},
		{annict: "Après la pluie―彼の願い―", file: "Apres la pluie―彼の願い―"},
		{annict: "ちょー5％ーマジか!?だった", file: "ちょ—5%—マジか!？だった"},
		{annict: "寳月詠子", file: "寶月詠子"},
		{annict: "素人《ビギナー》", file: "素人≪ビギナー≫"},
		{annict: "『確率機』『シングル二倍』『夢芝居』", file: "確率機／シングル二倍／夢芝居"},
		{annict: "友 (ダフネ・ラウロス)", file: "友(ダフネ・ラウロス)"},
		{annict: "ラリーしたいです", file: "ラリーしたいです。"},
		{annict: "てんこもり♡文化祭", file: "てんこもり文化祭"},
		{annict: "センパイ君♡", file: "センパイ君♥"},
		{annict: "するがモンキー 其ノ貮", file: "するがモンキー 其ノ貳"},
		{annict: "夢にまでみた？フジ◯◯", file: "夢にまでみた？フジ〇〇"},
		{annict: "なんでもない一日", file: "なんでもない１日"},
		{annict: "やがて雨はやんで", file: "やがて雨は止んで"},
		{annict: "はじめての鉱物採集", file: "初めての鉱物採集"},
		{annict: "るらちゃんはチヤホヤされたい", file: "るらちゃんはちやほやされたい"},
		{annict: "いけいけゴーゴー夏休み", file: "イケイケゴーゴー夏休み"},
		{annict: "ようこそ、ラ・ソレイユヘ！", file: "ようこそ、ラ・ソレイユへ！"},
		{annict: "俺達の戦いはこれからだ！", file: "俺たちの戦いはこれからだ！"},
		{annict: "柏田さんと太田くんと海", file: "柏田さんと太田君と海"},
		{annict: "魔物の町の住人たち", file: "魔物の町の住人達"},
		{annict: "街角ギャラクシー☆彡", file: "街角ギャラクシー"},
		{annict: "日常パートめっちゃすこ〰〰♡♡♡", file: "日常パートめっちゃすこ~~~~~・・・"},
		{annict: "『自由』を", file: "自由"},
		{annict: "ビーナスライン・シェルター", file: "ビーナスライン／シェルター"},
		{annict: "呪館 JUKAN", file: "呪館"},
		{annict: "戦場の少年たち -The Children's Echelon-", file: "戦場の少年たち―The Chiidren's Echelon―"},
		{annict: "生の代償、死の償い", file: "生の代償、死の贖い"},
		{annict: "無限の残骸 アンリミテッド／レイズ・デッド", file: "無限の——— —アンリミテッド／レイズ・デッド—"},
		{annict: "プリステラ攻略戦リザルト", file: "プリステラ攻防戦リザルト"},
		{annict: "思い出した記憶って、なに？", file: "思い残した記憶って、なに？"},
		{annict: "魔王と勇者、勤めに従い遊園地に行く", file: "魔王と勇者、勧めに従い遊園地に行く"},
		{annict: "ご先祖は進化する!! メガネが映す暗黒部屋", file: "ご先祖は進化する!!メガネが映す暗黒部室"},
		{annict: "脳汁プシャー", file: "脳汁ブシャー"},
		{annict: "デスゲーム挑まれたけど・・・／LONG LONG A GO GO", file: "デスゲーム挑まれたけどクソゲーだった／LONG LONG A GO GO"},
		{annict: "オプション開放おめでとうございます", file: "オプション解放おめでとうございます"},
		{annict: "天才と凡人", file: "天才と凡才"},
		{annict: "BUMP-BOO-CRUSADERS", file: "バン・ブ・クルセイダーズ"},
		{annict: "飯田橋の昇竜 ～復讐のピピ～", file: "飯田橋の登竜 復讐のピピ"},
		{annict: "青天の霹靂／瀕死の狩人", file: "晴天の霹靂／瀕死の狩人"},
		{annict: "スパイ昇級試験", file: "スパイ昇格試験"},
		{annict: "個人的にはラブコメ展開希望", file: "個人的にはラブコメ希望"},
		{annict: "キスしてもまたキスしても余韻に浸っても体育祭実行委員でもラブコメにならない", file: "キスしてもまたキスしても余韻に浸っても体育会実行委員でもラブコメにならない"},
		{annict: "雪待と昴", file: "雪侍と昴"},
		{annict: "坊ちゃんとアリスの二人だけの歌", file: "坊ちゃんとアリスと二人だけの歌"},
		{annict: "俺はひょっとして、最終話でヒロインの横にいるポッと出のモブキャラなのだろうか", file: "俺はひょっとして、最終話で負けヒロインの横にいるポッと出のモブキャラなのだろうか"},
		{annict: "集う者達", file: "集う物達"},
		{annict: "水着の一日", file: "水着で１日"},
		{annict: "つながるもの", file: "つながるものの"},
	} {
		episodes := map[int][]annict.Episode{1: {{ID: 100 + i, Number: float64Ptr(1), Title: tt.annict}}}
		meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file}
		result := Match(meta, works, episodes, nil)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestMatchNumberedMiniSegmentsReachThreshold(t *testing.T) {
	result := Match(
		&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: "任務と家族／子ども心I／子ども心II／目覚まし"},
		[]annict.Work{{ID: 1, Title: "作品"}},
		map[int][]annict.Episode{1: {{ID: 800, Number: float64Ptr(1), Title: "任務と家族／子ども心／目覚まし"}}},
		nil,
	)
	if result == nil || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want numbered mini-segments to reach %d", result, AutoRenameThreshold)
	}
}

func TestNumberedMiniSegmentsDoNotFoldEnglishParts(t *testing.T) {
	if subtitleNumberedSegmentExpansionMatch("Part I／Part II", "Part") {
		t.Error("subtitleNumberedSegmentExpansionMatch folded English part labels")
	}
}

func TestMatchExplicitDecorativeSubtitleSuffixReachThreshold(t *testing.T) {
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "遠い記憶 -sometime,somewhere-", file: "遠い記憶"},
		{annict: "イモ☆ヨバ", file: "イモ☆ヨバ ～妹なんて呼ばないで！～"},
		{annict: "元カノとカノジョ", file: "元カノとカノジョ -トリカノ-"},
		{annict: "笑顔のカタチ(〃＞▽＜〃)", file: "笑顔のカタチ"},
	} {
		result := Match(
			&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file},
			[]annict.Work{{ID: 1, Title: "作品"}},
			map[int][]annict.Episode{1: {{ID: 700 + i, Number: float64Ptr(1), Title: tt.annict}}},
			nil,
		)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestDecorativeSubtitleSuffixRejectsMeaningfulQualifiers(t *testing.T) {
	for _, decorated := range []string{
		"これは十分に長い本編タイトル（前編）",
		"これは十分に長い本編タイトル-後編-",
		"これは十分に長い本編タイトル～完結～",
	} {
		if subtitleDecorativeSuffixMatch("これは十分に長い本編タイトル", decorated) {
			t.Errorf("subtitleDecorativeSuffixMatch accepted %q", decorated)
		}
	}
}

func TestSubtitlePresentationScoringKeepsMeaningfulParentheticalText(t *testing.T) {
	for _, tt := range []struct {
		a, b string
	}{
		{a: "決戦 (前編)", b: "決戦"},
		{a: "決戦 (つづく)", b: "決戦"},
		{a: "作品 (リメイク)", b: "作品"},
	} {
		if subtitlesEquivalentForScoring(tt.a, tt.b) {
			t.Errorf("subtitlesEquivalentForScoring(%q, %q) = true, want false", tt.a, tt.b)
		}
	}
}

func TestFindMatchingEpisodePrefersUniqueExactSubtitleOverConflictingNumber(t *testing.T) {
	episodes := []annict.Episode{
		{ID: 101, Number: float64Ptr(1), Title: "そうさ、京都に行こう"},
		{ID: 102, Number: float64Ptr(2), Title: "修学旅行、いきなり襲撃です"},
	}
	got := findMatchingEpisode(2, "life.01 そうさ、京都に行こう", episodes)
	if got == nil || got.ID != 101 {
		t.Fatalf("findMatchingEpisode() = %+v, want unique subtitle episode 101", got)
	}

	episodes = append(episodes, annict.Episode{ID: 103, Number: float64Ptr(3), Title: "そうさ、京都に行こう"})
	got = findMatchingEpisode(2, "life.01 そうさ、京都に行こう", episodes)
	if got == nil || got.ID != 102 {
		t.Fatalf("findMatchingEpisode() with repeated subtitle = %+v, want number episode 102", got)
	}

	maxEpisodes := []annict.Episode{
		{ID: 111, Number: float64Ptr(11), Title: "赤龍帝(おとこ) 対 獅子王(おとこ)"},
		{ID: 112, Number: float64Ptr(12), Title: "学園祭のライオンハート"},
	}
	if got := findMatchingEpisode(12, "life.MAX vs power.MAX 赤龍帝(おとこ) 対 獅子王(おとこ)", maxEpisodes); got == nil || got.ID != 111 {
		t.Fatalf("findMatchingEpisode(life.MAX) = %+v, want episode 111", got)
	}
	if got := findMatchingEpisode(13, "life.MAXIMUM vs power.MAXIMUM 学園祭のライオンハート", maxEpisodes); got == nil || got.ID != 112 {
		t.Fatalf("findMatchingEpisode(life.MAXIMUM) = %+v, want episode 112", got)
	}
}

func TestMatchStructuredSubtitlePartsReachThreshold(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "ヒーロー", file: "ヒーロー／大丈夫"},
		{annict: "マルデチャックの穴／パンティ・ショーツ 魔根の伝説／ザ・プラッシュ", file: "パンティ・ショーツ 魔根の伝説／ザ・プラッシュ"},
		{annict: "三月の風と四月の雨で五月の花が咲く", file: "March winds and April showers bring forth May flowers.(三月の風と四月の雨で五月の花が咲く)"},
		{annict: "帰ってきた救世主", file: "上の巻「帰ってきた救世主」"},
		{annict: "Hello Strange(そして伝説へ……)／What A Wonderful World(この素晴らしい異世界生活にようこそ)／As(あの場所で集まろう)／Mean Old World(昔の話よ…)", file: "Hello Strange(そして伝説へ……)、What A Wonderful World(この素晴らしい異世界生活にようこそ)、As(あの場所で集まろう)、Mean Old World(昔の話よ…)"},
		{annict: "其の一 彼女には向かない職業／其の二 有頂天探偵社", file: "彼女には向かない職業"},
		{annict: "「とのさまんの特別/とのさまんの最後」/「フロ騒動/クマ！」/「晴れの日の面々/コンビニ弁当からの……」", file: "Episode04 とのさまんの特別／とのさまんの最後 ／ Episode05 フロ騒動／クマ！ ／ Episode06 晴れの日の面々／コンビニ弁当からの…"},
		{annict: "それはいつかの日のこと、なので / そしてある日のこと、なので", file: "Ａ：それはいつかの日のこと、なので"},
	} {
		episodes := map[int][]annict.Episode{1: {{ID: 200 + i, Number: float64Ptr(1), Title: tt.annict}}}
		meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file}
		result := Match(meta, works, episodes, nil)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d (segments: %q vs %q)", tt.annict, tt.file, result, AutoRenameThreshold, subtitleSegments(tt.annict), subtitleSegments(tt.file))
		}
	}
}

func TestMatchExplicitOtherEpisodeSummariesReachThreshold(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "吸血鬼ちゃんと球技祭／吸血鬼ちゃんと部活探訪", file: "吸血鬼ちゃんと球技祭 ほか"},
		{annict: "ホームレス女騎士/はじめてのおしごと他", file: "ホームレス女騎士／ホームレス女騎士アフター／はじめてのおしごと"},
		{annict: "救世主女騎士他", file: "ホームレスのグルメ／救世主女騎士"},
		{annict: "退治人(ハンター)来たりて空を飛ぶ 前編", file: "『退治人（ハンター）来たりて空を跳ぶ 前編』ほか２本"},
	} {
		episodes := map[int][]annict.Episode{1: {{ID: 500 + i, Number: float64Ptr(1), Title: tt.annict}}}
		meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file}
		result := Match(meta, works, episodes, nil)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestOtherEpisodeSummaryRejectsShortOrMeaningfullyDifferentSegments(t *testing.T) {
	for _, tt := range []struct {
		summary string
		full    string
	}{
		{summary: "歩ほか", full: "歩／バシ"},
		{summary: "決戦（前編）ほか", full: "決戦（後編）／帰還"},
		{summary: "これは十分に長い前編ほか", full: "これはまったく違う後編／帰還"},
	} {
		if subtitleOtherSummaryMatch(tt.summary, tt.full) {
			t.Errorf("subtitleOtherSummaryMatch(%q, %q) = true, want false", tt.summary, tt.full)
		}
	}
}

func TestMatchLongParentheticalExpansionReachThreshold(t *testing.T) {
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "劇団ドラゴン、オンステージ！(劇団名あったんですね)", file: "劇団ドラゴン、オンステージ！"},
		{annict: "柏田さんと太田君とプール", file: "柏田さんと太田君とプール(柏田さんと太田君と水泳／田淵さんの秘密／太田君のお昼)"},
	} {
		result := Match(
			&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file},
			[]annict.Work{{ID: 1, Title: "作品"}},
			map[int][]annict.Episode{1: {{ID: 600 + i, Number: float64Ptr(1), Title: tt.annict}}},
			nil,
		)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestLongParentheticalExpansionRejectsShortQualifiers(t *testing.T) {
	for _, qualifier := range []string{"前編", "後編", "完結"} {
		if subtitleLongParentheticalExpansionMatch("これは十分に長い本編タイトル", "これは十分に長い本編タイトル（"+qualifier+"）") {
			t.Errorf("subtitleLongParentheticalExpansionMatch accepted short qualifier %q", qualifier)
		}
	}
}

func TestMatchStructuredSubtitleRejectsShortOrMeaningfulQualifier(t *testing.T) {
	for _, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "歩", file: "歩／バシ"},
		{annict: "決戦（前編）", file: "決戦"},
	} {
		result := Match(
			&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file},
			[]annict.Work{{ID: 1, Title: "作品"}},
			map[int][]annict.Episode{1: {{ID: 301, Number: float64Ptr(1), Title: tt.annict}}},
			nil,
		)
		if result == nil || result.Confidence >= AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence below %d", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestMatchLongSubtitleTrailingLabelReachThreshold(t *testing.T) {
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "これは十分に長い公式説明文であり末尾の短いラベルまでを含む そんな第一話", file: "そんな第一話"},
		{annict: "いよいよ最終回を迎えるまでの十分に長い公式説明文 そんな第十二話(最終回)", file: "そんな第十二話（最終回）"},
	} {
		result := Match(
			&parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file},
			[]annict.Work{{ID: 1, Title: "作品"}},
			map[int][]annict.Episode{1: {{ID: 401 + i, Number: float64Ptr(1), Title: tt.annict}}},
			nil,
		)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d for exact trailing label", tt.annict, tt.file, result, AutoRenameThreshold)
		}
	}
}

func TestMatchingWorksIgnoresTitlePresentationPunctuation(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "16bitセンセーション ANOTHER LAYER"},
		{ID: 2, Title: "16bitセンセーション ANOTHER LAYER 特別番組"},
	}
	got := MatchingWorks("16bitセンセーション -ANOTHER LAYER-", works)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("MatchingWorks() = %+v, want only the punctuation-equivalent main work", got)
	}
}

func TestMatchingWorksPrefersExactPresentationBeforeNormalization(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "邪神ちゃんドロップキック"},
		{ID: 2, Title: "邪神ちゃんドロップキック'"},
	}
	got := MatchingWorks("邪神ちゃんドロップキック", works)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("MatchingWorks() = %+v, want only the exact base work", got)
	}
}

func TestMatchingRelatedWorksIncludesOnlyExplicitSeriesContinuations(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "【推しの子】"},
		{ID: 2, Title: "【推しの子】第2期"},
		{ID: 3, Title: "【推しの子】 Season 3"},
		{ID: 4, Title: "【推しの子】 Mother and Children"},
		{ID: 5, Title: "【推しの子】 part2"},
		{ID: 6, Title: "【推しの子】 2クール目"},
		{ID: 7, Title: "【推しの子】 (Netflixオリジナル)"},
		{ID: 8, Title: "【推しの子】 (TV放送)"},
	}
	got := MatchingRelatedWorks("【推しの子】", works)
	wantIDs := []int{1, 2, 3, 5, 6, 7, 8}
	if len(got) != len(wantIDs) {
		t.Fatalf("MatchingRelatedWorks() = %+v, want IDs %v", got, wantIDs)
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Errorf("MatchingRelatedWorks()[%d].ID = %d, want %d", i, got[i].ID, wantID)
		}
	}
}

func TestMatchingSpecialWorksIncludesOnlyExplicitSpecialLabels(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "作品2"},
		{ID: 2, Title: "作品2 OVA"},
		{ID: 3, Title: "作品2 特別編"},
		{ID: 4, Title: "作品2 第2期"},
		{ID: 5, Title: "作品2 劇場版"},
	}
	got := MatchingSpecialWorks("作品2", works)
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
		t.Fatalf("MatchingSpecialWorks() = %+v, want explicit OVA and 特別編 only", got)
	}
}

func TestMatchMapsMissingNumberByUniqueSubtitleWithinWork(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品 2nd"}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "後半開始"},
		{ID: 102, Number: float64Ptr(2), Title: "再会"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品 2nd", EpisodeNumber: 14, Subtitle: "後半開始"}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 101 || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want unique subtitle mapped to local episode 1", result)
	}
}

func TestMatchMapsAbsentNumberByUniqueSubtitle(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 3}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "はじまり"},
		{ID: 102, Number: float64Ptr(2), Title: "再会"},
		{ID: 103, Number: float64Ptr(3), Title: "旅立ち"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", Subtitle: "再 会"}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 102 || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want unique subtitle mapped to episode 2", result)
	}
}

func TestMatchRejectsAbsentNumberWithDuplicateSubtitle(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 2}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "総集編"},
		{ID: 102, Number: float64Ptr(2), Title: "総集編"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", Subtitle: "総集編"}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode != nil || result.Confidence >= AutoRenameThreshold {
		t.Errorf("Match() = %+v, want ambiguous subtitle to remain unresolved", result)
	}
}

func TestMatchMapsFinalMarkerOnlyFromCompleteEpisodeList(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 4}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "はじまり"},
		{ID: 102, Number: float64Ptr(2), Title: "再会"},
		{ID: 103, Number: float64Ptr(3), Title: "最終回"},
		{ID: 199, Number: float64Ptr(4), NumberText: "OVA", Title: "番外編"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", FinalEpisode: true}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 103 || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want complete work's final episode", result)
	}
}

func TestMatchInfersExplicitUnnumberedFinalEpisodeFromUniqueSubtitle(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 3}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, NumberText: "第1話", SortNumber: 10, Title: "はじまり"},
		{ID: 102, NumberText: "第2話", SortNumber: 20, Title: "再会"},
		{ID: 103, NumberText: "最終話", SortNumber: 30, Title: "旅立ち"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", Subtitle: "旅立ち", FinalEpisode: true}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 103 || result.Confidence < AutoRenameThreshold {
		t.Fatalf("Match() = %+v, want inferred final episode 3", result)
	}
	if number, ok := EpisodeNumber(result.Episode); !ok || number != 3 {
		t.Errorf("EpisodeNumber() = %d, %v; want 3, true", number, ok)
	}
}

func TestMatchDoesNotOverrideMeaningfulFinalSubtitleMismatch(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 3}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "はじまり"},
		{ID: 102, Number: float64Ptr(2), Title: "再会"},
		{ID: 103, Number: float64Ptr(3), Title: "旅立ち"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", Subtitle: "別の結末", FinalEpisode: true}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode != nil || result.Confidence >= AutoRenameThreshold {
		t.Errorf("Match() = %+v, want meaningful subtitle mismatch to remain unresolved", result)
	}
}

func TestMatchRejectsFinalMarkerWhenEpisodeListIsIncomplete(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品", EpisodesCount: 3}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "はじまり"},
		{ID: 102, Number: float64Ptr(2), Title: "再会"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", FinalEpisode: true}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode != nil || result.Confidence >= AutoRenameThreshold {
		t.Errorf("Match() = %+v, want incomplete list to remain unresolved", result)
	}
}

func TestMatchDoesNotMapMissingNumberByDuplicateSubtitle(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(1), Title: "総集編"},
		{ID: 102, Number: float64Ptr(2), Title: "総集編"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 14, Subtitle: "総集編"}
	result := Match(meta, works, episodes, nil)
	if result == nil || result.Episode != nil || result.Confidence >= AutoRenameThreshold {
		t.Errorf("Match() = %+v, want unresolved duplicate subtitle", result)
	}
}

func TestMatchMapsLocalSeasonNumberToContiguousAnnictNumber(t *testing.T) {
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(13), Title: "KNIGHTMARE"},
		{ID: 102, Number: float64Ptr(14), Title: "TRUTH OF THE HERO"},
		{ID: 103, Number: float64Ptr(15), Title: "ANSWER THE DOOR"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "HIGH CARD season2", EpisodeNumber: 2}
	result := Match(meta, []annict.Work{{ID: 1, Title: "HIGH CARD season2"}}, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 102 || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want local episode 2 mapped to Annict episode 14", result)
	}
}

func TestMatchRejectsLocalSeasonNumberForGappedAnnictNumbers(t *testing.T) {
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(13)},
		{ID: 102, Number: float64Ptr(15)},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品 第2期", EpisodeNumber: 2}
	result := Match(meta, []annict.Work{{ID: 1, Title: "作品 第2期"}}, episodes, nil)
	if result == nil || result.Episode != nil {
		t.Errorf("Match() = %+v, want no episode for a gapped Annict sequence", result)
	}
}

func TestMatchLocalSeasonNumberSkipsDescriptiveSpecial(t *testing.T) {
	episodes := map[int][]annict.Episode{1: {
		{ID: 101, Number: float64Ptr(73), NumberText: "第七十三話"},
		{ID: 102, Number: float64Ptr(74), NumberText: "第七十四話"},
		{ID: 199, Number: float64Ptr(74), NumberText: "総集編"},
		{ID: 103, Number: float64Ptr(75), NumberText: "第七十五話"},
	}}
	meta := &parser.RecordingMetadata{WorkTitle: "作品 第4期", EpisodeNumber: 3}
	result := Match(meta, []annict.Work{{ID: 1, Title: "作品 第4期"}}, episodes, nil)
	if result == nil || result.Episode == nil || result.Episode.ID != 103 || result.Confidence < AutoRenameThreshold {
		t.Errorf("Match() = %+v, want local episode 3 mapped past the descriptive special to Annict episode 75", result)
	}
}

func TestFindMatchingProgram(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2026, 8, 13, 0, 0, 0, 0, jst)

	t.Run("zero episode ID must not spuriously match a program with no linked episode", func(t *testing.T) {
		// Regression: 0 is the zero-value placeholder both for "episode ID
		// unknown" (the query side) and "program has no linked episode"
		// (the data side) — comparing them as if equal would match a
		// program on a completely wrong date.
		programs := []annict.Program{
			{ID: 1, StartedAt: date.AddDate(0, 0, 7), Episode: annict.Episode{ID: 0}},
		}
		if got := findMatchingProgram(date, 0, programs); got != nil {
			t.Errorf("findMatchingProgram() = %+v, want nil (wrong date, no real episode ID to match on)", got)
		}
	})

	t.Run("real episode ID match still works", func(t *testing.T) {
		programs := []annict.Program{
			{ID: 1, StartedAt: date.AddDate(0, 0, 7), Episode: annict.Episode{ID: 42}},
		}
		got := findMatchingProgram(date, 42, programs)
		if got == nil || got.ID != 1 {
			t.Errorf("findMatchingProgram() = %+v, want program ID 1 matched by episode ID despite the wrong date", got)
		}
	})

	t.Run("date-only match still works when episode ID is unknown", func(t *testing.T) {
		programs := []annict.Program{
			{ID: 1, StartedAt: date, Episode: annict.Episode{ID: 0}},
		}
		got := findMatchingProgram(date, 0, programs)
		if got == nil || got.ID != 1 {
			t.Errorf("findMatchingProgram() = %+v, want program ID 1 matched by date", got)
		}
	})

	t.Run("known different episode does not match by date alone", func(t *testing.T) {
		programs := []annict.Program{
			{ID: 1, StartedAt: date, Episode: annict.Episode{ID: 41}},
		}
		if got := findMatchingProgram(date, 42, programs); got != nil {
			t.Errorf("findMatchingProgram() = %+v, want nil for a different linked episode", got)
		}
	})

	t.Run("missing linked episode does not verify a known episode", func(t *testing.T) {
		programs := []annict.Program{
			{ID: 1, StartedAt: date, Episode: annict.Episode{ID: 0}},
		}
		if got := findMatchingProgram(date, 42, programs); got != nil {
			t.Errorf("findMatchingProgram() = %+v, want nil when the expected episode is known but the program link is missing", got)
		}
	})
}

func float64Ptr(f float64) *float64 {
	return &f
}
