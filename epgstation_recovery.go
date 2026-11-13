package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/dateinfer"
	"github.com/yude/anime-renamer/internal/epgstation"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/syobocal"
	"golang.org/x/text/unicode/norm"
)

type epgstationRecordingClient interface {
	FindRecording(filename, title string, date time.Time) (*epgstation.Recording, error)
	GetChannel(id int64) (*epgstation.Channel, error)
}

// Only a leading episode header may supply an episode or a work qualifier.
// References to other episodes elsewhere in a synopsis are never parsed.
var recordedEpisodeHeader = regexp.MustCompile(`(?i)^[\s\x{3000}]*(?:([~〜～][^~〜～\r\n]+[~〜～])[\s\x{3000}]*)?((?:第[\s\x{3000}]*[0-9０-９〇一二三四五六七八九十百壱弐参拾]+(?:[.．][0-9０-９]+)?[\s\x{3000}]*話|[#＃♯][\s\x{3000}]*[0-9０-９]+(?:[.．][0-9０-９]+)?|(?:chapter|episode|ep|score)[.．\s\x{3000}]*[0-9０-９]+(?:[.．][0-9０-９]+)?))(.*)$`)

var recordedEpisodeLikeHeader = regexp.MustCompile(`(?i)^[\s\x{3000}]*(?:[~〜～][^~〜～\r\n]+[~〜～][\s\x{3000}]*)?(?:第|[#＃♯]|chapter|episode|ep|score)[.．\s\x{3000}]*[0-9０-９〇一二三四五六七八九十百壱弐参拾]`)

func recordingMetadata(meta *parser.RecordingMetadata, record *epgstation.Recording) (*parser.RecordingMetadata, error) {
	date := meta.RecordedDate.Format("20060102")
	parsed, err := parser.ParseFilename(record.Name + " (" + date + ").ts")
	if err != nil {
		return nil, fmt.Errorf("invalid EPGStation program title: %w", err)
	}
	if normalize.NormalizeTitleForMatch(parsed.WorkTitle) != normalize.NormalizeTitleForMatch(meta.WorkTitle) {
		return nil, fmt.Errorf("EPGStation program title conflicts with the filename")
	}
	line, _, _ := strings.Cut(record.Description, "\n")
	header := recordedEpisodeHeader.FindStringSubmatch(strings.TrimSpace(line))
	if header == nil {
		if recordedEpisodeLikeHeader.MatchString(line) {
			return nil, fmt.Errorf("EPGStation description has an unsupported episode header")
		}
		if special, found, err := fractionalRecordingContents(parsed, record); found || err != nil {
			return special, err
		}
		return parsed, nil
	}
	title := strings.TrimSpace(parsed.WorkTitle + " " + header[1])
	description, err := parser.ParseFilename(title + " " + header[2] + header[3] + " (" + date + ").ts")
	if err != nil || (description.EpisodeNumber <= 0 && description.EpisodeLabel == "") {
		return nil, fmt.Errorf("EPGStation description has no safe single episode header: %v", err)
	}
	if normalize.NormalizeTitleForMatch(description.WorkTitle) != normalize.NormalizeTitleForMatch(title) {
		return nil, fmt.Errorf("EPGStation description has an ambiguous work/episode boundary")
	}
	if (parsed.EpisodeNumber > 0 && matcher.MetadataEpisodeLabel(parsed) != matcher.MetadataEpisodeLabel(description)) || (parsed.EpisodeLabel != "" && parsed.EpisodeLabel != description.EpisodeLabel) {
		return nil, fmt.Errorf("EPGStation name and description have conflicting episode numbers")
	}
	if description.Subtitle == "" {
		description.Subtitle = strings.TrimSpace(header[3])
	}
	if corrected, ok := matcher.CorrectMetadataSubtitle(description); ok {
		description = corrected
	}
	if special, found, err := fractionalRecordingContents(description, record); found || err != nil {
		return special, err
	}
	return description, nil
}

