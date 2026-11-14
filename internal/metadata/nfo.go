package metadata

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yude/anime-renamer/internal/fileops"
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
func WriteNFO(video, showDir string, info Info, dryRun bool, reservations ...map[string][]byte) ([]Result, error) {
	data, err := info.VideoNFO()
	if err != nil {
		return nil, err
	}
	files := []struct {
		path string
		data []byte
	}{{SidecarPath(video), data}}
	if !info.Movie && showDir != "" {
		if err := checkShowIdentity(filepath.Join(showDir, "tvshow.nfo"), info.WorkID); err != nil {
			return nil, err
		}
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
	var plans map[string][]byte
	if dryRun && len(reservations) > 0 {
		plans = reservations[0]
	}
	for _, file := range files {
		key, err := filepath.Abs(file.path)
		if err != nil {
			return nil, err
		}
		if existing, ok := plans[key]; ok && !bytes.Equal(existing, file.data) {
			return nil, fmt.Errorf("conflicting planned NFO: %s", file.path)
		}
	}
	for _, file := range files {
		key, _ := filepath.Abs(file.path)
		if _, ok := plans[key]; ok {
			results = append(results, Result{Path: file.path, Status: Unchanged})
			continue
		}
		result, err := writeMissing(file.path, file.data, dryRun)
		if err != nil {
			return results, fmt.Errorf("write NFO %s: %w", file.path, err)
		}
		results = append(results, result)
		if plans != nil && result.Status == Previewed {
			plans[key] = file.data
		}
	}
	return results, nil
}

func writeMissing(path string, data []byte, dryRun bool) (Result, error) {
	result := Result{Path: path}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("existing NFO is not a regular file")
		}
		if info.Size() != int64(len(data)) {
			result.Status = Preserved
			return result, nil
		}
		old, err := fileops.ReadRegular(path, int64(len(data)))
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
	if err := fileops.WriteNew(path, data, 0o644); err != nil {
		return result, err
	}
	result.Status = Created
	return result, nil
}

func checkShowIdentity(path string, workID int) error {
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
		return fmt.Errorf("existing show NFO cannot be safely verified: %s", path)
	}
	data, err := fileops.ReadRegular(path, 1<<20)
	if err != nil {
		return err
	}
	var doc struct {
		XMLName xml.Name
		IDs     []uniqueID `xml:"uniqueid"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("invalid existing show NFO %s: %w", path, err)
	}
	if doc.XMLName.Local != "tvshow" {
		return fmt.Errorf("existing show NFO has unexpected root: %s", path)
	}
	for _, id := range doc.IDs {
		if strings.EqualFold(id.Type, "annict") && id.ID != workID {
			return fmt.Errorf("show NFO identifies Annict work %d, expected %d: %s", id.ID, workID, path)
		}
	}
	return nil
}
