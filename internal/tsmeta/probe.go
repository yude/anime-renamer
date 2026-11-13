// Package tsmeta reads bounded, read-only samples of Japanese broadcast TS
// recordings. It uses PAT/PMT, actual SDT, present EIT, and the broadcast clock
// together; an EIT schedule entry alone does not identify recorded content.
package tsmeta

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var (
	ErrUnavailable = errors.New("no verified embedded broadcast metadata")
	ErrAmbiguous   = errors.New("embedded broadcast metadata is ambiguous")
	jst            = time.FixedZone("JST", 9*60*60)
)

type Broadcast struct {
	NetworkID, TransportID, ServiceID, EventID int
	Channel, Name, Description                 string
	StartedAt, EndedAt                         time.Time
}

const sampleBytes = 8 << 20

// ReadFile samples the middle of each third of a regular file, up to 24 MiB
// total. All samples must identify the same programme. This avoids assuming
// that the first EPG entry is the recording, or that the first/last padding is
// part of the target programme. It never reads a whole multi-gigabyte recording.
func ReadFile(path string) (*Broadcast, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() < 188*5 {
		return nil, ErrUnavailable
	}
	size := before.Size()
	length := int64(sampleBytes)
	offsets := []int64{size/6 - length/2, size/2 - length/2, size*5/6 - length/2}
	if size <= 3*length {
		length, offsets = size, []int64{0}
	}
	var result *Broadcast
	for _, offset := range offsets {
		b := make([]byte, length)
		if _, err := io.ReadFull(io.NewSectionReader(f, offset, length), b); err != nil {
			return nil, fmt.Errorf("read TS sample: %w", err)
		}
		found, err := probe(b)
		if err != nil {
			return nil, err
		}
		if result != nil && *result != *found {
			return nil, fmt.Errorf("%w: sampled recording contains different programmes", ErrAmbiguous)
		}
		result = found
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("%w: recording changed while sampling", ErrAmbiguous)
	}
	return result, nil
}

type service struct {
	network, transport, id int
	name                   string
}

type probeState struct {
	readers     map[int]*sectionReader
	patSections map[string]map[byte][]byte
	transport   int
	program     int
	pmtPID      int
	videoPIDs   map[int]bool
	seenPIDs    map[int]bool
	services    map[service]bool
	events      map[Broadcast]bool
	times       []time.Time
	conflict    bool
}

