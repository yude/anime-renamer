package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/dateinfer"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/renamer"
	"github.com/yude/anime-renamer/internal/syobocal"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview renames without actually renaming")
	recursive := flag.Bool("recursive", false, "Process subdirectories recursively")
	verbose := flag.Bool("verbose", false, "Show detailed output")
	noCache := flag.Bool("no-cache", false, "Disable caching")
	confidenceThreshold := flag.Int("confidence", matcher.AutoRenameThreshold, "Minimum confidence for auto-rename")
	outputDir := flag.String("output", "", "Output directory (default: same as input)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "anime-renamer - Annict-based recording file renamer\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  anime-renamer [options] <file|directory>\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  anime-renamer --dry-run \"花ざかりの君たちへ 第2期 ep．7「ずっとそばにいたいから」 (20260813).mp4\"\n")
		fmt.Fprintf(os.Stderr, "  anime-renamer --dry-run --recursive /recordings/\n")
		fmt.Fprintf(os.Stderr, "  anime-renamer /recordings/\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  ANNICT_ACCESS_TOKEN    Annict API access token (required)\n")
	}
	flag.Parse()

	if err := validateTargetArgs(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		flag.Usage()
		os.Exit(1)
	}
	if err := validateConfidenceThreshold(*confidenceThreshold); err != nil {
		log.Fatal(err)
	}

	// Get access token
	token := os.Getenv("ANNICT_ACCESS_TOKEN")
	if token == "" {
		token = loadTokenFromDotenv(flag.Arg(0))
	}
	if token == "" && !*dryRun {
		log.Fatal("ANNICT_ACCESS_TOKEN environment variable is required")
	}

	// Initialize components
	annictClient := annict.NewClient(token)
	syobocalClient := syobocal.NewClient()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.Getenv("HOME")
	}
	cacheDir := filepath.Join(homeDir, ".cache", "anime-renamer")
	var c *cache.Cache
	if *noCache {
		c = cache.NewDisabled(cacheDir)
	} else {
		c = cache.New(cacheDir)
	}

	// Collect files to process
	target := flag.Arg(0)
	files, err := collectFiles(target, *recursive)
	if err != nil {
		log.Fatalf("collect files: %v", err)
	}

	if len(files) == 0 {
		log.Fatal("no files found to process")
	}
	files, batchSchedule := prioritizeDateOnlyFiles(files)

	fmt.Fprintf(os.Stderr, "Found %d file(s) to process\n\n", len(files))

	// Process each file
	workCache := make(map[string][]annict.Work)
	episodesCache := make(map[int][]annict.Episode)
	programsCache := make(map[programsCacheKey][]annict.Program)
	plannedDestinations := make(map[string]string)
	processing := &processingContext{syobocalClient: syobocalClient, batchSchedule: batchSchedule}

	renamed := 0
	previewed := 0
	skipped := 0
	failed := 0

	for _, file := range files {
		result := processFile(file, annictClient, c, workCache, episodesCache, programsCache, plannedDestinations, *dryRun, *verbose, *confidenceThreshold, *outputDir, processing)

		switch {
		case result.Error != nil:
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n\n", result.Error)
			failed++
		case result.SkipReason != "":
			fmt.Fprintf(os.Stderr, "  SKIP: %s\n\n", result.SkipReason)
			skipped++
		case result.Renamed:
			renamed++
		case result.Previewed:
			previewed++
		default:
			skipped++
		}
	}

	// Summary
	fmt.Fprintf(os.Stderr, "\n--- Summary ---\n")
	if *dryRun {
		fmt.Fprintf(os.Stderr, "Dry-run mode: no files were renamed\n")
		fmt.Fprintf(os.Stderr, "Would rename: %d\n", previewed)
		fmt.Fprintf(os.Stderr, "Already organized: %d\n", renamed)
	} else {
		fmt.Fprintf(os.Stderr, "Renamed: %d\n", renamed)
	}
	fmt.Fprintf(os.Stderr, "Skipped: %d\n", skipped)
	fmt.Fprintf(os.Stderr, "Failed:  %d\n", failed)
	if code := exitCodeForFailures(failed); code != 0 {
		os.Exit(code)
	}
}

func exitCodeForFailures(failed int) int {
	if failed > 0 {
		return 1
	}
	return 0
}

type syobocalProgramClient interface {
	GetPrograms(tid int, date time.Time) ([]syobocal.Program, error)
}

type processingContext struct {
	syobocalClient syobocalProgramClient
	batchSchedule  *batchScheduleContext
}

type channelAnchorState struct {
	channelID int
	keys      map[string]bool
	conflict  bool
}

type batchScheduleContext struct {
	targetTitles map[string]bool
	anchors      map[int]*channelAnchorState
}

func newBatchScheduleContext() *batchScheduleContext {
	return &batchScheduleContext{
		targetTitles: make(map[string]bool),
		anchors:      make(map[int]*channelAnchorState),
	}
}

func (b *batchScheduleContext) wantsAnchors(titles ...string) bool {
	if b == nil {
		return false
	}
	for _, title := range titles {
		if b.targetTitles[normalize.NormalizeTitleForMatch(title)] {
			return true
		}
	}
	return false
}

