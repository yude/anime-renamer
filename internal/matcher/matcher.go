package matcher

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
)

// MatchResult holds the result of matching a recording to Annict data.
type MatchResult struct {
	Work         *annict.Work
	Episode      *annict.Episode
	Program      *annict.Program
	Confidence   int
	Reasons      []string
	FileSubtitle string // Subtitle parsed from filename, used when Annict has no subtitle
}

// Confidence thresholds
const (
	AutoRenameThreshold = 90
)

var seriesContinuationPattern = regexp.MustCompile(`^(?:第[0-9]+(?:期|クール)|season[0-9]+|[0-9]+(?:st|nd|rd|th)(?:season|シーズン)|シーズン[0-9]+|[0-9]+期)`)

var episodeNumberTextPattern = regexp.MustCompile(`(?i)^(?:第\s*([0-9]+)\s*話|#\s*([0-9]+)|episode\s*([0-9]+)|([0-9]+))$`)

// Season mapping from month to Annict season name. Each season is exactly
// a 3-month cour: winter=Jan-Mar, spring=Apr-Jun, summer=Jul-Sep,
// autumn=Oct-Dec.
func monthToSeason(month time.Month) string {
	switch {
	case month >= 4 && month <= 6:
		return "spring"
	case month >= 7 && month <= 9:
		return "summer"
	case month >= 10:
		return "autumn"
	default: // 1, 2, 3
		return "winter"
	}
}

// SeasonYearFromMonth returns the Annict season year for a given date. Annict
// labels each January-March cour as winter of that same calendar year.
func SeasonYearFromMonth(t time.Time) int {
	return t.Year()
}

// Match attempts to match parsed metadata against Annict data.
func Match(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, programsByWork map[int][]annict.Program) *MatchResult {
	return match(meta, works, episodesByWork, programsByWork, false)
}

// MatchRelated is the retry path for an exact base work whose episode could
// not be found. It additionally considers explicitly named later seasons, but
// still excludes movies, OVAs, and specials that merely share a title prefix.
func MatchRelated(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, programsByWork map[int][]annict.Program) *MatchResult {
	return match(meta, works, episodesByWork, programsByWork, true)
}

