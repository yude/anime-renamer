package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/yude/anime-renamer/internal/fileops"
)

// Backups have an unsupported video extension so Jellyfin and recursive reruns
// do not index the original recordings a second time.
const BackupDirectory = ".anime-renamer-backups"

// ErrCannotEmbed means the container could not be remuxed without changing
// existing streams, headers, timing or chapters. The source remains untouched.
var ErrCannotEmbed = errors.New("container cannot safely retain embedded metadata")

type probe struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Name string            `json:"format_name"`
		Tags map[string]string `json:"tags"`
	} `json:"format"`
	Chapters []probeChapter `json:"chapters"`
}

type probeStream struct {
	Index       int               `json:"index"`
	ID          string            `json:"id"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	CodecTag    string            `json:"codec_tag_string"`
	Disposition map[string]int    `json:"disposition"`
	Tags        map[string]string `json:"tags"`
}

func Backend(mode, path string) (string, error) {
	switch mode {
	case "nfo":
		return mode, nil
	case "auto", "embedded":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".mp4", ".m4v", ".mkv":
			return "embedded", nil
		case ".ts", ".m2ts":
			if mode == "auto" {
				return "nfo", nil
			}
		}
		return "", fmt.Errorf("embedded episode tags are unsupported for %s; use auto or nfo", filepath.Ext(path))
	default:
		return "", fmt.Errorf("unknown metadata mode %q", mode)
	}
}

func BackupPath(path string) string {
	name := fmt.Sprintf("%x.original", sha256.Sum256([]byte(filepath.Base(path))))
	return filepath.Join(filepath.Dir(path), BackupDirectory, name)
}

func tagsMatch(p *probe, tags map[string]string) bool {
	for key, value := range tags {
		// MP4 uses the standard iTunes 'show' atom; Matroska can store both.
		if key == "show_name" && p.Format.Tags[key] == "" && p.Format.Tags["show"] == value {
			continue
		}
		if p.Format.Tags[key] != value {
			return false
		}
	}
	return true
}

func retainedTags(before, after, replaced map[string]string) bool {
	for key, value := range before {
		key = strings.ToLower(key)
		if _, ok := replaced[key]; ok {
			continue
		}
		switch key {
		case "encoder", "duration", "major_brand", "minor_version", "compatible_brands":
			// These describe the container/muxer rather than library metadata.
			continue
		}
		found := false
		for newKey, newValue := range after {
			if strings.EqualFold(newKey, key) && newValue == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Embed remuxes a supported container using stream copy, verifies packet hashes
// and metadata, creates an exclusive recovery backup, then replaces the video.
// Successful backups are removed after a durable commit. It never transcodes
// or edits the source in place.
func Embed(path string, info Info, dryRun bool) (Result, error) {
	return EmbedWithOptions(path, info, dryRun, EmbedOptions{})
}

// EmbedWithOptions discards recovery backups only after a verified, durable
// replacement. KeepBackup is an explicit opt-in to retaining successful copies.
func EmbedWithOptions(path string, info Info, dryRun bool, options EmbedOptions) (result Result, err error) {
	result = Result{Path: path}
	parent := options.Context
	if parent == nil {
		parent = context.Background()
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 {
		return result, fmt.Errorf("metadata timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return result, err
	}
	path = absolute
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := Backend("embedded", path); err != nil {
		return result, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return result, fmt.Errorf("inspect metadata source %s: %w", path, err)
	}
	if !before.Mode().IsRegular() {
		return result, fmt.Errorf("metadata source must be a regular file: %s", path)
	}
	if !dryRun {
		lockPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".anime-renamer-%x.lock", sha256.Sum256([]byte(filepath.Base(path)))))
		release, lockErr := fileops.Lock(lockPath)
		if lockErr != nil {
			return result, lockErr
		}
		defer func() { err = errors.Join(err, release()) }()
		// The source may have changed while another operation held the lock.
		if err := fileops.Unchanged(path, before); err != nil {
			return result, err
		}
	}
	original, err := readProbeContext(ctx, path)
	if err != nil {
		return result, err
	}
	format := "mov"
	if strings.EqualFold(filepath.Ext(path), ".mkv") {
		format = "matroska"
	}
	if !slices.Contains(strings.Split(original.Format.Name, ","), format) {
		return result, fmt.Errorf("container %q does not match extension %s", original.Format.Name, filepath.Ext(path))
	}
	tags := info.tags()
	// Standard comments retain the Annict provenance even in MP4, whose muxer
	// does not preserve arbitrary user-defined tags by default.
	provenance := fmt.Sprintf("Annict: https://annict.com/works/%d", info.WorkID)
	if !info.Movie {
		provenance += fmt.Sprintf("/episodes/%d", info.EpisodeID)
	}
	comment := original.Format.Tags["comment"]
	if !slices.Contains(strings.Split(comment, "\n"), provenance) {
		if comment != "" {
			comment += "\n"
		}
		comment += provenance
	}
	tags["comment"] = comment
	if tagsMatch(original, tags) {
		result.Status = Unchanged
		if !dryRun {
			result.Backup, err = finishExistingBackup(ctx, path, before, original, tags, format == "mov", options.KeepBackup)
		}
		return result, err
	}
	result.Backup = BackupPath(path)
	if _, err := os.Lstat(result.Backup); err == nil {
		return result, fmt.Errorf("backup already exists: %s (restore or archive it before updating again)", result.Backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if dryRun {
		result.Status = Previewed
		return result, nil
	}
	// An unsupported extension keeps concurrent library scans from treating
	// the incomplete remux as another video.
	temp, err := os.CreateTemp(filepath.Dir(path), ".anime-renamer-*.tmp")
	if err != nil {
		return result, err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return result, errors.Join(err, os.Remove(tempPath))
	}
	defer func() {
		if cleanup := os.Remove(tempPath); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp %s: %w", tempPath, cleanup))
		}
	}()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-copyts", "-protocol_whitelist", "file,pipe", "-i", path, "-map", "0", "-map_metadata", "0", "-map_chapters", "0", "-c", "copy", "-copy_unknown", "-avoid_negative_ts", "disabled"}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+tags[key])
	}
	if format == "mov" {
		// .m4v otherwise selects the more restrictive iPod muxer.
		args = append(args, "-f", "mp4")
	} else {
		args = append(args, "-f", "matroska")
	}
	args = append(args, tempPath)
	if _, err := runContext(ctx, "ffmpeg", args...); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return result, err
		}
		return result, fmt.Errorf("%w: %v", ErrCannotEmbed, err)
	}
	updated, err := readProbeContext(ctx, tempPath)
	if err != nil {
		return result, err
	}
	if err := verifyEmbeddedFiles(ctx, path, tempPath, original, updated, tags, format == "mov"); err != nil {
		return result, err
	}
	if err := fileops.Unchanged(path, before); err != nil {
		return result, err
	}
	if err := fileops.PreserveOwner(tempPath, before); err != nil {
		return result, err
	}
	if err := os.Chmod(tempPath, before.Mode().Perm()); err != nil {
		return result, err
	}
	if err := fileops.PreserveXattrs(path, tempPath); err != nil {
		return result, fmt.Errorf("preserve extended attributes: %w", err)
	}
	if err := os.Chtimes(tempPath, before.ModTime(), before.ModTime()); err != nil {
		return result, err
	}
	completed, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return result, err
	}
	if err := errors.Join(completed.Sync(), completed.Close()); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	replaced, err := commitReplacement(path, tempPath, result.Backup, before, options.KeepBackup, replacementOps{
		backup: makeBackup, replace: os.Rename, syncDir: fileops.SyncDir, remove: os.Remove,
	})
	if replaced {
		result.Status = Created
	}
	if _, statErr := os.Lstat(result.Backup); errors.Is(statErr, os.ErrNotExist) {
		result.Backup = ""
	}
	return result, err
}

func verifyEmbeddedFiles(ctx context.Context, source, target string, original, updated *probe, tags map[string]string, mp4 bool) error {
	if !tagsMatch(updated, tags) {
		return fmt.Errorf("%w: remux did not retain the requested metadata", ErrCannotEmbed)
	}
	addedChapterTrack, err := verifyStreamLayout(original, updated, target, mp4)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrCannotEmbed, err)
	}
	if !retainedTags(original.Format.Tags, updated.Format.Tags, tags) {
		return fmt.Errorf("%w: remux lost existing metadata", ErrCannotEmbed)
	}
	for index, before := range original.Streams {
		after := updated.Streams[index]
		if before.CodecType != after.CodecType || before.CodecName != after.CodecName || !maps.Equal(before.Disposition, after.Disposition) || !retainedTags(before.Tags, after.Tags, nil) {
			return fmt.Errorf("%w: remux changed stream %d or its metadata", ErrCannotEmbed, index)
		}
	}
	if !chaptersEqual(original.Chapters, updated.Chapters, addedChapterTrack) {
		return fmt.Errorf("%w: remux changed chapters", ErrCannotEmbed)
	}
	var originalHash []byte
	for _, candidate := range []struct {
		path    string
		streams []probeStream
	}{{source, original.Streams}, {target, updated.Streams[:len(original.Streams)]}} {
		// Unsupported streams fail safely, retaining the original source.
		// A newly synthesized chapter track is checked by its MP4 references
		// and chapter contents; every original stream still gets hashed.
		hash, err := packetHash(ctx, candidate.path, candidate.streams)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("%w: %v", ErrCannotEmbed, err)
		}
		if candidate.path == source {
			originalHash = hash
		} else if !bytes.Equal(originalHash, hash) {
			return fmt.Errorf("%w: remux changed packet payloads, headers or timing", ErrCannotEmbed)
		}
	}
	return nil
}
