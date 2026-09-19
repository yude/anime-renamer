package matcher

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"golang.org/x/text/unicode/norm"
)

// MatchResult holds the result of matching a recording to Annict data.
type MatchResult struct {
	Work                *annict.Work
	Episode             *annict.Episode
	Program             *annict.Program
	Confidence          int
	Reasons             []string
	FileSubtitle        string // Subtitle parsed from filename, used when Annict has no subtitle
	OutputEpisodeNumber int    // Explicit public label matched in the filename, when different from Annict's local Number
	OutputNumberSet     bool   // Distinguishes an explicitly verified episode zero from no override
}

// Confidence thresholds
const (
	AutoRenameThreshold = 90
)

var seriesContinuationPattern = regexp.MustCompile(`^(?:第?[0-9]+(?:期|クール(?:目)?)|season[0-9]+|[0-9]+(?:st|nd|rd|th)(?:season|シーズン)|シーズン[0-9]+|part[0-9]+|netflixオリジナル|tv放送)`)
var specialWorkContinuationPattern = regexp.MustCompile(`^(?:ova|oad|特別編|special)`)
var parentheticalWorkYearPattern = regexp.MustCompile(`[（(]([0-9]{4})(?:年版)?[）)]`)

var episodeNumberTextPattern = regexp.MustCompile(`(?i)^(?:第\s*([0-9]+)\s*(?:話|幕|番|怪|夜|回|局|羽|R)|#\s*([0-9]+)|episode[.\s]*([0-9]+)|sailing\s*([0-9]+)|ride[.\s]*([0-9]+)|([0-9]+))$`)
var kanjiEpisodeNumberTextPattern = regexp.MustCompile(`^第\s*([〇一二三四五六七八九十百千壱弐参肆伍陸漆捌玖拾]+)\s*(?:話|幕|番|怪|夜|回|局|羽|R)$`)
var subtitleSegmentOrdinalPrefix = regexp.MustCompile(`(?i)^(?:episode[0-9]+|其の[0-9一二三四五六七八九十]+|[a-z]:)`)
var subtitleEpisodeLabelPrefix = regexp.MustCompile(`(?i)^(?:life[.\s]*(?:[0-9]+|max(?:imum)?)(?:\s*vs\s*power[.\s]*max(?:imum)?)?|コミュ[0-9]+)`)
var zeroEpisodeLabelPattern = regexp.MustCompile(`(?i)^life[.\s]*0+(?:\s|$)`)
var spacedKatakanaReadingPattern = regexp.MustCompile(`([\p{L}\p{N}])[\s\x{3000}]+([（(][\p{Katakana}ー・･\s\x{3000}]{6,}[）)])`)
var subtitleOtherSummarySuffix = regexp.MustCompile(`[\s\x{3000}]*(?:ほか|他)(?:[\s\x{3000}]*[0-9０-９〇一二三四五六七八九十]+[\s\x{3000}]*本)?[\s\x{3000}]*$`)
var repeatedMiddleDots = regexp.MustCompile(`・{2,}`)
var trailingPartMiddleDot = regexp.MustCompile(`・(前編|後編)$`)