func (b *batchScheduleContext) record(workID, channelID int, key string) {
	if b == nil || workID <= 0 || channelID <= 0 || key == "" {
		return
	}
	state := b.anchors[workID]
	if state == nil {
		state = &channelAnchorState{channelID: channelID, keys: make(map[string]bool)}
		b.anchors[workID] = state
	}
	if state.conflict || state.keys[key] {
		return
	}
	if state.channelID != channelID {
		state.conflict = true
		return
	}
	state.keys[key] = true
}

func (b *batchScheduleContext) trustedChannel(workID int) (channelID, anchors int, ok bool) {
	if b == nil {
		return 0, 0, false
	}
	state := b.anchors[workID]
	if state == nil || state.conflict || len(state.keys) < 2 {
		return 0, 0, false
	}
	return state.channelID, len(state.keys), true
}

func processFile(
	file string,
	client *annict.Client,
	c *cache.Cache,
	workCache map[string][]annict.Work,
	episodesCache map[int][]annict.Episode,
	programsCache map[programsCacheKey][]annict.Program,
	plannedDestinations map[string]string,
	dryRun, verbose bool,
	confidenceThreshold int,
	outputDir string,
	contexts ...*processingContext,
) *renamer.RenameResult {
	var runtime *processingContext
	if len(contexts) > 0 {
		runtime = contexts[0]
	}
	baseName := filepath.Base(file)
	fmt.Fprintf(os.Stderr, "Processing: %s\n", baseName)

	// Step 1: Parse filename
	meta, err := parser.ParseFilename(baseName)
	if err != nil {
		if errors.Is(err, parser.ErrNoMeaningfulContent) {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("no meaningful work title or episode in %q", baseName),
			}
		}
		if errors.Is(err, parser.ErrAmbiguousEpisode) || errors.Is(err, parser.ErrUnsupportedEpisode) {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("cannot represent as one positive integer episode: %v", err),
			}
		}
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("parse filename: %w", err),
		}
	}

	fmt.Fprintf(os.Stderr, "  Parsed:    Work=%q Episode=%d Subtitle=%q Date=%s\n",
		meta.WorkTitle, meta.EpisodeNumber, meta.Subtitle,
		meta.RecordedDate.Format("2006-01-02"))
	numberlessRecovery := meta.EpisodeNumber <= 0
	dateOnlyRecovery := isDateOnlyRecoveryCandidate(meta)
	dateBackedRecovery := isDateBackedRecoveryCandidate(meta)
	if meta.EpisodeNumber <= 0 && meta.Subtitle == "" && !meta.FinalEpisode && !dateOnlyRecovery {
		return &renamer.RenameResult{
			OriginalPath: file,
			SkipReason:   fmt.Sprintf("no supported single episode number, subtitle, or final-episode marker found in %q", baseName),
		}
	}

	// Step 2: Search Annict for work
	works, err := searchWork(client, c, meta.WorkTitle, workCache, episodesCache)
	if err != nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("search work: %w", err),
		}
	}
	if len(works) == 0 {
		if hint, ok := directoryTitleHint(file, meta.WorkTitle); ok {
			works, err = searchWork(client, c, hint, workCache, episodesCache)
			if err != nil {
				return &renamer.RenameResult{
					OriginalPath: file,
					Error:        fmt.Errorf("search work using directory title %q: %w", hint, err),
				}
			}
			if len(works) > 0 {
				metadataWithHint := *meta
				metadataWithHint.WorkTitle = hint
				meta = &metadataWithHint
				fmt.Fprintf(os.Stderr, "  Fallback:  using parent directory as work title: %q\n", hint)
			}
		}
	}

	if len(works) == 0 {
		if numberlessRecovery {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("numberless episode could not identify an Annict work for %q", meta.WorkTitle),
			}
		}
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("no works found for %q", meta.WorkTitle),
		}
	}

	fmt.Fprintf(os.Stderr, "  Annict:    %d work candidate(s): %s\n", len(works), workTitles(works))

	// Work entries cached before Syobocal support do not contain a TID. Refresh
	// only date-only candidates and numbered files that can serve as batch
	// anchors, leaving unrelated numbered paths on their existing cache flow.
	needsAnchorMetadata := runtime != nil && runtime.batchSchedule != nil && runtime.batchSchedule.wantsAnchors(meta.WorkTitle, works[0].Title)
	if (dateBackedRecovery || needsAnchorMetadata) && len(works) == 1 && works[0].SyobocalTID == "" {
		originalWork := works[0]
		refreshed, refreshErr := refreshWorkMetadata(client, c, meta.WorkTitle, workCache, episodesCache)
		if refreshErr != nil && dateOnlyRecovery {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("date-only episode could not refresh Annict metadata: %v", refreshErr),
			}
		}
		if refreshErr == nil && len(refreshed) == 1 && refreshed[0].ID == originalWork.ID {
			works = refreshed
		}
		if dateOnlyRecovery && (len(refreshed) != 1 || refreshed[0].ID != originalWork.ID) {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("date-only episode could not uniquely refresh Annict work %q", meta.WorkTitle),
			}
		}
	}

	// Step 3: Get episodes for each candidate work
	for _, w := range works {
		if _, ok := episodesCache[w.ID]; !ok {
			episodes, err := getEpisodes(client, c, w.ID, episodesCache)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: could not get episodes for %s: %v\n", w.Title, err)
				continue
			}
			episodesCache[w.ID] = episodes
			fmt.Fprintf(os.Stderr, "  Episodes: %d for %q\n", len(episodes), w.Title)
		}
	}

	// Step 4: Match work and episode before fetching date-sensitive program
	// data. Programs only verify the already-selected episode; they do not
	// participate in work disambiguation, so fetching them for every work
	// candidate wastes one API request per rejected candidate.
	var result *matcher.MatchResult
	if dateOnlyRecovery {
		var scheduleClient syobocalProgramClient
		var batchSchedule *batchScheduleContext
		if runtime != nil {
			scheduleClient = runtime.syobocalClient
			batchSchedule = runtime.batchSchedule
		}
		result, err = matchDateOnly(meta, works, episodesCache, c, scheduleClient, batchSchedule)
		if err != nil && len(works) == 1 {
			relatedWorks, relatedErr := searchRelatedWorks(client, c, meta.WorkTitle, workCache, episodesCache)
			if relatedErr == nil && len(relatedWorks) > len(works) {
				if relatedResult, relatedDateErr := matchDateOnlyAcrossWorks(meta, relatedWorks, episodesCache, c, scheduleClient, batchSchedule); relatedDateErr == nil {
					result = relatedResult
					err = nil
					fmt.Fprintf(os.Stderr, "  Fallback:  date uniquely identified an explicitly named related work\n")
				} else {
					err = fmt.Errorf("%v; related works: %w", err, relatedDateErr)
				}
			} else if relatedErr != nil {
				err = fmt.Errorf("%v; search related works: %w", err, relatedErr)
			}
		}
		if err != nil {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("date-only episode could not be verified: %v", err),
			}
		}
	} else if meta.FinalEpisode && meta.EpisodeNumber == 0 && meta.Subtitle == "" && !meta.RecordedDate.IsZero() && len(works) == 1 {
		var scheduleClient syobocalProgramClient
		var batchSchedule *batchScheduleContext
		if runtime != nil {
			scheduleClient = runtime.syobocalClient
			batchSchedule = runtime.batchSchedule
		}
		relatedWorks, relatedErr := searchRelatedWorks(client, c, meta.WorkTitle, workCache, episodesCache)
		if relatedErr != nil {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("dated final episode could not search related works: %v", relatedErr),
			}
		}
		if len(relatedWorks) > len(works) {
			result, err = matchDateOnlyAcrossWorks(meta, relatedWorks, episodesCache, c, scheduleClient, batchSchedule)
			if err != nil {
				return &renamer.RenameResult{
					OriginalPath: file,
					SkipReason:   fmt.Sprintf("dated final episode could not be verified across related works: %v", err),
				}
			}
			fmt.Fprintf(os.Stderr, "  Fallback:  date selected the final episode of an explicitly named related work\n")
		} else {
			result = matcher.Match(meta, works, episodesCache, nil)
		}
	} else {
		result = matcher.Match(meta, works, episodesCache, nil)
		if meta.EpisodeNumber > 0 && meta.Subtitle == "" && !meta.RecordedDate.IsZero() && len(works) > 1 &&
			(result == nil || result.Episode == nil || result.Confidence < matcher.AutoRenameThreshold) {
			var scheduleClient syobocalProgramClient
			if runtime != nil {
				scheduleClient = runtime.syobocalClient
			}
			if dateResult, dateErr := matchNumberedDateAcrossWorks(meta, works, episodesCache, c, scheduleClient); dateErr == nil {
				result = dateResult
				fmt.Fprintf(os.Stderr, "  Fallback:  date and episode number uniquely identified an Annict work\n")
			} else if result != nil {
				result.Reasons = append(result.Reasons, fmt.Sprintf("numbered date disambiguation failed: %v", dateErr))
			}
		}
		if dateBackedRecovery && (result == nil || result.Episode == nil || result.Confidence < matcher.AutoRenameThreshold) {
			var scheduleClient syobocalProgramClient
			var batchSchedule *batchScheduleContext
			if runtime != nil {
				scheduleClient = runtime.syobocalClient
				batchSchedule = runtime.batchSchedule
			}
			if dateResult, dateErr := matchDateOnly(meta, works, episodesCache, c, scheduleClient, batchSchedule); dateErr == nil {
				result = dateResult
			} else if result != nil {
				if len(works) == 1 {
					relatedWorks, relatedErr := searchRelatedWorks(client, c, meta.WorkTitle, workCache, episodesCache)
					if relatedErr == nil {
						if relatedResult, relatedDateErr := matchDateBackedRelated(meta, relatedWorks, episodesCache, c, scheduleClient); relatedDateErr == nil {
							result = relatedResult
							dateErr = nil
						} else {
							dateErr = fmt.Errorf("%v; related works: %w", dateErr, relatedDateErr)
						}
					} else {
						dateErr = fmt.Errorf("%v; search related works: %w", dateErr, relatedErr)
					}
				}
				if dateErr != nil {
					result.Reasons = append(result.Reasons, fmt.Sprintf("date-backed subtitle recovery failed: %v", dateErr))
				}
			}
		}
	}
	if result == nil {
		if numberlessRecovery {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("numberless episode did not uniquely match %q", meta.WorkTitle),
			}
		}
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("no match found for %q", meta.WorkTitle),
		}
	}
	usedRelatedWorks := false
	// An older work can share the exact base title and episode numbers with a
	// newer, explicitly labelled continuation or remake. If the selected
	// episode contradicts the file subtitle, give those related works a chance
	// to provide a stronger match instead of stopping at the first number.
	shouldRetryRelated := !dateOnlyRecovery && (result.Episode == nil || (meta.Subtitle != "" && result.Confidence < matcher.AutoRenameThreshold))
	if shouldRetryRelated {
		relatedWorks, relatedErr := searchRelatedWorks(client, c, meta.WorkTitle, workCache, episodesCache)
		if relatedErr != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  Warning: could not search related seasons: %v\n", relatedErr)
			}
		} else if relatedResult := matcher.MatchRelated(meta, relatedWorks, episodesCache, nil); relatedResult != nil && relatedResult.Episode != nil && (result.Episode == nil || relatedResult.Confidence > result.Confidence) {
			works = relatedWorks
			result = relatedResult
			usedRelatedWorks = true
			fmt.Fprintf(os.Stderr, "  Fallback:  found a stronger match in an explicitly named related work\n")
		}
	}

	// Step 5: Fetch programs only when date verification can change the
	// threshold decision, or when verbose output explicitly requests program
	// details. Programs do not alter the selected work or episode, so a normal
	// exact match that already meets the threshold needs no extra API round
	// trip. The in-memory cache remains keyed by work and recording date.
	needsProgram := !dateOnlyRecovery && (verbose || result.Confidence < confidenceThreshold)
	if needsProgram && result.Work != nil && result.Episode != nil && !meta.RecordedDate.IsZero() {
		programs, err := getPrograms(client, result.Work.ID, meta.RecordedDate, programsCache)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "  Warning: could not get programs for %s: %v\n", result.Work.Title, err)
			}
		} else {
			if verbose {
				fmt.Fprintf(os.Stderr, "  Programs: %d for %q\n", len(programs), result.Work.Title)
			}
			if len(programs) > 0 {
				programsByWork := map[int][]annict.Program{result.Work.ID: programs}
				if usedRelatedWorks {
					result = matcher.MatchRelated(meta, works, episodesCache, programsByWork)
				} else {
					result = matcher.Match(meta, works, episodesCache, programsByWork)
				}
			}
		}
	}

	// Always show match result (even without --verbose)
	fmt.Fprintf(os.Stderr, "  Match:     confidence=%d threshold=%d\n", result.Confidence, confidenceThreshold)
	for _, reason := range result.Reasons {
		fmt.Fprintf(os.Stderr, "    - %s\n", reason)
	}

	if verbose && result.Work != nil {
		fmt.Fprintf(os.Stderr, "  Annict detail:\n")
		fmt.Fprintf(os.Stderr, "    Work:      %s (ID: %d, season: %s)\n", result.Work.Title, result.Work.ID, result.Work.SeasonName)
		if result.Episode != nil {
			epNum, _ := matcher.MatchResultEpisodeNumber(result)
			fmt.Fprintf(os.Stderr, "    Episode:   %d - %s (ID: %d)\n", epNum, result.Episode.Title, result.Episode.ID)
		}
		if result.Program != nil {
			fmt.Fprintf(os.Stderr, "    Program:   %s (rebroadcast: %v, channel: %s)\n",
				result.Program.StartedAt.Format("2006-01-02 15:04"),
				result.Program.IsRebroadcast,
				result.Program.Channel.Name)
		}
	}

	// Step 6: Check confidence
	if result.Confidence < confidenceThreshold {
		if numberlessRecovery {
			return &renamer.RenameResult{
				OriginalPath: file,
				SkipReason:   fmt.Sprintf("numberless episode confidence %d below threshold %d", result.Confidence, confidenceThreshold),
			}
		}
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("confidence %d below threshold %d", result.Confidence, confidenceThreshold),
		}
	}

	// Numbered recordings in the same batch can fingerprint a broadcast
	// channel for later date-only files. Failure to establish an anchor never
	// changes the already-safe numbered result.
	if runtime != nil && runtime.batchSchedule != nil && runtime.syobocalClient != nil &&
		meta.EpisodeNumber > 0 && !meta.RecordedDate.IsZero() && result.Work != nil && result.Episode != nil &&
		runtime.batchSchedule.wantsAnchors(meta.WorkTitle, result.Work.Title) {
		if anchorReason, anchorErr := observeScheduleAnchor(meta, result, c, runtime.syobocalClient, runtime.batchSchedule); verbose {
			if anchorErr != nil {
				fmt.Fprintf(os.Stderr, "  Schedule anchor skipped: %v\n", anchorErr)
			} else {
				fmt.Fprintf(os.Stderr, "  Schedule anchor: %s\n", anchorReason)
			}
		}
	}

	// Step 7: Reserve the batch destination before renaming. The filesystem
	// no-replace checks still protect against pre-existing entries; this map
	// additionally catches two sources in the same batch that would otherwise
	// both appear valid during a dry-run.
	plannedPath, err := renamer.BuildDestinationPath(file, result, outputDir)
	if err != nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("build destination: %w", err),
		}
	}
	planKey := filepath.Clean(plannedPath)
	if previousSource, exists := plannedDestinations[planKey]; exists && previousSource != file {
		return &renamer.RenameResult{
			OriginalPath: file,
			NewPath:      plannedPath,
			Error:        fmt.Errorf("batch destination collision: %s is also planned from %s", plannedPath, previousSource),
		}
	}

	// Step 8: Rename (or preview in dry-run mode). outputDir, if set, is
	// honored for both the actual move and the dry-run preview.
	result2 := renamer.Rename(file, result, dryRun, outputDir)
	if result2.Error != nil {
		return result2
	}
	plannedDestinations[planKey] = file

	fmt.Fprintf(os.Stderr, "  Rename:    %s\n", filepath.Base(result2.NewPath))
	return result2
}

