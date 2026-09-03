package annict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yude/anime-renamer/internal/normalize"
)

// normalizePunctForSearch converts half-width ASCII punctuation to full-width
// to improve matching against Annict's database.
func normalizePunctForSearch(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '!' {
			b.WriteRune('！')
		} else if r == '~' {
			b.WriteRune('～')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const (
	defaultBaseURL       = "https://api.annict.com/v1"
	graphqlEndpoint      = "https://api.annict.com/graphql"
	pageSize             = 50
	maxPaginationPages   = 100
	maxResponseBodyBytes = 4 << 20
	maxErrorBodyBytes    = 16 << 10
	maxRetryDelay        = 30 * time.Second
)

// retrySleep is overridable in tests so retry-exhaustion paths don't have to
// wait out the real backoff delay.
var retrySleep = time.Sleep

// isRetryableStatus reports whether an HTTP status code is worth retrying.
// 4xx responses (bad token, not found, malformed request, ...) are
// permanent failures that won't succeed on a later attempt except for 429
// (rate limiting), so retrying them only adds backoff delay to every
// affected file for no benefit.
func isRetryableStatus(code int) bool {
	if code < 400 || code >= 500 {
		return true
	}
	return code == http.StatusTooManyRequests
}

// Client is an Annict API client.
type Client struct {
	accessToken string
	baseURL     string
	graphqlURL  string
	httpClient  *http.Client
}

// NewClient creates a new Annict API client.
func NewClient(accessToken string) *Client {
	return &Client{
		accessToken: accessToken,
		baseURL:     defaultBaseURL,
		graphqlURL:  graphqlEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithBaseURL creates a client with a custom REST base URL (for testing).
func NewClientWithBaseURL(accessToken, baseURL string) *Client {
	return &Client{
		accessToken: accessToken,
		baseURL:     baseURL,
		graphqlURL:  graphqlEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithURLs creates a client with custom REST and GraphQL endpoints
// (for testing both code paths against a local server).
func NewClientWithURLs(accessToken, baseURL, graphqlURL string) *Client {
	return &Client{
		accessToken: accessToken,
		baseURL:     baseURL,
		graphqlURL:  graphqlURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// graphqlQuery is a GraphQL request payload.
type graphqlQuery struct {
	Query     string      `json:"query"`
	Variables interface{} `json:"variables"`
}

// graphqlResponse is a GraphQL response payload.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// graphqlSearchWorksResponse represents the GraphQL searchWorks response.
type graphqlSearchWorksResponse struct {
	SearchWorks struct {
		Edges []struct {
			Node graphqlWorkNode `json:"node"`
		} `json:"edges"`
	} `json:"searchWorks"`
}

// graphqlWorkNode represents a work node in GraphQL response.
type graphqlWorkNode struct {
	AnnictID      int    `json:"annictId"`
	Title         string `json:"title"`
	TitleKana     string `json:"titleKana"`
	SeasonName    string `json:"seasonName"`
	SeasonYear    int    `json:"seasonYear"`
	EpisodesCount int    `json:"episodesCount"`
	WatchersCount int    `json:"watchersCount"`
	Episodes      struct {
		Edges []struct {
			Node struct {
				AnnictID   int      `json:"annictId"`
				Number     *float64 `json:"number"`
				NumberText string   `json:"numberText"`
				SortNumber int      `json:"sortNumber"`
				Title      string   `json:"title"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"episodes"`
}

// SearchWorks searches for works by title using the GraphQL API, falling
// back to the REST API if GraphQL fails or finds nothing.
//
// The GraphQL response also includes up to 100 episodes per work (already
// fetched as part of the same query), returned here keyed by work ID so
// callers with a small/complete work don't need a second round-trip to the
// REST episodes endpoint. A work whose EpisodesCount exceeds what GraphQL
// returned is truncated in this map; callers must fall back to GetEpisodes
// for those. The REST fallback path never populates this map, since the
// REST search endpoint doesn't return episode data.
func (c *Client) SearchWorks(title string) ([]Work, map[int][]Episode, error) {
	// Try GraphQL first
	works, episodesByWork, err := c.searchWorksGraphQL(title)
	if err == nil && len(works) > 0 {
		return works, episodesByWork, nil
	}

	// Fallback to REST API
	works, err = c.searchWorksREST(title)
	return works, nil, err
}

// searchWorksGraphQL uses the GraphQL API to search for works.
func (c *Client) searchWorksGraphQL(title string) ([]Work, map[int][]Episode, error) {
	query := `query SearchWorks($titles: [String!]!) {
  searchWorks(titles: $titles) {
    edges {
      node {
        annictId
        title
        titleKana
        seasonName
        seasonYear
        episodesCount
        watchersCount
        episodes(first: 100, orderBy: {field: SORT_NUMBER, direction: ASC}) {
          edges {
            node {
              annictId
              number
              numberText
              sortNumber
              title
            }
          }
        }
      }
    }
  }
}`

	titles := searchTitleVariants(title)

	variables := map[string]interface{}{
		"titles": titles,
	}

	reqBody := graphqlQuery{
		Query:     query,
		Variables: variables,
	}

	body, err := c.postGraphQL(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("graphql search works: %w", err)
	}

	var resp graphqlResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode graphql response: %w", err)
	}

	if len(resp.Errors) > 0 {
		return nil, nil, fmt.Errorf("graphql error: %s", resp.Errors[0].Message)
	}

	var searchResp graphqlSearchWorksResponse
	if err := json.Unmarshal(resp.Data, &searchResp); err != nil {
		return nil, nil, fmt.Errorf("decode search data: %w", err)
	}

	// Multiple title variants are submitted in one query (see titles
	// above); if the same underlying work matches more than one variant,
	// dedupe by AnnictID so callers don't see it twice and mistake it for
	// genuinely ambiguous results.
	var works []Work
	seenWorkIDs := make(map[int]bool)
	episodesByWork := make(map[int][]Episode)
	for _, edge := range searchResp.SearchWorks.Edges {
		node := edge.Node
		if seenWorkIDs[node.AnnictID] {
			continue
		}
		seenWorkIDs[node.AnnictID] = true

		w := Work{
			ID:            node.AnnictID,
			AnnictID:      node.AnnictID,
			Title:         node.Title,
			TitleKana:     node.TitleKana,
			SeasonName:    graphqlSeason(node.SeasonYear, node.SeasonName),
			EpisodesCount: node.EpisodesCount,
			WatchersCount: node.WatchersCount,
		}
		works = append(works, w)

		if len(node.Episodes.Edges) == 0 {
			continue
		}
		episodes := make([]Episode, 0, len(node.Episodes.Edges))
		for _, epEdge := range node.Episodes.Edges {
			episodes = append(episodes, Episode{
				ID:         epEdge.Node.AnnictID,
				Number:     epEdge.Node.Number,
				NumberText: epEdge.Node.NumberText,
				SortNumber: epEdge.Node.SortNumber,
				Title:      epEdge.Node.Title,
				WorkID:     node.AnnictID,
			})
		}
		episodesByWork[node.AnnictID] = episodes
	}

	return works, episodesByWork, nil
}

// searchTitleVariants keeps Annict search to one GraphQL request while
// covering presentation differences observed in recorder EPG names. The raw
// title always remains first; broader variants only affect candidate recall,
// and matcher.MatchingWorks performs the strict post-filtering step.
func searchTitleVariants(title string) []string {
	seen := make(map[string]bool)
	variants := make([]string, 0, 16)
	add := func(value string) {
		value = normalize.CollapseSpaces(strings.TrimSpace(value))
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		variants = append(variants, value)
	}
	normalizedTitle := normalize.Normalize(title)

	add(title)
	add(normalizePunctForSearch(title))
	add(mixedExclamationWidth(title))
	add(spaceBeforeParenthetical(mixedExclamationWidth(title)))
	add(parentheticalBase(title))
	add(normalizedTitle)
	add(normalizePunctForSearch(normalizedTitle))
	add(normalize.NormalizeForSearch(title))
	add(spaceBeforeInnerHyphens(title))
	add(compactSpacesAroundHyphens(title))
	add(compactSpaceAfterJapaneseStop(title))
	add(normalize.NormalizeKatakanaDashes(title))
	add(punctuationAsSpaces(title))
	add(removeAllSpaces(title))
	add(compactSeasonSpacing(normalizedTitle))
	add(normalizePunctForSearch(compactSeasonSpacing(normalizedTitle)))
	add(expandTrailingASCIISuffix(normalizedTitle))
	add(spaceBeforeJapaneseSeasonSuffix(normalizedTitle))
	if baseTitle := seriesBaseSearchTitle(title); baseTitle != title {
		add(baseTitle)
		add(mixedExclamationWidth(baseTitle))
		add(normalizePunctForSearch(normalize.Normalize(baseTitle)))
	}
	if baseTitle := seriesBaseSearchTitle(normalizedTitle); baseTitle != normalizedTitle {
		add(baseTitle)
		add(normalizePunctForSearch(baseTitle))
	}
	if baseTitle := decorativeSubtitleBase(title); baseTitle != title {
		add(baseTitle)
	}
	for _, alias := range titleSearchAliases[normalize.NormalizeTitleForMatch(title)] {
		add(alias)
	}

	if stripped := stripParentheticalSegments(title); stripped != title {
		add(stripped)
		normalizedStripped := normalize.Normalize(stripped)
		add(normalizedStripped)
		add(normalize.NormalizeForSearch(stripped))
		add(punctuationAsSpaces(stripped))
		add(spaceBeforeJapaneseSeasonSuffix(normalizedStripped))
	}
	return variants
}

var titleSearchAliases = map[string][]string{
	normalize.NormalizeTitleForMatch("シュタインズ・ゲート ゼロ"):                {"STEINS;GATE 0"},
	normalize.NormalizeTitleForMatch("ポプテピピック TVアニメーション作品第二シリーズ"):    {"ポプテピピック 第二シリーズ"},
	normalize.NormalizeTitleForMatch("「1分間だけ触れてもいいよ…」シェアハウスの秘密ルール。"): {"1分間だけ触れてもいいよ"},
	normalize.NormalizeTitleForMatch("うたの☆プリンスさまっ♪ マジLOVE1000%"):     {"うたの☆プリンスさまっ♪"},
	normalize.NormalizeTitleForMatch("ＯｖｅｒＤｒｉｖｅ"):                    {"Over Drive"},
	normalize.NormalizeTitleForMatch("タイムボカンシリーズ ヤッターマン"):            {"ヤッターマン"},
	normalize.NormalizeTitleForMatch("オーイ！とんぼ"):                      {"オーイ! とんぼ"},
}

func punctuationAsSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ' '
		}
		return r
	}, s)
}

var spacesAroundHyphenPattern = regexp.MustCompile(`\s*([-－―])\s*`)

func compactSpacesAroundHyphens(s string) string {
	return spacesAroundHyphenPattern.ReplaceAllString(s, "$1")
}

func compactSpaceAfterJapaneseStop(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsSpace(r) && i > 0 && runes[i-1] == '。' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func removeAllSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func mixedExclamationWidth(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i := 0; i < len(runes); {
		if runes[i] != '!' && runes[i] != '！' {
			b.WriteRune(runes[i])
			i++
			continue
		}
		end := i + 1
		for end < len(runes) && (runes[end] == '!' || runes[end] == '！') {
			end++
		}
		if end-i == 1 {
			b.WriteRune('！')
		} else {
			b.WriteString(strings.Repeat("!", end-i))
		}
		i = end
	}
	return b.String()
}

func spaceBeforeParenthetical(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if (r == '(' || r == '（') && i > 0 && !unicode.IsSpace(runes[i-1]) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parentheticalBase(s string) string {
	index := strings.IndexAny(s, "(（")
	if index <= 0 {
		return s
	}
	return strings.TrimSpace(s[:index])
}

var (
	seasonNumberSpacePattern = regexp.MustCompile(`(?i)(season)\s+([0-9]+)$`)
	ordinalSeasonGapPattern  = regexp.MustCompile(`(?i)\s+([0-9]+(?:st|nd|rd|th)\s+season)$`)
	trailingLatinNumber      = regexp.MustCompile(`([A-Za-z])([0-9]+)$`)
	trailingRomanNumeral     = regexp.MustCompile(`([0-9])([IVX]+)$`)
	japaneseSeasonSuffix     = regexp.MustCompile(`(\S)(第[0-9]+(?:期|クール))$`)
	japaneseSeriesSuffix     = regexp.MustCompile(`\s*[（(]?\s*第?[0-9一二三四五六七八九十]+期.*$`)
	englishSeriesSuffix      = regexp.MustCompile(`(?i)\s*(?:season\s*[0-9]+|[0-9]+(?:st|nd|rd|th)\s+season).*$`)
	romanSeriesSuffix        = regexp.MustCompile(`(?i)([^a-z])(?:[ivxＸ]+|[ⅡⅢⅣⅤⅥⅦⅧⅨⅩ]+)(?:[\s~～〜\-－―].*)?$`)
)

func compactSeasonSpacing(s string) string {
	s = seasonNumberSpacePattern.ReplaceAllString(s, "$1$2")
	return ordinalSeasonGapPattern.ReplaceAllString(s, "$1")
}

func expandTrailingASCIISuffix(s string) string {
	s = trailingLatinNumber.ReplaceAllString(s, "$1 $2")
	return trailingRomanNumeral.ReplaceAllString(s, "$1 $2")
}

func spaceBeforeJapaneseSeasonSuffix(s string) string {
	return japaneseSeasonSuffix.ReplaceAllString(s, "$1 $2")
}

func seriesBaseSearchTitle(s string) string {
	if base := japaneseSeriesSuffix.ReplaceAllString(s, ""); base != s {
		return strings.TrimSpace(base)
	}
	if base := englishSeriesSuffix.ReplaceAllString(s, ""); base != s {
		return strings.TrimSpace(base)
	}
	if base := romanSeriesSuffix.ReplaceAllString(s, "$1"); base != s {
		return strings.TrimSpace(base)
	}
	return s
}

func decorativeSubtitleBase(s string) string {
	index := strings.IndexAny(s, "~～〜")
	if englishIndex := strings.Index(s, " -"); englishIndex >= 0 && (index < 0 || englishIndex < index) {
		index = englishIndex
	}
	if index <= 0 {
		return s
	}
	return strings.TrimRight(strings.TrimSpace(s[:index]), "!！?？")
}

func spaceBeforeInnerHyphens(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if (r == '-' || r == '－' || r == '―') && i > 0 && i+1 < len(runes) && !unicode.IsSpace(runes[i-1]) && !unicode.IsSpace(runes[i+1]) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripParentheticalSegments(s string) string {
	depth := 0
	balanced := true
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '(', '（':
			depth++
		case ')', '）':
			if depth == 0 {
				balanced = false
				continue
			}
			depth--
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	if !balanced || depth != 0 {
		return s
	}
	return b.String()
}

// graphqlSeason converts GraphQL's separate year and enum fields (for
// example 2026 and "SUMMER") to the same "2026-summer" representation
// returned by the REST API and consumed by the matcher.
func graphqlSeason(year int, name string) string {
	if year <= 0 || name == "" {
		return ""
	}
	return fmt.Sprintf("%d-%s", year, strings.ToLower(name))
}

// searchWorksREST uses the REST API to search for works (fallback).
func (c *Client) searchWorksREST(title string) ([]Work, error) {
	params := url.Values{}
	params.Set("filter_title", normalize.NormalizeForSearch(title))
	params.Set("fields", "id,title,title_kana,season_name,syobocal_tid,episodes_count")
	params.Set("per_page", "25")

	var resp WorksResponse
	if err := c.get("/works", params, &resp); err != nil {
		return nil, fmt.Errorf("search works: %w", err)
	}
	return resp.Works, nil
}

// GetEpisodes returns all episodes for a given work via REST API.
func (c *Client) GetEpisodes(workID int) ([]Episode, error) {
	var allEpisodes []Episode
	page := 1

	for {
		params := url.Values{}
		params.Set("filter_work_id", strconv.Itoa(workID))
		params.Set("fields", "id,number,number_text,sort_number,title")
		params.Set("sort_sort_number", "asc")
		params.Set("per_page", strconv.Itoa(pageSize))
		params.Set("page", strconv.Itoa(page))

		var resp EpisodesResponse
		if err := c.get("/episodes", params, &resp); err != nil {
			return nil, fmt.Errorf("get episodes: %w", err)
		}

		for i := range resp.Episodes {
			resp.Episodes[i].WorkID = workID
		}
		allEpisodes = append(allEpisodes, resp.Episodes...)

		next, more, err := advancePage(page, len(resp.Episodes))
		if err != nil {
			return nil, fmt.Errorf("get episodes: %w", err)
		}
		if !more {
			break
		}
		page = next
	}

	return allEpisodes, nil
}

// GetPrograms returns programs for a work within a date range.
func (c *Client) GetPrograms(workID int, since, until time.Time) ([]Program, error) {
	var allPrograms []Program
	page := 1

	for {
		params := url.Values{}
		params.Set("filter_work_ids", strconv.Itoa(workID))
		// Annict interprets these timezone-less filter values as UTC. Convert
		// explicitly so callers can pass recording dates in JST without
		// shifting the requested window by nine hours.
		params.Set("filter_started_at_gt", since.UTC().Format("2006/01/02 15:04"))
		params.Set("filter_started_at_lt", until.UTC().Format("2006/01/02 15:04"))
		// "episode" must be requested explicitly (like "channel" already
		// was) or Annict omits it from the response entirely, leaving
		// Program.Episode.ID at its zero value — silently disabling
		// findMatchingProgram's episode-ID matching.
		params.Set("fields", "id,started_at,is_rebroadcast,channel,episode")
		params.Set("sort_started_at", "asc")
		params.Set("per_page", strconv.Itoa(pageSize))
		params.Set("page", strconv.Itoa(page))

		var resp ProgramsResponse
		if err := c.get("/me/programs", params, &resp); err != nil {
			return nil, fmt.Errorf("get programs: %w", err)
		}

		allPrograms = append(allPrograms, resp.Programs...)

		next, more, err := advancePage(page, len(resp.Programs))
		if err != nil {
			return nil, fmt.Errorf("get programs: %w", err)
		}
		if !more {
			break
		}
		page = next
	}

	return allPrograms, nil
}

// postGraphQL sends a GraphQL request and returns the raw response body.
func (c *Client) postGraphQL(query graphqlQuery) ([]byte, error) {
	if c.accessToken == "" {
		return nil, fmt.Errorf("ANNICT_ACCESS_TOKEN is required for API requests")
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	var lastErr error
	nextRetryAfter := ""
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			retrySleep(retryDelay(attempt, nextRetryAfter, time.Now()))
		}
		nextRetryAfter = ""

		req, err := http.NewRequest("POST", c.graphqlURL, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("create request: %w", err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.accessToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP POST graphql: %w", err)
			continue
		}
		nextRetryAfter = resp.Header.Get("Retry-After")

		respBody, truncated, err := readResponseBody(resp)
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				if !isRetryableStatus(resp.StatusCode) {
					return nil, lastErr
				}
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d from graphql: %s%s", resp.StatusCode, strings.TrimSpace(string(respBody)), truncationSuffix(truncated))
			if !isRetryableStatus(resp.StatusCode) {
				return nil, lastErr
			}
			continue
		}
		if truncated {
			lastErr = fmt.Errorf("graphql response exceeds %d bytes", maxResponseBodyBytes)
			continue
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// get performs an HTTP GET request and decodes the JSON response.
func (c *Client) get(path string, params url.Values, result interface{}) error {
	if c.accessToken == "" {
		return fmt.Errorf("ANNICT_ACCESS_TOKEN is required for API requests")
	}

	u := c.baseURL + path + "?" + params.Encode()

	var lastErr error
	nextRetryAfter := ""
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			retrySleep(retryDelay(attempt, nextRetryAfter, time.Now()))
		}
		nextRetryAfter = ""

		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			lastErr = fmt.Errorf("create request: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+c.accessToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP GET %s: %w", path, err)
			continue
		}
		nextRetryAfter = resp.Header.Get("Retry-After")

		respBody, truncated, err := readResponseBody(resp)
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				if !isRetryableStatus(resp.StatusCode) {
					return lastErr
				}
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d from %s: %s%s", resp.StatusCode, path, strings.TrimSpace(string(respBody)), truncationSuffix(truncated))
			if !isRetryableStatus(resp.StatusCode) {
				return lastErr
			}
			continue
		}
		if truncated {
			lastErr = fmt.Errorf("response from %s exceeds %d bytes", path, maxResponseBodyBytes)
			continue
		}

		if err := json.Unmarshal(respBody, result); err != nil {
			lastErr = fmt.Errorf("decode JSON: %w", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("all retries failed: %w", lastErr)
}

func retryDelay(attempt int, retryAfter string, now time.Time) time.Duration {
	delay := time.Duration(attempt) * time.Second
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		if serverDelay := time.Duration(seconds) * time.Second; serverDelay > delay {
			delay = serverDelay
		}
	} else if retryAt, err := http.ParseTime(retryAfter); err == nil {
		if serverDelay := retryAt.Sub(now); serverDelay > delay {
			delay = serverDelay
		}
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func advancePage(currentPage, itemCount int) (next int, more bool, err error) {
	if itemCount < pageSize {
		return currentPage, false, nil
	}
	if currentPage >= maxPaginationPages {
		return 0, false, fmt.Errorf("pagination exceeded %d pages", maxPaginationPages)
	}
	return currentPage + 1, true, nil
}

func readResponseBody(resp *http.Response) (body []byte, truncated bool, err error) {
	defer resp.Body.Close()
	limit := maxResponseBodyBytes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = maxErrorBodyBytes
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

func truncationSuffix(truncated bool) string {
	if truncated {
		return " [truncated]"
	}
	return ""
}
