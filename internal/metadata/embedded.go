package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Backups have an unsupported video extension so Jellyfin and recursive reruns
// do not index the original recordings a second time.
const BackupDirectory = ".anime-renamer-backups"

// ErrCannotEmbed means the container could not be remuxed without changing
// existing streams, headers, timing or chapters. The source remains untouched.
var ErrCannotEmbed = errors.New("container cannot safely retain embedded metadata")

type probe struct {
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Disposition map[string]int    `json:"disposition"`
		Tags        map[string]string `json:"tags"`
	} `json:"streams"`
	Format struct {
		Name string            `json:"format_name"`
		Tags map[string]string `json:"tags"`
	} `json:"format"`
	Chapters []json.RawMessage `json:"chapters"`
}

// limitedBuffer bounds diagnostic output from external programs.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (16 << 10) - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func run(program string, args ...string) ([]byte, error) {
	cmd := exec.Command(program, args...)
	var stdout bytes.Buffer
	var stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", program, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func packetHash(path string) ([]byte, error) {
	// Framehash records each copied packet's timestamp, duration, size and
	// encoded payload hash. Hash the textual output as a stream to bound memory
	// use for multi-gigabyte recordings. No decoding or transcoding takes place.
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-copyts", "-i", path, "-map", "0", "-c", "copy", "-avoid_negative_ts", "disabled", "-f", "framehash", "-hash", "sha256", "-")
	digest := sha256.New()
	var stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = digest, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("verify copied packets: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return digest.Sum(nil), nil
}

func readProbe(path string) (*probe, error) {
	data, err := run("ffprobe", "-v", "error", "-show_streams", "-show_format", "-show_chapters", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	var p probe
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	tags := make(map[string]string)
	for key, value := range p.Format.Tags {
		tags[strings.ToLower(key)] = value
	}
	p.Format.Tags = tags
	return &p, nil
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
// and metadata, preserves an exclusive backup, then replaces the video. It never
// transcodes, edits the source in place, or removes an existing backup.
func Embed(path string, info Info, dryRun bool) (Result, error) {
	result := Result{Path: path}
	if _, err := Backend("embedded", path); err != nil {
		return result, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return result, fmt.Errorf("metadata source must be a regular file: %s", path)
	}
	original, err := readProbe(path)
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
	if !strings.Contains(comment, provenance) {
		if comment != "" {
			comment += "\n"
		}
		comment += provenance
	}
	tags["comment"] = comment
	if tagsMatch(original, tags) {
		result.Status = Unchanged
		return result, nil
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
		_ = os.Remove(tempPath)
		return result, err
	}
	defer os.Remove(tempPath)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-copyts", "-i", path, "-map", "0", "-map_metadata", "0", "-map_chapters", "0", "-c", "copy", "-copy_unknown", "-avoid_negative_ts", "disabled"}
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
	if _, err := run("ffmpeg", args...); err != nil {
		return result, fmt.Errorf("%w: %v", ErrCannotEmbed, err)
	}
	updated, err := readProbe(tempPath)
	if err != nil {
		return result, err
	}
	if !tagsMatch(updated, tags) {
		return result, fmt.Errorf("%w: remux did not retain the requested metadata", ErrCannotEmbed)
	}
	if len(original.Streams) == 0 || len(original.Streams) != len(updated.Streams) {
		return result, fmt.Errorf("%w: remux changed streams", ErrCannotEmbed)
	}
	if !retainedTags(original.Format.Tags, updated.Format.Tags, tags) {
		return result, fmt.Errorf("%w: remux lost existing metadata", ErrCannotEmbed)
	}
	for index, before := range original.Streams {
		after := updated.Streams[index]
		if before.CodecType != after.CodecType || before.CodecName != after.CodecName || !maps.Equal(before.Disposition, after.Disposition) || !retainedTags(before.Tags, after.Tags, nil) {
			return result, fmt.Errorf("%w: remux changed stream %d or its metadata", ErrCannotEmbed, index)
		}
	}
	if !slices.EqualFunc(original.Chapters, updated.Chapters, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
		return result, fmt.Errorf("%w: remux changed chapters", ErrCannotEmbed)
	}
	var originalHash []byte
	for _, candidate := range []string{path, tempPath} {
		// Unsupported streams fail safely, retaining the original source.
		hash, err := packetHash(candidate)
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrCannotEmbed, err)
		}
		if candidate == path {
			originalHash = hash
		} else if !bytes.Equal(originalHash, hash) {
			return result, fmt.Errorf("%w: remux changed packet payloads, headers or timing", ErrCannotEmbed)
		}
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, current) || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
		return result, fmt.Errorf("source changed during metadata processing")
	}
	if err := os.Chmod(tempPath, before.Mode().Perm()); err != nil {
		return result, err
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
	if err := makeBackup(path, result.Backup); err != nil {
		return result, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return result, fmt.Errorf("replace video (original also retained at %s): %w", result.Backup, err)
	}
	result.Status = Created
	return result, nil
}

func makeBackup(source, destination string) error {
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup directory is not a real directory: %s", dir)
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}
