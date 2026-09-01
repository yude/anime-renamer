package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yude/anime-renamer/internal/normalize"
)

// RecordingMetadata holds parsed information from a recording filename.
type RecordingMetadata struct {
	WorkTitle     string
	EpisodeNumber int
	Subtitle      string
	RecordedDate  time.Time
}

var (
	// ErrAmbiguousEpisode is returned when one recording names more than one
	// episode or uses a fractional episode number. The current matcher and
	// output format represent exactly one positive integer episode, so accepting
	// only the first integer would silently rename the recording as a different
	// episode.
	ErrAmbiguousEpisode = errors.New("ambiguous episode notation")

	// ErrUnsupportedEpisode is returned when a filename explicitly contains an
	// episode number that the Annict matching/output model cannot represent.
	ErrUnsupportedEpisode = errors.New("unsupported episode number")
)

var (
	// Matches compact and recorder-style dates at the end: (YYYYMMDD),
	// (YYYY_M_D), and duplicate suffixes such as (YYYYMMDD)(1) or
	// (YYYY_MM_DD)-2.
	datePattern = regexp.MustCompile(`[（(]([0-9０-９]{8}|[0-9０-９]{4}_[0-9０-９]{1,2}_[0-9０-９]{1,2})[）)](?:\s*(?:[（(][0-9０-９]+[）)]|-[0-9０-９]+))?\s*$`)

	// Reject notations that cannot be represented as one positive integer
	// before trying the permissive single-episode patterns below.
	fractionalHashPattern    = regexp.MustCompile(`[#＃♯][\s\x{3000}]*[0-9０-９]+[.．][0-9０-９]+`)
	fractionalEpisodePattern = regexp.MustCompile(`(?:第[\s\x{3000}]*)?[0-9０-９]+[.．][0-9０-９]+[\s\x{3000}]*話`)
	fractionalEPPattern      = regexp.MustCompile(`[eEｅＥ][pPｐＰ][.．\s\x{3000}]*[0-9０-９]+[.．][0-9０-９]+`)
	multiHashPattern         = regexp.MustCompile(`[#＃♯][\s\x{3000}]*[0-9０-９]+[\s\x{3000}]*(?:[,，、・&＆/／~〜～]|[-－―ー][\s\x{3000}]*[#＃♯]?[\s\x{3000}]*[0-9０-９]+)`)
	multiBareEpisodePattern  = regexp.MustCompile(`[0-9０-９]+[\s\x{3000}]*話[\s\x{3000}]*(?:[,，、・&＆/／~〜～－―ー-])[\s\x{3000}]*(?:第[\s\x{3000}]*)?[0-9０-９]+[\s\x{3000}]*話`)

	// Episode patterns matching both full-width and half-width forms.
	// These run against the ORIGINAL string (pre-normalization).
	// ep.7, ep．7, EP.7, EP．7, ep 7, ep. 7, ep7, ep\u30007
	epPattern1 = regexp.MustCompile(`[eEｅＥ][pPｐＰ][.．\s\x{3000}]*([0-9０-９]+)`)
	// Episode 7, Chapter.7, track-7, #7, #07, and the musical sharp sign ♯7.
	episodeWordPattern = regexp.MustCompile(`[eEｅＥ][pPｐＰ][iIｉＩ][sSｓＳ][oOｏＯ][dDｄＤ][eEｅＥ][.．\s\x{3000}]*([0-9０-９]+)`)
	chapterPattern     = regexp.MustCompile(`[cCｃＣ][hHｈＨ][aAａＡ][pPｐＰ][tTｔＴ][eEｅＥ][rRｒＲ][.．\s\x{3000}]*([0-9０-９]+)`)
	trackPattern       = regexp.MustCompile(`[tTｔＴ][rRｒＲ][aAａＡ][cCｃＣ][kKｋＫ][.．\s\x{3000}\-－]*([0-9０-９]+)`)
	epPattern2         = regexp.MustCompile(`[#＃♯][\s\x{3000}]*([0-9０-９]+)`)

	// 第N話, 第N幕, 第N番, 第N怪, 第N夜, 第N回, 第N局. A suffix is
	// required to avoid treating 第2クール as episode 2.
	arabicEpisodePattern = regexp.MustCompile(`第[\s\x{3000}]*([0-9０-９]+)[\s\x{3000}]*([話幕番怪夜回局羽RＲ])`)
	// 第三話, 第五幕, 第一夜, 第六局 (kanji digits)
	kanjiEpisodePattern     = regexp.MustCompile(`第[\s\x{3000}]*([〇一二三四五六七八九十百千壱弐参肆伍陸漆捌玖拾]+)[\s\x{3000}]*([話幕番怪夜回局羽RＲ])`)
	bareEpisodePattern      = regexp.MustCompile(`([0-9０-９]+)[\s\x{3000}]*話`)
	bareKanjiEpisodePattern = regexp.MustCompile(`[「『]?([〇一二三四五六七八九十百千壱弐参肆伍陸漆捌玖拾]+)話`)
	stepEpisodePattern      = regexp.MustCompile(`[【\[]?(?:すてっぷ|ステップ)[\s\x{3000}]*([①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳])[】\]]?`)

	leadingBracketTagPattern = regexp.MustCompile(`^[\s]*【[^】]*】`)
	leadingAngleTagPattern   = regexp.MustCompile(`^[\s]*＜[^＞]*＞`)
	seasonQualifierPattern   = regexp.MustCompile(`^(?:第[0-9０-９]+(?:期|クール)[\s\x{3000}]*)+`)
	leadingSimpleTagPattern  = regexp.MustCompile(`^[\s]*(?:\[字\]|\[新\]|\[再\]|\[無\]|\[多\]|\[SS\]|\[解\]|\[終\]|\[デ\]|\[双\])`)
	trailingMetadataPattern  = regexp.MustCompile(`(?:\s*(?:\[(?:字|新|再|無|多|SS|解|終|デ|双)\]|【(?:ANiMAZiNG!!!|ＡＮｉＭＡＺｉＮＧ！！！|字幕|アニメギルド)】))+\s*$`)

	// Metadata tag patterns to strip from filenames (SCRename rp1 equivalent).
	metadataTagPatterns = []*regexp.Regexp{
		// Full-width bracket metadata at start: 【ANiMAZiNG!!!】, 【字幕】etc.
		leadingBracketTagPattern,
		// Angle bracket metadata at start: ＜アニメギルド＞
		leadingAngleTagPattern,
		// Single-character bracket tags: [字], [新], [再], [無], [多], [SS], [解], [終]
		leadingSimpleTagPattern,
		// Common recording prefixes at start
		regexp.MustCompile(`^[\s]*(?:無料≫|無料》)`),
		regexp.MustCompile(`^[\s]*BS11(?:ガンダム|アニメ)`),
		// Channel slot prefixes: アニメA, アニメB, etc. (BS channel names like BS11's アニメA)
		regexp.MustCompile(`^[\s]*アニメ[A-Z]・?`),
		// Generic EPG labels separated from the actual work title by whitespace.
		regexp.MustCompile(`^[\s]*(?:TV|テレビ)?アニメ[\s\x{3000}]+`),
	}
)

