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

func TestMatchSubtitlePresentationVariantsReachThreshold(t *testing.T) {
	works := []annict.Work{{ID: 1, Title: "作品"}}
	for i, tt := range []struct {
		annict string
		file   string
	}{
		{annict: "Ez Do Dance", file: "EZ DO DANCE"},
		{annict: "顔の無い王 ノーフェイス・メイキング", file: "顔の無い王—ノーフェイス・メイキング—"},
		{annict: "紅い瞳の魔法使い達【ウィザーズ】", file: "紅い瞳の魔法使い達(ウィザーズ)"},
	} {
		episodes := map[int][]annict.Episode{1: {{ID: 100 + i, Number: float64Ptr(1), Title: tt.annict}}}
		meta := &parser.RecordingMetadata{WorkTitle: "作品", EpisodeNumber: 1, Subtitle: tt.file}
		result := Match(meta, works, episodes, nil)
		if result == nil || result.Confidence < AutoRenameThreshold {
			t.Errorf("Match(%q, %q) = %+v, want confidence >= %d", tt.annict, tt.file, result, AutoRenameThreshold)
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

func TestMatchingRelatedWorksIncludesOnlyExplicitSeriesContinuations(t *testing.T) {
	works := []annict.Work{
		{ID: 1, Title: "【推しの子】"},
		{ID: 2, Title: "【推しの子】第2期"},
		{ID: 3, Title: "【推しの子】 Season 3"},
		{ID: 4, Title: "【推しの子】 Mother and Children"},
	}
	got := MatchingRelatedWorks("【推しの子】", works)
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Errorf("MatchingWorks() = %+v, want base work and explicit seasons only", got)
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
