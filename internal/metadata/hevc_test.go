package metadata

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real L-SMASH decoder record: min_spatial_segmentation_idc=0 and
// parallelismType=1. VPS/SPS/PPS must survive byte-for-byte.
const hevcConfigHex = "0102200000009000000000007bf000fdfdfafa00000f03a00001001840010c01ffff02200000030090000003000003007b914090a10001003242010102200000030090000003000003007ba002d0801107cad96454a4211913f0c39a80808082000007d20001d4c05f1a2da2000100074401c0acf03324"

func hevcTestRecord(t *testing.T) []byte {
	t.Helper()
	data, err := hex.DecodeString(hevcConfigHex)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hevcTestBox(kind string, payload ...[]byte) []byte {
	data := bytes.Join(payload, nil)
	box := make([]byte, 8, 8+len(data))
	binary.BigEndian.PutUint32(box, uint32(8+len(data)))
	copy(box[4:], kind)
	return append(box, data...)
}

func hevcTestMP4(t *testing.T, description []byte, version byte) string {
	t.Helper()
	header := make([]byte, 16)
	offset := 12
	if version == 1 {
		header, offset = make([]byte, 24), 20
	}
	header[0] = version
	binary.BigEndian.PutUint32(header[offset:], 3)
	track := hevcTestBox("trak", hevcTestBox("tkhd", header), hevcTestBox("mdia", hevcTestBox("minf", hevcTestBox("stbl", description))))
	// Decoys outside the selected sample description must never be patched.
	data := hevcTestBox("mdat", []byte("hvcC\x00\xfd"))
	data = append(data, hevcTestBox("moov", track)...)
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hevcTestDescription(data []byte) []byte {
	return hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, hevcTestBox("hvc1", make([]byte, 78), hevcTestBox("hvcC", data)))
}

func TestRestoreHEVCConfigOnlyForIrrelevantParallelism(t *testing.T) {
	for _, scenario := range []string{"unchanged", "slice", "tiles", "wavefront", "nonzero segmentation", "reserved bits", "changed VPS", "changed profile", "changed version", "reverse change", "changed length"} {
		t.Run(scenario, func(t *testing.T) {
			before, after := hevcTestRecord(t), hevcTestRecord(t)
			after[15] = 0xfc
			allowed := scenario == "unchanged" || scenario == "slice" || scenario == "tiles" || scenario == "wavefront"
			switch scenario {
			case "unchanged":
				after[15] = before[15]
			case "tiles":
				before[15] = 0xfe
			case "wavefront":
				before[15] = 0xff
			case "nonzero segmentation":
				before[14], after[14] = 1, 1
			case "reserved bits":
				after[15] = 0xbc
			case "changed VPS":
				after[40] ^= 1
			case "changed profile":
				after[1] ^= 1
			case "changed version":
				before[0] = 2
			case "reverse change":
				before[15], after[15] = 0xfc, 0xfd
			case "changed length":
				after = append(after, 0)
			}
			source := hevcTestMP4(t, hevcTestDescription(before), 0)
			target := hevcTestMP4(t, hevcTestDescription(after), 1)
			originalSource, _ := os.ReadFile(source)
			originalTarget, _ := os.ReadFile(target)
			p := &probe{Streams: []probeStream{{Index: 0, ID: "0x3", CodecType: "video", CodecName: "hevc", CodecTag: "hvc1"}}}
			err := restoreHEVCConfigs(source, target, p, p)
			if (err == nil) != allowed || err != nil && !errors.Is(err, ErrCannotEmbed) {
				t.Fatalf("allowed=%v, error=%v", allowed, err)
			}
			gotSource, _ := os.ReadFile(source)
			if !bytes.Equal(gotSource, originalSource) {
				t.Fatal("repair touched the original recording")
			}
			gotTarget, _ := os.ReadFile(target)
			if !allowed {
				if !bytes.Equal(gotTarget, originalTarget) {
					t.Fatal("unsupported header change was partially repaired")
				}
				return
			}
			config, err := readHEVCConfig(target, p.Streams[0])
			if err != nil || !bytes.Equal(config.data, before) {
				t.Fatalf("did not preserve the entire original decoder record: %v", err)
			}
			want := bytes.Clone(originalTarget)
			want[config.offset+15] = before[15]
			if !bytes.Equal(want, gotTarget) {
				t.Fatal("modified bytes outside the selected hvcC field")
			}
		})
	}
}

func TestReadHEVCConfigRejectsAmbiguousAndMalformedDescriptions(t *testing.T) {
	data := hevcTestRecord(t)
	entry := hevcTestBox("hvc1", make([]byte, 78), hevcTestBox("hvcC", data))
	for _, description := range [][]byte{
		hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 2}, entry, entry),
		hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, entry, entry),
		hevcTestDescription(data[:22]),
		hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, hevcTestBox("hvc1", make([]byte, 78), hevcTestBox("hvcC", data), hevcTestBox("hvcC", data))),
		hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, hevcTestBox("hvc1", make([]byte, 77))),
		hevcTestBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0}),
		append(hevcTestDescription(data), hevcTestDescription(data)...),
	} {
		path := hevcTestMP4(t, description, 0)
		if _, err := readHEVCConfig(path, probeStream{ID: "0x3", CodecTag: "hvc1"}); err == nil {
			t.Fatal("accepted an ambiguous or truncated sample description")
		}
	}
	path := hevcTestMP4(t, hevcTestDescription(data), 0)
	for _, id := range []string{"", "0", "0x4", "invalid"} {
		if _, err := readHEVCConfig(path, probeStream{ID: id, CodecTag: "hvc1"}); err == nil {
			t.Fatalf("accepted missing/invalid track %q", id)
		}
	}
}

