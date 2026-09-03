package normalize

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ep．7", "ep.7"},
		{"EP．10", "EP.10"},
		{"花ざかりの君たちへ", "花ざかりの君たちへ"},
		{"hello　world", "hello world"},
		{"test（abc）", "test(abc)"},
		{"＃7", "#7"},
		{"１２３", "123"},
		{"abcDEF", "abcDEF"},
	}
	for _, tt := range tests {
		got := Normalize(tt.input)
		if got != tt.expected {
			t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b   string
		expect bool
	}{
		{"ep．7", "ep.7", true},
		{"EP.7", "ep.7", false}, // case differs
		{"花ざかりの君たちへ", "花ざかりの君たちへ", true},
		{"hello", "world", false},
	}
	for _, tt := range tests {
		got := Compare(tt.a, tt.b)
		if got != tt.expect {
			t.Errorf("Compare(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expect)
		}
	}
}

func TestNormalizeForSearch(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Regression: 《》 are 3-byte UTF-8 runes; stripBracketContent must
		// skip past their full byte width, not assume 1 byte, or the
		// surrounding text gets corrupted into invalid UTF-8.
		{"作品《あ》続き", "作品 続き"},
		{"からくり撫子《オートマタ》", "からくり撫子"},
		{"『サブタイトル』", "サブタイトル"},
		{"作品(2026)", "作品 2026"},
		{"作品（2026）", "作品 2026"},
	}
	for _, tt := range tests {
		got := NormalizeForSearch(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeForSearch(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeTitleForMatch(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		eq   bool
	}{
		{a: "16bitセンセーション -ANOTHER LAYER-", b: "16bitセンセーション ANOTHER LAYER", eq: true},
		{a: "ひぐらしのなく頃に 卒", b: "ひぐらしのなく頃に卒", eq: true},
		{a: "プリンセスコネクト！Re：Dive", b: "プリンセスコネクト!re:dive", eq: true},
		{a: "うる星やつら(2022)", b: "うる星やつら", eq: true},
		{a: "マブラヴ オルタネイティヴ 第二期", b: "マブラヴ オルタネイティヴ 第2期", eq: true},
		{a: "ラブライブ！スーパースター！！(第3期)", b: "ラブライブ！スーパースター!! 3期", eq: true},
		{a: "黒子のバスケ 第1期", b: "黒子のバスケ", eq: true},
		{a: "ダンス・ダンス・ダンス―ル", b: "ダンス・ダンス・ダンスール", eq: true},
		{a: "シュタインズ・ゲート ゼロ", b: "STEINS;GATE 0", eq: true},
		{a: "作品 ～副題～", b: "作品 〜副題〜", eq: true},
		{a: "ポプテピピック TVアニメ—ション作品第二シリーズ", b: "ポプテピピック 第二シリーズ", eq: true},
		{a: "タイムボカンシリーズ ヤッターマン", b: "ヤッターマン", eq: true},
		{a: "けいおん！", b: "けいおん！！", eq: false},
		{a: "作品 第2期", b: "作品 第3期", eq: false},
	} {
		got := NormalizeTitleForMatch(tt.a) == NormalizeTitleForMatch(tt.b)
		if got != tt.eq {
			t.Errorf("NormalizeTitleForMatch(%q) == NormalizeTitleForMatch(%q) is %v, want %v", tt.a, tt.b, got, tt.eq)
		}
	}
}

func TestNormalizeSubtitleForMatch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "katakana reading aid",
			input:    "開幕！裏超闘球(スーパードッジ)大会！",
			expected: "開幕!裏超闘球大会!",
		},
		{
			name:     "hiragana reading aid and spaces",
			input:    "超常対決！巨人vs(たい)巨人！",
			expected: "超常対決!巨人vs巨人!",
		},
		{
			name:     "spaces around ASCII word",
			input:    "超常対決!巨人 vs 巨人!",
			expected: "超常対決!巨人vs巨人!",
		},
		{
			name:     "ASCII letter case",
			input:    "EZ DO DANCE",
			expected: "ezdodance",
		},
		{
			name:     "em dash is presentation only",
			input:    "顔の無い王—ノーフェイス—",
			expected: "顔の無い王ノーフエイス",
		},
		{
			name:     "square bracket katakana reading",
			input:    "紅い瞳の魔法使い達【ウィザーズ】",
			expected: "紅い瞳の魔法使い達",
		},
		{
			name:     "meaningful kanji qualifier is preserved",
			input:    "決戦（前編）",
			expected: "決戦前編",
		},
		{
			name:     "parenthesized-only subtitle is preserved",
			input:    "（つづく）",
			expected: "つづく",
		},
		{
			name:     "spaced parenthetical text is preserved",
			input:    "決戦 （つづく）",
			expected: "決戦つづく",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeSubtitleForMatch(tt.input); got != tt.expected {
				t.Errorf("NormalizeSubtitleForMatch(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCollapseSpaces(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello  world", "hello world"},
		{"a  b  c", "a b c"},
		{"nochange", "nochange"},
	}
	for _, tt := range tests {
		got := CollapseSpaces(tt.input)
		if got != tt.expected {
			t.Errorf("CollapseSpaces(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
