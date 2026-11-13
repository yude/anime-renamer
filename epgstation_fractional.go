package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yude/anime-renamer/internal/epgstation"
	"github.com/yude/anime-renamer/internal/parser"
)

var exactContentFraction = regexp.MustCompile(`^[#＃♯][\s\x{3000}]*[0-9０-９]+[.．][0-9０-９]+$`)
var contentFractionPrefix = regexp.MustCompile(`^(?:[#＃♯]|第)[\s\x{3000}]*[0-9０-９]+[.．][0-9０-９]+`)

// Only a whole dedicated program-content field may supply a special label.
// Synopsis, cast, announcements, and arbitrary references to earlier episodes
// are excluded. Structured and display fields must agree if both are present.
func fractionalRecordingContents(meta *parser.RecordingMetadata, record *epgstation.Recording) (*parser.RecordingMetadata, bool, error) {
	var contents []string
	for key, value := range record.RawExtended {
		if isProgramContentHeading(key) {
			contents = append(contents, strings.TrimSpace(value))
		}
	}
	var current []string
	collect := false
	flush := func() {
		if collect {
			contents = append(contents, strings.TrimSpace(strings.Join(current, "\n")))
		}
		current = nil
	}
	for _, line := range strings.Split(record.Extended, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "◇") || strings.HasPrefix(line, "◆") {
			flush()
			collect = isProgramContentHeading(line)
		} else if collect {
			current = append(current, line)
		}
	}
	flush()
	var label string
	for _, value := range contents {
		if !exactContentFraction.MatchString(value) {
			if contentFractionPrefix.MatchString(value) {
				return nil, false, fmt.Errorf("program-content field does not contain one exact fractional label")
			}
			continue
		}
		parsed, err := parser.ParseFilename(meta.WorkTitle + " " + value + ".ts")
		if err != nil || parsed.EpisodeLabel == "" {
			return nil, false, fmt.Errorf("invalid program-content fractional label: %v", err)
		}
		if label != "" && label != parsed.EpisodeLabel {
			return nil, false, fmt.Errorf("EPGStation program-content fields have conflicting fractional labels")
		}
		label = parsed.EpisodeLabel
	}
	if label == "" {
		return meta, false, nil
	}
	if meta.EpisodeNumber > 0 || (meta.EpisodeLabel != "" && meta.EpisodeLabel != label) {
		return nil, false, fmt.Errorf("program-content fractional label conflicts with the program's explicit episode")
	}
	copy := *meta
	copy.EpisodeLabel = label
	copy.EpisodeNumber = 0
	return &copy, true, nil
}

func isProgramContentHeading(key string) bool {
	key = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(key), "◇◆"))
	return key == "今回の番組内容" || key == "番組内容"
}