func isDateOnlyRecoveryCandidate(meta *parser.RecordingMetadata) bool {
	return isDateBackedRecoveryCandidate(meta) && meta.Subtitle == ""
}

func isDateBackedRecoveryCandidate(meta *parser.RecordingMetadata) bool {
	if meta == nil || meta.EpisodeNumber > 0 || meta.FinalEpisode || meta.RecordedDate.IsZero() {
		return false
	}
	title := normalize.Normalize(meta.WorkTitle)
	for _, marker := range []string{"総集編", "特別編", "特番", "スペシャル", "一挙放送", "セレクション", "劇場版", "映画"} {
		if strings.Contains(title, marker) {
			return false
		}
	}
	return true
}

// prioritizeDateOnlyFiles keeps each group stable but places explicit
// episode identities first. That guarantees channel anchors are available to
// every date-only candidate and gives explicit recordings first claim on a
// duplicate destination. Subtitle-bearing recovery candidates do not request
// anchors: doing so would add schedule lookups for every numbered recording
// in the same work merely to support one fallback.
func prioritizeDateOnlyFiles(files []string) ([]string, *batchScheduleContext) {
	batch := newBatchScheduleContext()
	dateOnly := make([]bool, len(files))
	for i, file := range files {
		meta, err := parser.ParseFilename(filepath.Base(file))
		if err != nil || !isDateOnlyRecoveryCandidate(meta) {
			continue
		}
		dateOnly[i] = true
		batch.targetTitles[normalize.NormalizeTitleForMatch(meta.WorkTitle)] = true
	}

	ordered := make([]string, 0, len(files))
	for i, file := range files {
		if !dateOnly[i] {
			ordered = append(ordered, file)
		}
	}
	for i, file := range files {
		if dateOnly[i] {
			ordered = append(ordered, file)
		}
	}
	return ordered, batch
}

