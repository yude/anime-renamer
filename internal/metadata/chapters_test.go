package metadata

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Reproduce an MP4 with Nero chapters only, as written by L-SMASH. Freeing
// the generated QuickTime chapter track leaves all media offsets untouched.
func neroOnlyFixture(t *testing.T) string {
	t.Helper()
	path := makeVideo(t, ".mp4")
	refs, err := mp4ChapterReferences(path)
	if err != nil || len(refs) != 1 {
		t.Fatalf("fixture chapter references: %v, %v", refs, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	err = visitMP4Boxes(f, 0, stat.Size(), func(kind string, start, end int64) error {
		if kind != "moov" {
			return nil
		}
		return visitMP4Boxes(f, start, end, func(kind string, start, end int64) error {
			if kind != "trak" {
				return nil
			}
			var trackID uint32
			var references []int64
			err := visitMP4Boxes(f, start, end, func(kind string, payload, limit int64) error {
				if kind == "tref" {
					references = append(references, payload-4)
				}
				if kind == "tkhd" {
					var header [24]byte
					if _, err := f.ReadAt(header[:], payload); err != nil {
						return err
					}
					offset := 12
					if header[0] == 1 {
						offset = 20
					}
					trackID = binary.BigEndian.Uint32(header[offset : offset+4])
				}
				return nil
			})
			if err != nil {
				return err
			}
			if refs[trackID] {
				_, err = f.WriteAt([]byte("free"), start-4)
				return err
			}
			for _, offset := range references {
				if _, err := f.WriteAt([]byte("free"), offset); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmbedNeroChaptersAllowsOnlyReferencedGeneratedTrack(t *testing.T) {
	path := neroOnlyFixture(t)
	before, err := readProbe(path)
	if err != nil || len(before.Streams) != 2 || len(before.Chapters) != 1 || before.Chapters[0].TimeBase != "1/10000000" {
		t.Fatalf("Nero-only fixture: %+v, %v", before, err)
	}
	original, _ := os.ReadFile(path)
	info, _ := FromMatch(verifiedMatch(), 1)
	result, err := Embed(path, info, false)
	if err != nil || result.Status != Created {
		t.Fatalf("Nero-only metadata embedding: %+v, %v", result, err)
	}
	after, _ := readProbe(path)
	if len(after.Streams) != 3 || after.Streams[2].CodecType != "data" || !chaptersEqual(before.Chapters, after.Chapters, true) || !tagsMatch(after, info.tags()) {
		t.Fatalf("unexpected output: %+v", after)
	}
	if backup, _ := os.ReadFile(result.Backup); !bytes.Equal(backup, original) {
		t.Fatal("original video backup changed")
	}
	// A similar-looking data track with no chapter reference is never ignored.
	after.Streams[2].ID = "0x9999"
	if _, err := verifyStreamLayout(before, after, path, true); err == nil {
		t.Fatal("accepted unrelated extra data stream")
	}
	after.Streams[2].ID = "0x3"
	after.Streams[2].CodecType = "subtitle"
	if _, err := verifyStreamLayout(before, after, path, true); err == nil {
		t.Fatal("accepted an extra subtitle stream as a chapter track")
	}
}

func TestChapterTimesAreComparedExactlyAcrossTimeBases(t *testing.T) {
	before := []probeChapter{
		{TimeBase: "1/10000000", Start: 0, End: 2960290000, Tags: map[string]string{"title": "A"}},
		{TimeBase: "1/10000000", Start: 2960290000, End: 14449916670, Tags: map[string]string{"title": "A5Sec"}},
	}
	after := []probeChapter{
		{TimeBase: "1/1000", Start: 0, End: 296029, Tags: map[string]string{"title": "A"}},
		{TimeBase: "1/1000", Start: 296029, End: 1444992, Tags: map[string]string{"title": "A5Sec"}},
	}
	if !chaptersEqual(before, after, true) || chaptersEqual(before, after, false) {
		t.Fatal("derived final duration may round by one chapter tick only when a QuickTime chapter track was generated")
	}
	for _, change := range []string{"start", "intermediate end", "last end", "title", "missing", "invalid time base"} {
		t.Run(change, func(t *testing.T) {
			changed := append([]probeChapter(nil), after...)
			switch change {
			case "start":
				changed[1].Start++
			case "intermediate end":
				changed[0].End++
			case "last end":
				changed[1].End += 2
			case "title":
				changed[1].Tags = map[string]string{"title": "changed"}
			case "missing":
				changed = changed[:1]
			case "invalid time base":
				changed[1].TimeBase = "0/0"
			}
			if chaptersEqual(before, changed, true) {
				t.Fatal("accepted changed chapter boundaries or metadata")
			}
		})
	}
}

func TestMP4ChapterReferencesRequireBoxStructureAndBounds(t *testing.T) {
	box := func(kind string, payload []byte) []byte {
		header := make([]byte, 8)
		binary.BigEndian.PutUint32(header, uint32(8+len(payload)))
		copy(header[4:], kind)
		return append(header, payload...)
	}
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], 3)
	refs := box("moov", box("trak", box("tref", box("chap", id[:]))))
	for index, data := range [][]byte{refs, box("mdat", refs), refs[:len(refs)-1]} {
		path := filepath.Join(t.TempDir(), strconv.Itoa(index)+".mp4")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		ids, err := mp4ChapterReferences(path)
		if index == 0 && (err != nil || !ids[3]) {
			t.Fatalf("missing valid chapter reference: %v, %v", ids, err)
		}
		if index == 1 && (err != nil || len(ids) != 0) {
			t.Fatalf("parsed fake chapter references inside media payload: %v, %v", ids, err)
		}
		if index == 2 && err == nil {
			t.Fatal("accepted truncated MP4")
		}
	}
}