var circledEpisodeNumbers = map[string]int{
	"①": 1, "②": 2, "③": 3, "④": 4, "⑤": 5,
	"⑥": 6, "⑦": 7, "⑧": 8, "⑨": 9, "⑩": 10,
	"⑪": 11, "⑫": 12, "⑬": 13, "⑭": 14, "⑮": 15,
	"⑯": 16, "⑰": 17, "⑱": 18, "⑲": 19, "⑳": 20,
}

// kanjiDigits maps single kanji digits to their integer values. Multipliers
// such as 十, 百, and 千 are handled separately by kanjiUnit.
var kanjiDigits = map[rune]int{
	'〇': 0,
	'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
	'六': 6, '七': 7, '八': 8, '九': 9,
	'壱': 1, '弐': 2, '参': 3, '肆': 4, '伍': 5,
	'陸': 6, '漆': 7, '捌': 8, '玖': 9,
}

func kanjiUnit(r rune) (int, bool) {
	switch r {
	case '十', '拾':
		return 10, true
	case '百':
		return 100, true
	case '千':
		return 1000, true
	default:
		return 0, false
	}
}

// kanjiToInt converts a kanji numeral string to an integer.
// Supports: 一〜十, 十一〜十九, 二十〜九十九, 百, 千.
func kanjiToInt(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}

	result := 0
	decimal := 0
	currentDigit := -1
	digitsBeforeFirstUnit := 0
	lastUnit := 10000
	hasUnit := false

	for _, r := range s {
		if digit, ok := kanjiDigits[r]; ok {
			if hasUnit && currentDigit >= 0 {
				return 0, false
			}
			decimal = decimal*10 + digit
			currentDigit = digit
			if !hasUnit {
				digitsBeforeFirstUnit++
			}
			continue
		}

		unit, ok := kanjiUnit(r)
		if !ok || unit >= lastUnit {
			return 0, false
		}
		if !hasUnit && digitsBeforeFirstUnit > 1 {
			return 0, false
		}
		hasUnit = true
		lastUnit = unit
		multiplier := currentDigit
		if multiplier < 0 {
			multiplier = 1
		}
		if multiplier == 0 {
			return 0, false
		}
		result += multiplier * unit
		currentDigit = -1
	}

	if !hasUnit {
		return decimal, true
	}
	if currentDigit >= 0 {
		result += currentDigit
	}
	return result, true
}