func directoryTitleHint(file, parsedTitle string) (string, bool) {
	dir := filepath.Clean(filepath.Dir(file))
	hint := filepath.Base(dir)
	if hint == "" || hint == "." || hint == string(filepath.Separator) {
		return "", false
	}
	if normalize.NormalizeForSearch(hint) == normalize.NormalizeForSearch(parsedTitle) {
		return "", false
	}
	return hint, true
}

func workTitles(works []annict.Work) string {
	titles := make([]string, len(works))
	for i, w := range works {
		titles[i] = fmt.Sprintf("%q", w.Title)
	}
	return strings.Join(titles, ", ")
}

func refreshWorkMetadata(client *annict.Client, c *cache.Cache, title string, wc map[string][]annict.Work, ec map[int][]annict.Episode) ([]annict.Work, error) {
	works, episodesByWork, err := client.SearchWorks(title)
	if err != nil {
		return nil, err
	}
	works = matcher.MatchingWorks(title, works)
	wc[title] = works
	if len(works) == 1 {
		_ = c.SetWork(title, &works[0])
	}
	for _, work := range works {
		episodes, ok := episodesByWork[work.ID]
		if !ok || !episodesComplete(work, episodes) {
			continue
		}
		ec[work.ID] = episodes
		_ = c.SetEpisodes(work.ID, episodes)
	}
	return works, nil
}

