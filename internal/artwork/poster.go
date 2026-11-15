package artwork

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/yude/anime-renamer/internal/annict"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/BIZUDPGothic-Regular.ttf
var fontData []byte

// FontLicense is embedded so binary distributions can display the full license.
//
//go:embed fonts/OFL.txt
var FontLicense string

var fontOnce sync.Once
var posterFont *opentype.Font
var posterFontError error

func posterFace(size float64) (font.Face, error) {
	fontOnce.Do(func() { posterFont, posterFontError = opentype.Parse(fontData) })
	if posterFontError != nil {
		return nil, posterFontError
	}
	return opentype.NewFace(posterFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

// Poster renders metadata only. Downloaded artwork is never cropped, resized,
// composited, or otherwise modified to create this independent title card.
func Poster(work *annict.ArtworkWork) ([]byte, error) {
	if work == nil || work.ID <= 0 || !utf8.ValidString(work.Title) || utf8.RuneCountInString(work.Title) > 1024 {
		return nil, fmt.Errorf("invalid poster identity or title")
	}
	title := strings.Join(strings.Fields(work.Title), " ")
	if title == "" {
		return nil, fmt.Errorf("poster title is empty")
	}
	var face font.Face
	var lines []string
	for _, size := range []float64{82, 74, 66, 58, 50, 42, 34, 28} {
		candidate, err := posterFace(size)
		if err != nil {
			return nil, err
		}
		lines, err = wrapTitle(candidate, title, 820)
		if err != nil {
			_ = candidate.Close()
			return nil, err
		}
		if len(lines) > 0 && len(lines) <= 8 && len(lines)*candidate.Metrics().Height.Ceil() <= 820 {
			face = candidate
			break
		}
		_ = candidate.Close()
	}
	if face == nil {
		return nil, fmt.Errorf("poster title does not fit without truncation")
	}
	defer face.Close()
	canvas := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{17, 24, 39, 255}), image.Point{}, draw.Src)
	palette := []color.RGBA{{71, 211, 183, 255}, {146, 170, 255, 255}, {245, 181, 117, 255}, {229, 145, 197, 255}}
	accent := palette[work.ID%len(palette)]
	draw.Draw(canvas, image.Rect(90, 182, 170, 190), image.NewUniform(accent), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(90, 1320, 910, 1322), image.NewUniform(color.RGBA{52, 65, 82, 255}), image.Point{}, draw.Src)
	label, err := posterFace(24)
	if err != nil {
		return nil, err
	}
	defer label.Close()
	textAt(canvas, label, 90, 132, "ANIME SERIES", accent)
	height := face.Metrics().Height.Ceil()
	y := 750 - len(lines)*height/2 + face.Metrics().Ascent.Ceil()
	for _, line := range lines {
		textAt(canvas, face, 90, y, line, color.RGBA{243, 246, 250, 255})
		y += height
	}
	season := seasonLabel(work.SeasonName)
	textAt(canvas, label, 90, 1390, season, color.RGBA{163, 177, 194, 255})
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func textAt(dst draw.Image, face font.Face, x, y int, text string, ink color.Color) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(text)
}

func wrapTitle(face font.Face, title string, width int) ([]string, error) {
	var tokens []string
	word := ""
	for _, r := range title {
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("poster title contains a control character")
		}
		if _, ok := face.GlyphAdvance(r); !ok {
			return nil, fmt.Errorf("bundled poster font has no glyph for %U", r)
		}
		if unicode.In(r, unicode.Latin, unicode.Greek, unicode.Cyrillic) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '-' || r == '\'' {
			word += string(r)
		} else {
			if word != "" {
				tokens, word = append(tokens, word), ""
			}
			tokens = append(tokens, string(r))
		}
	}
	if word != "" {
		tokens = append(tokens, word)
	}
	wrap := func(limit int) []string {
		var lines []string
		line := ""
		for _, token := range tokens {
			if font.MeasureString(face, token).Ceil() > limit {
				return nil
			}
			candidate := line + token
			if line != "" && font.MeasureString(face, candidate).Ceil() > limit {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, strings.TrimSpace(line))
				}
				line = strings.TrimLeftFunc(token, unicode.IsSpace)
			} else {
				line = candidate
			}
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
		return lines
	}
	initial := wrap(width)
	if initial == nil {
		return nil, nil // Try a smaller font; never split a word or truncate it.
	}
	// Find the narrowest width preserving the line count, balancing short
	// Japanese titles instead of leaving one or two characters on the last line.
	low, high := 1, width
	for low < high {
		middle := (low + high) / 2
		if lines := wrap(middle); lines != nil && len(lines) <= len(initial) {
			high = middle
		} else {
			low = middle + 1
		}
	}
	return wrap(high), nil
}

func seasonLabel(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) != 2 || len(parts[0]) != 4 || strings.Trim(parts[0], "0123456789") != "" {
		return ""
	}
	seasons := map[string]string{"winter": "冬", "spring": "春", "summer": "夏", "autumn": "秋"}
	if season := seasons[parts[1]]; season != "" {
		return parts[0] + "年 " + season
	}
	return ""
}
