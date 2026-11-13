// Package epgstation reads recording metadata without modifying the recorder.
package epgstation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const pageSize = 100

type Recording struct {
	ID          int64       `json:"id"`
	ChannelID   int64       `json:"channelId"`
	StartAt     int64       `json:"startAt"`
	EndAt       int64       `json:"endAt"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	IsRecording bool        `json:"isRecording"`
	IsEncoding  bool        `json:"isEncoding"`
	VideoFiles  []VideoFile `json:"videoFiles"`
}

type VideoFile struct {
	Filename string `json:"filename"`
}

type Channel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	queries    map[string][]Recording
	channels   map[int64]Channel
}

func NewClient(baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("EPGStation URL must be an absolute http(s) URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{
		baseURL: u,
		httpClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		queries: make(map[string][]Recording),
	}, nil
}

// FindRecording accepts only an exact source filename stem (allowing the
// container extension to change during transcoding), on the same JST date.
// Title searches retrieve candidates; they never establish recording identity.
func (c *Client) FindRecording(filename, title string, date time.Time) (*Recording, error) {
	if date.IsZero() {
		return nil, fmt.Errorf("EPGStation lookup requires a recording date")
	}
	keyword := searchKeyword(title)
	if keyword == "" {
		return nil, fmt.Errorf("EPGStation lookup requires a work title")
	}
	records, err := c.search(keyword)
	if err != nil {
		return nil, err
	}
	key := filenameKey(filename)
	if key == "" {
		return nil, fmt.Errorf("EPGStation lookup requires a supported recording filename")
	}
	var found *Recording
	jst := time.FixedZone("JST", 9*60*60)
	for i := range records {
		r := &records[i]
		matched := false
		for _, video := range r.VideoFiles {
			if filenameKey(video.Filename) == key {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if r.ID <= 0 || r.ChannelID <= 0 || r.StartAt <= 0 || r.EndAt <= r.StartAt || r.IsRecording || r.IsEncoding || strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("EPGStation recording has incomplete metadata or is still recording/encoding")
		}
		if time.UnixMilli(r.StartAt).In(jst).Format("2006-01-02") != date.In(jst).Format("2006-01-02") {
			return nil, fmt.Errorf("EPGStation source filename conflicts with its recording date")
		}
		if found != nil {
			return nil, fmt.Errorf("multiple EPGStation recordings have the same source filename")
		}
		found = r
	}
	return found, nil
}

func (c *Client) GetChannel(id int64) (*Channel, error) {
	if c.channels == nil {
		var channels []Channel
		if err := c.get("/api/channels", nil, &channels); err != nil {
			return nil, err
		}
		index := make(map[int64]Channel)
		for _, channel := range channels {
			if channel.ID <= 0 || strings.TrimSpace(channel.Name) == "" {
				return nil, fmt.Errorf("EPGStation returned an invalid channel")
			}
			if _, exists := index[channel.ID]; exists {
				return nil, fmt.Errorf("EPGStation returned duplicate channel IDs")
			}
			index[channel.ID] = channel
		}
		c.channels = index
	}
	channel, ok := c.channels[id]
	if !ok {
		return nil, fmt.Errorf("EPGStation recording channel %d is unknown", id)
	}
	return &channel, nil
}

func (c *Client) search(keyword string) ([]Recording, error) {
	if records, ok := c.queries[keyword]; ok {
		return records, nil
	}
	var records []Recording
	seen := make(map[int64]bool)
	total := -1
	for page := 0; page < 1000; page++ {
		query := url.Values{"isHalfWidth": {"false"}, "keyword": {keyword}, "limit": {"100"}, "offset": {fmt.Sprint(len(records))}}
		var response struct {
			Records []Recording `json:"records"`
			Total   *int        `json:"total"`
		}
		if err := c.get("/api/recorded", query, &response); err != nil {
			return nil, err
		}
		if response.Total == nil || *response.Total < 0 || (total >= 0 && total != *response.Total) || len(response.Records) > pageSize {
			return nil, fmt.Errorf("EPGStation recording search is incomplete or changed during pagination")
		}
		total = *response.Total
		for _, record := range response.Records {
			if record.ID <= 0 || seen[record.ID] {
				return nil, fmt.Errorf("EPGStation returned invalid or repeated recording IDs")
			}
			seen[record.ID] = true
		}
		records = append(records, response.Records...)
		if len(records) == total {
			c.queries[keyword] = records
			return records, nil
		}
		if len(records) > total || len(response.Records) == 0 {
			return nil, fmt.Errorf("EPGStation recording search ended before all candidates were verified")
		}
	}
	return nil, fmt.Errorf("EPGStation recording search exceeded its page limit")
}

func (c *Client) get(endpoint string, query url.Values, result any) error {
	u := *c.baseURL
	u.Path += endpoint
	u.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "anime-renamer (+https://github.com/yude/anime-renamer)")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request EPGStation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("EPGStation %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	const limit = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if len(body) > limit {
		return fmt.Errorf("EPGStation response exceeds size limit")
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode EPGStation %s: %w", endpoint, err)
	}
	return nil
}

func filenameKey(filename string) string {
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	ext := path.Ext(base)
	switch strings.ToLower(ext) {
	case ".mp4", ".ts", ".m2ts", ".mkv", ".m4v":
		return norm.NFKC.String(strings.TrimSuffix(base, ext))
	default:
		return ""
	}
}

func searchKeyword(title string) string {
	words := strings.FieldsFunc(title, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	best, japanese := "", false
	for _, word := range words {
		hasJapanese := strings.ContainsFunc(word, func(r rune) bool { return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) })
		if (hasJapanese && !japanese) || (hasJapanese == japanese && len([]rune(word)) > len([]rune(best))) {
			best, japanese = word, hasJapanese
		}
	}
	return best
}
