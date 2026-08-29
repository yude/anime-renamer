package annict

import "time"

// Work represents an Annict work (anime title).
type Work struct {
	ID            int    `json:"id"`
	AnnictID      int    `json:"annictId,omitempty"`
	Title         string `json:"title"`
	TitleKana     string `json:"title_kana"`
	Media         string `json:"media"`
	MediaText     string `json:"media_text"`
	SeasonName    string `json:"season_name"`
	SeasonText    string `json:"season_name_text"`
	ReleasedOn    string `json:"released_on"`
	SyobocalTID   string `json:"syobocal_tid"`
	EpisodesCount int    `json:"episodes_count"`
	WatchersCount int    `json:"watchers_count"`
}

// Episode represents an Annict episode.
type Episode struct {
	ID           int      `json:"id"`
	Number       *float64 `json:"number"`
	NumberText   string   `json:"number_text"`
	SortNumber   int      `json:"sort_number"`
	Title        string   `json:"title"`
	WorkID       int      `json:"-"`
	RecordsCount int      `json:"records_count"`
}

// Program represents a broadcast program.
type Program struct {
	ID            int       `json:"id"`
	StartedAt     time.Time `json:"started_at"`
	IsRebroadcast bool      `json:"is_rebroadcast"`
	Channel       Channel   `json:"channel"`
	Work          Work      `json:"work"`
	Episode       Episode   `json:"episode"`
}

// Channel represents a TV channel.
type Channel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// WorksResponse is the API response for works.
type WorksResponse struct {
	Works []Work `json:"works"`
}

// EpisodesResponse is the API response for episodes.
type EpisodesResponse struct {
	Episodes []Episode `json:"episodes"`
}

// ProgramsResponse is the API response for programs.
type ProgramsResponse struct {
	Programs []Program `json:"programs"`
}
