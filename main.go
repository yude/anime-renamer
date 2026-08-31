package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/cache"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/parser"
	"github.com/yude/anime-renamer/internal/renamer"
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

	if flag.NArg() < 1 {
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

	fmt.Fprintf(os.Stderr, "Found %d file(s) to process\n\n", len(files))

	// Process each file
	workCache := make(map[string][]annict.Work)
	episodesCache := make(map[int][]annict.Episode)
	programsCache := make(map[programsCacheKey][]annict.Program)

	renamed := 0
	skipped := 0
	failed := 0

	for _, file := range files {
		result := processFile(file, annictClient, c, workCache, episodesCache, programsCache, *dryRun, *verbose, *confidenceThreshold, *outputDir)

		switch {
		case result.Error != nil:
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n\n", result.Error)
			failed++
		case result.Renamed:
			renamed++
		default:
			skipped++
		}
	}

	// Summary
	fmt.Fprintf(os.Stderr, "\n--- Summary ---\n")
	if *dryRun {
		fmt.Fprintf(os.Stderr, "Dry-run mode: no files were renamed\n")
	}
	fmt.Fprintf(os.Stderr, "Renamed: %d\n", renamed)
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

func processFile(
	file string,
	client *annict.Client,
	c *cache.Cache,
	workCache map[string][]annict.Work,
	episodesCache map[int][]annict.Episode,
	programsCache map[programsCacheKey][]annict.Program,
	dryRun, verbose bool,
	confidenceThreshold int,
	outputDir string,
) *renamer.RenameResult {
	baseName := filepath.Base(file)
	fmt.Fprintf(os.Stderr, "Processing: %s\n", baseName)

	// Step 1: Parse filename
	meta, err := parser.ParseFilename(baseName)
	if err != nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("parse filename: %w", err),
		}
	}

	fmt.Fprintf(os.Stderr, "  Parsed:    Work=%q Episode=%d Subtitle=%q Date=%s\n",
		meta.WorkTitle, meta.EpisodeNumber, meta.Subtitle,
		meta.RecordedDate.Format("2006-01-02"))

	// Step 2: Search Annict for work
	works, err := searchWork(client, c, meta.WorkTitle, workCache, episodesCache)
	if err != nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("search work: %w", err),
		}
	}

	if len(works) == 0 {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("no works found for %q", meta.WorkTitle),
		}
	}

	fmt.Fprintf(os.Stderr, "  Annict:    %d work candidate(s): %s\n", len(works), workTitles(works))

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
	result := matcher.Match(meta, works, episodesCache, nil)
	if result == nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("no match found for %q", meta.WorkTitle),
		}
	}

	// Step 5: Fetch programs only for the selected work, then rerun matching
	// to add date/episode verification to the confidence score. The in-memory
	// cache remains keyed by both work and recording date.
	if result.Work != nil && result.Episode != nil && !meta.RecordedDate.IsZero() {
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
				result = matcher.Match(meta, works, episodesCache, map[int][]annict.Program{
					result.Work.ID: programs,
				})
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
			epNum := 0
			if result.Episode.Number != nil {
				epNum = int(*result.Episode.Number)
			} else {
				epNum = result.Episode.SortNumber
			}
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
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("confidence %d below threshold %d", result.Confidence, confidenceThreshold),
		}
	}

	// Step 7: Rename (or preview in dry-run mode). outputDir, if set, is
	// honored for both the actual move and the dry-run preview.
	result2 := renamer.Rename(file, result, dryRun, outputDir)
	if result2.Error != nil {
		return result2
	}

	fmt.Fprintf(os.Stderr, "  Rename:    %s\n", filepath.Base(result2.NewPath))
	return result2
}

func workTitles(works []annict.Work) string {
	titles := make([]string, len(works))
	for i, w := range works {
		titles[i] = fmt.Sprintf("%q", w.Title)
	}
	return strings.Join(titles, ", ")
}

func searchWork(client *annict.Client, c *cache.Cache, title string, wc map[string][]annict.Work, ec map[int][]annict.Episode) ([]annict.Work, error) {
	// Check in-memory cache first (cheapest, and populated even for
	// ambiguous results so repeated files of the same show within this run
	// don't hit the Annict API again).
	if works, ok := wc[title]; ok {
		return works, nil
	}

	// Check persistent disk cache (only ever stores unambiguous matches).
	if cached, ok := c.GetWork(title); ok {
		works := []annict.Work{*cached}
		wc[title] = works
		return works, nil
	}

	// Search Annict
	works, episodesByWork, err := client.SearchWorks(title)
	if err != nil {
		return nil, err
	}

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
	case ".mp4", ".ts", ".mkv", ".m4v":
		return true
	default:
		return false
	}
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