func probe(b []byte) (*Broadcast, error) {
	stride, sync := packetLayout(b)
	if stride == 0 {
		return nil, ErrUnavailable
	}
	s := &probeState{readers: make(map[int]*sectionReader), patSections: make(map[string]map[byte][]byte),
		videoPIDs: make(map[int]bool), seenPIDs: make(map[int]bool), services: make(map[service]bool), events: make(map[Broadcast]bool)}
	for _, pid := range []int{0, 0x11, 0x12, 0x14} {
		s.addReader(pid)
	}
	for i := sync; i+188 <= len(b); i += stride {
		t := b[i : i+188]
		if t[0] != 0x47 {
			return nil, ErrUnavailable
		}
		pid := u16(t[1:3]) & 0x1fff
		if t[1]&0x80 == 0 && t[3]&0x10 != 0 {
			s.seenPIDs[pid] = true
		}
		if r := s.readers[pid]; r != nil {
			r.packet(t)
		}
	}
	if s.conflict {
		return nil, ErrAmbiguous
	}
	video := false
	for pid := range s.videoPIDs {
		video = video || s.seenPIDs[pid]
	}
	if s.program <= 0 || !video || len(s.times) == 0 {
		return nil, ErrUnavailable
	}
	var result *Broadcast
	for e := range s.events {
		if e.ServiceID != s.program || e.TransportID != s.transport {
			continue
		}
		inside := true
		for _, now := range s.times {
			inside = inside && !now.Before(e.StartedAt) && now.Before(e.EndedAt)
		}
		if !inside {
			continue
		}
		for ch := range s.services {
			if ch.id != e.ServiceID || ch.transport != e.TransportID || ch.network != e.NetworkID {
				continue
			}
			e.Channel = ch.name
			if result != nil && *result != e {
				return nil, ErrAmbiguous
			}
			copy := e
			result = &copy
		}
	}
	if result == nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

func packetLayout(b []byte) (int, int) {
	for _, stride := range []int{188, 192, 204} {
		for offset := 0; offset < stride && offset+stride*4 < len(b); offset++ {
			ok := true
			for j := range 5 {
				ok = ok && b[offset+j*stride] == 0x47
			}
			if ok {
				return stride, offset
			}
		}
	}
	return 0, 0
}

func (s *probeState) addReader(pid int) {
	if s.readers[pid] == nil {
		s.readers[pid] = &sectionReader{handler: func(b []byte) { s.section(pid, b) }}
	}
}

func (s *probeState) section(pid int, b []byte) {
	if pid == 0x14 {
		if b[0] == 0x70 && len(b) == 8 && b[1]&0x80 == 0 || b[0] == 0x73 && len(b) >= 14 && b[1]&0x80 == 0 && sectionCRC(b) == 0 && u16(b[8:10])&0xfff == len(b)-14 {
			if now, ok := broadcastTime(b[3:8]); ok {
				s.times = append(s.times, now)
			}
		}
		return
	}
	if !currentSection(b) {
		return
	}
	switch {
	case pid == 0 && b[0] == 0:
		s.pat(b)
	case pid == s.pmtPID && b[0] == 2 && u16(b[3:5]) == s.program:
		s.pmt(b)
	case pid == 0x11 && b[0] == 0x42:
		s.sdt(b)
	case pid == 0x12 && b[0] == 0x4e && b[6] == 0:
		s.eit(b)
	}
}

func (s *probeState) pat(b []byte) {
	key := fmt.Sprintf("%d/%d/%d", u16(b[3:5]), b[5], b[7])
	parts := s.patSections[key]
	if parts == nil {
		parts = make(map[byte][]byte)
		s.patSections[key] = parts
	}
	parts[b[6]] = append([]byte(nil), b...)
	if len(parts) != int(b[7])+1 {
		return
	}
	program, pmt, count := 0, 0, 0
	for _, part := range parts {
		entries := part[8 : len(part)-4]
		if len(entries)%4 != 0 {
			return
		}
		for len(entries) >= 4 {
			id, pid := u16(entries[:2]), u16(entries[2:4])&0x1fff
			entries = entries[4:]
			if id != 0 {
				program, pmt, count = id, pid, count+1
			}
		}
	}
	if count > 1 || s.program != 0 && (s.program != program || s.transport != u16(b[3:5]) || s.pmtPID != pmt) {
		s.conflict = true
		return
	}
	if count == 1 && pmt > 0x1f && pmt < 0x1fff {
		s.program, s.pmtPID, s.transport = program, pmt, u16(b[3:5])
		s.addReader(pmt)
	}
}

func (s *probeState) pmt(b []byte) {
	if len(b) < 16 || b[6] != 0 || b[7] != 0 {
		return
	}
	pos := 12 + u16(b[10:12])&0xfff
	video := make(map[int]bool)
	for pos+5 <= len(b)-4 {
		pid := u16(b[pos+1:pos+3]) & 0x1fff
		end := pos + 5 + u16(b[pos+3:pos+5])&0xfff
		if end > len(b)-4 {
			return
		}
		switch b[pos] {
		case 0x01, 0x02, 0x10, 0x1b, 0x24:
			video[pid] = true
		}
		pos = end
	}
	if pos == len(b)-4 {
		for pid := range video {
			s.videoPIDs[pid] = true
		}
	}
}

func (s *probeState) sdt(b []byte) {
	if len(b) < 15 {
		return
	}
	for pos := 11; pos+5 <= len(b)-4; {
		ch := service{id: u16(b[pos : pos+2]), transport: u16(b[3:5]), network: u16(b[8:10])}
		end := pos + 5 + u16(b[pos+3:pos+5])&0xfff
		if end > len(b)-4 {
			return
		}
		count := 0
		ok := descriptors(b[pos+5:end], func(tag byte, d []byte) bool {
			if tag != 0x48 {
				return true
			}
			if len(d) < 3 || int(d[1])+3 > len(d) {
				return false
			}
			n := int(d[1]) + 2
			if n+1+int(d[n]) != len(d) {
				return false
			}
			var err error
			ch.name, err = decodeText(d[n+1:])
			count++
			return err == nil
		})
		if ok && count == 1 && ch.name != "" {
			s.services[ch] = true
		}
		pos = end
	}
}

func (s *probeState) eit(b []byte) {
	if len(b) < 30 {
		return
	}
	// Present section 0 must contain exactly one event. Other services,
	// following events, schedules and obsolete versions never become evidence.
	e := Broadcast{ServiceID: u16(b[3:5]), TransportID: u16(b[8:10]), NetworkID: u16(b[10:12]), EventID: u16(b[14:16])}
	start, ok := broadcastTime(b[16:21])
	duration, durationOK := broadcastDuration(b[21:24])
	if !ok || !durationOK || duration <= 0 || 26+u16(b[24:26])&0xfff != len(b)-4 {
		return
	}
	e.StartedAt, e.EndedAt = start, start.Add(duration)
	count := 0
	ok = descriptors(b[26:len(b)-4], func(tag byte, d []byte) bool {
		if tag != 0x4d {
			return true
		}
		if len(d) < 5 || string(d[:3]) != "jpn" || int(d[3])+5 > len(d) {
			return false
		}
		n := int(d[3]) + 4
		if n+1+int(d[n]) != len(d) {
			return false
		}
		var err error
		e.Name, err = decodeText(d[4:n])
		if err != nil {
			return false
		}
		e.Description, err = decodeText(d[n+1:])
		count++
		return err == nil
	})
	if ok && count == 1 && e.Name != "" {
		s.events[e] = true
	}
}

func bcd(b byte) (int, bool) {
	return int(b>>4)*10 + int(b&15), b>>4 <= 9 && b&15 <= 9
}

func broadcastDuration(b []byte) (time.Duration, bool) {
	if len(b) != 3 {
		return 0, false
	}
	h, a := bcd(b[0])
	m, c := bcd(b[1])
	s, d := bcd(b[2])
	return time.Duration(h*3600+m*60+s) * time.Second, a && c && d && m < 60 && s < 60
}

func broadcastTime(b []byte) (time.Time, bool) {
	if len(b) != 5 {
		return time.Time{}, false
	}
	duration, ok := broadcastDuration(b[2:])
	if !ok || duration >= 24*time.Hour || u16(b[:2]) == 0xffff {
		return time.Time{}, false
	}
	return time.Date(1858, 11, 17, 0, 0, 0, 0, jst).AddDate(0, 0, u16(b[:2])).Add(duration), true
}
