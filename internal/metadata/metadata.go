// Package metadata exports verified matches without inferring episode identities.
package metadata

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/yude/anime-renamer/internal/matcher"
)

// Info uses one Annict work as one independent show, as the renamer's folders do.
// Season is a user-selected local season, not an inferred TMDb/TVDb season.
type Info struct {
	Title, ShowTitle  string
	WorkID, EpisodeID int
	Season, Episode   int
	Movie             bool
}

func FromMatch(match *matcher.MatchResult, season int) (Info, error) {
	if match == nil || match.Work == nil || match.Work.ID <= 0 || strings.TrimSpace(match.Work.Title) == "" {
		return Info{}, fmt.Errorf("metadata requires a verified Annict work")
	}
	info := Info{WorkID: match.Work.ID, ShowTitle: match.Work.Title, Season: season}
	if match.Standalone {
		if !strings.EqualFold(match.Work.Media, "movie") || match.Episode != nil || match.RecordingEpisode != nil || match.OutputNumberSet || match.OutputEpisodeNumber != 0 || match.OutputEpisodeLabel != "" {
			return Info{}, fmt.Errorf("metadata requires a verified standalone movie")
		}
		info.Title, info.Movie = match.Work.Title, true
		return info, nil
	}
	number, ok := matcher.MatchResultEpisodeNumber(match)
	if !ok || number <= 0 || match.Episode == nil || match.Episode.ID <= 0 || season <= 0 {
		return Info{}, fmt.Errorf("episode metadata requires a positive integer episode and Annict episode ID; fractional episodes and unnumbered specials are not renumbered")
	}
	if match.Episode.Number == nil && strings.TrimSpace(match.Episode.NumberText) == "" {
		return Info{}, fmt.Errorf("episode metadata requires an explicit Annict number; sort order alone is not an episode label")
	}
	info.Episode, info.EpisodeID = number, match.Episode.ID
	if match.Episode.WorkID > 0 && match.Episode.WorkID != match.Work.ID {
		return Info{}, fmt.Errorf("episode belongs to a different Annict work")
	}
	info.Title = match.Episode.Title
	if match.FileSubtitle != "" {
		info.Title = match.FileSubtitle
	}
	if strings.TrimSpace(info.Title) == "" {
		info.Title = fmt.Sprintf("第%d話", number)
	}
	return info, nil
}

type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr,omitempty"`
	ID      int    `xml:",chardata"`
}

type document struct {
	XMLName   xml.Name
	Title     string   `xml:"title"`
	ShowTitle string   `xml:"showtitle,omitempty"`
	Season    int      `xml:"season,omitempty"`
	Episode   int      `xml:"episode,omitempty"`
	UniqueID  uniqueID `xml:"uniqueid"`
}

func encode(doc document) ([]byte, error) {
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("NFO metadata exceeds 1 MiB")
	}
	return []byte(xml.Header + string(data) + "\n"), nil
}

func (i Info) VideoNFO() ([]byte, error) {
	doc := document{XMLName: xml.Name{Local: "episodedetails"}, Title: i.Title, ShowTitle: i.ShowTitle, Season: i.Season, Episode: i.Episode, UniqueID: uniqueID{Type: "annict", Default: "true", ID: i.EpisodeID}}
	if i.Movie {
		doc.XMLName.Local = "movie"
		doc.ShowTitle, doc.Season, doc.Episode = "", 0, 0
		doc.UniqueID.ID = i.WorkID
	}
	return encode(doc)
}

func (i Info) ShowNFO() ([]byte, error) {
	return encode(document{XMLName: xml.Name{Local: "tvshow"}, Title: i.ShowTitle, UniqueID: uniqueID{Type: "annict", Default: "true", ID: i.WorkID}})
}

func (i Info) tags() map[string]string {
	tags := map[string]string{"title": i.Title}
	if !i.Movie {
		tags["show"] = i.ShowTitle
		tags["show_name"] = i.ShowTitle
		tags["season_number"] = strconv.Itoa(i.Season)
		tags["episode_sort"] = strconv.Itoa(i.Episode)
	}
	return tags
}
