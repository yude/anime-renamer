package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/dateinfer"
	"github.com/yude/anime-renamer/internal/epgstation"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/syobocal"
	"github.com/yude/anime-renamer/internal/tsmeta"
)

type embeddedRecording struct {
	broadcast *tsmeta.Broadcast
	metadata  *parser.RecordingMetadata
}

func isTransportStream(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m2ts" || ext == ".ts"
}

// The filename retains its identity: embedded metadata may fill missing fields
// but cannot replace a different title, episode, date or complete subtitle.
func mergeEmbeddedRecording(file *parser.RecordingMetadata, broadcast *tsmeta.Broadcast) (*parser.RecordingMetadata, *embeddedRecording, error) {
	date := broadcast.StartedAt.Format("20060102")
	name, err := parser.ParseBroadcastFilename(broadcast.Name + " (" + date + ").ts")
	if err != nil {
		return nil, nil, err
	}
	if corrected, ok := matcher.CorrectMetadataSubtitle(name); ok {
		name = corrected
	}
	program, err := recordingMetadata(name, &epgstation.Recording{Name: broadcast.Name, Description: broadcast.Description})
	if err != nil {
		return nil, nil, err
	}
	if corrected, ok := matcher.CorrectMetadataSubtitle(program); ok {
		program = corrected
	}
	program, err = combineRecordingIdentity(name, program)
	if err != nil {
		return nil, nil, fmt.Errorf("EIT name and description disagree: %w", err)
	}
	merged, err := combineRecordingIdentity(file, program)
	if err != nil {
		return nil, nil, err
	}
	return merged, &embeddedRecording{broadcast: broadcast, metadata: program}, nil
}

func combineRecordingIdentity(file, program *parser.RecordingMetadata) (*parser.RecordingMetadata, error) {
	if normalize.NormalizeTitleForMatch(file.WorkTitle) != normalize.NormalizeTitleForMatch(program.WorkTitle) {
		return nil, fmt.Errorf("work titles differ: %q / %q", file.WorkTitle, program.WorkTitle)
	}
	if !file.RecordedDate.IsZero() && file.RecordedDate.Format("2006-01-02") != program.RecordedDate.Format("2006-01-02") {
		return nil, fmt.Errorf("recording dates differ")
	}
	fileNumber, epgNumber := matcher.MetadataEpisodeLabel(file), matcher.MetadataEpisodeLabel(program)
	fileHasNumber := file.EpisodeNumber > 0 || file.EpisodeLabel != ""
	epgHasNumber := program.EpisodeNumber > 0 || program.EpisodeLabel != ""
	if fileHasNumber && epgHasNumber && fileNumber != epgNumber {
		return nil, fmt.Errorf("episode labels differ: %s / %s", fileNumber, epgNumber)
	}
	if file.Subtitle != "" && program.Subtitle != "" && !normalize.Compare(file.Subtitle, program.Subtitle) {
		return nil, fmt.Errorf("subtitles differ: %q / %q", file.Subtitle, program.Subtitle)
	}
	merged := *file
	if !fileHasNumber {
		merged.EpisodeNumber, merged.EpisodeLabel = program.EpisodeNumber, program.EpisodeLabel
	}
	if merged.Subtitle == "" {
		merged.Subtitle = program.Subtitle
	}
	if merged.RecordedDate.IsZero() {
		merged.RecordedDate = program.RecordedDate
	}
	merged.FinalEpisode = merged.FinalEpisode || program.FinalEpisode
	return &merged, nil
}

