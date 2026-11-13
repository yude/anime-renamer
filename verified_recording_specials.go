package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/epgstation"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/renamer"
)

type verifiedRecordingSpecial struct {
	workID              int
	title, media, label string
	channel, start, end string
	subtitle, source    string
}

// These rows bind an independently documented broadcast to the exact source
// recording. They do not manufacture an Annict episode or infer an ordinal.
var verifiedRecordingSpecials = []verifiedRecordingSpecial{
	{
		workID: 17071, title: "マウスカーソルで現実を操作できるようになったので、女の子をいっぱいクリックしまーす", media: "tv", label: "3.5",
		channel: "BS11イレブン", start: "2026-10-26T01:00:00+09:00", end: "2026-10-26T01:05:00+09:00", subtitle: "オーディオコメンタリー",
		source: "https://maukuri.af-original.com/news/817/",
	},
	{
		workID: 17354, title: "死亡遊戯で飯を食う。 44:CLOUDY BEACH", media: "movie",
		channel: "BS日テレ", start: "2026-09-28T23:30:00+09:00", end: "2026-09-29T01:00:00+09:00",
		source: "https://shiboyugi-anime.com/theater/",
	},
}

func standaloneCandidate(meta *parser.RecordingMetadata) bool {
	if meta == nil || meta.EpisodeNumber != 0 || meta.EpisodeLabel != "" || meta.Subtitle != "" || meta.FinalEpisode || meta.RecordedDate.IsZero() {
		return false
	}
	for _, spec := range verifiedRecordingSpecials {
		if spec.media == "movie" && normalize.NormalizeTitleForMatch(meta.WorkTitle) == normalize.NormalizeTitleForMatch(spec.title) {
			return true
		}
	}
	return false
}

func matchVerifiedRecordingSpecial(meta *parser.RecordingMetadata, record *epgstation.Recording, work annict.Work, runtime *processingContext) (*matcher.MatchResult, error) {
	for _, spec := range verifiedRecordingSpecials {
		if work.ID != spec.workID || strings.ToLower(work.Media) != spec.media || meta.EpisodeLabel != spec.label || normalize.NormalizeTitleForMatch(work.Title) != normalize.NormalizeTitleForMatch(spec.title) || normalize.NormalizeTitleForMatch(meta.WorkTitle) != normalize.NormalizeTitleForMatch(spec.title) {
			continue
		}
		if meta.Subtitle != "" && normalize.NormalizeSubtitleForMatch(meta.Subtitle) != normalize.NormalizeSubtitleForMatch(spec.subtitle) {
			return nil, fmt.Errorf("recording subtitle conflicts with the verified special")
		}
		start, _ := time.Parse(time.RFC3339, spec.start)
		end, _ := time.Parse(time.RFC3339, spec.end)
		if record.ID <= 0 || record.IsRecording || record.IsEncoding || !time.UnixMilli(record.StartAt).Equal(start) || !time.UnixMilli(record.EndAt).Equal(end) || meta.RecordedDate.Format("2006-01-02") != start.Format("2006-01-02") {
			return nil, fmt.Errorf("recording does not match the independently verified broadcast interval")
		}
		channel, err := runtime.epgstationClient.GetChannel(record.ChannelID)
		if err != nil {
			return nil, err
		}
		if channel == nil || channel.ID != record.ChannelID || channelNameKey(channel.Name) != channelNameKey(spec.channel) {
			return nil, fmt.Errorf("recording station does not match the independently verified broadcast")
		}
		result := &matcher.MatchResult{Work: &work, Confidence: matcher.AutoRenameThreshold,
			Reasons: []string{fmt.Sprintf("Annict work %d and media type %s verified", work.ID, spec.media), fmt.Sprintf("exact EPGStation source filename and broadcast interval corroborated by %s", spec.source)}}
		if spec.media == "movie" {
			result.Standalone = true
		} else {
			result.RecordingEpisode = &matcher.RecordingEpisode{Label: spec.label, Subtitle: spec.subtitle}
			result.Reasons = append(result.Reasons, "fractional label comes from EPGStation's dedicated program-content field; no Annict episode is invented")
		}
		return result, nil
	}
	return nil, fmt.Errorf("no independently verified recording special matches this work and label")
}

func processStandaloneRecording(file string, meta *parser.RecordingMetadata, client *annict.Client, c *cache.Cache, wc map[string][]annict.Work, ec map[int][]annict.Episode, plans map[string]string, dryRun bool, threshold int, outputDir string, runtime *processingContext) *renamer.RenameResult {
	skip := func(err error) *renamer.RenameResult {
		return &renamer.RenameResult{OriginalPath: file, SkipReason: fmt.Sprintf("standalone movie could not be verified: %v", err)}
	}
	if runtime == nil || runtime.epgstationClient == nil {
		return skip(fmt.Errorf("EPGStation recording history is required"))
	}
	works, err := searchWork(client, c, meta.WorkTitle, wc, ec)
	if err != nil {
		return skip(err)
	}
	if len(works) == 1 && works[0].Media == "" {
		works, err = refreshWorkMetadata(client, c, meta.WorkTitle, wc, ec)
		if err != nil {
			return skip(err)
		}
	}
	if len(works) != 1 || normalize.NormalizeTitleForMatch(works[0].Title) != normalize.NormalizeTitleForMatch(meta.WorkTitle) {
		return skip(fmt.Errorf("movie work is not an exact unique Annict title"))
	}
	record, err := runtime.epgstationClient.FindRecording(file, meta.WorkTitle, meta.RecordedDate)
	if err != nil || record == nil {
		return skip(fmt.Errorf("no exact source recording: %v", err))
	}
	parsed, err := recordingMetadata(meta, record)
	if err != nil || parsed.EpisodeNumber != 0 || parsed.EpisodeLabel != "" || parsed.Subtitle != "" {
		return skip(fmt.Errorf("recording does not identify a complete standalone movie: %v", err))
	}
	result, err := matchVerifiedRecordingSpecial(meta, record, works[0], runtime)
	if err != nil {
		return skip(err)
	}
	if result.Confidence < threshold {
		return skip(fmt.Errorf("confidence %d below threshold %d", result.Confidence, threshold))
	}
	fmt.Fprintf(os.Stderr, "  Match:     confidence=%d threshold=%d\n", result.Confidence, threshold)
	for _, reason := range result.Reasons {
		fmt.Fprintf(os.Stderr, "    - %s\n", reason)
	}
	return renameMatchedRecording(file, result, dryRun, outputDir, plans)
}
