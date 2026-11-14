package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yude/anime-renamer/internal/matcher"
	"github.com/yude/anime-renamer/internal/metadata"
	"github.com/yude/anime-renamer/internal/renamer"
)

type metadataOptions struct {
	mode   string
	only   bool
	season int
}

func (o *metadataOptions) validate(output string) error {
	switch o.mode {
	case "none", "auto", "embedded", "nfo":
	default:
		return fmt.Errorf("--metadata must be none, auto, embedded or nfo")
	}
	if o.season <= 0 {
		return fmt.Errorf("--metadata-season must be a positive integer")
	}
	if o.only && output != "" {
		return fmt.Errorf("--metadata-only cannot be combined with --output: videos stay in place")
	}
	return nil
}

func processMatchedMetadata(file string, match *matcher.MatchResult, dryRun bool, outputDir string, plans map[string]string, options *metadataOptions) *renamer.RenameResult {
	if match.Confidence < matcher.AutoRenameThreshold {
		return &renamer.RenameResult{OriginalPath: file, SkipReason: "metadata requires confidence >= 90 even when --confidence is lower"}
	}
	info, err := metadata.FromMatch(match, options.season)
	if err != nil {
		return &renamer.RenameResult{OriginalPath: file, SkipReason: err.Error()}
	}
	backend, err := metadata.Backend(options.mode, file)
	if err != nil {
		return &renamer.RenameResult{OriginalPath: file, SkipReason: err.Error()}
	}
	source, err := os.Lstat(file)
	if err != nil || !source.Mode().IsRegular() {
		return &renamer.RenameResult{OriginalPath: file, Error: fmt.Errorf("metadata source is not a regular file: %s", file)}
	}
	// Existing sidecars would be orphaned by a move, or override new embedded
	// tags in Jellyfin. Preserve them and request an explicit review instead.
	if _, err := os.Lstat(metadata.SidecarPath(file)); err == nil {
		if !options.only || backend == "embedded" {
			return &renamer.RenameResult{OriginalPath: file, SkipReason: "existing NFO must be reviewed before renaming or embedding; it takes precedence in Jellyfin"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return &renamer.RenameResult{OriginalPath: file, Error: err}
	}
	r := &renamer.RenameResult{OriginalPath: file, NewPath: file, Confidence: match.Confidence}
	if !options.only {
		preview := renamer.RenameWithReservations(file, match, true, outputDir, plans)
		if preview.Error != nil {
			return preview
		}
		if _, err := os.Lstat(metadata.SidecarPath(preview.NewPath)); err == nil {
			return &renamer.RenameResult{OriginalPath: file, SkipReason: "destination already has an NFO; review it before moving a recording beside it"}
		} else if !errors.Is(err, os.ErrNotExist) {
			return &renamer.RenameResult{OriginalPath: file, Error: err}
		}
		r = renameMatchedRecording(file, match, dryRun, outputDir, plans)
		if r.Error != nil || r.SkipReason != "" {
			return r
		}
	}
	var results []metadata.Result
	if backend == "embedded" {
		input := r.NewPath
		if dryRun {
			input = file
		}
		result, embedErr := metadata.Embed(input, info, dryRun)
		result.Path = r.NewPath
		if result.Backup != "" {
			result.Backup = metadata.BackupPath(r.NewPath)
		}
		err = embedErr
		if err == nil {
			results = append(results, result)
		}
		if options.mode == "auto" && errors.Is(err, metadata.ErrCannotEmbed) {
			fmt.Fprintf(os.Stderr, "  Fallback:  %v; preserving video and writing NFO instead\n", err)
			backend = "nfo"
		}
	}
	if backend == "nfo" {
		showDir := ""
		canonical, buildErr := renamer.BuildPath(r.NewPath, match)
		if buildErr == nil {
			dir := filepath.Dir(canonical)
			if dir == filepath.Dir(r.NewPath) || (filepath.Base(filepath.Dir(r.NewPath)) == "duplicate" && dir == filepath.Dir(filepath.Dir(r.NewPath))) {
				showDir = dir
			}
		}
		results, err = metadata.WriteNFO(r.NewPath, showDir, info, dryRun)
	}
	for _, result := range results {
		fmt.Fprintf(os.Stderr, "  Metadata:  %s (%s): %s\n", backend, result.Status, result.Path)
		if result.Backup != "" {
			fmt.Fprintf(os.Stderr, "  Backup:    %s\n", result.Backup)
		}
		switch result.Status {
		case metadata.Created:
			r.MetadataWritten++
		case metadata.Previewed:
			r.MetadataPreviewed++
		case metadata.Unchanged:
			r.MetadataUnchanged++
		case metadata.Preserved:
			r.MetadataPreserved++
		}
	}
	if err != nil {
		r.Error = fmt.Errorf("metadata %s: %w", backend, err)
		if !options.only && !dryRun && filepath.Clean(file) != filepath.Clean(r.NewPath) {
			r.Error = fmt.Errorf("%w (recording was already moved to %s)", r.Error, r.NewPath)
		}
	}
	return r
}
