package tsmeta

import (
	"bytes"
	"encoding/binary"
)

// PSI/SI sections can cross packets, and a pointer field can finish one
// section before starting several more. Never join across a continuity gap.
type sectionReader struct {
	buf     []byte
	last    []byte
	cc      byte
	haveCC  bool
	handler func([]byte)
}

func (r *sectionReader) packet(t []byte) {
	if len(t) != 188 || t[0] != 0x47 {
		return
	}
	if t[1]&0x80 != 0 || t[3]&0xc0 != 0 || t[3]&0x30 == 0 {
		r.buf, r.haveCC = nil, false
		return
	}
	offset := 4
	if t[3]&0x20 != 0 {
		if t[4] > 183 {
			r.buf, r.haveCC = nil, false
			return
		}
		if t[4] > 0 && t[5]&0x80 != 0 {
			r.buf, r.haveCC = nil, false
		}
		offset += 1 + int(t[4])
	}
	if t[3]&0x10 == 0 || offset >= len(t) {
		return
	}
	cc := t[3] & 15
	if r.haveCC {
		if cc == r.cc && bytes.Equal(t, r.last) {
			return // A legal repeated packet carries no new section bytes.
		}
		if cc != (r.cc+1)&15 {
			r.buf = nil
		}
	}
	r.cc, r.haveCC = cc, true
	r.last = append(r.last[:0], t...)
	payload := t[offset:]
	if t[1]&0x40 != 0 {
		pointer := int(payload[0])
		payload = payload[1:]
		if pointer > len(payload) {
			r.buf = nil
			return
		}
		if len(r.buf) > 0 {
			r.buf = append(r.buf, payload[:pointer]...)
			r.consume()
		}
		r.buf = append([]byte(nil), payload[pointer:]...)
	} else if len(r.buf) > 0 {
		r.buf = append(r.buf, payload...)
	}
	r.consume()
}

func (r *sectionReader) consume() {
	for len(r.buf) >= 3 {
		if r.buf[0] == 0xff {
			r.buf = nil
			return
		}
		n := 3 + int(binary.BigEndian.Uint16(r.buf[1:3])&0xfff)
		if n > 4096 || n < 8 {
			r.buf = nil
			return
		}
		if len(r.buf) < n {
			return
		}
		r.handler(r.buf[:n])
		r.buf = r.buf[n:]
	}
}

// MPEG-2 CRC-32 (non-reflected), ARIB STD-B10 Annex B. The residue of a
// complete section including its transmitted CRC is zero.
func sectionCRC(b []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, v := range b {
		crc ^= uint32(v) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func currentSection(s []byte) bool {
	return len(s) >= 12 && s[1]&0x80 != 0 && s[5]&1 != 0 && s[6] <= s[7] && sectionCRC(s) == 0
}

func u16(b []byte) int { return int(binary.BigEndian.Uint16(b)) }

func descriptors(b []byte, handle func(byte, []byte) bool) bool {
	for len(b) > 0 {
		if len(b) < 2 || int(b[1])+2 > len(b) {
			return false
		}
		n := int(b[1]) + 2
		if !handle(b[0], b[2:n]) {
			return false
		}
		b = b[n:]
	}
	return true
}