func matchDateOnly(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, c *cache.Cache, client syobocalProgramClient, batch *batchScheduleContext) (*matcher.MatchResult, error) {
	if len(works) != 1 {
		return nil, fmt.Errorf("annict work is ambiguous (%d candidates)", len(works))
	}
	work := works[0]
	tid, err := strconv.Atoi(work.SyobocalTID)
	if err != nil || tid <= 0 {
		return nil, fmt.Errorf("annict work %q has no valid Syobocal TID", work.Title)
	}
	if client == nil {
		return nil, fmt.Errorf("syobocal client is unavailable")
	}

	programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
	if err != nil {
		return nil, err
	}

	episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, episodesByWork[work.ID], programs, meta.Subtitle)
	if episode == nil {
		if channelID, anchors, ok := batch.trustedChannel(work.ID); ok {
			if channelEpisode, channelReason := dateinfer.ResolveForChannelWithSubtitle(meta.RecordedDate, episodesByWork[work.ID], programs, channelID, meta.Subtitle); channelEpisode != nil {
				episode = channelEpisode
				reason = fmt.Sprintf("%s using %d consistent batch anchors", channelReason, anchors)
			} else {
				reason = fmt.Sprintf("%s; trusted channel also failed: %s", reason, channelReason)
			}
		}
	}
	if episode == nil {
		return nil, errors.New(reason)
	}
	return &matcher.MatchResult{
		Work:       &work,
		Episode:    episode,
		Confidence: matcher.AutoRenameThreshold,
		Reasons: []string{
			"work title uniquely matched Annict",
			reason,
		},
	}, nil
}