func decimalEpisodeNumber(s string) (int, error) {
	number, err := strconv.Atoi(normalize.Normalize(s))
	if err != nil {
		return 0, err
	}
	if number <= 0 {
		return 0, fmt.Errorf("%w: must be positive", ErrUnsupportedEpisode)
	}
	return number, nil
}

func parseRecordedDate(raw string) (time.Time, error) {
	normalized := normalize.Normalize(raw)
	if !strings.Contains(normalized, "_") {
		return time.ParseInLocation("20060102", normalized, time.FixedZone("JST", 9*60*60))
	}

	parts := strings.Split(normalized, "_")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("unsupported date format")
	}
	values := make([]int, len(parts))
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return time.Time{}, err
		}
		values[i] = value
	}
	digits := fmt.Sprintf("%04d%02d%02d", values[0], values[1], values[2])
	return time.ParseInLocation("20060102", digits, time.FixedZone("JST", 9*60*60))
}

func ambiguousEpisodeNotation(name string) string {
	for _, pattern := range []*regexp.Regexp{
		fractionalHashPattern,
		fractionalEpisodePattern,
		fractionalEPPattern,
		multiHashPattern,
		multiBareEpisodePattern,
	} {
		if match := pattern.FindString(name); match != "" {
			return match
		}
	}
	return ""
}

func bareEpisodeMatch(name string) []int {
	match := bareEpisodePattern.FindStringSubmatchIndex(name)
	if match == nil {
		return nil
	}
	if strings.TrimSpace(name[:match[0]]) == "" {
		return nil
	}
	// Phrases such as "9話までを振り返り" describe a recap rather than the
	// recording of episode 9. Keep them unsupported instead of fabricating a
	// single-episode match.
	after := strings.TrimSpace(name[match[1]:])
	if strings.HasPrefix(after, "まで") {
		return nil
	}
	return match
}

func earliestEpisodeMatch(name string, patterns ...*regexp.Regexp) []int {
	var earliest []int
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatchIndex(name)
		if match == nil || strings.TrimSpace(name[:match[0]]) == "" {
			continue
		}
		if earliest == nil || match[0] < earliest[0] {
			earliest = match
		}
	}
	return earliest
}

