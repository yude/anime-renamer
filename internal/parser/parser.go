package parser

import (
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
	// Matches date in parentheses at end: (YYYYMMDD)
	datePattern = regexp.MustCompile(`\((\d{8})\)\s*$`)

	// Episode patterns matching both full-width and half-width forms.
	// These run against the ORIGINAL string (pre-normalization).
	// ep.7, ep．7, EP.7, EP．7, ep 7, ep. 7, ep7, ep\u30007
	epPattern1 = regexp.MustCompile(`(?i)ep[.．\s\x{3000}]*(\d+)`)
	// #7, #07
	epPattern2 = regexp.MustCompile(`#(\d+)`)

	// 第N話, 第N幕, 第N番 (arabic digits, suffix required to avoid 第2クール false positive)
	arabicEpisodePattern = regexp.MustCompile(`第(\d+)([話幕番])`)
	// 第三話, 第五幕, 第四番, 第十七話 (kanji digits)
	kanjiEpisodePattern = regexp.MustCompile(`第([〇一二三四五六七八九十百千]+)([話幕番怪])`)

	// Metadata tag patterns to strip from filenames (SCRename rp1 equivalent).
	metadataTagPatterns = []*regexp.Regexp{
		// Full-width bracket metadata at start: 【ANiMAZiNG!!!】, 【字幕】etc.
		regexp.MustCompile(`^[\s]*【[^】]*】`),
		// Angle bracket metadata at start: ＜アニメギルド＞
		regexp.MustCompile(`^[\s]*＜[^＞]*＞`),
		// Single-character bracket tags: [字], [新], [再], [無], [多], [SS], [解], [終]
		regexp.MustCompile(`^[\s]*(?:\[字\]|\[新\]|\[再\]|\[無\]|\[多\]|\[SS\]|\[解\]|\[終\])`),
		// Common recording prefixes at start
		regexp.MustCompile(`^[\s]*(?:無料≫|無料》)`),
		regexp.MustCompile(`^[\s]*BS11(?:ガンダム|アニメ)`),
		// Channel slot prefixes: アニメA, アニメB, etc. (BS channel names like BS11's アニメA)
		regexp.MustCompile(`^[\s]*アニメ[A-Z]・?`),
	}
)

// kanjiDigits maps single kanji digits to their integer values.
var kanjiDigits = map[rune]int{
	'〇': 0,
	'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
	'六': 6, '七': 7, '八': 8, '九': 9, '十': 10,
}

// kanjiToInt converts a kanji numeral string to an integer.
// Supports: 一〜十, 十一〜十九, 二十〜九十九, 百, 千.
func kanjiToInt(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}

	runes := []rune(s)
	result := 0
	current := 0
	hasKanji := false

	for _, r := range runes {
		v, ok := kanjiDigits[r]
		if !ok {
			return 0, false
		}
		hasKanji = true

		switch r {
		case '十':
			if current == 0 {
				current = 1
			}
			result += current * 10
			current = 0
		case '百':
			if current == 0 {
				current = 1
			}
			result += current * 100
			current = 0
		case '千':
			if current == 0 {
				current = 1
			}
			result += current * 1000
			current = 0
		default:
			current = v
		}
	}
	result += current

	if !hasKanji {
		return 0, false
	}
	return result, true
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
		d, err := time.ParseInLocation("20060102", m[1], time.FixedZone("JST", 9*60*60))
		if err != nil {
			return nil, fmt.Errorf("invalid date %q: %w", m[1], err)
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

	// 4. Extract episode number first (needed to locate subtitle position)
	episodeNumber := 0
	epStart := -1
	epEnd := -1

	// Try patterns in priority order
	if m := epPattern1.FindStringSubmatchIndex(name); m != nil {
		episodeNumber, _ = strconv.Atoi(name[m[2]:m[3]])
		epStart = m[0]
		epEnd = m[1]
	} else if m := epPattern2.FindStringSubmatchIndex(name); m != nil {
		episodeNumber, _ = strconv.Atoi(name[m[2]:m[3]])
		epStart = m[0]
		epEnd = m[1]
	} else if m := arabicEpisodePattern.FindStringSubmatchIndex(name); m != nil {
		episodeNumber, _ = strconv.Atoi(name[m[2]:m[3]])
		epStart = m[0]
		epEnd = m[1]
	} else if m := kanjiEpisodePattern.FindStringSubmatchIndex(name); m != nil {
		kanjiNum := name[m[2]:m[3]]
		if v, ok := kanjiToInt(kanjiNum); ok {
			episodeNumber = v
			epStart = m[0]
			epEnd = m[1]
		}
	}

	// 5. Extract subtitle from 「...」 AFTER episode marker (not before)
	subtitle := ""
	if epEnd >= 0 {
		// Look for 「」 after the episode marker
		afterEp := name[epEnd:]
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
	} else {
		// No episode marker found — no subtitle extraction
	}

	// 6. Extract work title: everything before the episode pattern
	workTitle := name
	if epStart >= 0 {
		workTitle = name[:epStart]
	}

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
		changed := false
		for _, re := range metadataTagPatterns {
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
