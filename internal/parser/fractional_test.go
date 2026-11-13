package parser

import (
	"testing"
)

func TestFractionalLabelsAreNeverRounded(t *testing.T) {
	for _, tt := range []struct{ name, label string }{
		{"作品 #13.5「総集編」 (20261007).mp4", "13.5"},
		{"作品 第１３．５０話「総集編」.ts", "13.5"},
		{"作品 ep.003.50「補足」.mkv", "3.5"},
		{"作品 #0.5「前日譚」.mp4", "0.5"},
	} {
		meta, err := ParseFilename(tt.name)
		if err != nil || meta.EpisodeNumber != 0 || meta.EpisodeLabel != tt.label || meta.WorkTitle != "作品" {
			t.Errorf("ParseFilename(%q) = %+v, %v", tt.name, meta, err)
		}
	}
	for _, name := range []string{
		"作品 #4.5,#5.mp4", "作品 #4.5／#5.5.mp4", "作品 第4.5話～第5話.mp4",
		"作品 #4.5「前半」#5「後半」.mp4", "作品 #4.5.6.mp4", "作品 #4.5e2.mp4",
		"作品 #4.0.mp4", "作品 #4.1234567.mp4",
	} {
		if meta, err := ParseFilename(name); err == nil {
			t.Errorf("ParseFilename(%q) = %+v, want safe rejection", name, meta)
		}
	}
}
