package artwork

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yude/anime-renamer/internal/fileops"
)

var ErrNoWorkID = errors.New("no verified Annict work ID in tvshow.nfo or artwork identity")

const IdentityFile = ".anime-renamer-artwork.json"

type identity struct {
	Version  int    `json:"version"`
	WorkID   int    `json:"annict_id"`
	Title    string `json:"title"`
	ImageURL string `json:"image_url,omitempty"`
}

// ResolveWorkID also accepts the provenance record from an earlier verified
// artwork run. It never interprets episode IDs or derives IDs from names.
func ResolveWorkID(dir string) (int, error) {
	id, err := ReadWorkID(dir)
	if err != nil && !errors.Is(err, ErrNoWorkID) {
		return 0, err
	}
	data, readErr := fileops.ReadRegular(filepath.Join(dir, IdentityFile), 64<<10)
	if errors.Is(readErr, os.ErrNotExist) {
		return id, err
	}
	if readErr != nil {
		return 0, readErr
	}
	var saved identity
	if err := json.Unmarshal(data, &saved); err != nil {
		return 0, err
	}
	if saved.Version != 1 || saved.WorkID <= 0 || id != 0 && saved.WorkID != id {
		return 0, fmt.Errorf("conflicting or unsupported artwork identity")
	}
	return saved.WorkID, nil
}

// ReadWorkID deliberately ignores episode NFO IDs: those identify episodes,
// not works. No folder-title, filename or external database fallback is used.
func ReadWorkID(dir string) (int, error) {
	data, err := fileops.ReadRegular(filepath.Join(dir, "tvshow.nfo"), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrNoWorkID
	}
	if err != nil {
		return 0, err
	}
	var doc struct {
		XMLName xml.Name
		IDs     []struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"uniqueid"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("invalid show NFO: %w", err)
	}
	if doc.XMLName.Local != "tvshow" {
		return 0, fmt.Errorf("expected tvshow root in show NFO")
	}
	id := 0
	for _, field := range doc.IDs {
		if !strings.EqualFold(field.Type, "annict") {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(field.Value))
		if err != nil || value <= 0 || id != 0 && id != value {
			return 0, fmt.Errorf("invalid or conflicting Annict work IDs in show NFO")
		}
		id = value
	}
	if id == 0 {
		return 0, ErrNoWorkID
	}
	return id, nil
}

func checkIdentity(dir string, workID int) (exists bool, err error) {
	if nfoID, err := ReadWorkID(dir); err == nil && nfoID != workID {
		return false, fmt.Errorf("show NFO identifies Annict work %d, expected %d", nfoID, workID)
	} else if err != nil && !errors.Is(err, ErrNoWorkID) {
		return false, err
	}
	data, err := fileops.ReadRegular(filepath.Join(dir, IdentityFile), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var current identity
	if err := json.Unmarshal(data, &current); err != nil {
		return false, fmt.Errorf("invalid artwork identity: %w", err)
	}
	if current.Version != 1 || current.WorkID != workID {
		return false, fmt.Errorf("artwork identity conflicts with Annict work %d", workID)
	}
	return true, nil
}
