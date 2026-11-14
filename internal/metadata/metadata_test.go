package metadata

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/matcher"
)

func verifiedMatch() *matcher.MatchResult {
	number := 13.0
	return &matcher.MatchResult{Work: &annict.Work{ID: 12, Title: "作品 & 続編", Media: "tv"}, Episode: &annict.Episode{ID: 34, Number: &number, NumberText: "第13話", Title: "出会い <再び>"}, Confidence: 90}
}

func TestNFORoundTripUsesPublicEpisodeAndEscapesXML(t *testing.T) {
	match := verifiedMatch()
	match.OutputEpisodeNumber, match.OutputNumberSet = 25, true
	info, err := FromMatch(match, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, err := info.VideoNFO()
	if err != nil {
		t.Fatal(err)
	}
	var got document
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.XMLName.Local != "episodedetails" || got.Title != match.Episode.Title || got.ShowTitle != match.Work.Title || got.Episode != 25 || got.Season != 1 || got.UniqueID.ID != 34 || got.UniqueID.Type != "annict" {
		t.Fatalf("unexpected NFO: %s", data)
	}
	if bytes.Contains(data, []byte("<aired>")) {
		t.Fatal("recording dates must not become original air dates")
	}
}

func TestMetadataRejectsFractionalEpisodeWithoutRounding(t *testing.T) {
	match := verifiedMatch()
	match.Episode.NumberText, match.OutputEpisodeLabel = "第13.5話", "13.5"
	if _, err := FromMatch(match, 1); err == nil {
		t.Fatal("fractional metadata must not be rounded to episode 13")
	}
}

func TestMetadataDoesNotUseSortOrderAsEpisodeNumber(t *testing.T) {
	match := verifiedMatch()
	match.Episode.Number = nil
	match.Episode.NumberText = ""
	match.Episode.SortNumber = 100
	if _, err := FromMatch(match, 1); err == nil {
		t.Fatal("used Annict sort order as a public episode number")
	}
}

func TestNFOMissingOnlyAndDryRun(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "作品 #13.mp4")
	info, _ := FromMatch(verifiedMatch(), 1)
	if results, err := WriteNFO(video, dir, info, true); err != nil || len(results) != 2 || results[0].Status != Previewed {
		t.Fatalf("preview: %+v, %v", results, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("dry-run wrote files")
	}
	if _, err := WriteNFO(video, dir, info, false); err != nil {
		t.Fatal(err)
	}
	if results, err := WriteNFO(video, dir, info, false); err != nil || results[0].Status != Unchanged || results[1].Status != Unchanged {
		t.Fatalf("rerun: %+v, %v", results, err)
	}
	manual := []byte("<episodedetails><title>手動設定</title></episodedetails>")
	if err := os.WriteFile(SidecarPath(video), manual, 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := WriteNFO(video, dir, info, false)
	if err != nil || results[0].Status != Preserved {
		t.Fatalf("existing: %+v, %v", results, err)
	}
	if data, _ := os.ReadFile(SidecarPath(video)); !bytes.Equal(data, manual) {
		t.Fatal("manual NFO was overwritten")
	}
}

func TestNFORejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "manual.xml")
	if err := os.WriteFile(outside, []byte("manual"), 0o600); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "episode.mp4")
	if err := os.Symlink(outside, SidecarPath(video)); err != nil {
		t.Skip(err)
	}
	info, _ := FromMatch(verifiedMatch(), 1)
	if _, err := WriteNFO(video, "", info, false); err == nil {
		t.Fatal("must not follow NFO symlinks")
	}
}

func TestBatchNFOPreviewsSharedShowOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	info, _ := FromMatch(verifiedMatch(), 1)
	plans := make(map[string][]byte)
	first, err := WriteNFO(filepath.Join(dir, "first.mp4"), dir, info, true, plans)
	if err != nil || first[1].Status != Previewed {
		t.Fatalf("first: %+v, %v", first, err)
	}
	second, err := WriteNFO(filepath.Join(dir, "second.mp4"), dir, info, true, plans)
	if err != nil || second[0].Status != Previewed || second[1].Status != Unchanged {
		t.Fatalf("duplicate show preview: %+v, %v", second, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("preview wrote files")
	}
}

func TestShowIdentityConflictDoesNotWriteEpisode(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "episode.mp4")
	if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte(`<tvshow><uniqueid type="annict">999</uniqueid></tvshow>`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := FromMatch(verifiedMatch(), 1)
	if _, err := WriteNFO(video, dir, info, false); err == nil {
		t.Fatal("attached episode to conflicting work identity")
	}
	if _, err := os.Stat(SidecarPath(video)); !os.IsNotExist(err) {
		t.Fatal("wrote episode before checking show identity")
	}
}