// ParseFilename parses a recording filename and extracts metadata.
// Expected format:
//
//	<WorkTitle> ep．<Episode>「<Subtitle>」 (<YYYYMMDD>).mp4
//
// Variations in episode notation are supported.
func ParseFilename(filename string) (*RecordingMetadata, error) {
	name := filename

	// 1. Remove extension
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[:idx]
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("empty filename: %q", filename)
	}

	// 2. Extract date from end
	var recordedDate time.Time
	if m := datePattern.FindStringSubmatch(name); m != nil {
		d, err := parseRecordedDate(m[1])
		if err != nil {
			return nil, fmt.Errorf("invalid date %q: %w", normalize.Normalize(m[1]), err)
		}
		recordedDate = d
		name = strings.TrimSpace(name[:len(name)-len(m[0])])
	}

	// 3. Strip metadata tags (SCRename rp1 equivalent)
	name = StripMetadataTags(name)

	// Check if anything meaningful remains after tag stripping
	remaining := strings.ReplaceAll(name, ".", "")
	remaining = strings.ReplaceAll(remaining, " ", "")
	remaining = strings.ReplaceAll(remaining, "\u3000", "")
	if strings.TrimSpace(remaining) == "" {
		return nil, fmt.Errorf("no meaningful content in filename: %q", filename)
	}
	if notation := ambiguousEpisodeNotation(name); notation != "" {
		return nil, fmt.Errorf("%w %q in %q", ErrAmbiguousEpisode, notation, filename)
	}

	// 4. Extract episode number first (needed to locate subtitle position)
	episodeNumber := 0
	epStart := -1
	epEnd := -1
	episodeMarkerOpensSubtitle := false

	// Try decimal patterns in priority order. Longer words precede the short
	// "ep" form to keep their boundaries explicit.
	decimalPatterns := []*regexp.Regexp{
		episodeWordPattern,
		chapterPattern,
		trackPattern,
		epPattern1,
		epPattern2,
		arabicEpisodePattern,
	}
	if m := earliestEpisodeMatch(name, decimalPatterns...); m != nil {
		parsedNumber, err := decimalEpisodeNumber(name[m[2]:m[3]])
		if err != nil {
			return nil, fmt.Errorf("invalid episode number %q: %w", name[m[2]:m[3]], err)
		}
		episodeNumber = parsedNumber
		epStart = m[0]
		epEnd = m[1]
	}
	if episodeNumber == 0 {
		if m := earliestEpisodeMatch(name, kanjiEpisodePattern); m != nil {
			kanjiNum := name[m[2]:m[3]]
			v, ok := kanjiToInt(kanjiNum)
			if !ok {
				return nil, fmt.Errorf("invalid episode number %q", kanjiNum)
			}
			if v <= 0 {
				return nil, fmt.Errorf("invalid episode number %q: %w", kanjiNum, ErrUnsupportedEpisode)
			}
			episodeNumber = v
			epStart = m[0]
			epEnd = m[1]
		}
	}
	if episodeNumber == 0 {
		if m := earliestEpisodeMatch(name, bareKanjiEpisodePattern); m != nil {
			kanjiNum := name[m[2]:m[3]]
			v, ok := kanjiToInt(kanjiNum)
			if !ok || v <= 0 {
				return nil, fmt.Errorf("invalid episode number %q", kanjiNum)
			}
			episodeNumber = v
			epStart = m[0]
			epEnd = m[1]
			episodeMarkerOpensSubtitle = strings.ContainsAny(name[m[0]:m[2]], "「『")
		}
	}
	if episodeNumber == 0 {
		if m := earliestEpisodeMatch(name, stepEpisodePattern); m != nil {
			episodeNumber = circledEpisodeNumbers[name[m[2]:m[3]]]
			epStart = m[0]
			epEnd = m[1]
		}
	}
	if episodeNumber == 0 {
		if m := bareEpisodeMatch(name); m != nil {
			parsedNumber, err := decimalEpisodeNumber(name[m[2]:m[3]])
			if err != nil {
				return nil, fmt.Errorf("invalid episode number %q: %w", name[m[2]:m[3]], err)
			}
			episodeNumber = parsedNumber
			epStart = m[0]
			epEnd = m[1]
		}
	}

	// 5. Extract subtitle from 「...」 AFTER episode marker (not before)
	subtitle := ""
	if epEnd >= 0 {
		afterEp := name[epEnd:]
		if episodeMarkerOpensSubtitle {
			// Some EPGs place the episode number inside the same quotes as the
			// subtitle: 「一話 カエルの歌を吹いた」.
			runes := []rune(strings.TrimSpace(afterEp))
			for i, r := range runes {
				if r == '」' || r == '』' {
					subtitle = string(runes[:i])
					break
				}
			}
		} else {
			// Look for 「」 after the episode marker.
			runes := []rune(afterEp)
			for i, r := range runes {
				if r == '「' {
					for j := i + 1; j < len(runes); j++ {
						if runes[j] == '」' {
							subtitle = string(runes[i+1 : j])
							break
						}
					}
					break
				}
			}
		}
	} else {
		// No episode marker found — no subtitle extraction
	}

	// 6. Extract work title: everything before the episode pattern
	workTitle := name
	if epStart >= 0 {
		workTitle = name[:epStart]
	}
	workTitle = stripTrailingMetadataTags(workTitle)
	workTitle = unwrapEPGTitle(workTitle)

	// Trim and clean
	workTitle = normalize.TrimSpaces(workTitle)
	workTitle = normalize.CollapseSpaces(workTitle)
	subtitle = normalize.TrimSpaces(subtitle)

	if workTitle == "" {
		return nil, fmt.Errorf("could not extract work title from %q", filename)
	}

	return &RecordingMetadata{
		WorkTitle:     workTitle,
		EpisodeNumber: episodeNumber,
		Subtitle:      subtitle,
		RecordedDate:  recordedDate,
	}, nil
}

