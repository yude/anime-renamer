package parser

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/yude/anime-renamer/internal/normalize"
)

var fractionalNumber = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}$`)
var fractionalMarkers = []*regexp.Regexp{
	regexp.MustCompile(`[#＃♯][\s\x{3000}]*([0-9０-９]+[.．][0-9０-９]+)`),
	regexp.MustCompile(`(?:第[\s\x{3000}]*)?([0-9０-９]+[.．][0-9０-９]+)[\s\x{3000}]*話`),
	regexp.MustCompile(`(?i)ep[.．\s\x{3000}]*([0-9０-９]+[.．][0-9０-９]+)`),
}
var fractionalContinuation = regexp.MustCompile(`^[\s\x{3000}]*[.．,，、/&＆／~〜～－―ー-][\s\x{3000}]*(?:第|[#＃♯])?[\s\x{3000}]*[0-9０-９]+`)

// CanonicalFractionalNumber uses decimal text, not floating-point arithmetic.
// Integer-valued decimals, exponents, signs, and excessive precision are not
// accepted as special-episode identities.
func CanonicalFractionalNumber(raw string) (string, bool) {
	raw = normalize.Normalize(strings.TrimSpace(raw))
	if !fractionalNumber.MatchString(raw) {
		return "", false
	}
	whole, fraction, _ := strings.Cut(raw, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return "", false
	}
	return whole + "." + fraction, true
}

func parseFractionalFilename(name string, date time.Time) (*RecordingMetadata, bool, error) {
	m := earliestEpisodeMatch(name, fractionalMarkers...)
	if m == nil {
		return nil, false, nil
	}
	label, ok := CanonicalFractionalNumber(name[m[2]:m[3]])
	if !ok {
		return nil, true, fmt.Errorf("%w: invalid fractional label", ErrUnsupportedEpisode)
	}
	after := name[m[1]:]
	if after != "" && !strings.HasSuffix(name[m[0]:m[1]], "話") {
		first := []rune(after)[0]
		if !unicode.IsSpace(first) && first != '「' && first != '『' {
			return nil, true, fmt.Errorf("%w: unsupported fractional continuation", ErrAmbiguousEpisode)
		}
	}
	masked := name[:m[0]] + after
	if fractionalContinuation.MatchString(after) || ambiguousEpisodeNotation(masked) != "" || earliestEpisodeMatch(masked, epPattern2, epPattern1, episodeWordPattern, arabicEpisodePattern, kanjiEpisodePattern, bareEpisodePattern) != nil {
		return nil, true, fmt.Errorf("%w: fractional recording names multiple episodes", ErrAmbiguousEpisode)
	}
	title := unwrapEPGTitle(stripTrailingMetadataTags(name[:m[0]]))
	title = normalize.CollapseSpaces(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(title), "_＿▼")))
	if title == "" {
		return nil, true, fmt.Errorf("empty fractional work title")
	}
	subtitle := firstQuotedContent(after)
	if subtitle == "" {
		subtitle = stripTrailingMetadataTags(strings.TrimSpace(after))
	}
	return &RecordingMetadata{WorkTitle: title, EpisodeLabel: label, Subtitle: subtitle, RecordedDate: date}, true, nil
}
