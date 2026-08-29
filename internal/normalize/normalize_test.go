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
