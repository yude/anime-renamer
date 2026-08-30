package renamer

import (
	"errors"
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

	// Renaming does not transcode the recording, so preserve its container
	// extension. Keep the historical .mp4 fallback for extensionless paths.
	ext := filepath.Ext(originalPath)
	if ext == "" {
		ext = ".mp4"
	}

	// Format filename: <WorkTitle> #<N> 「<Subtitle>」<original extension>
	var filename string
	if subtitle != "" {
		filename = fmt.Sprintf("%s #%d 「%s」%s", workTitle, epNum, subtitle, ext)
	} else {
		filename = fmt.Sprintf("%s #%d%s", workTitle, epNum, ext)
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

	// Check destination exists. Lstat also catches dangling symlinks, which
	// Stat reports as missing even though replacing them would overwrite a
	// directory entry the user already owns.
	if _, err := os.Lstat(newPath); err == nil {
		r.Error = fmt.Errorf("destination already exists: %s", newPath)
		return r
	} else if !errors.Is(err, os.ErrNotExist) {
		r.Error = fmt.Errorf("check destination %s: %w", newPath, err)
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

// moveFile moves src to dst without ever replacing an existing destination.
// A hard link makes the common same-filesystem path atomic and O(1). If links
// are unavailable (most commonly across filesystems), it falls back to a
// copy-then-remove operation with the same no-replace guarantee.
func moveFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		if err := os.Remove(src); err != nil {
			return fmt.Errorf("remove original after linking destination: %w", err)
		}
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("destination already exists: %s", dst)
	}

	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove original after copy: %w", err)
	}
	return nil
}

// copyFile copies src to dst via a unique temp file in dst's directory. It
// publishes the completed temp file without replacing an existing dst and
// never touches src.
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

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".anime-renamer-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("set temp file mode: %w", err)
	}

	if _, err = io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy contents: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync copied file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close copied file: %w", err)
	}

	if err = publishTempFile(tmp.Name(), dst, info.Mode().Perm()); err != nil {
		return err
	}
	return nil
}

// publishTempFile installs a completed temp file at dst without overwriting.
// Hard-linking is atomic. Filesystems without hard-link support fall back to
// an O_EXCL copy, which retains the no-replace guarantee.
func publishTempFile(tmp, dst string, mode os.FileMode) error {
	if err := os.Link(tmp, dst); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("destination already exists: %s", dst)
	}

	in, err := os.Open(tmp)
	if err != nil {
		return fmt.Errorf("open completed temp file: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("destination already exists: %s", dst)
		}
		return fmt.Errorf("create destination: %w", err)
	}
	removeIncomplete := true
	defer func() {
		out.Close()
		if removeIncomplete {
			os.Remove(dst)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("publish copied file: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync destination: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("set destination mode: %w", err)
	}
	removeIncomplete = false
	return nil
}