func matchDateOnlyAcrossWorks(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, c *cache.Cache, client syobocalProgramClient, batch *batchScheduleContext) (*matcher.MatchResult, error) {
	if len(works) == 0 {
		return nil, errors.New("no related Annict works")
	}
	if len(works) == 1 {
		return matchDateOnly(meta, works, episodesByWork, c, client, batch)
	}
	if client == nil {
		return nil, errors.New("syobocal client is unavailable")
	}

	type resolvedWork struct {
		work    annict.Work
		episode *annict.Episode
		reason  string
	}
	resolved := make([]resolvedWork, 0, 1)
	var unresolved []string
	for _, work := range works {
		tid, err := strconv.Atoi(work.SyobocalTID)
		if err != nil || tid <= 0 {
			unresolved = append(unresolved, fmt.Sprintf("%q has no valid Syobocal TID", work.Title))
			continue
		}
		programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
		if err != nil {
			unresolved = append(unresolved, fmt.Sprintf("%q schedule lookup failed: %v", work.Title, err))
			continue
		}
		episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, episodesByWork[work.ID], programs, meta.Subtitle)
		if episode == nil {
			if channelID, anchors, ok := batch.trustedChannel(work.ID); ok {
				if channelEpisode, channelReason := dateinfer.ResolveForChannelWithSubtitle(meta.RecordedDate, episodesByWork[work.ID], programs, channelID, meta.Subtitle); channelEpisode != nil {
					episode = channelEpisode
					reason = fmt.Sprintf("%s using %d consistent batch anchors", channelReason, anchors)
				} else {
					reason = fmt.Sprintf("%s; trusted channel also failed: %s", reason, channelReason)
				}
			}
		}
		if episode == nil {
			if dateinfer.HasUsableSchedule(meta.RecordedDate, programs) {
				unresolved = append(unresolved, fmt.Sprintf("%q: %s", work.Title, reason))
			}
			continue
		}
		resolved = append(resolved, resolvedWork{work: work, episode: episode, reason: reason})
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("related work candidates remain unverified: %s", strings.Join(unresolved, "; "))
	}
	if len(resolved) != 1 {
		return nil, fmt.Errorf("date resolved %d related works", len(resolved))
	}
	match := resolved[0]
	return &matcher.MatchResult{
		Work:       &match.work,
		Episode:    match.episode,
		Confidence: matcher.AutoRenameThreshold,
		Reasons: []string{
			"related work title and date uniquely matched",
			match.reason,
		},
	}, nil
}

func matchNumberedDateAcrossWorks(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, c *cache.Cache, client syobocalProgramClient) (*matcher.MatchResult, error) {
	if meta.EpisodeNumber <= 0 || meta.RecordedDate.IsZero() {
		return nil, errors.New("episode number or recording date is missing")
	}
	if len(works) < 2 {
		return nil, errors.New("multiple Annict work candidates are required")
	}
	if client == nil {
		return nil, errors.New("syobocal client is unavailable")
	}

	type resolvedWork struct {
		work    annict.Work
		episode *annict.Episode
		reason  string
	}
	resolved := make([]resolvedWork, 0, 1)
	var unresolved []string
	for _, work := range works {
		tid, err := strconv.Atoi(work.SyobocalTID)
		if err != nil || tid <= 0 {
			unresolved = append(unresolved, fmt.Sprintf("%q has no valid Syobocal TID", work.Title))
			continue
		}
		programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
		if err != nil {
			unresolved = append(unresolved, fmt.Sprintf("%q schedule lookup failed: %v", work.Title, err))
			continue
		}
		episode, reason := dateinfer.ResolveUnique(meta.RecordedDate, episodesByWork[work.ID], programs)
		if episode == nil {
			if dateinfer.HasUsableSchedule(meta.RecordedDate, programs) {
				unresolved = append(unresolved, fmt.Sprintf("%q: %s", work.Title, reason))
			}
			continue
		}
		number, ok := matcher.EpisodeNumber(episode)
		if !ok || number != meta.EpisodeNumber {
			continue
		}
		resolved = append(resolved, resolvedWork{work: work, episode: episode, reason: reason})
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("work candidates remain unverified: %s", strings.Join(unresolved, "; "))
	}
	if len(resolved) != 1 {
		return nil, fmt.Errorf("date and episode number resolved %d works", len(resolved))
	}
	match := resolved[0]
	return &matcher.MatchResult{
		Work:       &match.work,
		Episode:    match.episode,
		Confidence: matcher.AutoRenameThreshold,
		Reasons: []string{
			"work title, recording date, and episode number uniquely matched",
			match.reason,
		},
	}, nil
}

