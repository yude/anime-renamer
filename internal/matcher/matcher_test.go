package matcher

import (
	"encoding/json"
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

func float64Ptr(f float64) *float64 {
	return &f
}
