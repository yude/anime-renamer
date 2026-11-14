package metadata

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Status string

const (
	Created   Status = "created"
	Previewed Status = "would write"
	Unchanged Status = "unchanged"
	Preserved Status = "preserved existing NFO"
)

type Result struct {
	Path, Backup string
	Status       Status
}

func SidecarPath(video string) string {
	return strings.TrimSuffix(video, filepath.Ext(video)) + ".nfo"
}

// WriteNFO adds missing NFOs. Existing files, including user-written metadata,
// are never replaced. showDir must be an independently verified work folder;
// leave it empty for a flat directory containing multiple works.
func WriteNFO(video, showDir string, info Info, dryRun bool) ([]Result, error) {
	data, err := info.VideoNFO()
	if err != nil {
		return nil, err
	}
	files := []struct {
		path string
		data []byte
	}{{SidecarPath(video), data}}
	if !info.Movie && showDir != "" {
		data, err := info.ShowNFO()
		if err != nil {
			return nil, err
		}
		files = append(files, struct {
			path string
			data []byte
		}{filepath.Join(showDir, "tvshow.nfo"), data})
	}
	var results []Result
	for _, file := range files {
		result, err := writeMissing(file.path, file.data, dryRun)
		if err != nil {
			return results, fmt.Errorf("write NFO %s: %w", file.path, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func writeMissing(path string, data []byte, dryRun bool) (Result, error) {
	result := Result{Path: path}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("existing NFO is not a regular file")
		}
		old, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		result.Status = Preserved
		if bytes.Equal(old, data) {
			result.Status = Unchanged
		}
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if dryRun {
		result.Status = Previewed
		return result, nil
	}
	// Exclusive creation also protects against files appearing after the check.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return result, err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return result, err
	}
	result.Status = Created
	return result, nil
}