func match(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, programsByWork map[int][]annict.Program, includeRelated bool) *MatchResult {
	if len(works) == 0 {
		return nil
	}

	// Step 1: Find matching works
	candidateWorks := MatchingWorks(meta.WorkTitle, works)
	if includeRelated {
		candidateWorks = MatchingRelatedWorks(meta.WorkTitle, works)
	}
	if len(candidateWorks) == 0 {
		return nil
	}

	// If multiple candidates, try to narrow down by season
	work := candidateWorks[0]
	// The episode number to actually look up in work's episode list. Equal
	// to meta.EpisodeNumber unless narrowByEpisodeNumber resolves the match
	// via its multi-cour offset heuristic, in which case it's the
	// offset-adjusted number within the resolved (2nd-cour) work.
	episodeNumberForMatch := meta.EpisodeNumber
	if len(candidateWorks) > 1 {
		if !meta.RecordedDate.IsZero() {
			seasonYear := SeasonYearFromMonth(meta.RecordedDate)
			seasonName := monthToSeason(meta.RecordedDate.Month())
			seasonStr := fmt.Sprintf("%d-%s", seasonYear, seasonName)

			narrowed := narrowBySeason(candidateWorks, seasonYear, seasonName)
			if len(narrowed) == 1 {
				work = narrowed[0]
			} else if len(narrowed) > 0 {
				// Still multiple, try narrowing by episode number range
				if meta.EpisodeNumber > 0 {
					epMatched := narrowByEpisodeNumber(narrowed, meta.EpisodeNumber, meta.Subtitle, episodesByWork)
					if epMatched != nil {
						work = *epMatched.Work
						episodeNumberForMatch = epMatched.EpisodeNumber
					} else {
						return &MatchResult{
							Confidence: 0,
							Reasons: []string{
								fmt.Sprintf("%d件のWorks候補があり一意に特定できません (season=%s): %s",
									len(candidateWorks), seasonStr, workTitles(candidateWorks)),
							},
						}
					}
				} else {
					return &MatchResult{
						Confidence: 0,
						Reasons: []string{
							fmt.Sprintf("%d件のWorks候補があり一意に特定できません (season=%s): %s",
								len(candidateWorks), seasonStr, workTitles(candidateWorks)),
						},
					}
				}
			} else {
				// Season filtering removed all candidates, fall back to original
				if meta.EpisodeNumber > 0 {
					epMatched := narrowByEpisodeNumber(candidateWorks, meta.EpisodeNumber, meta.Subtitle, episodesByWork)
					if epMatched != nil {
						work = *epMatched.Work
						episodeNumberForMatch = epMatched.EpisodeNumber
					} else {
						return &MatchResult{
							Confidence: 0,
							Reasons: []string{
								fmt.Sprintf("%d件のWorks候補があり一意に特定できません (season=%s): %s",
									len(candidateWorks), seasonStr, workTitles(candidateWorks)),
							},
						}
					}
				} else {
					return &MatchResult{
						Confidence: 0,
						Reasons: []string{
							fmt.Sprintf("%d件のWorks候補があり一意に特定できません (season=%s): %s",
								len(candidateWorks), seasonStr, workTitles(candidateWorks)),
						},
					}
				}
			}
		} else if meta.EpisodeNumber > 0 {
			// No date, try narrowing by episode number
			epMatched := narrowByEpisodeNumber(candidateWorks, meta.EpisodeNumber, meta.Subtitle, episodesByWork)
			if epMatched != nil {
				work = *epMatched.Work
				episodeNumberForMatch = epMatched.EpisodeNumber
			} else {
				return &MatchResult{
					Confidence: 0,
					Reasons: []string{
						fmt.Sprintf("%d件のWorks候補があり一意に特定できません: %s",
							len(candidateWorks), workTitles(candidateWorks)),
					},
				}
			}
		} else {
			return &MatchResult{
				Confidence: 0,
				Reasons: []string{
					fmt.Sprintf("%d件のWorks候補があり一意に特定できません: %s",
						len(candidateWorks), workTitles(candidateWorks)),
				},
			}
		}
	}

	result := &MatchResult{Work: &work}

	// Step 2: Find matching episode
	episodes := episodesByWork[work.ID]
	if meta.EpisodeNumber > 0 && len(episodes) > 0 {
		episode := findMatchingEpisode(episodeNumberForMatch, meta.Subtitle, episodes)
		if episode != nil {
			result.Episode = episode
			result.Confidence += 40
			result.Reasons = append(result.Reasons, "work title match")

			result.Confidence += 30
			matchedNumber, _ := EpisodeNumber(episode)
			if matchedNumber == episodeNumberForMatch {
				result.Reasons = append(result.Reasons, fmt.Sprintf("episode number %d matched", episodeNumberForMatch))
			} else {
				result.Reasons = append(result.Reasons, fmt.Sprintf("unique subtitle mapped file episode %d to Annict episode %d", episodeNumberForMatch, matchedNumber))
			}

			if meta.Subtitle == "" {
				result.Confidence += 20
				result.Reasons = append(result.Reasons, "no subtitle in file, episode matched")
			} else if episode.Title == "" {
				result.Confidence += 20
				result.FileSubtitle = meta.Subtitle
				result.Reasons = append(result.Reasons, "subtitle in file but not in annict")
			} else if normalize.Compare(episode.Title, meta.Subtitle) {
				result.Confidence += 20
				result.Reasons = append(result.Reasons, "subtitle exact match")
			} else if subtitlesEquivalentForScoring(episode.Title, meta.Subtitle) {
				result.Confidence += 20
				result.Reasons = append(result.Reasons, fmt.Sprintf("subtitle normalized match: annict=%q, file=%q", episode.Title, meta.Subtitle))
			} else if subtitlePartialMatch(episode.Title, meta.Subtitle) {
				result.Confidence += 10
				result.Reasons = append(result.Reasons, fmt.Sprintf("subtitle partial match: annict=%q, file=%q", episode.Title, meta.Subtitle))
			} else {
				result.Reasons = append(result.Reasons, fmt.Sprintf("subtitle mismatch: annict=%q, file=%q", episode.Title, meta.Subtitle))
			}
		} else {
			result.Confidence += 40
			result.Reasons = append(result.Reasons, "work title match")
			result.Reasons = append(result.Reasons, fmt.Sprintf("episode %d not found in %d episodes", episodeNumberForMatch, len(episodes)))
		}
	} else if meta.EpisodeNumber > 0 && len(episodes) == 0 {
		// Work matched but episodes unavailable (API error or not fetched)
		result.Confidence += 40
		result.Reasons = append(result.Reasons, "work title match (episodes unavailable)")
	} else if meta.EpisodeNumber == 0 {
		result.Confidence += 40
		result.Reasons = append(result.Reasons, "work title match (no episode number in file)")
	}

	// Step 3: Program date verification
	if result.Episode != nil && !meta.RecordedDate.IsZero() {
		programs := programsByWork[work.ID]
		if len(programs) > 0 {
			program := findMatchingProgram(meta.RecordedDate, result.Episode.ID, programs)
			if program != nil {
				result.Program = program
				result.Confidence += 10
				result.Reasons = append(result.Reasons, "program schedule match")
			} else {
				result.Reasons = append(result.Reasons, "no program found matching recording date")
			}
		}
	}

	return result
}

