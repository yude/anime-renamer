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
