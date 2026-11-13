package tsmeta

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
)

// decodeText implements the common SI character sets in ARIB STD-B24, part 1,
// chapter 7. Unsupported sets (including DRCS) and malformed text are rejected:
// silently dropping a character could turn a different title into a match.
func decodeText(src []byte) (string, error) {
	sets := [4]byte{0x42, 0x4a, 0x30, 0x31}
	gl, gr, single := 0, 2, -1
	var out strings.Builder
	for i := 0; i < len(src); {
		b := src[i]
		i++
		switch b {
		case 0x0e:
			gl = 1
			continue
		case 0x0f:
			gl = 0
			continue
		case 0x19, 0x1d:
			if single >= 0 {
				return "", fmt.Errorf("repeated single shift")
			}
			single = 2
			if b == 0x1d {
				single = 3
			}
			continue
		case 0x1b:
			if i >= len(src) {
				return "", fmt.Errorf("truncated escape")
			}
			p := src[i]
			i++
			switch p {
			case 0x6e, 0x6f:
				gl = int(p - 0x6c)
			case 0x7e, 0x7d, 0x7c:
				gr = int(0x7f - p)
			default:
				index, double := 0, p == 0x24
				if double {
					if i >= len(src) {
						return "", fmt.Errorf("truncated designation")
					}
					p = src[i]
					i++
				}
				if p >= 0x28 && p <= 0x2b {
					index = int(p - 0x28)
					if i >= len(src) {
						return "", fmt.Errorf("truncated designation")
					}
					p = src[i]
					i++
				} else if !double {
					return "", fmt.Errorf("unsupported escape %02x", p)
				}
				if (double && p != 0x42 && p != 0x3b) || (!double && p != 0x4a && p != 0x30 && p != 0x31 && p != 0x36 && p != 0x37 && p != 0x38 && p != 0x49) {
					return "", fmt.Errorf("unsupported character set %02x", p)
				}
				sets[index] = p
			}
			continue
		case 0x20, 0xa0:
			out.WriteByte(' ')
			continue
		case 0x0d:
			out.WriteByte('\n')
			continue
		case 0x88, 0x89, 0x8a: // Text size affects presentation, not identity.
			continue
		}
		if b < 0x21 || (b >= 0x7f && b < 0xa1) || b == 0xff {
			return "", fmt.Errorf("unsupported text control %02x", b)
		}
		index := gr
		if b < 0x7f {
			index = gl
			if single >= 0 {
				index, single = single, -1
			}
		}
		code, set := b&0x7f, sets[index]
		var encoded []byte
		switch set {
		case 0x4a, 0x36:
			// The ARIB alphanumeric set uses yen and overline at these positions.
			switch code {
			case 0x5c:
				out.WriteRune('￥')
			case 0x7e:
				out.WriteRune('￣')
			default:
				out.WriteByte(code)
			}
			continue
		case 0x30, 0x37, 0x31, 0x38:
			katakana := set == 0x31 || set == 0x38
			if code >= 0x77 {
				ending := []rune("ゝゞー。「」、・")
				if katakana {
					ending = []rune("ヽヾー。「」、・")
				}
				out.WriteRune(ending[code-0x77])
				continue
			}
			if !katakana && code > 0x73 {
				return "", fmt.Errorf("undefined hiragana code")
			}
			row := byte(0xa4)
			if katakana {
				row = 0xa5
			}
			encoded = []byte{row, code | 0x80}
		case 0x49:
			if code > 0x5f {
				return "", fmt.Errorf("undefined JIS katakana code")
			}
			encoded = []byte{0x8e, code | 0x80}
		case 0x42, 0x3b:
			if i >= len(src) || (src[i] < 0x21 || src[i] > 0x7e) && (src[i] < 0xa1 || src[i] > 0xfe) || src[i]&0x80 != b&0x80 {
				return "", fmt.Errorf("invalid two-byte character")
			}
			second := src[i] & 0x7f
			i++
			if code >= 0x75 || set == 0x3b {
				// A small, explicit subset of broadcast flags. Unknown gaiji are
				// never replaced by a dot, a space, or the replacement character.
				flag := broadcastSymbols[uint16(code)<<8|uint16(second)]
				if flag == "" {
					return "", fmt.Errorf("unsupported additional symbol %02x%02x", code, second)
				}
				out.WriteString(flag)
				continue
			}
			encoded = []byte{code | 0x80, second | 0x80}
		}
		decoded, err := japanese.EUCJP.NewDecoder().Bytes(encoded)
		if err != nil || len(decoded) == 0 || strings.ContainsRune(string(decoded), utf8.RuneError) {
			return "", fmt.Errorf("invalid JIS character")
		}
		out.Write(decoded)
	}
	if single >= 0 {
		return "", fmt.Errorf("trailing single shift")
	}
	return out.String(), nil
}

// ARIB STD-B24 additional symbols, row 90. Use the same explicit flags as the
// filename parser; semantic programme annotations are not discarded here.
var broadcastSymbols = map[uint16]string{
	0x7a50: "[HV]", 0x7a51: "[SD]", 0x7a52: "[P]", 0x7a53: "[W]",
	0x7a54: "[MV]", 0x7a55: "[手]", 0x7a56: "[字]", 0x7a57: "[双]",
	0x7a58: "[デ]", 0x7a59: "[S]", 0x7a5a: "[二]", 0x7a5b: "[多]",
	0x7a5c: "[解]", 0x7a5d: "[SS]", 0x7a5e: "[B]", 0x7a5f: "[N]",
	0x7a60: "■", 0x7a61: "●", 0x7a62: "[天]", 0x7a63: "[交]",
	0x7a64: "[映]", 0x7a65: "[無]", 0x7a66: "[料]", 0x7a67: "[年齢制限]",
	0x7a68: "[前]", 0x7a69: "[後]", 0x7a6a: "[再]", 0x7a6b: "[新]",
	0x7a6c: "[初]", 0x7a6d: "[終]", 0x7a6e: "[生]", 0x7a6f: "[販]",
	0x7a70: "[声]", 0x7a71: "[吹]", 0x7a72: "[PPV]", 0x7a73: "(秘)", 0x7a74: "ほか",
}
