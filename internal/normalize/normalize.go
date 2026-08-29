package normalize

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize applies NFKC normalization and full-width to half-width conversion.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(toHalfWidth(toSmallKana(r)))
	}
	return b.String()
}

// NormalizeForSearch normalizes for fuzzy matching:
// half-width ASCII punctuation → full-width Japanese equivalents,
// then full-width ASCII → half-width, then NFKC.
// This handles both directions of punctuation mismatch.
func NormalizeForSearch(s string) string {
	// Remove corner brackets 『』 used in some filenames, adding spaces to avoid joining words
	s = strings.ReplaceAll(s, "『", " ")
	s = strings.ReplaceAll(s, "』", " ")

	// Strip 《》 brackets and their content (reading aids)
	s = stripBracketContent(s, '《', '》')

	// Convert parentheses to spaces for fuzzy matching
	// Handles: (X), （X） → " X "
	s = strings.ReplaceAll(s, "（", " ")
	s = strings.ReplaceAll(s, "）", " ")
	s = strings.ReplaceAll(s, "(", " ")
	s = strings.ReplaceAll(s, ")", " ")

	// First: half-width ASCII punctuation → full-width
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(toFullWidthPunct(r))
	}
	// Then apply standard normalize (NFKC + full→half) and collapse spaces
	return CollapseSpaces(strings.TrimSpace(Normalize(b.String())))
}

// Compare compares two strings after normalization.
func Compare(a, b string) bool {
	return Normalize(a) == Normalize(b)
}

// toFullWidthPunct converts common half-width ASCII punctuation to full-width
// Japanese equivalents. This handles the mismatch between recording filenames
// (which use half-width !~ etc.) and Annict data (which uses full-width ！～).
func toFullWidthPunct(r rune) rune {
	switch r {
	case '!':
		return '！'
	case '~':
		return '～'
	case '@':
		return '＠'
	case '#':
		return '＃'
	case '$':
		return '＄'
	case '%':
		return '％'
	case '&':
		return '＆'
	case '*':
		return '＊'
	case '+':
		return '＋'
	case '-':
		return '－'
	case '=':
		return '＝'
	case '?':
		return '？'
	case '^':
		return '＾'
	case '_':
		return '＿'
	case '|':
		return '｜'
	}
	return r
}

// toHalfWidth converts full-width ASCII characters and digits to half-width.
func toHalfWidth(r rune) rune {
	// Full-width space → half-width space
	if r == '\u3000' {
		return ' '
	}
	// Full-width digits ０-９ (U+FF10-U+FF19) → 0-9
	if r >= '０' && r <= '９' {
		return r - '０' + '0'
	}
	// Full-width upper A-Z (U+FF21-U+FF3A) → A-Z
	if r >= 'Ａ' && r <= 'Ｚ' {
		return r - 'Ａ' + 'A'
	}
	// Full-width lower a-z (U+FF41-U+FF5A) → a-z
	if r >= 'ａ' && r <= 'ｚ' {
		return r - 'ａ' + 'a'
	}
	// Full-width punctuation commonly seen in filenames
	switch r {
	case '．':
		return '.'
	case '（':
		return '('
	case '）':
		return ')'
	case '「':
		return '「' // Keep as-is for Japanese content
	case '」':
		return '」'
	case '：':
		return ':'
	case '；':
		return ';'
	case '／':
		return '/'
	case '＊':
		return '*'
	case '？':
		return '?'
	case '＜':
		return '<'
	case '＞':
		return '>'
	case '｜':
		return '|'
	case '＿':
		return '_'
	case '＃':
		return '#'
	case '　':
		return ' '
	}
	// Roman numeral full-width characters are handled separately if needed
	return r
}

// IsASCIIAlphaNum checks if a rune is an ASCII alphanumeric character.
func IsASCIIAlphaNum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// IsSpaceOrFullWidthSpace checks if a rune is a space or full-width space.
func IsSpaceOrFullWidthSpace(r rune) bool {
	return r == ' ' || r == '\u3000' || r == '　' || unicode.IsSpace(r)
}

// TrimSpaces removes leading/trailing spaces (including full-width).
func TrimSpaces(s string) string {
	return strings.TrimFunc(s, IsSpaceOrFullWidthSpace)
}

// CollapseSpaces replaces consecutive spaces with a single space.
func CollapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if IsSpaceOrFullWidthSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}

// toSmallKana converts small katakana (ァィゥェォ) to their big equivalents (アイウエオ).
// This handles subtitle mismatches where small kana are used interchangeably.
func toSmallKana(r rune) rune {
	switch r {
	case 'ァ': // U+30A1
		return 'ア' // U+30A2
	case 'ィ': // U+30A3
		return 'イ' // U+30A4
	case 'ゥ': // U+30A5
		return 'ウ' // U+30A6
	case 'ェ': // U+30A7
		return 'エ' // U+30A8
	case 'ォ': // U+30A9
		return 'オ' // U+30AA
	}
	return r
}

// stripBracketContent removes content between matching brackets (inclusive).
func stripBracketContent(s string, open, close rune) string {
	// open/close are multi-byte runes (e.g. 《 》), so skipping past them
	// must advance by their actual UTF-8 byte width, not by 1 byte — doing
	// so would slice into the middle of the rune and corrupt the string.
	openStr, closeStr := string(open), string(close)
	result := s
	for {
		start := strings.Index(result, openStr)
		if start == -1 {
			break
		}
		rest := result[start+len(openStr):]
		end := strings.Index(rest, closeStr)
		if end == -1 {
			break
		}
		// Remove from open to close (inclusive), replace with space
		result = result[:start] + " " + rest[end+len(closeStr):]
	}
	return strings.TrimSpace(result)
}
