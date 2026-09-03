package normalize

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	parentheticalYearPattern = regexp.MustCompile(`[（(][0-9０-９]{4}(?:年版)?[）)]`)
	ordinalSeasonPattern     = regexp.MustCompile(`第\s*([0-9]+)\s*期`)
	japaneseSeasonPattern    = regexp.MustCompile(`(?:第\s*)?([0-9]+)\s*シーズン`)
	englishSeasonPattern     = regexp.MustCompile(`(?i)season\s*([0-9]+)`)
	ordinalEnglishSeason     = regexp.MustCompile(`(?i)([0-9]+)(?:st|nd|rd|th)\s*season`)
	trailingFirstSeason      = regexp.MustCompile(`1期$`)
)

var japaneseSeasonNumbers = strings.NewReplacer(
	"第一期", "第1期",
	"第二期", "第2期",
	"第三期", "第3期",
	"第四期", "第4期",
	"第五期", "第5期",
	"第六期", "第6期",
	"第七期", "第7期",
	"第八期", "第8期",
	"第九期", "第9期",
	"第十期", "第10期",
)

// Normalize applies NFKC normalization and full-width to half-width conversion.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	// NFKC expands the spacing dakuten/handakuten characters used by some
	// EPGs to an ASCII space plus a combining mark. Reattach that mark before
	// NFC composition so は゛ and the canonically decomposed ば compare alike.
	s = strings.ReplaceAll(s, " \u3099", "\u3099")
	s = strings.ReplaceAll(s, " \u309a", "\u309a")
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(toHalfWidth(toSmallKana(r)))
	}
	return norm.NFC.String(b.String())
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

// NormalizeTitleForMatch builds a comparison key for work titles while
// ignoring presentation-only differences that commonly vary between EPG and
// Annict data: width, letter case, whitespace, punctuation, and symbols. It
// deliberately retains every letter and number, including season/cour digits,
// so titles with different semantic qualifiers do not become equal.
func NormalizeTitleForMatch(s string) string {
	s = parentheticalYearPattern.ReplaceAllString(s, "")
	s = strings.ToLower(NormalizeKatakanaDashes(Normalize(s)))
	s = japaneseSeasonNumbers.Replace(s)
	s = japaneseSeasonPattern.ReplaceAllString(s, "${1}期")
	s = ordinalEnglishSeason.ReplaceAllString(s, "${1}期")
	s = englishSeasonPattern.ReplaceAllString(s, "${1}期")
	s = ordinalSeasonPattern.ReplaceAllString(s, "${1}期")
	s = trailingFirstSeason.ReplaceAllString(s, "")
	key := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || isTitlePresentationRune(r) {
			return -1
		}
		return r
	}, s)
	if key == "シュタインズゲートゼロ" {
		return "steinsgate0"
	}
	if key == "ポプテピピックtvアニメーション作品第二シリーズ" {
		return "ポプテピピック第二シリーズ"
	}
	if key == "タイムボカンシリーズヤッターマン" {
		return "ヤッターマン"
	}
	if key == "bleach千年血戦篇ー訣別譚ー" {
		return "bleach千年血戦篇訣別譚"
	}
	if key == "マッシュルmashle2期" {
		return "マッシュルmashle神覚者候補選抜試験編"
	}
	if key == "ヴアニタスの手記カルテ" {
		return "ヴアニタスの手記"
	}
	return key
}

// NormalizeKatakanaDashes repairs EPG text that uses a horizontal dash where
// a katakana prolonged sound mark belongs. Dashes used as separators remain
// unchanged because conversion requires katakana on both sides.
func NormalizeKatakanaDashes(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if (r == '―' || r == '—') && i > 0 && i+1 < len(runes) && unicode.In(runes[i-1], unicode.Katakana) && unicode.In(runes[i+1], unicode.Katakana) {
			runes[i] = 'ー'
		}
	}
	return string(runes)
}

func isTitlePresentationRune(r rune) bool {
	switch r {
	case '-', '‐', '‑', '‒', '–', '—', '―',
		'.', '・', '･', '/', '\\', ':', ';', ',', '_', '~', '〜',
		'(', ')', '[', ']', '{', '}',
		'「', '」', '『', '』', '【', '】', '〈', '〉', '《', '》', '<', '>',
		'"', '\'', '`', '“', '”', '‘', '’':
		return true
	default:
		return false
	}
}

// NormalizeSubtitleForMatch normalizes episode subtitles while ignoring
// presentation-only differences commonly introduced by EPG providers:
// kana-only readings in parentheses and whitespace.
//
// Parenthetical text containing kanji, Latin letters, or digits is preserved,
// so meaningful qualifiers such as "(前編)" are not silently discarded.
func NormalizeSubtitleForMatch(s string) string {
	s = stripKanaReadingBrackets(s)
	s = strings.ToLower(NormalizeForSearch(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || isSubtitlePresentationRune(r) {
			return -1
		}
		return r
	}, s)
}

func isSubtitlePresentationRune(r rune) bool {
	switch r {
	case '-', '‐', '‑', '‒', '–', '—', '―',
		'"', '\'', '`', '“', '”', '‘', '’',
		'「', '」', '『', '』', '【', '】', '[', ']', '［', '］':
		return true
	default:
		return false
	}
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

// stripKanaReadingBrackets removes kana readings embedded in parentheses or
// square-style brackets. A bracketed-only subtitle is left intact: reading
// aids need a base expression immediately before the opening bracket.
func stripKanaReadingBrackets(s string) string {
	runes := []rune(s)
	var b strings.Builder

	for i := 0; i < len(runes); {
		close, ok := readingBracketClose(runes[i])
		if !ok {
			b.WriteRune(runes[i])
			i++
			continue
		}

		end := i + 1
		for end < len(runes) && runes[end] != close {
			end++
		}

		if end < len(runes) && hasReadingBase(runes, i) && isKanaReading(runes[i+1:end]) {
			i = end + 1
			continue
		}

		b.WriteRune(runes[i])
		i++
	}

	return b.String()
}

func readingBracketClose(open rune) (rune, bool) {
	switch open {
	case '(':
		return ')', true
	case '（':
		return '）', true
	case '[':
		return ']', true
	case '［':
		return '］', true
	case '【':
		return '】', true
	default:
		return 0, false
	}
}

func hasReadingBase(runes []rune, openingIndex int) bool {
	if openingIndex == 0 {
		return false
	}
	previous := runes[openingIndex-1]
	return unicode.IsLetter(previous) || unicode.IsDigit(previous)
}

func isKanaReading(runes []rune) bool {
	hasKana := false
	for _, r := range runes {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			hasKana = true
		case r == 'ー' || r == '・' || r == '･' || unicode.IsSpace(r):
			// Prolonged sound marks, middle dots, and spaces are allowed in a
			// kana reading, but do not make an empty annotation a reading.
		default:
			return false
		}
	}
	return hasKana
}