// This fallback adds evidence from one actual recording to the existing
// schedule proof. It never chooses a station by popularity or closest time.
func matchEPGStationRecording(filename string, meta *parser.RecordingMetadata, client *annict.Client, c *cache.Cache, wc map[string][]annict.Work, ec map[int][]annict.Episode, runtime *processingContext) (*matcher.MatchResult, error) {
	record, err := runtime.epgstationClient.FindRecording(filename, meta.WorkTitle, meta.RecordedDate)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("no recording has the exact source filename and date")
	}
	metadata, err := recordingMetadata(meta, record)
	if err != nil {
		return nil, err
	}
	works, err := searchRelatedWorks(client, c, metadata.WorkTitle, wc, ec)
	if err != nil {
		return nil, err
	}
	works = matcher.MatchingWorks(metadata.WorkTitle, works)
	if len(works) != 1 {
		return nil, fmt.Errorf("recording metadata leaves %d Annict work candidates", len(works))
	}
	work := works[0]
	if metadata.EpisodeLabel != "" {
		if result := matcher.Match(metadata, works, ec, nil); result != nil {
			result.Reasons = append(result.Reasons, fmt.Sprintf("EPGStation recording %d supplied an explicit fractional label", record.ID))
			return result, nil
		}
		if work.Media == "" {
			fresh, refreshErr := refreshWorkMetadata(client, c, metadata.WorkTitle, wc, ec)
			if refreshErr != nil || len(fresh) != 1 || fresh[0].ID != work.ID {
				return nil, fmt.Errorf("special recording work metadata could not be refreshed: %v", refreshErr)
			}
			work = fresh[0]
		}
		return matchVerifiedRecordingSpecial(metadata, record, work, runtime)
	}
	tid, err := strconv.Atoi(work.SyobocalTID)
	if err != nil || tid <= 0 {
		return nil, fmt.Errorf("recording work has no valid Syobocal TID")
	}
	channel, err := runtime.epgstationClient.GetChannel(record.ChannelID)
	if err != nil {
		return nil, err
	}
	channelClient, ok := runtime.syobocalClient.(interface {
		GetChannels() ([]syobocal.Channel, error)
	})
	if !ok {
		return nil, fmt.Errorf("Syobocal channel catalogue is unavailable")
	}
	if runtime.syobocalChannels == nil {
		runtime.syobocalChannels, err = channelClient.GetChannels()
		if err != nil {
			return nil, err
		}
	}
	channelID, matches := 0, 0
	for _, candidate := range runtime.syobocalChannels {
		if candidate.ID > 0 && channelNameKey(candidate.Name) == channelNameKey(channel.Name) {
			channelID = candidate.ID
			matches++
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("recorded channel %q maps to %d Syobocal channels", channel.Name, matches)
	}
	programs, err := getSyobocalPrograms(c, runtime.syobocalClient, tid, meta.RecordedDate)
	if err != nil {
		return nil, err
	}
	var clean, warned []syobocal.Program
	for _, program := range programs {
		if program.Deleted || program.ChannelID != channelID || !program.StartedAt.Equal(time.UnixMilli(record.StartAt)) || !program.EndedAt.Equal(time.UnixMilli(record.EndAt)) {
			continue
		}
		if program.Count <= 0 || strings.TrimSpace(program.Subtitle) == "" {
			return nil, fmt.Errorf("recorded broadcast has no positive episode number or schedule subtitle")
		}
		if metadata.EpisodeNumber > 0 && program.Count != metadata.EpisodeNumber {
			return nil, fmt.Errorf("EPGStation episode %d conflicts with schedule episode %d", metadata.EpisodeNumber, program.Count)
		}
		if program.Warn {
			program.Warn = false // Usable below only with an independent explicit header AND subtitle.
			warned = append(warned, program)
		} else {
			clean = append(clean, program)
		}
	}
	rows := clean
	fileSubtitle := meta.Subtitle
	if metadata.EpisodeNumber > 0 && metadata.Subtitle != "" {
		fileSubtitle = metadata.Subtitle
	}
	if len(rows) == 0 {
		if metadata.EpisodeNumber <= 0 || metadata.Subtitle == "" {
			return nil, fmt.Errorf("no clean broadcast matches the recording; warned rows require an explicit EPG episode and subtitle")
		}
		rows, fileSubtitle = warned, metadata.Subtitle
	}
	episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, ec[work.ID], rows, fileSubtitle)
	if episode == nil {
		return nil, fmt.Errorf("recorded broadcast could not identify an episode: %s", reason)
	}
	if strings.TrimSpace(episode.Title) == "" {
		return nil, fmt.Errorf("Annict has no subtitle to corroborate the recorded broadcast")
	}
	reasons := []string{fmt.Sprintf("EPGStation recording %d exactly matched the source filename; channel %q and start/end times matched the schedule", record.ID, channel.Name), reason}
	if len(clean) == 0 {
		reasons = append(reasons, "explicit EPGStation episode and subtitle corroborated a warned schedule row")
	}
	return &matcher.MatchResult{
		Work: &work, Episode: episode, Confidence: matcher.AutoRenameThreshold,
		Reasons: reasons,
	}, nil
}

func channelNameKey(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFKC.String(name))
}