// StripMetadataTags removes common recording metadata tags from a filename.
// This is the equivalent of SCRename's rp1 file processing.
// Tags like [字], [新], 【ANiMAZiNG!!!】, ＜アニメギルド＞, アニメA etc. are removed.
// Applied iteratively until no more tags are found.
func StripMetadataTags(s string) string {
	for {
		// regexp's \s is ASCII-only. Normalize leading Unicode whitespace
		// before every pass so a full-width space cannot hide a real tag.
		s = strings.TrimSpace(s)
		changed := false
		for _, re := range metadataTagPatterns {
			match := re.FindStringIndex(s)
			if match == nil {
				continue
			}
			// Bracket forms are also used by real work titles such as
			// 【推しの子】. If the text after the bracket starts directly with
			// episode metadata (optionally after a season qualifier), the
			// bracketed component is the title rather than a broadcast tag.
			if isPotentialTitleTag(re) && startsWithEpisodeMetadata(s[match[1]:]) {
				continue
			}
			newS := re.ReplaceAllString(s, "")
			if newS != s {
				changed = true
				s = newS
			}
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(s)
}

func isPotentialTitleTag(re *regexp.Regexp) bool {
	return re == leadingBracketTagPattern || re == leadingAngleTagPattern
}

func stripTrailingMetadataTags(s string) string {
	for {
		trimmed := strings.TrimSpace(s)
		cleaned := trailingMetadataPattern.ReplaceAllString(trimmed, "")
		if cleaned == trimmed {
			return trimmed
		}
		s = cleaned
	}
}

func unwrapEPGTitle(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"TVアニメ", "テレビアニメ", "日5"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
			break
		}
	}
	for _, pair := range [][2]string{{"『", "』"}, {"「", "」"}} {
		if strings.HasPrefix(s, pair[0]) && strings.HasSuffix(s, pair[1]) {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, pair[0]), pair[1]))
		}
	}
	return s
}

func startsWithEpisodeMetadata(s string) bool {
	s = strings.TrimSpace(s)
	for {
		match := leadingSimpleTagPattern.FindStringIndex(s)
		if match == nil || match[0] != 0 {
			break
		}
		s = strings.TrimSpace(s[match[1]:])
	}
	if qualifier := seasonQualifierPattern.FindString(s); qualifier != "" {
		s = strings.TrimSpace(s[len(qualifier):])
	}
	for _, re := range []*regexp.Regexp{
		episodeWordPattern,
		chapterPattern,
		trackPattern,
		epPattern1,
		epPattern2,
		arabicEpisodePattern,
		kanjiEpisodePattern,
		bareEpisodePattern,
		bareKanjiEpisodePattern,
		stepEpisodePattern,
	} {
		if match := re.FindStringIndex(s); match != nil && match[0] == 0 {
			return true
		}
	}
	return false
}
