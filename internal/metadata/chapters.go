package metadata

import (
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"math/big"
	"os"
	"strconv"
)

type probeChapter struct {
	TimeBase string            `json:"time_base"`
	Start    int64             `json:"start"`
	End      int64             `json:"end"`
	Tags     map[string]string `json:"tags"`
}

func verifyStreamLayout(before, after *probe, path string, mp4 bool) (bool, error) {
	if len(before.Streams) > 0 && len(before.Streams) == len(after.Streams) {
		return false, nil
	}
	// Nero's chpl atom contains a chapter table without a media track. The
	// MP4 muxer additionally synthesizes a QuickTime chapter text track. Only
	// that single appended, explicitly referenced track may be added.
	if mp4 && len(before.Streams) > 0 && len(before.Chapters) > 0 && len(after.Streams) == len(before.Streams)+1 {
		added := after.Streams[len(before.Streams)]
		if added.CodecType == "data" && added.CodecName == "bin_data" && added.CodecTag == "text" {
			id, err := strconv.ParseUint(added.ID, 0, 32)
			if err == nil && id > 0 {
				ids, err := mp4ChapterReferences(path)
				if err == nil && ids[uint32(id)] {
					return true, nil
				}
			}
		}
	}
	return false, fmt.Errorf("remux changed streams (%d -> %d); additional stream is not a verified MP4 chapter track", len(before.Streams), len(after.Streams))
}

func chapterTime(ch probeChapter, ticks int64) (*big.Rat, bool) {
	base, ok := new(big.Rat).SetString(ch.TimeBase)
	if !ok || base.Sign() <= 0 || ticks < 0 {
		return nil, false
	}
	return new(big.Rat).Mul(base, new(big.Rat).SetInt64(ticks)), true
}

func chaptersEqual(before, after []probeChapter, addedChapterTrack bool) bool {
	if len(before) != len(after) {
		return false
	}
	for index, a := range before {
		b := after[index]
		startA, okA := chapterTime(a, a.Start)
		startB, okB := chapterTime(b, b.Start)
		endA, okEndA := chapterTime(a, a.End)
		endB, okEndB := chapterTime(b, b.End)
		if !okA || !okB || !okEndA || !okEndB || a.End < a.Start || b.End < b.Start || startA.Cmp(startB) != 0 || !maps.Equal(a.Tags, b.Tags) {
			return false
		}
		if endA.Cmp(endB) == 0 {
			continue
		}
		// chpl stores starts only: the final end is derived from the movie's
		// duration. QuickTime quantizes it to the chapter track's timescale.
		// Permit at most one tick, capped at 1 ms, only for this derived end.
		// All starts, titles, intermediate ends and media packet times remain
		// exact; coarse or changed chapter boundaries still fail.
		quantum, ok := chapterTime(b, 1)
		difference := new(big.Rat).Abs(new(big.Rat).Sub(endA, endB))
		if !addedChapterTrack || index != len(before)-1 || !ok || quantum.Cmp(big.NewRat(1, 1000)) > 0 || difference.Cmp(quantum) > 0 {
			return false
		}
	}
	return true
}

// visitMP4Boxes walks bounded box headers using ReaderAt. mdat payloads are
// never loaded. A malformed or unsupported layout fails closed.
func visitMP4Boxes(r io.ReaderAt, start, end int64, visit func(string, int64, int64) error) error {
	for count := 0; start < end; count++ {
		if count >= 100000 || end-start < 8 {
			return fmt.Errorf("invalid MP4 box table")
		}
		var header [16]byte
		if _, err := r.ReadAt(header[:8], start); err != nil {
			return err
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		if size == 1 {
			if end-start < 16 {
				return fmt.Errorf("truncated extended MP4 box")
			}
			if _, err := r.ReadAt(header[8:], start+8); err != nil {
				return err
			}
			size, headerSize = binary.BigEndian.Uint64(header[8:]), 16
		} else if size == 0 {
			size = uint64(end - start)
		}
		if size < uint64(headerSize) || size > uint64(end-start) {
			return fmt.Errorf("MP4 box exceeds parent bounds")
		}
		next := start + int64(size)
		if err := visit(string(header[4:8]), start+headerSize, next); err != nil {
			return err
		}
		start = next
	}
	return nil
}

func mp4ChapterReferences(path string) (map[uint32]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	ids := make(map[uint32]bool)
	// Only moov/trak/tref/chap references identify chapter tracks. The text
	// codec tag or a handler name alone cannot distinguish unrelated data.
	var descend func(int64, int64, int) error
	levels := []string{"moov", "trak", "tref", "chap"}
	descend = func(start, end int64, level int) error {
		return visitMP4Boxes(f, start, end, func(kind string, payload, limit int64) error {
			if kind != levels[level] {
				return nil
			}
			if level < len(levels)-1 {
				return descend(payload, limit, level+1)
			}
			if (limit-payload)%4 != 0 || limit-payload > 4096 {
				return fmt.Errorf("invalid chapter track references")
			}
			var id [4]byte
			for offset := payload; offset < limit; offset += 4 {
				if _, err := f.ReadAt(id[:], offset); err != nil {
					return err
				}
				ids[binary.BigEndian.Uint32(id[:])] = true
			}
			return nil
		})
	}
	if err := descend(0, info.Size(), 0); err != nil {
		return nil, err
	}
	return ids, nil
}
