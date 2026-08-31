package matcher

import (
	"fmt"
	"math"
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
	if len(works) == 0 {
		return nil
	}

	// Step 1: Find matching works
	candidateWorks := findMatchingWorks(meta.WorkTitle, works)
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
					epMatched := narrowByEpisodeNumber(narrowed, meta.EpisodeNumber, episodesByWork)
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
					epMatched := narrowByEpisodeNumber(candidateWorks, meta.EpisodeNumber, episodesByWork)
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
			epMatched := narrowByEpisodeNumber(candidateWorks, meta.EpisodeNumber, episodesByWork)
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
			result.Reasons = append(result.Reasons, fmt.Sprintf("episode number %d matched", episodeNumberForMatch))

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
			} else if subtitlesEquivalent(episode.Title, meta.Subtitle) {
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

// findMatchingWorks finds works matching the given title.
func findMatchingWorks(title string, works []annict.Work) []annict.Work {
	var matches []annict.Work
	normalized := normalize.NormalizeForSearch(title)

	for _, w := range works {
		if normalize.NormalizeForSearch(w.Title) == normalized {
			matches = append(matches, w)
		}
	}

	// Fallback: substring match if no exact match
	if len(matches) == 0 {
		for _, w := range works {
			wNorm := normalize.NormalizeForSearch(w.Title)
			if len(wNorm) > 0 && len(normalized) > 0 {
				if contains(wNorm, normalized) || contains(normalized, wNorm) {
					matches = append(matches, w)
				}
			}
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

// narrowByEpisodeNumber returns the single work whose episodes contain the given number.
// For multi-cour works (e.g., "鎧真伝サムライトルーパー" + "鎧真伝サムライトルーパー 第2クール"),
// if the episode number exceeds the first cour's count, tries to match against the 2nd cour
// with an offset.
func narrowByEpisodeNumber(works []annict.Work, episodeNum int, episodesByWork map[int][]annict.Episode) *episodeNumberNarrowing {
	var match *annict.Work
	for i := range works {
		episodes := episodesByWork[works[i].ID]
		for j := range episodes {
			if episodeNumberMatches(&episodes[j], episodeNum) {
				if match != nil {
					return nil // ambiguous
				}
				match = &works[i]
				break
			}
		}
	}
	if match != nil {
		return &episodeNumberNarrowing{Work: match, EpisodeNumber: episodeNum}
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
	}

	// Prefer exact number+subtitle match
	if numberAndSubtitleMatch != nil {
		return numberAndSubtitleMatch
	}
	// Fall back to number-only match
	if numberMatch != nil {
		return numberMatch
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

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
