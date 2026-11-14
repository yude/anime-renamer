package matcher

import (
	"github.com/yude/anime-renamer/internal/annict"
	"testing"
)

func TestDuplicateEpisodeNumbersNeedUniqueSubtitle(t *testing.T) {
	one := 1.0
	episodes := []annict.Episode{{ID: 1, Number: &one, Title: "前編"}, {ID: 2, Number: &one, Title: "後編"}}
	if match := findMatchingEpisode(1, "", episodes); match != nil {
		t.Fatal("selected first ambiguous episode")
	}
	if match := findMatchingEpisode(1, "前編", episodes); match == nil || match.ID != 1 {
		t.Fatal("unique subtitle should still resolve identity")
	}
	episodes[1].Title = "前編"
	if match := findMatchingEpisode(1, "前編", episodes); match != nil {
		t.Fatal("duplicate number and subtitle were accepted")
	}
	two := 2.0
	episodes[0].NumberText = "第1話"
	episodes[1].NumberText = "第1話"
	episodes[1].Number = &two
	if match := findMatchingEpisode(1, "前編", episodes); match != nil {
		t.Fatal("a local number overrode an ambiguous public label")
	}
}