// MatchingWorks returns the Annict works that can match the given parsed
// title. Callers may use it before fetching episodes so fuzzy API results that
// Match would reject do not trigger unnecessary follow-up requests.
func MatchingWorks(title string, works []annict.Work) []annict.Work {
	var matches []annict.Work
	normalized := normalize.NormalizeTitleForMatch(title)

	for _, w := range works {
		if normalize.NormalizeTitleForMatch(w.Title) == normalized {
			matches = append(matches, w)
		}
	}

	// Fallback: substring match if no exact match
	if len(matches) == 0 {
		for _, w := range works {
			wNorm := normalize.NormalizeTitleForMatch(w.Title)
			if len(wNorm) > 0 && len(normalized) > 0 {
				if contains(wNorm, normalized) || contains(normalized, wNorm) {
					matches = append(matches, w)
				}
			}
		}
	}

	return matches
}

// MatchingRelatedWorks expands an exact base-title match with only explicit
// season/cour continuations. Callers use it after the base work failed to
// provide the requested episode, never as the first-pass candidate set.
func MatchingRelatedWorks(title string, works []annict.Work) []annict.Work {
	matches := MatchingWorks(title, works)
	if len(matches) == 0 {
		return nil
	}
	baseTitle := normalize.NormalizeTitleForMatch(title)
	seen := make(map[int]bool, len(matches))
	for _, work := range matches {
		seen[work.ID] = true
	}
	for _, work := range works {
		if seen[work.ID] {
			continue
		}
		workTitle := normalize.NormalizeTitleForMatch(work.Title)
		if strings.HasPrefix(workTitle, baseTitle) && seriesContinuationPattern.MatchString(strings.TrimPrefix(workTitle, baseTitle)) {
			matches = append(matches, work)
		}
	}
	return matches
}

// narrowBySeason filters works by season year and name.
func narrowBySeason(works []annict.Work, seasonYear int, seasonName string) []annict.Work {
	seasonPrefix := fmt.Sprintf("%d-%s", seasonYear, seasonName)
	var result []annict.Work
	for _, w := range works {
		if strings.HasPrefix(w.SeasonName, seasonPrefix) {
			result = append(result, w)
		}
	}
	return result
}

// episodeNumberNarrowing is the result of narrowByEpisodeNumber: which work
// matched, and the episode number to actually look up within that work's
// episode list (equal to the input episodeNum, unless the multi-cour offset
// heuristic below fired, in which case it's the offset-adjusted number).
type episodeNumberNarrowing struct {
	Work          *annict.Work
	EpisodeNumber int
}