var subtitleOrthographyReplacer = strings.NewReplacer(
	"やがて雨は止んで", "やがて雨はやんで",
	"初めての鉱物採集", "はじめての鉱物採集",
	"るらちゃんはちやほやされたい", "るらちゃんはチヤホヤされたい",
	"イケイケゴーゴー夏休み", "いけいけゴーゴー夏休み",
	"ラ・ソレイユヘ", "ラ・ソレイユへ",
	"俺たちの戦いはこれからだ", "俺達の戦いはこれからだ",
	"柏田さんと太田君と海", "柏田さんと太田くんと海",
	"魔物の町の住人達", "魔物の町の住人たち",
	"街角ギャラクシー☆彡", "街角ギャラクシー",
	"『自由』を", "自由",
	"ビーナスライン／シェルター", "ビーナスライン・シェルター",
	"呪館 JUKAN", "呪館",
	"Chiidren's Echelon", "Children's Echelon",
	"生の代償、死の贖い", "生の代償、死の償い",
	"無限の——— —アンリミテッド／レイズ・デッド—", "無限の残骸 アンリミテッド／レイズ・デッド",
	"プリステラ攻防戦リザルト", "プリステラ攻略戦リザルト",
	"思い残した記憶って、なに？", "思い出した記憶って、なに？",
	"魔王と勇者、勧めに従い遊園地に行く", "魔王と勇者、勤めに従い遊園地に行く",
	"暗黒部室", "暗黒部屋",
	"脳汁ブシャー", "脳汁プシャー",
	"デスゲーム挑まれたけどクソゲーだった", "デスゲーム挑まれたけど・・・",
	"オプション解放おめでとうございます", "オプション開放おめでとうございます",
	"天才と凡才", "天才と凡人",
	"バン・ブ・クルセイダーズ", "BUMP-BOO-CRUSADERS",
	"飯田橋の登竜 復讐のピピ", "飯田橋の昇竜 ～復讐のピピ～",
	"晴天の霹靂", "青天の霹靂",
	"スパイ昇格試験", "スパイ昇級試験",
	"個人的にはラブコメ希望", "個人的にはラブコメ展開希望",
	"体育会実行委員", "体育祭実行委員",
	"雪侍と昴", "雪待と昴",
	"坊ちゃんとアリスと二人だけの歌", "坊ちゃんとアリスの二人だけの歌",
	"俺はひょっとして、最終話で負けヒロインの横にいるポッと出のモブキャラなのだろうか", "俺はひょっとして、最終話でヒロインの横にいるポッと出のモブキャラなのだろうか",
	"集う物達", "集う者達",
	"水着で１日", "水着の1日",
	"つながるものの", "つながるもの",
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
	if meta.RecordedDate.IsZero() {
		if year, ok := workYearFromTitle(meta.WorkTitle); ok {
			if narrowed := narrowByWorkYear(candidateWorks, year); len(narrowed) > 0 {
				candidateWorks = narrowed
			}
		}
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
			if explicitZeroEpisodeMatches(episode, meta.Subtitle) {
				result.OutputEpisodeNumber = 0
				result.OutputNumberSet = true
			} else if displayed, ok := episodeLabelNumber(episode); ok && displayed == meta.EpisodeNumber {
				result.OutputEpisodeNumber = displayed
			} else if specialSortNumberMatches(episode, meta.EpisodeNumber, meta.Subtitle) {
				result.OutputEpisodeNumber = meta.EpisodeNumber
			}
			result.Confidence += 40
			result.Reasons = append(result.Reasons, "work title match")

			result.Confidence += 30
			matchedNumber, _ := MatchResultEpisodeNumber(result)
			if matchedNumber == episodeNumberForMatch {
				result.Reasons = append(result.Reasons, fmt.Sprintf("episode number %d matched", episodeNumberForMatch))
			} else if meta.Subtitle != "" && episode.Title != "" && subtitlesEquivalent(episode.Title, meta.Subtitle) {
				result.Reasons = append(result.Reasons, fmt.Sprintf("unique subtitle mapped file episode %d to Annict episode %d", episodeNumberForMatch, matchedNumber))
			} else {
				result.Reasons = append(result.Reasons, fmt.Sprintf("local episode %d mapped to Annict episode %d", episodeNumberForMatch, matchedNumber))
			}

			if meta.Subtitle == "" {
				result.Confidence += 20
				result.Reasons = append(result.Reasons, "no subtitle in file, episode matched")
			} else if episodeTitleUnavailable(episode.Title) {
				result.Confidence += 20
				result.FileSubtitle = meta.Subtitle
				result.Reasons = append(result.Reasons, "subtitle in file but unavailable in annict")
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

		if meta.Subtitle != "" {
			episode, matches := findUniqueEpisodeBySubtitle(meta.Subtitle, episodes)
			if episode != nil {
				resolvedEpisode := episode
				matchedNumber, numberOK := EpisodeNumber(resolvedEpisode)
				if !numberOK && meta.FinalEpisode {
					if final := findFinalEpisode(work, episodes); final != nil && final.ID == episode.ID {
						resolvedEpisode = final
						matchedNumber, numberOK = EpisodeNumber(final)
					}
				}
				if numberOK {
					result.Episode = resolvedEpisode
					result.Confidence += 50
					result.Reasons = append(result.Reasons, fmt.Sprintf("unique subtitle matched Annict episode %d", matchedNumber))
				} else {
					result.Reasons = append(result.Reasons, "unique subtitle matched an episode without a safe positive integer number")
				}
			} else if matches > 1 {
				result.Reasons = append(result.Reasons, fmt.Sprintf("subtitle matched %d Annict episodes and is ambiguous", matches))
			} else {
				result.Reasons = append(result.Reasons, "subtitle did not exactly identify an Annict episode")
			}
		}

		if result.Episode == nil && meta.FinalEpisode && (meta.Subtitle == "" || genericFinalSubtitle(meta.Subtitle)) {
			if episode := findFinalEpisode(work, episodes); episode != nil {
				result.Episode = episode
				result.Confidence += 50
				matchedNumber, _ := EpisodeNumber(episode)
				result.Reasons = append(result.Reasons, fmt.Sprintf("final-episode marker matched complete Annict episode %d", matchedNumber))
				if episode.Title == "" {
					result.FileSubtitle = meta.Subtitle
				}
			} else {
				result.Reasons = append(result.Reasons, "final episode could not be identified from a complete Annict episode list")
			}
		}
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

// findUniqueEpisodeBySubtitle resolves a numberless recording only when the
// strict subtitle identity key names exactly one positive-integer episode.
// The broader scoring-only equivalences intentionally do not participate.
func findUniqueEpisodeBySubtitle(subtitle string, episodes []annict.Episode) (*annict.Episode, int) {
	var match *annict.Episode
	matches := 0
	for i := range episodes {
		if episodes[i].Title == "" || !subtitlesEquivalent(episodes[i].Title, subtitle) {
			continue
		}
		matches++
		if match == nil {
			match = &episodes[i]
		}
	}
	if matches != 1 {
		return nil, matches
	}
	return match, matches
}

func genericFinalSubtitle(subtitle string) bool {
	switch subtitleIdentityKey(subtitle) {
	case "最終話", "最終回", "最終話sp", "最終回sp", "最終話スペシャル", "最終回スペシャル":
		return true
	default:
		return false
	}
}

// findFinalEpisode returns the uniquely highest positive-integer episode only
// when Annict says the fetched list is complete. This avoids treating the
// newest known episode of an incomplete or still-populating list as a finale.
func findFinalEpisode(work annict.Work, episodes []annict.Episode) *annict.Episode {
	if work.EpisodesCount <= 0 || len(episodes) < work.EpisodesCount {
		return nil
	}
	var explicitFinal *annict.Episode
	explicitFinalAmbiguous := false
	for i := range episodes {
		switch normalize.Normalize(strings.TrimSpace(episodes[i].NumberText)) {
		case "最終話", "最終回":
			if explicitFinal != nil && explicitFinal.ID != episodes[i].ID {
				explicitFinalAmbiguous = true
			} else {
				explicitFinal = &episodes[i]
			}
		}
	}
	if explicitFinalAmbiguous {
		return nil
	}

	var final *annict.Episode
	maxNumber := 0
	minNumber := 0
	numberCount := 0
	maxSortNumber := 0
	duplicateMax := false
	duplicateNumber := false
	seenNumbers := make(map[int]bool)
	for i := range episodes {
		number, ok := EpisodeNumber(&episodes[i])
		if !ok {
			continue
		}
		numberText := normalize.Normalize(strings.TrimSpace(episodes[i].NumberText))
		if numberText != "" && episodeNumberTextPattern.FindStringSubmatch(numberText) == nil && !kanjiEpisodeNumberTextPattern.MatchString(numberText) {
			// Descriptive extras such as OVA or 総集編 can carry a numeric
			// Number in Annict but are not evidence for the TV finale.
			continue
		}
		if seenNumbers[number] {
			duplicateNumber = true
			if number == maxNumber {
				duplicateMax = true
			}
			continue
		}
		seenNumbers[number] = true
		numberCount++
		if minNumber == 0 || number < minNumber {
			minNumber = number
		}
		if episodes[i].SortNumber > maxSortNumber {
			maxSortNumber = episodes[i].SortNumber
		}
		switch {
		case number > maxNumber:
			maxNumber = number
			final = &episodes[i]
			duplicateMax = false
		case number == maxNumber:
			duplicateMax = true
		}
	}
	if explicitFinal != nil {
		if numberCount == 0 || duplicateNumber || maxNumber-minNumber+1 != numberCount || explicitFinal.SortNumber <= maxSortNumber {
			return nil
		}
		inferredNumber := float64(maxNumber + 1)
		resolved := *explicitFinal
		resolved.Number = &inferredNumber
		return &resolved
	}
	if maxNumber == 0 || duplicateMax {
		return nil
	}
	return final
}

// MatchingWorks returns the Annict works that can match the given parsed
// title. Callers may use it before fetching episodes so fuzzy API results that
// Match would reject do not trigger unnecessary follow-up requests.
func MatchingWorks(title string, works []annict.Work) []annict.Work {
	var matches []annict.Work
	trimmed := strings.TrimSpace(title)

	// Prefer an exact presentation match before punctuation-insensitive
	// normalization. Some sequel markers consist only of punctuation (for
	// example a trailing apostrophe), so normalizing first can collapse a base
	// series and its sequel into an avoidably ambiguous candidate set.
	for _, w := range works {
		if strings.TrimSpace(w.Title) == trimmed {
			matches = append(matches, w)
		}
	}
	if len(matches) > 0 {
		return matches
	}

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

// MatchingSpecialWorks returns only explicitly labelled special productions
// whose title extends the parsed base title. It is intentionally separate
// from MatchingRelatedWorks so an OVA can never compete with normal TV
// episodes during the primary match.
func MatchingSpecialWorks(title string, works []annict.Work) []annict.Work {
	baseTitle := normalize.NormalizeTitleForMatch(title)
	if baseTitle == "" {
		return nil
	}
	var matches []annict.Work
	for _, work := range works {
		workTitle := normalize.NormalizeTitleForMatch(work.Title)
		if !strings.HasPrefix(workTitle, baseTitle) {
			continue
		}
		suffix := strings.TrimPrefix(workTitle, baseTitle)
		if specialWorkContinuationPattern.MatchString(suffix) {
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

func workYearFromTitle(title string) (int, bool) {
	matches := parentheticalWorkYearPattern.FindStringSubmatch(normalize.Normalize(title))
	if len(matches) != 2 {
		return 0, false
	}
	year, err := strconv.Atoi(matches[1])
	return year, err == nil && year > 0
}

func narrowByWorkYear(works []annict.Work, year int) []annict.Work {
	seasonPrefix := strconv.Itoa(year) + "-"
	result := make([]annict.Work, 0, 1)
	for _, work := range works {
		if strings.HasPrefix(strings.ToLower(work.SeasonName), seasonPrefix) {
			result = append(result, work)
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
	var labelMatch *annict.Work
	var labelAndSubtitleMatch *episodeNumberNarrowing
	labelAmbiguous := false
	var numberMatch *annict.Work
	var numberAndSubtitleMatch *episodeNumberNarrowing
	numberAmbiguous := false
	subtitleMatches := make([]episodeNumberNarrowing, 0, 1)
	for i := range works {
		episodes := episodesByWork[works[i].ID]
		for j := range episodes {
			effectiveNumber, numberOK := EpisodeNumber(&episodes[j])
			labelNumber, labelOK := episodeLabelNumber(&episodes[j])
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
			if labelOK && labelNumber == episodeNum {
				if subtitleOK {
					match := episodeNumberNarrowing{Work: &works[i], EpisodeNumber: effectiveNumber}
					if labelAndSubtitleMatch != nil && labelAndSubtitleMatch.Work.ID != works[i].ID {
						return nil
					}
					labelAndSubtitleMatch = &match
				}
				if labelMatch != nil && labelMatch.ID != works[i].ID {
					labelAmbiguous = true
				} else {
					labelMatch = &works[i]
				}
			}
		}
	}
	if labelAndSubtitleMatch != nil {
		return labelAndSubtitleMatch
	}
	if numberAndSubtitleMatch != nil {
		return numberAndSubtitleMatch
	}
	if labelMatch != nil && !labelAmbiguous {
		return &episodeNumberNarrowing{Work: labelMatch, EpisodeNumber: episodeNum}
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
			return episodeNumberFromText(e.NumberText)
		}
		integer := int(number)
		if integer <= 0 || float64(integer) != number {
			return episodeNumberFromText(e.NumberText)
		}
		return integer, true
	}
	if strings.TrimSpace(e.NumberText) != "" {
		return episodeNumberFromText(e.NumberText)
	}
	return e.SortNumber, e.SortNumber > 0
}

// MatchResultEpisodeNumber returns the public episode number selected for a
// filename. Annict sometimes stores a local Number while NumberText carries
// the continuous broadcast label (for example Number=5, NumberText=第44話).
// The override is populated only when that explicit label matched the input.
func MatchResultEpisodeNumber(result *MatchResult) (int, bool) {
	if result == nil || result.Episode == nil {
		return 0, false
	}
	if result.OutputNumberSet || result.OutputEpisodeNumber > 0 {
		return result.OutputEpisodeNumber, true
	}
	return EpisodeNumber(result.Episode)
}

func episodeLabelNumber(e *annict.Episode) (int, bool) {
	if e == nil || strings.TrimSpace(e.NumberText) == "" {
		return 0, false
	}
	return episodeNumberFromText(e.NumberText)
}

func episodeNumberFromText(text string) (int, bool) {
	numberText := normalize.Normalize(strings.TrimSpace(text))
	if matches := episodeNumberTextPattern.FindStringSubmatch(numberText); matches != nil {
		for _, digits := range matches[1:] {
			if digits == "" {
				continue
			}
			number, err := strconv.Atoi(digits)
			return number, err == nil && number > 0
		}
		return 0, false
	}
	if matches := kanjiEpisodeNumberTextPattern.FindStringSubmatch(numberText); matches != nil {
		number, ok := parser.ParseKanjiNumber(matches[1])
		return number, ok && number > 0
	}
	return 0, false
}

// episodeNumberMatches reports whether an episode's effective number (its
// Number if set, otherwise its SortNumber) equals the given number.
func episodeNumberMatches(e *annict.Episode, number int) bool {
	episodeNumber, ok := EpisodeNumber(e)
	return ok && episodeNumber == number
}

// findMatchingEpisode finds an episode matching the given number and subtitle.
func findMatchingEpisode(number int, subtitle string, episodes []annict.Episode) *annict.Episode {
	var zeroMatch *annict.Episode
	zeroAmbiguous := false
	var labelMatch *annict.Episode
	var labelAndSubtitleMatch *annict.Episode
	labelAmbiguous := false
	labelAndSubtitleAmbiguous := false
	var numberMatch *annict.Episode
	var numberAndSubtitleMatch *annict.Episode
	var specialSortAndSubtitleMatch *annict.Episode
	specialSortAndSubtitleAmbiguous := false
	var subtitleMatch *annict.Episode
	subtitleAmbiguous := false

	for i := range episodes {
		e := &episodes[i]
		if explicitZeroEpisodeMatches(e, subtitle) {
			if zeroMatch != nil {
				zeroAmbiguous = true
			} else {
				zeroMatch = e
			}
		}

		if labelNumber, ok := episodeLabelNumber(e); ok && labelNumber == number {
			if labelMatch != nil {
				labelAmbiguous = true
			} else {
				labelMatch = e
			}
			if subtitle != "" && e.Title != "" && subtitlesEquivalent(e.Title, subtitle) {
				if labelAndSubtitleMatch != nil {
					labelAndSubtitleAmbiguous = true
				} else {
					labelAndSubtitleMatch = e
				}
			}
		}
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
		if specialSortNumberMatches(e, number, subtitle) {
			if specialSortAndSubtitleMatch != nil {
				specialSortAndSubtitleAmbiguous = true
			} else {
				specialSortAndSubtitleMatch = e
			}
		}
	}

	// An explicit public label is stronger than a work-local Number.
	if zeroMatch != nil && !zeroAmbiguous {
		return zeroMatch
	}
	if labelAndSubtitleMatch != nil && !labelAndSubtitleAmbiguous {
		return labelAndSubtitleMatch
	}
	// Prefer exact number+subtitle match
	if numberAndSubtitleMatch != nil {
		return numberAndSubtitleMatch
	}
	if specialSortAndSubtitleMatch != nil && !specialSortAndSubtitleAmbiguous {
		return specialSortAndSubtitleMatch
	}
	// A unique exact subtitle is stronger evidence than a conflicting local
	// number. Some EPGs count an episode zero as local #1 while Annict retains
	// the official life.0/life.1 numbering. Ambiguous repeated subtitles do
	// not override the direct number match.
	if subtitleMatch != nil && !subtitleAmbiguous {
		return subtitleMatch
	}
	if labelMatch != nil && !labelAmbiguous {
		return labelMatch
	}
	// Fall back to number-only match.
	if numberMatch != nil {
		return numberMatch
	}
	// Some explicitly titled later seasons restart their EPG numbering at 1
	// while Annict retains continuous series numbers. Only map by ordinal when
	// every supported Annict episode number is a contiguous sequence starting
	// above 1; gaps or duplicates make the mapping unsafe.
	var localMatch *annict.Episode
	firstNumber := 0
	ordinal := 0
	for i := range episodes {
		effectiveNumber, ok := EpisodeNumber(&episodes[i])
		if !ok {
			continue
		}
		numberText := normalize.Normalize(strings.TrimSpace(episodes[i].NumberText))
		if numberText != "" && episodeNumberTextPattern.FindStringSubmatch(numberText) == nil && !kanjiEpisodeNumberTextPattern.MatchString(numberText) {
			// GraphQL can coerce a fractional special such as 88.5 to 88.
			// A descriptive label (for example 総集編) identifies it as a
			// non-episode entry that must not break the contiguous TV sequence.
			continue
		}
		if firstNumber == 0 {
			firstNumber = effectiveNumber
		}
		if effectiveNumber != firstNumber+ordinal {
			return nil
		}
		ordinal++
		if ordinal == number {
			localMatch = &episodes[i]
		}
	}
	if firstNumber > 1 && localMatch != nil {
		return localMatch
	}
	return nil
}

func explicitZeroEpisodeMatches(episode *annict.Episode, subtitle string) bool {
	if episode == nil || episode.Number == nil || *episode.Number != 0 || episode.Title == "" || subtitle == "" {
		return false
	}
	if !zeroEpisodeLabelPattern.MatchString(normalize.Normalize(strings.TrimSpace(episode.NumberText))) ||
		!zeroEpisodeLabelPattern.MatchString(normalize.Normalize(strings.TrimSpace(subtitle))) {
		return false
	}
	return subtitlesEquivalent(episode.Title, subtitle)
}

func specialSortNumberMatches(episode *annict.Episode, number int, subtitle string) bool {
	if episode == nil || episode.Number != nil || strings.TrimSpace(episode.NumberText) == "" || episode.SortNumber != number || number <= 0 || subtitle == "" || episode.Title == "" {
		return false
	}
	if _, supported := episodeNumberFromText(episode.NumberText); supported {
		return false
	}
	return subtitlesEquivalent(episode.Title, subtitle)
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
	na := subtitleIdentityKey(a)
	nb := subtitleIdentityKey(b)
	return na != "" && nb != "" && na == nb
}

// DateProvenSubtitleMatch is the subtitle gate for an episode whose identity
// will also be proven independently by schedule date and episode number.
func DateProvenSubtitleMatch(complete, observed string) bool {
	return subtitlesEquivalent(complete, observed) ||
		CompositeSubtitlePartMatch(complete, observed) ||
		subtitleOtherSummaryMatch(observed, complete)
}

// CompositeSubtitlePartMatch reports whether observed is a contiguous set of
// complete slash-delimited parts from complete. It is deliberately narrower
// than generic substring matching and is intended only for callers that have
// already established the episode identity independently (for example from a
// date and schedule episode number).
func CompositeSubtitlePartMatch(complete, observed string) bool {
	completeParts := slashSubtitleParts(complete)
	observedParts := slashSubtitleParts(observed)
	if len(completeParts) < 2 || len(observedParts) == 0 || len(observedParts) >= len(completeParts) {
		return false
	}
	if utf8.RuneCountInString(strings.Join(observedParts, "")) < 3 {
		return false
	}
	for offset := 0; offset+len(observedParts) <= len(completeParts); offset++ {
		matched := true
		for i := range observedParts {
			if completeParts[offset+i] != observedParts[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func slashSubtitleParts(s string) []string {
	raw := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '／' })
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if key := subtitleScoringKey(part); key != "" {
			parts = append(parts, key)
		}
	}
	return parts
}

func subtitleIdentityKey(s string) string {
	key := normalize.NormalizeSubtitleForMatch(s)
	key = subtitleEpisodeLabelPrefix.ReplaceAllString(key, "")
	// A middle dot immediately before a terminal 前編/後編 label is only a
	// separator in some providers (others use parentheses). Keep all other
	// middle dots, and keep the qualifier itself, so 前編 and 後編 remain
	// distinct episode identities.
	key = trailingPartMiddleDot.ReplaceAllString(key, "$1")
	// Terminal question/exclamation marks are frequently added by the EPG but
	// omitted from Annict (or vice versa). Ignore only a trailing run so the
	// semantic punctuation inside a subtitle remains part of its identity.
	return strings.TrimRight(key, "!?！？")
}

func episodeTitleUnavailable(title string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return true
	}
	// Annict episode 161796 currently exposes a whitespace-padded conjunction
	// placeholder instead of its subtitle. Keep this deliberately narrower
	// than treating every short title as absent: a literal title "と" remains
	// meaningful unless the surrounding whitespace marks the known placeholder.
	return trimmed == "と" && title != trimmed
}

// subtitlesEquivalentForScoring permits a minor EPG omission only after the
// work and integer episode number have already selected one Annict episode.
// Candidate selection deliberately continues to use subtitlesEquivalent.
func subtitlesEquivalentForScoring(a, b string) bool {
	na := subtitleScoringKey(a)
	nb := subtitleScoringKey(b)
	return na != "" && nb != "" && (na == nb || knownSubtitleVariant(na, nb) || subtitleStructuredPartMatch(a, b) || subtitleTrailingLabelMatch(a, b) || subtitleTrailingLabelMatch(b, a) || oneRuneInsertionApart([]rune(na), []rune(nb)))
}

func knownSubtitleVariant(a, b string) bool {
	return (a == "かんばれムース" && b == "がんばれムース") ||
		(a == "がんばれムース" && b == "かんばれムース")
}

func subtitleTrailingLabelMatch(container, whole string) bool {
	want := subtitleScoringKey(whole)
	if utf8.RuneCountInString(want) < 5 {
		return false
	}
	fields := strings.Fields(normalize.NormalizeForSearch(container))
	if len(fields) < 2 {
		return false
	}
	if utf8.RuneCountInString(subtitleScoringKey(container)) < 3*utf8.RuneCountInString(want) {
		return false
	}
	// Parentheses become field boundaries during search normalization, so a
	// recorder label such as "そんな第十二話（最終回）" spans the final two
	// fields of Annict's longer official description. Compare every trailing
	// field sequence rather than only the last field.
	for start := len(fields) - 1; start > 0; start-- {
		if subtitleScoringKey(strings.Join(fields[start:], "")) == want {
			return true
		}
	}
	return false
}

func subtitleStructuredPartMatch(a, b string) bool {
	if subtitleSegmentSequenceMatch(a, b) || subtitleSegmentSequenceMatch(b, a) {
		return true
	}
	if subtitleNumberedSegmentExpansionMatch(a, b) {
		return true
	}
	if subtitleOtherSummaryMatch(a, b) || subtitleOtherSummaryMatch(b, a) {
		return true
	}
	if subtitleLongParentheticalExpansionMatch(a, b) || subtitleLongParentheticalExpansionMatch(b, a) {
		return true
	}
	if subtitleDecorativeSuffixMatch(a, b) || subtitleDecorativeSuffixMatch(b, a) {
		return true
	}
	return subtitleBracketPartMatch(a, b) || subtitleBracketPartMatch(b, a)
}

// subtitleNumberedSegmentExpansionMatch folds consecutive Japanese segment
// labels that differ only by trailing Roman I/II/III. Some EPGs expand one
// official segment into separately numbered mini-segments.
func subtitleNumberedSegmentExpansionMatch(a, b string) bool {
	left, leftChanged := collapseNumberedSubtitleSegments(subtitleSegments(a))
	right, rightChanged := collapseNumberedSubtitleSegments(subtitleSegments(b))
	if !leftChanged && !rightChanged || len(left) == 0 || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func collapseNumberedSubtitleSegments(segments []string) ([]string, bool) {
	result := make([]string, 0, len(segments))
	changed := false
	for _, segment := range segments {
		runes := []rune(segment)
		end := len(runes)
		for end > 0 && end >= len(runes)-2 && runes[end-1] == 'i' {
			end--
		}
		if end < len(runes) && end > 0 && unicode.In(runes[end-1], unicode.Han, unicode.Hiragana, unicode.Katakana) {
			segment = string(runes[:end])
			changed = true
		}
		if len(result) > 0 && result[len(result)-1] == segment {
			changed = true
			continue
		}
		result = append(result, segment)
	}
	return result, changed
}

// subtitleOtherSummaryMatch recognizes the explicit EPG convention where a
// title lists one or more segments and ends in "ほか", "他", or "ほかN本".
// The listed segments must occur in order in the complete title. A single
// long one-rune discrepancy is tolerated because recorder and Annict metadata
// occasionally differ in one glyph, but short or substantially different
// qualifiers remain distinct.
func subtitleOtherSummaryMatch(summary, full string) bool {
	marker := subtitleOtherSummarySuffix.FindStringIndex(summary)
	if marker == nil || marker[0] == 0 {
		return false
	}
	summarySegments := subtitleSegments(strings.TrimSpace(summary[:marker[0]]))
	fullSegments := subtitleSegments(full)
	if len(summarySegments) == 0 || len(summarySegments) > len(fullSegments) {
		return false
	}

	next := 0
	for _, want := range summarySegments {
		if utf8.RuneCountInString(want) < 3 {
			return false
		}
		found := false
		for next < len(fullSegments) {
			candidate := fullSegments[next]
			next++
			if want == candidate || oneRuneSubstitutionApart(want, candidate) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func oneRuneSubstitutionApart(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	if len(ar) != len(br) || len(ar) < 10 {
		return false
	}
	differences := 0
	for i := range ar {
		if ar[i] != br[i] {
			differences++
			if differences > 1 {
				return false
			}
		}
	}
	return differences == 1
}

// subtitleLongParentheticalExpansionMatch accepts a long exact main title
// with a long parenthetical expansion appended by only one metadata source.
// Length guards keep short semantic qualifiers such as 前編 and 後編 distinct.
func subtitleLongParentheticalExpansionMatch(base, expanded string) bool {
	runes := []rune(strings.TrimSpace(expanded))
	for i, open := range runes {
		close := rune(0)
		switch open {
		case '(':
			close = ')'
		case '（':
			close = '）'
		default:
			continue
		}
		if len(runes) == 0 || runes[len(runes)-1] != close {
			continue
		}
		baseKey := subtitleScoringKey(base)
		prefixKey := subtitleScoringKey(string(runes[:i]))
		expansionKey := subtitleScoringKey(string(runes[i+1 : len(runes)-1]))
		if baseKey != "" && baseKey == prefixKey &&
			utf8.RuneCountInString(baseKey) >= 8 &&
			utf8.RuneCountInString(expansionKey) >= 8 {
			return true
		}
	}
	return false
}

// subtitleDecorativeSuffixMatch recognizes a separately delimited alternate
// label or gloss appended to an otherwise exact title. It also accepts a
// symbol-only parenthetical emoticon. Meaningful short qualifiers remain
// distinct through minimum-length and character-class checks.
func subtitleDecorativeSuffixMatch(base, decorated string) bool {
	baseKey := subtitleScoringKey(base)
	if utf8.RuneCountInString(baseKey) < 4 {
		return false
	}
	runes := []rune(strings.TrimSpace(decorated))
	if len(runes) < 3 {
		return false
	}
	for i, open := range runes {
		if subtitleScoringKey(string(runes[:i])) != baseKey {
			continue
		}
		last := runes[len(runes)-1]
		switch open {
		case '-', '‐', '‑', '‒', '–', '—', '―':
			if last != '-' && last != '‐' && last != '‑' && last != '‒' && last != '–' && last != '—' && last != '―' {
				continue
			}
			if utf8.RuneCountInString(subtitleScoringKey(string(runes[i+1:len(runes)-1]))) >= 4 {
				return true
			}
		case '~', '〜', '～':
			if last != '~' && last != '〜' && last != '～' {
				continue
			}
			if utf8.RuneCountInString(subtitleScoringKey(string(runes[i+1:len(runes)-1]))) >= 4 {
				return true
			}
		case '(', '（':
			wantClose := ')'
			if open == '（' {
				wantClose = '）'
			}
			if last != wantClose {
				continue
			}
			content := runes[i+1 : len(runes)-1]
			if len(content) < 3 {
				continue
			}
			hasLetterOrDigit := false
			for _, r := range content {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					hasLetterOrDigit = true
					break
				}
			}
			if !hasLetterOrDigit {
				return true
			}
		}
	}
	return false
}

// subtitleSegmentSequenceMatch reports whether every slash/ampersand-delimited
// segment in subset occurs contiguously in full. A minimum normalized length
// prevents generic one-character segment titles from becoming strong matches.
func subtitleSegmentSequenceMatch(subset, full string) bool {
	shorter := subtitleSegments(subset)
	longer := subtitleSegments(full)
	if len(shorter) == 0 || len(shorter) > len(longer) {
		return false
	}
	matchedLength := 0
	for _, segment := range shorter {
		matchedLength += utf8.RuneCountInString(segment)
	}
	if matchedLength < 3 {
		return false
	}
	for offset := 0; offset+len(shorter) <= len(longer); offset++ {
		matched := true
		for i := range shorter {
			if shorter[i] != longer[offset+i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func subtitleSegments(s string) []string {
	raw := strings.FieldsFunc(s, func(r rune) bool {
		return r == '/' || r == '／' || r == '&' || r == '＆' || r == '、'
	})
	segments := make([]string, 0, len(raw))
	for _, part := range raw {
		if key := subtitleSegmentOrdinalPrefix.ReplaceAllString(subtitleScoringKey(part), ""); key != "" {
			segments = append(segments, key)
		}
	}
	return segments
}

func subtitleBracketPartMatch(container, whole string) bool {
	want := subtitleScoringKey(whole)
	if utf8.RuneCountInString(want) < 3 {
		return false
	}
	runes := []rune(container)
	for i, open := range runes {
		close := rune(0)
		switch open {
		case '(':
			close = ')'
		case '（':
			close = '）'
		case '「':
			close = '」'
		case '『':
			close = '』'
		default:
			continue
		}
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == close {
				if subtitleScoringKey(string(runes[i+1:j])) == want {
					return true
				}
				break
			}
		}
	}
	return false
}

// subtitleScoringKey ignores a few recorder-only decorations after the work
// and integer episode number have already selected an episode. Candidate
// selection continues to use the stricter subtitlesEquivalent key.
func subtitleScoringKey(s string) string {
	// Some metadata inserts a space before an otherwise ordinary katakana
	// reading aid. Close only that narrow gap before the shared subtitle
	// normalizer runs; parenthetical prose and kanji qualifiers stay intact.
	s = spacedKatakanaReadingPattern.ReplaceAllString(s, `$1$2`)
	// Recorder metadata alternates between the kanji and Arabic spelling of
	// this common duration phrase. Keep this deliberately narrower than a
	// general numeral conversion so semantic kanji elsewhere are preserved.
	s = strings.ReplaceAll(s, "一日", "1日")
	s = subtitleOrthographyReplacer.Replace(s)
	s = repeatedMiddleDots.ReplaceAllString(s, "")
	s = strings.NewReplacer("』『", "／", "」「", "／").Replace(s)
	s = normalizeNumericJoinerDashes(s)
	s = stripLatinDiacritics(normalize.NormalizeSubtitleForMatch(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case '!', '?', '.', '。', '~', '〜', '～', '〰', '…', '♡', '♥':
			continue
		case '&', '＆', '／':
			r = '/'
		case '寶', '寳':
			// These historical forms of the same name character are both in
			// active metadata sources but are not folded by Unicode NFKC.
			r = '宝'
		case '貳', '貮':
			r = '弐'
		case '〇', '◯':
			r = '○'
		}
		b.WriteRune(r)
	}
	return subtitleEpisodeLabelPrefix.ReplaceAllString(b.String(), "")
}

// normalizeNumericJoinerDashes repairs the narrow EPG convention where a
// horizontal dash is substituted for a prolonged sound mark beside a number
// or percent sign. Other dashes remain presentation punctuation and are not
// made equivalent to meaningful prolonged sounds.
func normalizeNumericJoinerDashes(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		switch r {
		case '-', '‐', '‑', '‒', '–', '—', '―':
		default:
			continue
		}
		previousIsNumeric := i > 0 && (unicode.IsDigit(runes[i-1]) || runes[i-1] == '%' || runes[i-1] == '％')
		nextIsNumeric := i+1 < len(runes) && (unicode.IsDigit(runes[i+1]) || runes[i+1] == '%' || runes[i+1] == '％')
		if previousIsNumeric || nextIsNumeric {
			runes[i] = 'ー'
		}
	}
	return string(runes)
}

func stripLatinDiacritics(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	latinBase := false
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			if latinBase {
				continue
			}
		} else {
			latinBase = unicode.In(r, unicode.Latin)
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
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