func TestEmbedPreservesHEVCConfigNormalizedByFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "録画.mp4")
	_, err := run("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:r=2", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-t", "1", "-c:v", "libx265", "-x265-params", "log-level=error:pools=1:frame-threads=1", "-c:a", "aac", "-tag:v", "hvc1", path)
	if err != nil {
		if strings.Contains(err.Error(), "Unknown encoder 'libx265'") {
			t.Skip("libx265 not installed")
		}
		t.Fatal(err)
	}
	p, err := readProbe(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := readHEVCConfig(path, p.Streams[0])
	if err != nil || binary.BigEndian.Uint16(config.data[13:15]) != 0xf000 {
		t.Fatalf("unexpected HEVC fixture: %v", err)
	}
	// Model the valid L-SMASH header that FFmpeg 7 rewrites during remux.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	config.data[15] = 0xfd
	_, writeErr := f.WriteAt(config.data[15:16], config.offset+15)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		t.Fatal(err)
	}
	before, err := packetHash(context.Background(), path, p.Streams)
	if err != nil {
		t.Fatal(err)
	}
	originalProbe := p
	originalBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := FromMatch(verifiedMatch(), 1)
	result, err := Embed(path, info, false)
	if err != nil || result.Status != Created || result.Backup != "" {
		t.Fatalf("HEVC embed failed: %+v, %v", result, err)
	}
	p, err = readProbe(path)
	if err != nil || !tagsMatch(p, info.tags()) {
		t.Fatalf("missing embedded tags: %v", err)
	}
	after, err := packetHash(context.Background(), path, p.Streams)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("changed codec headers, packets, or timestamps: %v", err)
	}
	got, err := readHEVCConfig(path, p.Streams[0])
	if err != nil || !bytes.Equal(got.data, config.data) {
		t.Fatalf("HEVC decoder configuration was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(BackupPath(path))); !os.IsNotExist(err) {
		t.Fatal("successful HEVC embed left a backup directory")
	}

	// The repaired header must not hide any media changes. Corrupt one byte
	// inside mdat while leaving codec headers, packet lengths and times intact.
	baseline := filepath.Join(t.TempDir(), "original.mp4")
	if err := os.WriteFile(baseline, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	video, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	if err := visitMP4Boxes(bytes.NewReader(video), 0, int64(len(video)), func(kind string, start, end int64) error {
		if kind == "mdat" && end > start {
			video[end-1] ^= 1
			changed = true
		}
		return nil
	}); err != nil || !changed {
		t.Fatalf("cannot mutate fixture media: %v", err)
	}
	if err := os.WriteFile(path, video, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyEmbeddedFiles(context.Background(), baseline, path, originalProbe, p, info.tags(), true); !errors.Is(err, ErrCannotEmbed) || !strings.Contains(err.Error(), "packet") {
		t.Fatalf("media corruption escaped packet verification: %v", err)
	}
}