// narrowByEpisodeNumber returns the single work whose episode number or unique
// normalized subtitle identifies the recording.
// For multi-cour works (e.g., "鎧真伝サムライトルーパー" + "鎧真伝サムライトルーパー 第2クール"),
// if the episode number exceeds the first cour's count, tries to match against the 2nd cour
// with an offset.
func narrowByEpisodeNumber(works []annict.Work, episodeNum int, subtitle string, episodesByWork map[int][]annict.Episode) *episodeNumberNarrowing {
	var numberMatch *annict.Work
	var numberAndSubtitleMatch *episodeNumberNarrowing
	numberAmbiguous := false
	subtitleMatches := make([]episodeNumberNarrowing, 0, 1)
	for i := range works {
		episodes := episodesByWork[works[i].ID]
		for j := range episodes {
			effectiveNumber, numberOK := EpisodeNumber(&episodes[j])
			subtitleOK := subtitle != "" && episodes[j].Title != "" && subtitlesEquivalent(episodes[j].Title, subtitle)
			if subtitleOK && numberOK {
				subtitleMatches = append(subtitleMatches, episodeNumberNarrowing{Work: &works[i], EpisodeNumber: effectiveNumber})
			}
			if numberOK && effectiveNumber == episodeNum {
				if subtitleOK {
					match := episodeNumberNarrowing{Work: &works[i], EpisodeNumber: effectiveNumber}
					if numberAndSubtitleMatch != nil && numberAndSubtitleMatch.Work.ID != works[i].ID {
						return nil
					}
					numberAndSubtitleMatch = &match
				}
				if numberMatch != nil && numberMatch.ID != works[i].ID {
					numberAmbiguous = true
				} else {
					numberMatch = &works[i]
				}
			}
		}
	}
	if numberAndSubtitleMatch != nil {
		return numberAndSubtitleMatch
	}
	if numberMatch != nil && !numberAmbiguous {
		return &episodeNumberNarrowing{Work: numberMatch, EpisodeNumber: episodeNum}
	}
	// Some EPGs use a continuous series number while Annict splits later
	// parts into another work whose local episode numbers restart at 1. A
	// unique exact-normalized subtitle identifies both the work and its local
	// episode number without guessing an offset.
	if len(subtitleMatches) == 1 {
		return &subtitleMatches[0]
	}
	if numberAmbiguous {
		return nil
	}

	// If no direct match, try to find a "第Nクール" variant with offset matching
	if len(works) > 1 {
		// Find the base work (longest episode list) and the kour variant
		var baseWork *annict.Work
		var kourWork *annict.Work
		maxEpisodes := 0
		for i := range works {
			epCount := len(episodesByWork[works[i].ID])
			if epCount > maxEpisodes {
				maxEpisodes = epCount
				baseWork = &works[i]
			}
		}
		// Find the "第Nクール" variant
		for i := range works {
			if baseWork != nil && works[i].ID != baseWork.ID && strings.Contains(works[i].Title, "第") && strings.Contains(works[i].Title, "クール") {
				kourWork = &works[i]
				break
			}
		}
		if kourWork != nil && baseWork != nil {
			baseEpisodes := episodesByWork[baseWork.ID]
			if len(baseEpisodes) > 0 {
				// Find the max episode number in the base work
				maxBaseEp := 0
				for j := range baseEpisodes {
					if n, ok := EpisodeNumber(&baseEpisodes[j]); ok && n > maxBaseEp {
						maxBaseEp = n
					}
				}
				// If the file's episode number exceeds the base's max, try matching with offset
				if episodeNum > maxBaseEp {
					offset := episodeNum - maxBaseEp
					kourEpisodes := episodesByWork[kourWork.ID]
					for j := range kourEpisodes {
						if episodeNumberMatches(&kourEpisodes[j], offset) {
							return &episodeNumberNarrowing{Work: kourWork, EpisodeNumber: offset}
						}
					}
				}
			}
		}
	}

	return nil
}

// workTitles returns a comma-separated list of work titles.
func workTitles(works []annict.Work) string {
	titles := make([]string, len(works))
	for i, w := range works {
		titles[i] = fmt.Sprintf("%q", w.Title)
	}
	return strings.Join(titles, ", ")
}

// EpisodeNumber returns the positive integer number that the CLI can safely
// match and place in a filename. Annict represents Number as a float because
// special episodes can use fractional values; those must not be truncated to
// an unrelated integer episode. SortNumber is only a fallback when Number is
// absent, not when it is present but unsupported.
func EpisodeNumber(e *annict.Episode) (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.Number != nil {
		number := *e.Number
		if number <= 0 || math.Trunc(number) != number {
			return 0, false
		}
		integer := int(number)
		if integer <= 0 || float64(integer) != number {
			return 0, false
		}
		return integer, true
	}
	if strings.TrimSpace(e.NumberText) != "" {
		matches := episodeNumberTextPattern.FindStringSubmatch(normalize.Normalize(strings.TrimSpace(e.NumberText)))
		if matches == nil {
			return 0, false
		}
		for _, digits := range matches[1:] {
			if digits == "" {
				continue
			}
			number, err := strconv.Atoi(digits)
			return number, err == nil && number > 0
		}
		return 0, false
	}
	return e.SortNumber, e.SortNumber > 0
}

// episodeNumberMatches reports whether an episode's effective number (its
// Number if set, otherwise its SortNumber) equals the given number.
func episodeNumberMatches(e *annict.Episode, number int) bool {
	episodeNumber, ok := EpisodeNumber(e)
	return ok && episodeNumber == number
}

