package matcher

import (
	"strconv"

	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
)

// CorrectMetadataSubtitle is limited to the complete subtitle, named work,
// and explicit episode number. It does not introduce kana-wide substitutions
// or general edit-distance matching.
func CorrectMetadataSubtitle(meta *parser.RecordingMetadata) (*parser.RecordingMetadata, bool) {
	if meta == nil || meta.EpisodeLabel != "" || meta.EpisodeNumber <= 0 {
		return meta, false
	}
	type correction struct {
		work                string
		number              int
		observed, canonical string
	}
	corrections := []correction{
		// Broadcaster and distributor both identify episode 1:
		// https://s.mxtv.jp/bangumi/program.html?ch=1&date=20261006&hm=0000
		// https://animestore.docomo.ne.jp/animestore/ci_pc?workId=29233
		{"テムパル～アイテムの力～", 1, "レジェンダリー職業", "レジェンダリー転職"},
		// The official episode-4 announcement uses both spellings:
		// https://gup-mottolovelove.jp/article-b4/
		{"ガールズ＆パンツァー もっとらぶらぶ作戦です！", 4,
			"すぱボコりぞーとです！／ミニぺパ？／ひょっこり知波単島であります！／心にバレーを！です！",
			"すぱボコりぞーとです！／ミニペパ？／ひょっこり知波単島であります！／心にバレーを！ です！"},
	}
	for _, rule := range corrections {
		if meta.EpisodeNumber == rule.number && normalize.NormalizeTitleForMatch(meta.WorkTitle) == normalize.NormalizeTitleForMatch(rule.work) && subtitleIdentityKey(meta.Subtitle) == subtitleIdentityKey(rule.observed) {
			copy := *meta
			copy.Subtitle = rule.canonical
			return &copy, true
		}
	}
	return meta, false
}

// MetadataEpisodeLabel retains the existing integer representation and the
// separate decimal label without coercing one into the other.
func MetadataEpisodeLabel(meta *parser.RecordingMetadata) string {
	if meta.EpisodeLabel != "" {
		return meta.EpisodeLabel
	}
	return strconv.Itoa(meta.EpisodeNumber)
}
