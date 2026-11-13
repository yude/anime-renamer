package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/dateinfer"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
)

var explicitCourSuffix = regexp.MustCompile(`^第([0-9]+)クール(?:目)?$`)

// matchSharedCourSchedule handles Annict splitting one continuously numbered
// series across cours while only its base work carries a Syobocal TID. It
// requires complete, disjoint episode lists and an exact schedule subtitle;
// neither an episode offset nor a missing work-to-schedule link is guessed.
func matchSharedCourSchedule(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, c *cache.Cache, client syobocalProgramClient) (*matcher.MatchResult, error) {
	if meta == nil || meta.EpisodeNumber != 0 || meta.RecordedDate.IsZero() || client == nil || len(works) < 2 {
		return nil, fmt.Errorf("shared-cour recovery requires a dated numberless recording and multiple works")
	}
	base := normalize.NormalizeTitleForMatch(meta.WorkTitle)
	baseCount, missingTIDs, tid := 0, 0, 0
	var episodes []annict.Episode
	seenNumbers := make(map[int]bool)
	for _, work := range works {
		key := normalize.NormalizeTitleForMatch(work.Title)
		workTID, _ := strconv.Atoi(work.SyobocalTID)
		if key == base {
			baseCount++
			if workTID <= 0 {
				return nil, fmt.Errorf("base work has no valid shared schedule TID")
			}
		} else {
			suffix := strings.TrimPrefix(key, base)
			match := explicitCourSuffix.FindStringSubmatch(suffix)
			if !strings.HasPrefix(key, base) || match == nil {
				return nil, fmt.Errorf("work %q is not an explicit cour of the base title", work.Title)
			}
			cour, _ := strconv.Atoi(match[1])
			if cour < 2 {
				return nil, fmt.Errorf("invalid continuation cour in %q", work.Title)
			}
		}
		if strings.TrimSpace(work.SyobocalTID) == "" {
			missingTIDs++
		} else if workTID <= 0 || (tid > 0 && tid != workTID) {
			return nil, fmt.Errorf("related works do not share one valid schedule TID")
		} else {
			tid = workTID
		}
		workEpisodes := episodesByWork[work.ID]
		if work.EpisodesCount <= 0 || len(workEpisodes) != work.EpisodesCount {
			return nil, fmt.Errorf("work %q has no complete episode list", work.Title)
		}
		for _, episode := range workEpisodes {
			number, ok := matcher.EpisodeNumber(&episode)
			if !ok || seenNumbers[number] {
				return nil, fmt.Errorf("cour episode numbers are invalid or overlap")
			}
			seenNumbers[number] = true
			episode.WorkID = work.ID
			episodes = append(episodes, episode)
		}
	}
	if baseCount != 1 || missingTIDs == 0 {
		return nil, fmt.Errorf("shared-cour recovery requires one base work and a missing continuation TID")
	}
	programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
	if err != nil {
		return nil, err
	}
	episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, episodes, programs, meta.Subtitle)
	if episode == nil {
		return nil, fmt.Errorf("shared schedule: %s", reason)
	}
	if strings.TrimSpace(episode.Title) == "" {
		return nil, fmt.Errorf("shared schedule requires an Annict subtitle")
	}
	rows := 0
	jst := time.FixedZone("JST", 9*60*60)
	for _, program := range programs {
		if program.Deleted || program.Warn || program.Count <= 0 || program.StartedAt.In(jst).Format("2006-01-02") != meta.RecordedDate.In(jst).Format("2006-01-02") {
			continue
		}
		if !normalize.Compare(program.Subtitle, episode.Title) {
			return nil, fmt.Errorf("shared schedule requires every clean row to have the exact Annict subtitle")
		}
		rows++
	}
	if rows == 0 {
		return nil, fmt.Errorf("shared schedule has no corroborating clean rows")
	}
	for _, work := range works {
		if work.ID == episode.WorkID && strings.TrimSpace(work.SyobocalTID) == "" {
			return &matcher.MatchResult{
				Work: &work, Episode: episode, Confidence: matcher.AutoRenameThreshold,
				Reasons: []string{"explicit cours have complete, disjoint episode numbers", fmt.Sprintf("base work TID %d corroborates the continuation with an exact subtitle; %s", tid, reason)},
			}, nil
		}
	}
	return nil, fmt.Errorf("shared schedule did not identify a continuation with a missing TID")
}
