package matcher

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
)

type RecordingEpisode struct {
	Label    string
	Subtitle string
}

var fractionalNumberText = regexp.MustCompile(`(?i)^(?:第\s*([0-9]+\.[0-9]+)\s*話|#\s*([0-9]+\.[0-9]+)|(?:episode|ep)[.\s]*([0-9]+\.[0-9]+)|([0-9]+\.[0-9]+))$`)
var fractionalNumberMention = regexp.MustCompile(`[0-9]+\.[0-9]+`)

func fractionalTextLabel(text string) (string, bool) {
	m := fractionalNumberText.FindStringSubmatch(normalize.Normalize(strings.TrimSpace(text)))
	if m == nil {
		return "", false
	}
	for _, raw := range m[1:] {
		if raw != "" {
			return parser.CanonicalFractionalNumber(raw)
		}
	}
	return "", false
}

func hasFractionalIdentity(e *annict.Episode) bool {
	if e == nil {
		return false
	}
	if _, ok := fractionalTextLabel(e.NumberText); ok {
		return true
	}
	if fractionalNumberMention.MatchString(normalize.Normalize(e.NumberText)) {
		return true
	}
	if e.Number != nil && math.Trunc(*e.Number) != *e.Number {
		// A public integer label can override an internal fractional Number.
		// Preserve the existing 第11話 / Number=10.5 interpretation.
		_, integerLabel := episodeNumberFromText(e.NumberText)
		return !integerLabel
	}
	return false
}

func FractionalEpisodeLabel(e *annict.Episode) (string, bool) {
	if e == nil {
		return "", false
	}
	text, textOK := fractionalTextLabel(e.NumberText)
	if e.Number != nil && math.Trunc(*e.Number) != *e.Number {
		numeric, ok := parser.CanonicalFractionalNumber(strconv.FormatFloat(*e.Number, 'f', -1, 64))
		if !ok || (textOK && text != numeric) {
			return "", false
		}
		if e.NumberText != "" && !textOK {
			return "", false
		}
		return numeric, true
	}
	return text, textOK
}

func MatchResultEpisodeLabel(result *MatchResult) (string, bool) {
	if result == nil || result.Standalone {
		return "", false
	}
	if result.RecordingEpisode != nil {
		label, ok := parser.CanonicalFractionalNumber(result.RecordingEpisode.Label)
		return label, ok && label == result.RecordingEpisode.Label && result.Episode == nil
	}
	if result.OutputEpisodeLabel != "" {
		label, ok := FractionalEpisodeLabel(result.Episode)
		return label, ok && label == result.OutputEpisodeLabel
	}
	number, ok := MatchResultEpisodeNumber(result)
	return strconv.Itoa(number), ok
}

func matchFractional(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode) *MatchResult {
	label, ok := parser.CanonicalFractionalNumber(meta.EpisodeLabel)
	if !ok || label != meta.EpisodeLabel || meta.EpisodeNumber != 0 || strings.TrimSpace(meta.Subtitle) == "" {
		return nil
	}
	works = MatchingWorks(meta.WorkTitle, works)
	if len(works) != 1 || normalize.NormalizeTitleForMatch(works[0].Title) != normalize.NormalizeTitleForMatch(meta.WorkTitle) {
		return nil
	}
	work := works[0]
	var matched *annict.Episode
	for i := range episodesByWork[work.ID] {
		episode := &episodesByWork[work.ID][i]
		candidate, ok := FractionalEpisodeLabel(episode)
		if !ok || candidate != label {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = episode
	}
	if matched == nil || (meta.Subtitle != "" && !subtitlesEquivalent(matched.Title, meta.Subtitle)) {
		return nil
	}
	return &MatchResult{Work: &work, Episode: matched, OutputEpisodeLabel: label, Confidence: AutoRenameThreshold,
		Reasons: []string{"work title uniquely matched Annict", fmt.Sprintf("explicit fractional episode %s matched Annict without rounding", label)}}
}
