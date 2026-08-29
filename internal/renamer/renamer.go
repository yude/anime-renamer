package renamer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yude/anime-renamer/internal/matcher"
)

// pathSanitizer replaces characters that are illegal in file or directory
// names on Windows, macOS, and/or Linux with visually similar full-width
// equivalents, so a work title or episode subtitle containing them (e.g. a
// literal "/" or ":") doesn't break directory creation or the rename itself.
var pathSanitizer = strings.NewReplacer(
	"/", "／",
	"\\", "＼",
	":", "：",
	"*", "＊",
	"?", "？",
	"\"", "″",
	"<", "＜",
	">", "＞",
	"|", "｜",
)

// RenameResult holds the result of a rename operation.
type RenameResult struct {
	OriginalPath string
	NewPath      string
	WorkTitle    string
	EpisodeNum   int
	Subtitle     string
	Confidence   int
	Renamed      bool
	Error        error
}

// BuildPath generates the destination path from a match result.
func BuildPath(originalPath string, result *matcher.MatchResult) (string, error) {
	if result == nil || result.Work == nil || result.Episode == nil {
		return "", fmt.Errorf("incomplete match result")
	}

	// Episode number: use Number if available, otherwise use sort_number
	epNum := 0
	if result.Episode.Number != nil {
		epNum = int(*result.Episode.Number)
	} else {
		epNum = result.Episode.SortNumber
	}

	if epNum <= 0 {
		return "", fmt.Errorf("invalid episode number: %d", epNum)
	}

	// Build output path: <dir>/<WorkTitle>/<WorkTitle> #<N> 「<Subtitle>」.mp4
	workTitle := pathSanitizer.Replace(result.Work.Title)
	dir := filepath.Dir(originalPath)
	workDir := filepath.Join(dir, workTitle)

	// Use file subtitle when Annict episode has no title
	subtitle := result.Episode.Title
	if subtitle == "" {
		subtitle = result.FileSubtitle
	}
	subtitle = pathSanitizer.Replace(subtitle)

	// Format filename: <WorkTitle> #<N> 「<Subtitle>」.mp4
	var filename string
	if subtitle != "" {
		filename = fmt.Sprintf("%s #%d 「%s」.mp4", workTitle, epNum, subtitle)
	} else {
		filename = fmt.Sprintf("%s #%d.mp4", workTitle, epNum)
	}

	return filepath.Join(workDir, filename), nil
}

// Rename performs the actual rename if dryRun is false.
// If outputDir is non-empty, the destination is relocated there while
// preserving the <WorkTitle>/<filename> structure computed by BuildPath.
// Returns the result of the operation.
func Rename(originalPath string, result *matcher.MatchResult, dryRun bool, outputDir string) *RenameResult {
	r := &RenameResult{
		OriginalPath: originalPath,
		Confidence:   result.Confidence,
	}

	newPath, err := BuildPath(originalPath, result)
	if err != nil {
		r.Error = fmt.Errorf("build path: %w", err)
		return r
	}

	if outputDir != "" {
		rel, err := filepath.Rel(filepath.Dir(originalPath), newPath)
		if err != nil {
			r.Error = fmt.Errorf("compute output path: %w", err)
			return r
		}
		newPath = filepath.Join(outputDir, rel)
	}

	r.NewPath = newPath
	r.WorkTitle = result.Work.Title
	if result.Episode.Number != nil {
		r.EpisodeNum = int(*result.Episode.Number)
	} else {
		r.EpisodeNum = result.Episode.SortNumber
	}
	r.Subtitle = result.Episode.Title
	if r.Subtitle == "" {
		r.Subtitle = result.FileSubtitle
	}

	// Check if already at destination
	if originalPath == newPath {
		r.Renamed = true
		return r
	}

	// Check destination exists
	if _, err := os.Stat(newPath); err == nil {
		r.Error = fmt.Errorf("destination already exists: %s", newPath)
		return r
	}

	if dryRun {
		r.Renamed = false
		return r
	}

	// Create directory
	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.Error = fmt.Errorf("create directory %s: %w", dir, err)
		return r
	}

	// Perform rename
	if err := os.Rename(originalPath, newPath); err != nil {
		r.Error = fmt.Errorf("rename failed: %w", err)
		return r
	}

	r.Renamed = true
	return r
}
