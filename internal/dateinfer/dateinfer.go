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

// AnchorChannel identifies the only channel whose clean same-day schedule row
// agrees with an already-known Annict episode. A date that maps that episode to
// more than one channel is not a usable channel fingerprint.
func AnchorChannel(date time.Time, episode *annict.Episode, programs []syobocal.Program) (int, string) {
	if date.IsZero() || episode == nil {
		return 0, "anchor date or episode is missing"
	}
	number, ok := matcher.EpisodeNumber(episode)
	if !ok {
		return 0, "anchor episode has no positive integer number"
	}
	dateKey := date.In(jst).Format("2006-01-02")
	channelID := 0
	rows := 0
	for _, program := range programs {
		if program.Deleted || program.Warn || program.Count != number || program.ChannelID <= 0 {
			continue
		}
		if program.StartedAt.In(jst).Format("2006-01-02") != dateKey {
			continue
		}
		if episode.Title != "" && program.Subtitle != "" && !normalize.Compare(episode.Title, program.Subtitle) {
			continue
		}
		rows++
		if channelID == 0 {
			channelID = program.ChannelID
		} else if channelID != program.ChannelID {
			return 0, fmt.Sprintf("anchor episode %d appears on multiple channels", number)
		}
	}
	if channelID == 0 {
		return 0, fmt.Sprintf("no schedule row confirms anchor episode %d", number)
	}
	return channelID, fmt.Sprintf("episode %d uniquely fingerprints channel %d from %d row(s)", number, channelID, rows)
}

// ResolveUnique returns an Annict episode only when every usable schedule row
// that starts on date agrees on one positive episode number and that number
// maps to exactly one Annict episode. The explanatory string is suitable for
// a safe-skip diagnostic when no episode is returned.
func ResolveUnique(date time.Time, episodes []annict.Episode, programs []syobocal.Program) (*annict.Episode, string) {
	return resolve(date, episodes, programs, 0, "")
}

// ResolveUniqueWithSubtitle applies ResolveUnique and additionally requires a
// filename subtitle to agree with either the whole Annict title or one or more
// complete slash-delimited parts of a composite title.
func ResolveUniqueWithSubtitle(date time.Time, episodes []annict.Episode, programs []syobocal.Program, subtitle string) (*annict.Episode, string) {
	return resolve(date, episodes, programs, 0, subtitle)
}

// ResolveForChannel applies the same strict resolution after limiting schedule
// rows to a channel established independently by batch anchors.
func ResolveForChannel(date time.Time, episodes []annict.Episode, programs []syobocal.Program, channelID int) (*annict.Episode, string) {
	if channelID <= 0 {
		return nil, "trusted channel is missing"
	}
	return resolve(date, episodes, programs, channelID, "")
}

// ResolveForChannelWithSubtitle is the trusted-channel counterpart of
// ResolveUniqueWithSubtitle.
func ResolveForChannelWithSubtitle(date time.Time, episodes []annict.Episode, programs []syobocal.Program, channelID int, subtitle string) (*annict.Episode, string) {
	if channelID <= 0 {
		return nil, "trusted channel is missing"
	}
	return resolve(date, episodes, programs, channelID, subtitle)
}

// HasUsableSchedule reports whether at least one clean positive-count row
// starts on date. A false result can safely exclude a related work before
// deciding whether exactly one work proves the recording identity.
func HasUsableSchedule(date time.Time, programs []syobocal.Program) bool {
	if date.IsZero() {
		return false
	}
	dateKey := date.In(jst).Format("2006-01-02")
	for _, program := range programs {
		if !program.Deleted && !program.Warn && program.Count > 0 && program.StartedAt.In(jst).Format("2006-01-02") == dateKey {
			return true
		}
	}
	return false
}

func resolve(date time.Time, episodes []annict.Episode, programs []syobocal.Program, channelID int, fileSubtitle string) (*annict.Episode, string) {
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
		if channelID > 0 && program.ChannelID != channelID {
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
		if channelID > 0 {
			return nil, fmt.Sprintf("no usable schedule starts on %s for channel %d", dateKey, channelID)
		}
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
			if !normalize.Compare(matched.Title, subtitle) && !matcher.DateProvenSubtitleMatch(matched.Title, subtitle) {
				return nil, fmt.Sprintf("schedule subtitle %q conflicts with Annict subtitle %q", subtitle, matched.Title)
			}
		}
	}
	if fileSubtitle != "" {
		if matched.Title == "" {
			return nil, "filename subtitle cannot be verified because the Annict subtitle is missing"
		}
		if !normalize.Compare(matched.Title, fileSubtitle) && !matcher.DateProvenSubtitleMatch(matched.Title, fileSubtitle) {
			return nil, fmt.Sprintf("filename subtitle %q conflicts with Annict subtitle %q", fileSubtitle, matched.Title)
		}
	}
	if channelID > 0 {
		return matched, fmt.Sprintf("trusted channel %d schedule uniquely identified Annict episode %d from %d broadcast row(s)%s", channelID, count, usable, subtitleEvidence(fileSubtitle))
	}
	return matched, fmt.Sprintf("schedule uniquely identified Annict episode %d from %d broadcast row(s)%s", count, usable, subtitleEvidence(fileSubtitle))
}

func subtitleEvidence(subtitle string) string {
	if subtitle == "" {
		return ""
	}
	return " and filename subtitle matched the Annict episode title"
}
