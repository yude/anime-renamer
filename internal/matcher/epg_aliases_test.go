package matcher

import (
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
)

func TestEPGWorkAliasesPreserveSequelIdentity(t *testing.T) {
	for _, tt := range []struct{ file, canonical string }{
		{"マウスカーソルで現実を操作できるようになったので女の子をいっぱいクリックします", "マウスカーソルで現実を操作できるようになったので、女の子をいっぱいクリックしまーす"},
		{"目覚めたら最強装備と宇宙船持ちだったので一戸建て目指して傭兵として自由に生きた", "目覚めたら最強装備と宇宙船持ちだったので、一戸建て目指して傭兵として自由に生きたい"},
		{"目覚めたら最強装備と宇宙船持ちだったので一戸建て目指して傭兵として自由に生きたい", "目覚めたら最強装備と宇宙船持ちだったので、一戸建て目指して傭兵として自由に生きたい"},
	} {
		works := []annict.Work{{ID: 1, Title: tt.canonical}, {ID: 2, Title: tt.canonical + " 第2期"}}
		if got := MatchingWorks(tt.file, works); len(got) != 1 || got[0].ID != 1 {
			t.Errorf("MatchingWorks(%q) = %+v, want canonical work only", tt.file, got)
		}
		for _, got := range MatchingWorks(tt.file+" 第2期", works) {
			if got.ID == 1 {
				t.Errorf("alias for %q collapsed an explicit sequel into the base work", tt.file)
			}
		}
	}
}
