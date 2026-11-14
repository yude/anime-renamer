package metadata

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
)

type hevcConfig struct {
	offset int64
	data   []byte
}

// restoreHEVCConfigs preserves an otherwise identical MP4 decoder record when
// FFmpeg normalizes parallelismType to zero. That field imposes restrictions
// only when min_spatial_segmentation_idc is nonzero (ISO/IEC 14496-15).
// Restore the original byte in the disposable remux, then require the usual
// exact header/packet/timestamp verification. Never relax packetHash.
func restoreHEVCConfigs(source, target string, before, after *probe) error {
	var repairs []hevcConfig
	for i, stream := range before.Streams {
		if stream.CodecName != "hevc" || stream.CodecType != "video" || (stream.CodecTag != "hvc1" && stream.CodecTag != "hev1") {
			continue
		}
		if i >= len(after.Streams) || after.Streams[i].CodecName != stream.CodecName || after.Streams[i].CodecTag != stream.CodecTag {
			return fmt.Errorf("%w: remux changed HEVC stream %d", ErrCannotEmbed, i)
		}
		original, err := readHEVCConfig(source, stream)
		if err != nil {
			return fmt.Errorf("%w: read original HEVC configuration for stream %d: %v", ErrCannotEmbed, i, err)
		}
		updated, err := readHEVCConfig(target, after.Streams[i])
		if err != nil {
			return fmt.Errorf("%w: read remuxed HEVC configuration for stream %d: %v", ErrCannotEmbed, i, err)
		}
		if bytes.Equal(original.data, updated.data) {
			continue
		}
		if !normalizedHEVCParallelism(original.data, updated.data) {
			return fmt.Errorf("%w: remux changed HEVC decoder configuration for stream %d", ErrCannotEmbed, i)
		}
		repairs = append(repairs, hevcConfig{offset: updated.offset + 15, data: original.data[15:16]})
	}
	if len(repairs) == 0 {
		return nil
	}
	f, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	for _, repair := range repairs {
		if _, err := f.WriteAt(repair.data, repair.offset); err != nil {
			_ = f.Close()
			return fmt.Errorf("restore HEVC configuration: %w", err)
		}
	}
	// The caller verifies and fsyncs the entire completed file before commit.
	return f.Close()
}

func normalizedHEVCParallelism(before, after []byte) bool {
	if len(before) < 23 || len(before) != len(after) || before[0] != 1 || after[0] != 1 {
		return false
	}
	// Keep reserved bits, every other decoder field, and all NAL parameter
	// sets exact. Accept only normalization from a specified type to zero.
	return binary.BigEndian.Uint16(before[13:15]) == 0xf000 &&
		binary.BigEndian.Uint16(after[13:15]) == 0xf000 &&
		before[15]&0xfc == 0xfc && before[15]&3 != 0 && after[15] == 0xfc &&
		bytes.Equal(before[:15], after[:15]) && bytes.Equal(before[16:], after[16:])
}

// readHEVCConfig resolves the actual hvcC box through the selected track's
// sample description. Searching for the string "hvcC" could hit media bytes or
// another track. Multiple sample entries/configurations are not repaired.
func readHEVCConfig(path string, stream probeStream) (config hevcConfig, err error) {
	id, err := strconv.ParseUint(stream.ID, 0, 32)
	if err != nil || id == 0 {
		return config, fmt.Errorf("invalid MP4 track ID %q", stream.ID)
	}
	f, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return config, err
	}
	tracks := 0
	err = visitMP4Boxes(f, 0, info.Size(), func(kind string, start, end int64) error {
		if kind != "moov" {
			return nil
		}
		return visitMP4Boxes(f, start, end, func(kind string, start, end int64) error {
			if kind != "trak" {
				return nil
			}
			var trackID uint32
			headers := 0
			if err := visitMP4Boxes(f, start, end, func(kind string, payload, limit int64) error {
				if kind != "tkhd" {
					return nil
				}
				headers++
				var version [1]byte
				if limit-payload < 1 {
					return io.ErrUnexpectedEOF
				}
				if _, err := f.ReadAt(version[:], payload); err != nil {
					return err
				}
				offset := int64(12)
				if version[0] == 1 {
					offset = 20
				} else if version[0] != 0 {
					return fmt.Errorf("unsupported MP4 track header version")
				}
				if limit-payload < offset+4 {
					return io.ErrUnexpectedEOF
				}
				var value [4]byte
				if _, err := f.ReadAt(value[:], payload+offset); err != nil {
					return err
				}
				trackID = binary.BigEndian.Uint32(value[:])
				return nil
			}); err != nil {
				return err
			}
			if headers != 1 {
				return fmt.Errorf("MP4 track has no unique header")
			}
			if trackID != uint32(id) {
				return nil
			}
			tracks++
			return visitHEVCConfig(f, start, end, []string{"mdia", "minf", "stbl", "stsd"}, stream.CodecTag, &config)
		})
	})
	if err == nil && (tracks != 1 || config.data == nil) {
		err = fmt.Errorf("MP4 track has no unique HEVC configuration")
	}
	return config, err
}

func visitHEVCConfig(r io.ReaderAt, start, end int64, levels []string, codecTag string, config *hevcConfig) error {
	matches := 0
	err := visitMP4Boxes(r, start, end, func(kind string, payload, limit int64) error {
		if kind != levels[0] {
			return nil
		}
		matches++
		if matches != 1 {
			return fmt.Errorf("ambiguous MP4 %s boxes", levels[0])
		}
		if len(levels) > 1 {
			return visitHEVCConfig(r, payload, limit, levels[1:], codecTag, config)
		}
		var description [8]byte // FullBox version/flags and entry_count.
		if limit-payload < 8 {
			return io.ErrUnexpectedEOF
		}
		if _, err := r.ReadAt(description[:], payload); err != nil {
			return err
		}
		if binary.BigEndian.Uint32(description[:4]) != 0 || binary.BigEndian.Uint32(description[4:]) != 1 {
			return fmt.Errorf("unsupported MP4 sample description")
		}
		entries := 0
		err := visitMP4Boxes(r, payload+8, limit, func(kind string, sample, sampleEnd int64) error {
			entries++
			if kind != codecTag || sampleEnd-sample < 78 {
				return fmt.Errorf("unsupported HEVC sample entry")
			}
			// VisualSampleEntry has 78 bytes before its child boxes.
			return visitMP4Boxes(r, sample+78, sampleEnd, func(kind string, data, dataEnd int64) error {
				if kind != "hvcC" {
					return nil
				}
				if config.data != nil || dataEnd-data < 23 || dataEnd-data > 1<<20 {
					return fmt.Errorf("invalid or ambiguous HEVC configuration")
				}
				config.offset, config.data = data, make([]byte, dataEnd-data)
				_, err := r.ReadAt(config.data, data)
				return err
			})
		})
		if err == nil && entries != 1 {
			err = fmt.Errorf("MP4 sample description entry count changed")
		}
		return err
	})
	if err == nil && matches != 1 {
		err = fmt.Errorf("missing MP4 %s box", levels[0])
	}
	return err
}
