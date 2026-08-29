package main

import (
	"bufio"
	"flag"
	"fmt"
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

	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "anime-renamer")
	var c *cache.Cache
	if *noCache {
		c = cache.New(cacheDir)
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
	workCache := make(map[string]*annict.Work)
	episodesCache := make(map[int][]annict.Episode)
	programsCache := make(map[int][]annict.Program)

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
}

func processFile(
	file string,
	client *annict.Client,
	c *cache.Cache,
	workCache map[string]*annict.Work,
	episodesCache map[int][]annict.Episode,
	programsCache map[int][]annict.Program,
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
	works, err := searchWork(client, c, meta.WorkTitle, workCache)
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

	// Step 4: Get programs for each candidate work
	if !meta.RecordedDate.IsZero() {
		for _, w := range works {
			if _, ok := programsCache[w.ID]; !ok {
				since := meta.RecordedDate.AddDate(0, 0, -1)
				until := meta.RecordedDate.AddDate(0, 0, 2)
				programs, err := getPrograms(client, c, w.ID, since, until, programsCache)
				if err != nil {
					if verbose {
						fmt.Fprintf(os.Stderr, "  Warning: could not get programs for %s: %v\n", w.Title, err)
					}
					continue
				}
				programsCache[w.ID] = programs
				if verbose {
					fmt.Fprintf(os.Stderr, "  Programs: %d for %q\n", len(programs), w.Title)
				}
			}
		}
	}

	// Step 5: Match
	result := matcher.Match(meta, works, episodesCache, programsCache)
	if result == nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("no match found for %q", meta.WorkTitle),
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

	// Step 7: Build destination path
	newPath, err := renamer.BuildPath(file, result)
	if err != nil {
		return &renamer.RenameResult{
			OriginalPath: file,
			Error:        fmt.Errorf("build path: %w", err),
		}
	}

	// Override output directory if specified
	if outputDir != "" {
		rel, err := filepath.Rel(filepath.Dir(file), newPath)
		if err == nil {
			newPath = filepath.Join(outputDir, rel)
		}
	}

	fmt.Fprintf(os.Stderr, "  Rename:    %s\n", filepath.Base(newPath))

	if dryRun {
		return &renamer.RenameResult{
			OriginalPath: file,
			NewPath:      newPath,
			WorkTitle:    result.Work.Title,
			Confidence:   result.Confidence,
			Renamed:      false,
		}
	}

	// Perform rename
	result2 := renamer.Rename(file, result, false)
	if result2.Error != nil {
		return result2
	}
	result2.NewPath = newPath
	return result2
}

func workTitles(works []annict.Work) string {
	titles := make([]string, len(works))
	for i, w := range works {
		titles[i] = fmt.Sprintf("%q", w.Title)
	}
	return strings.Join(titles, ", ")
}

func searchWork(client *annict.Client, c *cache.Cache, title string, wc map[string]*annict.Work) ([]annict.Work, error) {
	// Check cache first
	if cached, ok := c.GetWork(title); ok {
		return []annict.Work{*cached}, nil
	}

	// Check in-memory cache
	if w, ok := wc[title]; ok {
		return []annict.Work{*w}, nil
	}

	// Search Annict
	works, err := client.SearchWorks(title)
	if err != nil {
		return nil, err
	}

	// Cache results
	if len(works) == 1 {
		_ = c.SetWork(title, &works[0])
		wc[title] = &works[0]
	}

	return works, nil
}

func getEpisodes(client *annict.Client, c *cache.Cache, workID int, ec map[int][]annict.Episode) ([]annict.Episode, error) {
	// Check cache
	if cached, ok := c.GetEpisodes(workID); ok {
		ec[workID] = cached
		return cached, nil
	}

	// Check in-memory cache
	if eps, ok := ec[workID]; ok {
		return eps, nil
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

func getPrograms(client *annict.Client, c *cache.Cache, workID int, since, until time.Time, pc map[int][]annict.Program) ([]annict.Program, error) {
	// Check in-memory cache
	if progs, ok := pc[workID]; ok {
		return progs, nil
	}

	// Fetch from API (programs are date-sensitive, don't cache long-term)
	programs, err := client.GetPrograms(workID, since, until)
	if err != nil {
		return nil, err
	}

	pc[workID] = programs
	return programs, nil
}

func collectFiles(target string, recursive bool) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", target, err)
	}

	if !info.IsDir() {
		return []string{target}, nil
	}

	var files []string
	exts := map[string]bool{".mp4": true, ".ts": true, ".mkv": true, ".m4v": true}

	if recursive {
		err = filepath.Walk(target, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if exts[ext] {
				files = append(files, path)
			}
			return nil
		})
	} else {
		entries, err := os.ReadDir(target)
		if err != nil {
			return nil, fmt.Errorf("read dir %s: %w", target, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if exts[ext] {
				files = append(files, filepath.Join(target, e.Name()))
			}
		}
	}

	return files, err
}

// loadTokenFromDotenv looks for .env in the file's directory and parent directories.
func loadTokenFromDotenv(path string) string {
	dir := filepath.Dir(path)
	for {
		dotenv := filepath.Join(dir, ".env")
		f, err := os.Open(dotenv)
		if err != nil {
			if dir == "/" {
				break
			}
			dir = filepath.Dir(dir)
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || key != "ANNICT_ACCESS_TOKEN" {
				continue
			}
			value = strings.Trim(value, "'\"")
			f.Close()
			return value
		}
		f.Close()

		if dir == "/" {
			break
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
