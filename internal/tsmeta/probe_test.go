package tsmeta

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

func encodeSI(t *testing.T, text string) []byte {
	t.Helper()
	var b []byte
	for _, r := range text {
		if r < 0x7f {
			b = append(b, 0x0e, byte(r), 0x0f)
			continue
		}
		encoded, err := japanese.EUCJP.NewEncoder().String(string(r))
		if err != nil || len(encoded) != 2 {
			t.Fatalf("cannot encode %q", r)
		}
		b = append(b, encoded[0]&0x7f, encoded[1]&0x7f)
	}
	return b
}

func withCRC(b []byte) []byte {
	return binary.BigEndian.AppendUint32(b, sectionCRC(b))
}

func longSection(tid byte, id int, section, last byte, payload []byte) []byte {
	b := []byte{tid, 0xb0, 0, byte(id >> 8), byte(id), 0xc1, section, last}
	b = append(b, payload...)
	n := len(b) + 1 // section_length excludes the first 3 bytes, includes CRC.
	b[1], b[2] = 0xb0|byte(n>>8), byte(n)
	return withCRC(b)
}

func descriptor(tag byte, data []byte) []byte { return append([]byte{tag, byte(len(data))}, data...) }

type sectionPacket struct {
	pid int
	b   []byte
}

func fixtureSections(t *testing.T) []sectionPacket {
	t.Helper()
	channel := encodeSI(t, "BS日テレ")
	serviceDesc := descriptor(0x48, append([]byte{1, 0, byte(len(channel))}, channel...))
	title, text := encodeSI(t, "アニメ 探偵はもう、死んでいる。Season2 第6話 世界の守護者"), encodeSI(t, "それは、完全無欠の名探偵が犯したミス")
	short := append([]byte("jpn"), byte(len(title)))
	short = append(short, title...)
	short = append(short, byte(len(text)))
	short = append(short, text...)
	event := []byte{0x40, 0xd0, 0, 4, 1, 0x4e, 0x3f, 0xdb, 0xef, 0xac, 0, 0x30, 0, 0, 0x30, 0}
	desc := descriptor(0x4d, short)
	event = append(event, 0x80|byte(len(desc)>>8), byte(len(desc)))
	event = append(event, desc...)
	return []sectionPacket{
		{0, longSection(0, 0x40d0, 0, 0, []byte{0, 0, 0xe0, 0x10, 0, 141, 0xe1, 0})},
		{0x100, longSection(2, 141, 0, 0, []byte{0xe1, 0x40, 0xf0, 0, 2, 0xe1, 0x40, 0xf0, 0})},
		{0x11, longSection(0x42, 0x40d0, 0, 0, append([]byte{0, 4, 0xff, 0, 141, 0xff, 0xf0, byte(len(serviceDesc))}, serviceDesc...))},
		{0x12, longSection(0x4e, 141, 0, 1, event)},
		{0x14, withCRC([]byte{0x73, 0x70, 11, 0xef, 0xac, 0, 0x40, 0, 0xf0, 0})},
		{0x140, nil}, // A video PID must really be present in the recording.
	}
}

func packetize(parts []sectionPacket) []byte {
	var out []byte
	cc := make(map[int]byte)
	for _, part := range parts {
		data := append([]byte{0}, part.b...)
		first := true
		for len(data) > 0 || first {
			p := bytes.Repeat([]byte{0xff}, 188)
			p[0], p[1], p[2], p[3] = 0x47, byte(part.pid>>8), byte(part.pid), 0x10|cc[part.pid]
			cc[part.pid] = (cc[part.pid] + 1) & 15
			if first && len(part.b) > 0 {
				p[1] |= 0x40
			}
			n := copy(p[4:], data)
			data = data[n:]
			out = append(out, p...)
			first = false
		}
	}
	return out
}