func verifyEmbeddedMatch(meta *parser.RecordingMetadata, embedded *embeddedRecording, result *matcher.MatchResult, works []annict.Work, ec map[int][]annict.Episode, c *cache.Cache, runtime *processingContext) error {
	matching := matcher.MatchingWorks(meta.WorkTitle, works)
	if result == nil || result.Work == nil || result.Episode == nil || len(matching) != 1 || matching[0].ID != result.Work.ID || normalize.NormalizeTitleForMatch(result.Work.Title) != normalize.NormalizeTitleForMatch(meta.WorkTitle) {
		return fmt.Errorf("work and episode are not uniquely identified")
	}
	epg := embedded.metadata
	if (epg.EpisodeNumber > 0 || epg.EpisodeLabel != "") && epg.Subtitle != "" {
		label, ok := matcher.MatchResultEpisodeLabel(result)
		if !ok || label != matcher.MetadataEpisodeLabel(epg) || result.Episode.Title == "" || !normalize.Compare(result.Episode.Title, epg.Subtitle) {
			return fmt.Errorf("embedded explicit episode/subtitle conflicts with Annict")
		}
		// No ordinal or multi-cour offset is evidence for an embedded label.
		matches := 0
		for i := range ec[result.Work.ID] {
			e := &ec[result.Work.ID][i]
			if n, ok := explicitEmbeddedEpisodeLabel(e); ok && n == label {
				matches++
				if e.ID != result.Episode.ID {
					return fmt.Errorf("embedded episode has conflicting Annict identities")
				}
			}
		}
		if matches != 1 {
			return fmt.Errorf("embedded episode maps to %d Annict episodes", matches)
		}
		return nil
	}
	// A station name or a bare episode number is insufficient by itself.
	verified, err := matchEmbeddedSchedule(meta, embedded, works, ec, c, runtime)
	if err != nil {
		return err
	}
	if verified.Work.ID != result.Work.ID || verified.Episode.ID != result.Episode.ID {
		return fmt.Errorf("embedded broadcast schedule identifies a different episode")
	}
	return nil
}

func explicitEmbeddedEpisodeLabel(episode *annict.Episode) (string, bool) {
	if label, ok := matcher.FractionalEpisodeLabel(episode); ok {
		return label, true
	}
	copy := *episode
	copy.SortNumber = 0 // A list position cannot prove an embedded episode label.
	number, ok := matcher.EpisodeNumber(&copy)
	return strconv.Itoa(number), ok && number > 0
}

func matchEmbeddedSchedule(meta *parser.RecordingMetadata, embedded *embeddedRecording, works []annict.Work, ec map[int][]annict.Episode, c *cache.Cache, runtime *processingContext) (*matcher.MatchResult, error) {
	works = matcher.MatchingWorks(meta.WorkTitle, works)
	if len(works) != 1 || normalize.NormalizeTitleForMatch(works[0].Title) != normalize.NormalizeTitleForMatch(meta.WorkTitle) || runtime == nil || runtime.syobocalClient == nil {
		return nil, fmt.Errorf("embedded programme has no unique work/schedule source")
	}
	work := works[0]
	tid, err := strconv.Atoi(work.SyobocalTID)
	if err != nil || tid <= 0 {
		return nil, fmt.Errorf("work has no Syobocal TID")
	}
	channels, ok := runtime.syobocalClient.(interface {
		GetChannels() ([]syobocal.Channel, error)
	})
	if !ok {
		return nil, fmt.Errorf("channel catalogue is unavailable")
	}
	if runtime.syobocalChannels == nil {
		runtime.syobocalChannels, err = channels.GetChannels()
		if err != nil {
			return nil, err
		}
	}
	channelID, count := 0, 0
	for _, ch := range runtime.syobocalChannels {
		if ch.ID > 0 && channelNameKey(ch.Name) == channelNameKey(embedded.broadcast.Channel) {
			channelID, count = ch.ID, count+1
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("embedded channel maps to %d schedule channels", count)
	}
	programs, err := getSyobocalPrograms(c, runtime.syobocalClient, tid, meta.RecordedDate)
	if err != nil {
		return nil, err
	}
	var rows []syobocal.Program
	for _, p := range programs {
		if p.Deleted || p.Warn || p.ChannelID != channelID || !p.StartedAt.Equal(embedded.broadcast.StartedAt) || !p.EndedAt.Equal(embedded.broadcast.EndedAt) {
			continue
		}
		if meta.EpisodeNumber > 0 && p.Count != meta.EpisodeNumber {
			return nil, fmt.Errorf("embedded episode conflicts with the broadcast schedule")
		}
		if p.Count <= 0 || p.Subtitle == "" {
			return nil, fmt.Errorf("schedule has no explicit episode and subtitle")
		}
		rows = append(rows, p)
	}
	episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, ec[work.ID], rows, meta.Subtitle)
	if episode == nil || episode.Title == "" {
		return nil, fmt.Errorf("embedded broadcast schedule is unverified: %s", reason)
	}
	if label, ok := explicitEmbeddedEpisodeLabel(episode); !ok || len(rows) == 0 || label != strconv.Itoa(rows[0].Count) {
		return nil, fmt.Errorf("schedule episode lacks a matching explicit Annict label")
	}
	return &matcher.MatchResult{Work: &work, Episode: episode, Confidence: matcher.AutoRenameThreshold,
		Reasons: []string{"embedded channel and exact start/end times matched the broadcast schedule", reason}}, nil
}
