package parser

import (
	"strconv"
	"strings"
	"testing"
)

func TestKanjiEpisodeOverflowIsRejected(t *testing.T) {
	tooLarge := strconv.FormatUint(^uint64(0), 10)
	digits := []rune("〇一二三四五六七八九")
	var b strings.Builder
	for _, r := range tooLarge {
		b.WriteRune(digits[r-'0'])
	}
	if _, ok := ParseKanjiNumber(b.String()); ok {
		t.Fatal("overflowed kanji integer was accepted")
	}
	if _, err := ParseFilename("作品 第" + b.String() + "話.mp4"); err == nil {
		t.Fatal("overflowed episode label was accepted")
	}
}

func FuzzParseFilename(f *testing.F) {
	for _, name := range []string{"作品 #1 「字幕」.mp4", "作品 第13.5話.ts", "作品 (20261115).m2ts", "[終]作品「最終回」.mkv", "\xff\x00.mp4"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4096 {
			t.Skip()
		}
		meta, err := ParseFilename(name)
		if err == nil {
			if meta == nil || meta.EpisodeNumber < 0 {
				t.Fatalf("invalid parse result: %+v", meta)
			}
			if meta.EpisodeLabel != "" {
				label, ok := CanonicalFractionalNumber(meta.EpisodeLabel)
				if !ok || label != meta.EpisodeLabel || meta.EpisodeNumber != 0 {
					t.Fatal("fractional identity changed")
				}
			}
		}
	})
}
