package renamer

import (
	"fmt"
	"io"
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
	if err := moveFile(originalPath, newPath); err != nil {
		r.Error = fmt.Errorf("rename failed: %w", err)
		return r
	}

	r.Renamed = true
	return r
}

// moveFile moves src to dst, falling back to a copy-then-remove when
// os.Rename fails (as it always does across filesystem/device boundaries,
// e.g. "invalid cross-device link") — relevant since --output can point
// anywhere, not just a subdirectory of the source.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove original after copy: %w", err)
	}
	return nil
}

// copyFile copies src to dst via a temp file in dst's directory, renamed
// into place only once the copy fully succeeds, so a failure partway
// through never leaves a truncated file at dst and never touches src.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	tmp := dst + ".anime-renamer-tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()

	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("copy contents: %w", err)
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("sync copied file: %w", err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close copied file: %w", err)
	}

	if err = os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("finalize copy: %w", err)
	}
	return nil
}