func matchDateBackedRelated(meta *parser.RecordingMetadata, works []annict.Work, episodesByWork map[int][]annict.Episode, c *cache.Cache, client syobocalProgramClient) (*matcher.MatchResult, error) {
	if client == nil {
		return nil, fmt.Errorf("syobocal client is unavailable")
	}
	type resolvedWork struct {
		work    annict.Work
		episode *annict.Episode
		reason  string
	}
	var resolved []resolvedWork
	var unresolved []string
	compatibleWorks := 0
	for _, work := range works {
		compatible := false
		for i := range episodesByWork[work.ID] {
			if episodesByWork[work.ID][i].Title != "" && matcher.DateProvenSubtitleMatch(episodesByWork[work.ID][i].Title, meta.Subtitle) {
				compatible = true
				break
			}
		}
		if !compatible {
			continue
		}
		compatibleWorks++
		tid, err := strconv.Atoi(work.SyobocalTID)
		if err != nil || tid <= 0 {
			unresolved = append(unresolved, fmt.Sprintf("%q has no valid Syobocal TID", work.Title))
			continue
		}
		programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
		if err != nil {
			unresolved = append(unresolved, fmt.Sprintf("%q schedule lookup failed: %v", work.Title, err))
			continue
		}
		if !dateinfer.HasUsableSchedule(meta.RecordedDate, programs) {
			continue
		}
		episode, reason := dateinfer.ResolveUniqueWithSubtitle(meta.RecordedDate, episodesByWork[work.ID], programs, meta.Subtitle)
		if episode == nil {
			unresolved = append(unresolved, fmt.Sprintf("%q: %s", work.Title, reason))
			continue
		}
		resolved = append(resolved, resolvedWork{work: work, episode: episode, reason: reason})
	}
	if compatibleWorks == 0 {
		return nil, fmt.Errorf("no related work has a compatible official subtitle")
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("related work candidates remain unverified: %s", strings.Join(unresolved, "; "))
	}
	if len(resolved) != 1 {
		return nil, fmt.Errorf("date and subtitle resolved %d related works", len(resolved))
	}
	match := resolved[0]
	return &matcher.MatchResult{
		Work:       &match.work,
		Episode:    match.episode,
		Confidence: matcher.AutoRenameThreshold,
		Reasons: []string{
			"related work title and date-proven subtitle matched",
			match.reason,
		},
	}, nil
}

func getSyobocalPrograms(c *cache.Cache, client syobocalProgramClient, tid int, date time.Time) ([]syobocal.Program, error) {
	if programs, ok := c.GetSyobocalPrograms(tid, date); ok {
		return programs, nil
	}
	programs, err := client.GetPrograms(tid, date)
	if err != nil {
		return nil, err
	}
	_ = c.SetSyobocalPrograms(tid, date, programs)
	return programs, nil
}

func observeScheduleAnchor(meta *parser.RecordingMetadata, result *matcher.MatchResult, c *cache.Cache, client syobocalProgramClient, batch *batchScheduleContext) (string, error) {
	tid, err := strconv.Atoi(result.Work.SyobocalTID)
	if err != nil || tid <= 0 {
		return "", fmt.Errorf("annict work %q has no valid Syobocal TID", result.Work.Title)
	}
	programs, err := getSyobocalPrograms(c, client, tid, meta.RecordedDate)
	if err != nil {
		return "", err
	}
	channelID, reason := dateinfer.AnchorChannel(meta.RecordedDate, result.Episode, programs)
	if channelID <= 0 {
		return "", errors.New(reason)
	}
	episodeNumber, _ := matcher.MatchResultEpisodeNumber(result)
	key := fmt.Sprintf("%s:%d", meta.RecordedDate.In(time.FixedZone("JST", 9*60*60)).Format("2006-01-02"), episodeNumber)
	batch.record(result.Work.ID, channelID, key)
	return reason, nil
}

func searchWork(client *annict.Client, c *cache.Cache, title string, wc map[string][]annict.Work, ec map[int][]annict.Episode) ([]annict.Work, error) {
	// Check in-memory cache first (cheapest, and populated even for
	// ambiguous results so repeated files of the same show within this run
	// don't hit the Annict API again).
	if works, ok := wc[title]; ok {
		return works, nil
	}

	// Check persistent disk cache (only ever stores unambiguous matches).
	// Revalidate the title so a stale cache entry produced by an older matching
	// rule cannot suppress a fresh API search for its full TTL.
	if cached, ok := c.GetWork(title); ok {
		works := matcher.MatchingWorks(title, []annict.Work{*cached})
		if len(works) > 0 {
			wc[title] = works
			return works, nil
		}
	}

	// Search Annict
	works, episodesByWork, err := client.SearchWorks(title)
	if err != nil {
		return nil, err
	}

	// Annict search is intentionally fuzzy. Match uses stricter normalized
	// exact/substring rules, so filter by those same rules before fetching and
	// caching episodes for every raw search result.
	works = matcher.MatchingWorks(title, works)

	wc[title] = works
	if len(works) == 1 {
		_ = c.SetWork(title, &works[0])
	}

	// The GraphQL search response already includes up to 100 episodes per
	// work. Reuse them directly instead of a redundant REST call, but only
	// when they fully cover the work's known episode count — otherwise
	// getEpisodes falls back to the paginated REST fetch as usual.
	for _, w := range works {
		episodes, ok := episodesByWork[w.ID]
		if !ok || !episodesComplete(w, episodes) {
			continue
		}
		if _, cached := ec[w.ID]; cached {
			continue
		}
		ec[w.ID] = episodes
		_ = c.SetEpisodes(w.ID, episodes)
	}

	return works, nil
}

const relatedWorkCachePrefix = "\x00related:"

