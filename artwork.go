package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yude/anime-renamer/internal/artwork"
	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/metadata"
	"github.com/yude/anime-renamer/internal/renamer"
)

type artworkJob struct {
	dir     string
	workIDs map[int]bool
	err     error
}

type artworkJobs map[string]*artworkJob

func (jobs artworkJobs) add(dir string, workID int) *artworkJob {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		job := &artworkJob{dir: dir, err: err}
		jobs[dir] = job
		return job
	}
	job := jobs[absolute]
	if job == nil {
		job = &artworkJob{dir: absolute, workIDs: make(map[int]bool)}
		jobs[absolute] = job
	}
	if workID > 0 {
		job.workIDs[workID] = true
	}
	return job
}

func (jobs artworkJobs) matched(path string, match *matcher.MatchResult) {
	if match == nil || match.Work == nil || match.Work.ID <= 0 || match.Confidence < matcher.AutoRenameThreshold {
		return
	}
	canonical, err := renamer.BuildPath(path, match)
	if err != nil {
		return
	}
	dir, actual := filepath.Dir(canonical), filepath.Dir(path)
	if dir == actual || filepath.Base(actual) == "duplicate" && dir == filepath.Dir(actual) {
		jobs.add(dir, match.Work.ID)
	}
}

// collectArtworkJobs inspects identity sidecars rather than rematching every
// episode. Unknown IDs remain skips, even if a folder title looks familiar.
func collectArtworkJobs(target string, recursive bool) (artworkJobs, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("artwork target must not be a symlink")
	}
	jobs := make(artworkJobs)
	directories := make(map[string]bool)
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("artwork target is not a regular file")
		}
		if filepath.Base(target) != "tvshow.nfo" && filepath.Base(target) != artwork.IdentityFile && !isSupportedRecordingExtension(target) {
			return nil, fmt.Errorf("artwork target must be a recording or work identity file")
		}
		directories[filepath.Dir(target)] = true
	} else {
		err := filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == metadata.BackupDirectory || !recursive && path != target {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Name() == "tvshow.nfo" || entry.Name() == artwork.IdentityFile || isSupportedRecordingExtension(entry.Name()) {
				directories[filepath.Dir(path)] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(directories) == 0 {
			directories[target] = true
		}
	}
	for dir := range directories {
		id, err := artwork.ResolveWorkID(dir)
		if errors.Is(err, artwork.ErrNoWorkID) && filepath.Base(dir) == "duplicate" {
			// A duplicate recording belongs to its independently identified show.
			if parentID, parentErr := artwork.ResolveWorkID(filepath.Dir(dir)); parentErr == nil {
				dir, id, err = filepath.Dir(dir), parentID, nil
			}
		}
		job := jobs.add(dir, id)
		if err != nil && !errors.Is(err, artwork.ErrNoWorkID) {
			job.err = err
		}
	}
	return jobs, nil
}

func runArtworkJobs(ctx context.Context, jobs artworkJobs, client artwork.WorkClient, dryRun bool, timeout time.Duration) int {
	keys := make([]string, 0, len(jobs))
	for key := range jobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	runner := artwork.New(client, dryRun, timeout)
	written, previewed, preserved, skipped, failed := 0, 0, 0, 0, 0
	fmt.Fprintf(os.Stderr, "\nArtwork: %d work folder(s)\n", len(keys))
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		job := jobs[key]
		fmt.Fprintf(os.Stderr, "Artwork folder: %s\n", job.dir)
		if job.err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", job.err)
			failed++
			continue
		}
		if len(job.workIDs) > 1 {
			fmt.Fprintln(os.Stderr, "  SKIP: multiple Annict works share this folder")
			skipped++
			continue
		}
		id := 0
		for value := range job.workIDs {
			id = value
		}
		results, err := runner.Process(ctx, job.dir, id)
		for _, result := range results {
			fmt.Fprintf(os.Stderr, "  Artwork:   %s (%s): %s\n", result.Kind, result.Status, result.Path)
			if result.Source != "" {
				fmt.Fprintf(os.Stderr, "  Source:    %s\n", result.Source)
			}
			if result.Reason != "" {
				fmt.Fprintf(os.Stderr, "  Reason:    %s\n", result.Reason)
			}
			switch result.Status {
			case artwork.Created:
				written++
			case artwork.Previewed:
				previewed++
			case artwork.Preserved:
				preserved++
			case artwork.Skipped:
				skipped++
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
		}
	}
	fmt.Fprintf(os.Stderr, "Artwork summary: written=%d would-write=%d preserved=%d skipped=%d failed-folders=%d\n", written, previewed, preserved, skipped, failed)
	return failed
}
