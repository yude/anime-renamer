// Package dateinfer resolves an otherwise numberless recording only when a
// Shoboi Calendar schedule and Annict identify exactly the same episode.
package dateinfer

import (
	"fmt"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/syobocal"
)

var jst = time.FixedZone("JST", 9*60*60)

// ResolveUnique returns an Annict episode only when every usable schedule row
// that starts on date agrees on one positive episode number and that number
// maps to exactly one Annict episode. The explanatory string is suitable for
// a safe-skip diagnostic when no episode is returned.
func ResolveUnique(date time.Time, episodes []annict.Episode, programs []syobocal.Program) (*annict.Episode, string) {
	if date.IsZero() {
		return nil, "recording date is missing"
	}

	dateKey := date.In(jst).Format("2006-01-02")
	count := 0
	usable := 0
	var subtitles []string
	for _, program := range programs {
		if program.Deleted || program.Warn || program.Count <= 0 {
			continue
		}
		if program.StartedAt.In(jst).Format("2006-01-02") != dateKey {
			continue
		}
		usable++
		if count == 0 {
			count = program.Count
		} else if program.Count != count {
			return nil, fmt.Sprintf("schedule is ambiguous: episode counts %d and %d both air on %s", count, program.Count, dateKey)
		}
		if program.Subtitle != "" {
			subtitles = append(subtitles, program.Subtitle)
		}
	}
	if usable == 0 {
		return nil, fmt.Sprintf("no usable schedule starts on %s", dateKey)
	}

	var matched *annict.Episode
	matches := 0
	for i := range episodes {
		number, ok := matcher.EpisodeNumber(&episodes[i])
		if !ok || number != count {
			continue
		}
		matches++
		matched = &episodes[i]
	}
	if matches != 1 {
		return nil, fmt.Sprintf("schedule episode %d maps to %d Annict episodes", count, matches)
	}

	if matched.Title != "" {
		for _, subtitle := range subtitles {
			if !normalize.Compare(matched.Title, subtitle) {
				return nil, fmt.Sprintf("schedule subtitle %q conflicts with Annict subtitle %q", subtitle, matched.Title)
			}
		}
	}
	return matched, fmt.Sprintf("schedule uniquely identified Annict episode %d from %d broadcast row(s)", count, usable)
}