func searchRelatedWorks(client *annict.Client, c *cache.Cache, title string, wc map[string][]annict.Work, ec map[int][]annict.Episode) ([]annict.Work, error) {
	cacheKey := relatedWorkCachePrefix + title
	if works, ok := wc[cacheKey]; ok {
		return works, nil
	}

	works, episodesByWork, err := client.SearchWorks(title)
	if err != nil {
		return nil, err
	}
	works = matcher.MatchingRelatedWorks(title, works)
	wc[cacheKey] = works

	for _, work := range works {
		if episodes, ok := episodesByWork[work.ID]; ok && episodesComplete(work, episodes) {
			if _, cached := ec[work.ID]; !cached {
				ec[work.ID] = episodes
				_ = c.SetEpisodes(work.ID, episodes)
			}
		}
	}
	for _, work := range works {
		if _, ok := ec[work.ID]; ok {
			continue
		}
		if _, err := getEpisodes(client, c, work.ID, ec); err != nil {
			return nil, fmt.Errorf("get episodes for related work %q: %w", work.Title, err)
		}
	}
	return works, nil
}

// episodesComplete reports whether episodes (as returned alongside a
// GraphQL work search) fully covers the work's known episode count, i.e.
// it's safe to use directly instead of falling back to a full REST fetch.
func episodesComplete(w annict.Work, episodes []annict.Episode) bool {
	return w.EpisodesCount > 0 && len(episodes) >= w.EpisodesCount
}

func getEpisodes(client *annict.Client, c *cache.Cache, workID int, ec map[int][]annict.Episode) ([]annict.Episode, error) {
	// Check in-memory cache first (cheapest)
	if eps, ok := ec[workID]; ok {
		return eps, nil
	}

	// Check disk cache
	if cached, ok := c.GetEpisodes(workID); ok {
		ec[workID] = cached
		return cached, nil
	}

	// Fetch from API
	episodes, err := client.GetEpisodes(workID)
	if err != nil {
		return nil, err
	}

	_ = c.SetEpisodes(workID, episodes)
	ec[workID] = episodes
	return episodes, nil
}

// programsCacheKey caches a work's programs per recorded date, not just per
// work ID: the fetched window (date-1day .. date+2days) depends entirely on
// the date, so two files for the same work on different dates must not
// share a cache entry — see getPrograms.
type programsCacheKey struct {
	WorkID int
	Date   string // date formatted as 2006-01-02
}

func getPrograms(client *annict.Client, workID int, date time.Time, pc map[programsCacheKey][]annict.Program) ([]annict.Program, error) {
	key := programsCacheKey{WorkID: workID, Date: date.Format("2006-01-02")}

	// Check in-memory cache (programs are date-sensitive, not cached to disk)
	if progs, ok := pc[key]; ok {
		return progs, nil
	}

	since := date.AddDate(0, 0, -1)
	until := date.AddDate(0, 0, 2)
	programs, err := client.GetPrograms(workID, since, until)
	if err != nil {
		return nil, err
	}

	pc[key] = programs
	return programs, nil
}

func collectFiles(target string, recursive bool) ([]string, error) {
	return collectFilesWithWalker(target, recursive, filepath.WalkDir)
}

type walkDirFunc func(string, fs.WalkDirFunc) error

func collectFilesWithWalker(target string, recursive bool, walk walkDirFunc) ([]string, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", target, err)
	}

	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("target is not a regular file: %s", target)
		}
		if !isSupportedRecordingExtension(target) {
			return nil, fmt.Errorf("unsupported recording extension: %s", filepath.Ext(target))
		}
		return []string{target}, nil
	}

	var files []string
	appendRecording := func(path string, entry fs.DirEntry) error {
		if !isSupportedRecordingExtension(path) {
			return nil
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat candidate %s: %w", path, err)
		}
		if entryInfo.Mode().IsRegular() {
			files = append(files, path)
		}
		return nil
	}

	if recursive {
		err = walk(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			return appendRecording(path, d)
		})
		if err != nil {
			return nil, fmt.Errorf("walk dir %s: %w", target, err)
		}
	} else {
		entries, err := os.ReadDir(target)
		if err != nil {
			return nil, fmt.Errorf("read dir %s: %w", target, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := appendRecording(filepath.Join(target, e.Name()), e); err != nil {
				return nil, err
			}
		}
	}

	return files, nil
}

func isSupportedRecordingExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".ts", ".mkv", ".m4v", ".m2ts":
		return true
	default:
		return false
	}
}

func validateTargetArgs(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("expected exactly one file or directory, got %d", len(args))
	}
	return nil
}

func validateConfidenceThreshold(value int) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("confidence must be between 0 and 100: %d", value)
	}
	return nil
}

// loadTokenFromDotenv looks for .env in target's own directory (or target
// itself, if it is a directory) and its parent directories.
func loadTokenFromDotenv(target string) string {
	dir := target
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		dir = filepath.Dir(target)
	}

	for {
		if token, ok := readTokenFromEnvFile(filepath.Join(dir, ".env")); ok {
			return token
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root (e.g. "/" or "C:\"); filepath.Dir
			// is a no-op there on every OS, so stop instead of looping forever.
			break
		}
		dir = parent
	}
	return ""
}

// readTokenFromEnvFile reads ANNICT_ACCESS_TOKEN from a dotenv-style file.
func readTokenFromEnvFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		if key != "ANNICT_ACCESS_TOKEN" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if value == "" {
			continue
		}
		return value, true
	}
	return "", false
}