func TestProbeRecordedProgramme(t *testing.T) {
	for _, stride := range []int{188, 192, 204} {
		t.Run(strconv.Itoa(stride), func(t *testing.T) {
			packets := packetize(fixtureSections(t))
			var input []byte
			for i := 0; i < len(packets); i += 188 {
				if stride == 192 {
					input = append(input, 0, 0, 0, 0)
				}
				input = append(input, packets[i:i+188]...)
				if stride == 204 {
					input = append(input, make([]byte, 16)...)
				}
			}
			got, err := probe(input)
			if err != nil || got.ServiceID != 141 || got.NetworkID != 4 || got.Channel != "BS日テレ" || !strings.Contains(got.Name, "第6話 世界の守護者") || got.StartedAt.Format(time.RFC3339) != "2026-11-12T00:30:00+09:00" || got.EndedAt.Sub(got.StartedAt) != 30*time.Minute {
				t.Fatalf("probe() = %+v, %v", got, err)
			}
		})
	}
}

func TestProbeRejectsUnrelatedOrBrokenSI(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]sectionPacket) []sectionPacket
		want   error
	}{
		{"bad EIT CRC", func(p []sectionPacket) []sectionPacket { p[3].b[28] ^= 1; return p }, ErrUnavailable},
		{"future EIT version", func(p []sectionPacket) []sectionPacket { p[3].b[5] &^= 1; return p }, ErrUnavailable},
		{"following event", func(p []sectionPacket) []sectionPacket { p[3].b[6] = 1; return p }, ErrUnavailable},
		{"other TS EIT", func(p []sectionPacket) []sectionPacket { p[3].b[0] = 0x4f; return p }, ErrUnavailable},
		{"schedule entry", func(p []sectionPacket) []sectionPacket { p[3].b[0] = 0x50; return p }, ErrUnavailable},
		{"other service", func(p []sectionPacket) []sectionPacket { p[3].b[4]++; return p }, ErrUnavailable},
		{"other transport", func(p []sectionPacket) []sectionPacket { p[3].b[9]++; return p }, ErrUnavailable},
		{"other network", func(p []sectionPacket) []sectionPacket { p[3].b[11]++; return p }, ErrUnavailable},
		{"other SDT", func(p []sectionPacket) []sectionPacket { p[2].b[0] = 0x46; return p }, ErrUnavailable},
		{"invalid time BCD", func(p []sectionPacket) []sectionPacket { p[4].b[6] = 0x6a; return p }, ErrUnavailable},
		{"time outside programme", func(p []sectionPacket) []sectionPacket { p[4].b[6] = 0x20; return p }, ErrUnavailable},
		{"no clock", func(p []sectionPacket) []sectionPacket { return append(p[:4], p[5:]...) }, ErrUnavailable},
		{"no video packets", func(p []sectionPacket) []sectionPacket { return p[:5] }, ErrUnavailable},
		{"no PMT", func(p []sectionPacket) []sectionPacket { return append(p[:1], p[2:]...) }, ErrUnavailable},
		{"incomplete PAT", func(p []sectionPacket) []sectionPacket { p[0].b[7] = 1; return p }, ErrUnavailable},
		{"multiple PAT services", func(p []sectionPacket) []sectionPacket {
			p[0].b = longSection(0, 0x40d0, 0, 0, []byte{0, 141, 0xe1, 0, 0, 142, 0xe1, 1})
			return p
		}, ErrAmbiguous},
		{"conflicting present events", func(p []sectionPacket) []sectionPacket {
			extra := append([]byte(nil), p[3].b...)
			extra[15]++
			p = append(p, sectionPacket{0x12, extra})
			return p
		}, ErrAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts := test.change(fixtureSections(t))
			// Recompute CRC except in the intentional CRC corruption case.
			if test.name != "bad EIT CRC" {
				for i := range parts {
					if len(parts[i].b) > 0 {
						parts[i].b = withCRC(parts[i].b[:len(parts[i].b)-4])
					}
				}
			}
			if got, err := probe(packetize(parts)); got != nil || !errors.Is(err, test.want) {
				t.Fatalf("probe() = %+v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestReadFileRequiresConsistentSamples(t *testing.T) {
	parts := fixtureSections(t)
	packets := packetize(parts)
	// A sparse 80 MiB file has SI only in the three bounded sample windows.
	// Reading from the start instead would not find any broadcast information.
	path := filepath.Join(t.TempDir(), "recording.m2ts")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(80 << 20)
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	window := bytes.Repeat(packets, sampleBytes/len(packets)+1)[:sampleBytes]
	offsets := []int64{size/6 - sampleBytes/2, size/2 - sampleBytes/2, size*5/6 - sampleBytes/2}
	for _, offset := range offsets {
		if _, err := f.WriteAt(window, offset); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadFile(path); err != nil {
		t.Fatal(err)
	}
	parts[3].b[15]++
	parts[3].b = withCRC(parts[3].b[:len(parts[3].b)-4])
	other := packetize(parts)
	if _, err := f.WriteAt(bytes.Repeat(other, sampleBytes/len(other)+1)[:sampleBytes], offsets[2]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got, err := ReadFile(path); got != nil || !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("different programmes: %+v, %v", got, err)
	}
}

func TestARIBTextFromRecording(t *testing.T) {
	// Short-event name bytes from the supplied recording (no video/audio).
	b, _ := hex.DecodeString("1b7ca2cbe12121433544651b7dcfe2a6fd3b60f3c7a4ebfa0e89536561736f6e328a0f212142680e360f4f43212140243326ce3c69386e3c54")
	want := "アニメ　探偵はもう、死んでいる。Season2　第6話　世界の守護者"
	if got, err := decodeText(b); err != nil || got != want {
		t.Fatalf("decodeText() = %q, %v", got, err)
	}
	if got, err := decodeText([]byte{0x7a, 0x56, 0x7a, 0x65, 0x7a, 0x6a, 0x7a, 0x6b, 0x7a, 0x6c, 0x7a, 0x6d, 0x7a, 0x6e}); err != nil || got != "[字][無][再][新][初][終][生]" {
		t.Fatalf("broadcast flags = %q, %v", got, err)
	}
	for _, b := range [][]byte{{0x1b}, {0x1b, 0x28, 0x20, 0x40}, {0x21}, {0x7a, 0x7e}, {0x95}, {0x1d}, {0x1b, 0x24, 0x3a, 0x21, 0x21}} {
		if got, err := decodeText(b); err == nil {
			t.Fatalf("unsupported/incomplete text %x decoded as %q", b, got)
		}
	}
}

func TestSectionAssemblyRejectsGapsAndSupportsDuplicates(t *testing.T) {
	section := longSection(0x4e, 141, 0, 1, bytes.Repeat([]byte{0x41}, 700))
	packets := packetize([]sectionPacket{{0x12, section}})
	for _, gap := range []bool{false, true} {
		count := 0
		r := sectionReader{handler: func(b []byte) {
			if !bytes.Equal(b, section) {
				t.Fatal("assembled a different section")
			}
			count++
		}}
		for i := 0; i < len(packets); i += 188 {
			if gap && i == 188 {
				continue
			}
			r.packet(packets[i : i+188])
			r.packet(packets[i : i+188]) // legal duplicate must be ignored.
		}
		if gap && count != 0 || !gap && count != 1 {
			t.Fatalf("gap=%v: assembled %d sections", gap, count)
		}
	}
}

func FuzzProbe(f *testing.F) {
	f.Add(bytes.Repeat([]byte{0x47, 0, 0, 0}, 1000))
	f.Add([]byte{0x1b, 0x24, 0x29, 0x20})
	f.Fuzz(func(t *testing.T, b []byte) {
		probe(b)
		decodeText(b)
	})
}
