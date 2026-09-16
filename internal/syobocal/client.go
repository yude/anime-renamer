// Package syobocal provides the small, read-only subset of the
// Shoboi Calendar API needed to resolve a recording date to an episode.
package syobocal

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	defaultBaseURL       = "https://cal.syoboi.jp/db.php"
	userAgent            = "anime-renamer (+https://github.com/yude/anime-renamer)"
	maxResponseBodyBytes = 4 << 20
	minimumRequestGap    = time.Second
)

var jst = time.FixedZone("JST", 9*60*60)

// Program is one ProgLookup broadcast row. Only fields used to establish a
// safe episode identity are retained.
type Program struct {
	PID       int
	TID       int
	ChannelID int
	Count     int
	StartedAt time.Time
	EndedAt   time.Time
	Subtitle  string
	Deleted   bool
	Warn      bool
}

type xmlProgram struct {
	PID       int    `xml:"PID"`
	TID       int    `xml:"TID"`
	ChannelID int    `xml:"ChID"`
	Count     int    `xml:"Count"`
	StartedAt string `xml:"StTime"`
	EndedAt   string `xml:"EdTime"`
	Subtitle  string `xml:"STSubTitle"`
	Deleted   int    `xml:"Deleted"`
	Warn      int    `xml:"Warn"`
}

type lookupResponse struct {
	Programs []xmlProgram `xml:"ProgItems>ProgItem"`
	Result   struct {
		Code    int    `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Result"`
}

// Client is a rate-limited Shoboi Calendar client.
type Client struct {
	baseURL    string
	httpClient *http.Client

	mu          sync.Mutex
	lastRequest time.Time
	requestGap  time.Duration
	now         func() time.Time
	sleep       func(time.Duration)
}

// NewClient returns a client configured for the public db.php endpoint.
func NewClient() *Client {
	return newClient(defaultBaseURL, minimumRequestGap)
}

// NewClientWithBaseURL returns a client pointed at baseURL. It is intended
// for tests and compatible mirrors; production callers should use NewClient.
func NewClientWithBaseURL(baseURL string) *Client {
	return newClient(baseURL, 0)
}

func newClient(baseURL string, requestGap time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		requestGap: requestGap,
		now:        time.Now,
		sleep:      time.Sleep,
	}
}

// GetPrograms returns programs for tid that overlap the specified JST
// calendar date. The upstream Range contract includes any program whose time
// interval overlaps this interval, rather than comparing only its start time.
func (c *Client) GetPrograms(tid int, date time.Time) ([]Program, error) {
	if tid <= 0 {
		return nil, fmt.Errorf("invalid TID %d", tid)
	}
	if date.IsZero() {
		return nil, fmt.Errorf("recording date is required")
	}

	date = date.In(jst)
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, jst)
	end := start.AddDate(0, 0, 1)

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	q := u.Query()
	q.Set("Command", "ProgLookup")
	q.Set("TID", strconv.Itoa(tid))
	q.Set("Range", start.Format("20060102_150405")+"-"+end.Format("20060102_150405"))
	q.Set("JOIN", "SubTitles")
	u.RawQuery = q.Encode()

	c.waitForRateLimit()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ProgLookup request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ProgLookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ProgLookup returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ProgLookup response: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("ProgLookup response exceeds %d bytes", maxResponseBodyBytes)
	}

	var decoded lookupResponse
	if err := xml.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode ProgLookup response: %w", err)
	}
	if decoded.Result.Code != http.StatusOK {
		return nil, fmt.Errorf("ProgLookup result %d: %s", decoded.Result.Code, decoded.Result.Message)
	}

	programs := make([]Program, 0, len(decoded.Programs))
	for _, item := range decoded.Programs {
		startedAt, err := time.ParseInLocation("2006-01-02 15:04:05", item.StartedAt, jst)
		if err != nil {
			return nil, fmt.Errorf("decode ProgLookup PID %d start time: %w", item.PID, err)
		}
		endedAt, err := time.ParseInLocation("2006-01-02 15:04:05", item.EndedAt, jst)
		if err != nil {
			return nil, fmt.Errorf("decode ProgLookup PID %d end time: %w", item.PID, err)
		}
		programs = append(programs, Program{
			PID:       item.PID,
			TID:       item.TID,
			ChannelID: item.ChannelID,
			Count:     item.Count,
			StartedAt: startedAt,
			EndedAt:   endedAt,
			Subtitle:  item.Subtitle,
			Deleted:   item.Deleted != 0,
			Warn:      item.Warn != 0,
		})
	}
	return programs, nil
}

func (c *Client) waitForRateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if wait := c.requestGap - now.Sub(c.lastRequest); wait > 0 {
		c.sleep(wait)
		now = c.now()
	}
	c.lastRequest = now
}