// findMatchingEpisode finds an episode matching the given number and subtitle.
func findMatchingEpisode(number int, subtitle string, episodes []annict.Episode) *annict.Episode {
	var numberMatch *annict.Episode
	var numberAndSubtitleMatch *annict.Episode
	var subtitleMatch *annict.Episode
	subtitleAmbiguous := false

	for i := range episodes {
		e := &episodes[i]

		if episodeNumberMatches(e, number) {
			if numberMatch == nil {
				numberMatch = e
			}
			if subtitle != "" && e.Title != "" && subtitlesEquivalent(e.Title, subtitle) {
				if numberAndSubtitleMatch == nil {
					numberAndSubtitleMatch = e
				}
			}
		}
		_, validEpisodeNumber := EpisodeNumber(e)
		if validEpisodeNumber && subtitle != "" && e.Title != "" && subtitlesEquivalent(e.Title, subtitle) {
			if subtitleMatch != nil && subtitleMatch.ID != e.ID {
				subtitleAmbiguous = true
			} else {
				subtitleMatch = e
			}
		}
	}

	// Prefer exact number+subtitle match
	if numberAndSubtitleMatch != nil {
		return numberAndSubtitleMatch
	}
	// Fall back to number-only match
	if numberMatch != nil {
		return numberMatch
	}
	if subtitleMatch != nil && !subtitleAmbiguous {
		return subtitleMatch
	}
	return nil
}

// findMatchingProgram finds a program matching the recording date and episode.
func findMatchingProgram(date time.Time, episodeID int, programs []annict.Program) *annict.Program {
	dateStr := date.Format("2006-01-02")

	var best *annict.Program
	bestScore := -1

	for i := range programs {
		p := &programs[i]
		score := 0

		// When the expected episode is known, a schedule entry linked to a
		// different (or missing) episode cannot verify the match merely by
		// airing on the same date. This matters for works with multiple
		// broadcasts in the fetched date window.
		if episodeID > 0 && p.Episode.ID != episodeID {
			continue
		}
		if episodeID > 0 {
			score += 10
		}

		programDate := p.StartedAt.In(time.FixedZone("JST", 9*60*60)).Format("2006-01-02")
		if programDate == dateStr {
			score += 10
		}

		if !p.IsRebroadcast {
			score += 1
		}

		if score > bestScore {
			bestScore = score
			best = p
		}
	}

	if bestScore >= 10 {
		return best
	}
	return nil
}

// subtitlePartialMatch checks if subtitles are similar but not identical.
// Handles common differences like full-width/half-width slashes.
func subtitlePartialMatch(a, b string) bool {
	na := normalize.Normalize(a)
	nb := normalize.Normalize(b)
	if na == nb {
		return true
	}
	// Check if one contains the other, but only once both sides are long
	// enough that a shared substring is meaningful. Must count runes, not
	// bytes: a single Japanese character is already 3 bytes, so a byte
	// length check here would let e.g. one shared kanji between two
	// otherwise-unrelated subtitles register as a "partial match".
	if utf8.RuneCountInString(na) > 2 && utf8.RuneCountInString(nb) > 2 {
		if strings.Contains(na, nb) || strings.Contains(nb, na) {
			return true
		}
	}
	return false
}

func subtitlesEquivalent(a, b string) bool {
	na := normalize.NormalizeSubtitleForMatch(a)
	nb := normalize.NormalizeSubtitleForMatch(b)
	return na != "" && nb != "" && na == nb
}

// subtitlesEquivalentForScoring permits a minor EPG omission only after the
// work and integer episode number have already selected one Annict episode.
// Candidate selection deliberately continues to use subtitlesEquivalent.
func subtitlesEquivalentForScoring(a, b string) bool {
	na := normalize.NormalizeSubtitleForMatch(a)
	nb := normalize.NormalizeSubtitleForMatch(b)
	return na != "" && nb != "" && (na == nb || oneRuneInsertionApart([]rune(na), []rune(nb)))
}

// oneRuneInsertionApart tolerates one omitted or duplicated character only in
// long subtitles. It intentionally rejects substitutions and short qualifiers
// such as 前編/後編, which are too semantically significant to blur.
func oneRuneInsertionApart(a, b []rune) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b) < 10 || len(b)-len(a) != 1 {
		return false
	}
	for i, j := 0, 0; i < len(a); i, j = i+1, j+1 {
		if a[i] == b[j] {
			continue
		}
		j++
		if j >= len(b) || a[i] != b[j] {
			return false
		}
	}
	return true
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
