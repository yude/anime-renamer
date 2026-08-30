package annict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	defaultBaseURL  = "https://api.annict.com/v1"
	graphqlEndpoint = "https://api.annict.com/graphql"
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
				ID         string   `json:"id"`
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
              id
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

	punctNorm := normalizePunctForSearch(title)

	titles := []string{title}
	if punctNorm != title {
		titles = append(titles, punctNorm)
	}
	if norm := normalize.NormalizeForSearch(title); norm != title && norm != punctNorm {
		titles = append(titles, norm)
	}
	// Deduplicate
	seen := make(map[string]bool)
	var uniqueTitles []string
	for _, t := range titles {
		if !seen[t] {
			seen[t] = true
			uniqueTitles = append(uniqueTitles, t)
		}
	}
	titles = uniqueTitles

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
			SeasonName:    node.SeasonName,
			EpisodesCount: node.EpisodesCount,
			WatchersCount: node.WatchersCount,
		}
		works = append(works, w)

		if len(node.Episodes.Edges) == 0 {
			continue
		}
		episodes := make([]Episode, 0, len(node.Episodes.Edges))
		for _, epEdge := range node.Episodes.Edges {
			epID := 0
			if id, err := strconv.Atoi(epEdge.Node.ID); err == nil {
				epID = id
			}
			episodes = append(episodes, Episode{
				ID:         epID,
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
		params.Set("per_page", "50")
		params.Set("page", strconv.Itoa(page))

		var resp EpisodesResponse
		if err := c.get("/episodes", params, &resp); err != nil {
			return nil, fmt.Errorf("get episodes: %w", err)
		}

		for i := range resp.Episodes {
			resp.Episodes[i].WorkID = workID
		}
		allEpisodes = append(allEpisodes, resp.Episodes...)

		if len(resp.Episodes) < 50 {
			break
		}
		page++
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
		params.Set("filter_started_at_gt", since.Format("2006/01/02 15:04"))
		params.Set("filter_started_at_lt", until.Format("2006/01/02 15:04"))
		// "episode" must be requested explicitly (like "channel" already
		// was) or Annict omits it from the response entirely, leaving
		// Program.Episode.ID at its zero value — silently disabling
		// findMatchingProgram's episode-ID matching.
		params.Set("fields", "id,started_at,is_rebroadcast,channel,episode")
		params.Set("sort_started_at", "asc")
		params.Set("per_page", "50")
		params.Set("page", strconv.Itoa(page))

		var resp ProgramsResponse
		if err := c.get("/programs", params, &resp); err != nil {
			return nil, fmt.Errorf("get programs: %w", err)
		}

		allPrograms = append(allPrograms, resp.Programs...)

		if len(resp.Programs) < 50 {
			break
		}
		page++
	}

	return allPrograms, nil
}

// postGraphQL sends a GraphQL request and returns the raw response body.
func (c *Client) postGraphQL(query graphqlQuery) ([]byte, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			retrySleep(time.Duration(attempt) * time.Second)
		}

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

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d from graphql: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			if !isRetryableStatus(resp.StatusCode) {
				return nil, lastErr
			}
			continue
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

// get performs an HTTP GET request and decodes the JSON response.
func (c *Client) get(path string, params url.Values, result interface{}) error {
	u := c.baseURL + path + "?" + params.Encode()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			retrySleep(time.Duration(attempt) * time.Second)
		}

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

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, path, strings.TrimSpace(string(respBody)))
			if !isRetryableStatus(resp.StatusCode) {
				return lastErr
			}
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
