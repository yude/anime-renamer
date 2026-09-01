package annict

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Retry-exhaustion tests would otherwise wait out the real 1s+2s backoff.
	retrySleep = func(time.Duration) {}
	os.Exit(m.Run())
}

func alwaysFailServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

func TestSearchWorks_GraphQLSuccessSkipsREST(t *testing.T) {
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","titleKana":"サクヒン","seasonName":"2026-summer","seasonYear":2026,"episodesCount":12,"watchersCount":100,"episodes":{"edges":[]}}}
		]}}}`)
	}))
	defer graphql.Close()

	rest := alwaysFailServer(t)
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, _, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if len(works) != 1 || works[0].Title != "作品" || works[0].ID != 1 {
		t.Errorf("SearchWorks() = %+v, want single work with ID=1 Title=作品", works)
	}
}

func TestSearchWorks_GraphQLDedupesSameWorkAcrossTitleVariants(t *testing.T) {
	// Regression test: SearchWorks submits multiple title variants (raw,
	// punctuation-normalized, ...) in a single query. If the same work
	// matches more than one variant, the response could contain it more
	// than once; callers (MatchingWorks and its "len(candidateWorks)
	// > 1 means ambiguous" logic) must see it exactly once, or a single
	// real work would be misreported as an ambiguous multi-work match.
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品!","episodesCount":1,"episodes":{"edges":[]}}},
			{"node":{"annictId":1,"title":"作品!","episodesCount":1,"episodes":{"edges":[]}}}
		]}}}`)
	}))
	defer graphql.Close()

	rest := alwaysFailServer(t)
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, _, err := client.SearchWorks("作品!")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if len(works) != 1 {
		t.Errorf("SearchWorks() = %+v, want exactly 1 deduped work, got %d", works, len(works))
	}
}

func TestSearchWorks_GraphQLReturnsEmbeddedEpisodes(t *testing.T) {
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","seasonName":"SUMMER","seasonYear":2026,"episodesCount":2,"episodes":{"edges":[
				{"node":{"id":"opaque-graphql-id","annictId":101,"number":1,"sortNumber":1,"title":"第一話"}},
				{"node":{"id":"another-opaque-id","annictId":102,"number":2,"sortNumber":2,"title":"第二話"}}
			]}}}
		]}}}`)
	}))
	defer graphql.Close()

	rest := alwaysFailServer(t)
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, episodesByWork, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if len(works) != 1 {
		t.Fatalf("SearchWorks() = %+v, want 1 work", works)
	}
	if works[0].SeasonName != "2026-summer" {
		t.Errorf("works[0].SeasonName = %q, want %q", works[0].SeasonName, "2026-summer")
	}

	episodes, ok := episodesByWork[1]
	if !ok {
		t.Fatal("episodesByWork missing entry for work ID 1")
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes = %+v, want 2 entries", episodes)
	}
	if episodes[0].ID != 101 || episodes[0].Number == nil || int(*episodes[0].Number) != 1 || episodes[0].SortNumber != 1 || episodes[0].Title != "第一話" || episodes[0].WorkID != 1 {
		t.Errorf("episodes[0] = %+v, unexpected field values", episodes[0])
	}
}

func TestGraphQLSeason(t *testing.T) {
	tests := []struct {
		year int
		name string
		want string
	}{
		{year: 2026, name: "WINTER", want: "2026-winter"},
		{year: 2026, name: "summer", want: "2026-summer"},
		{year: 0, name: "WINTER", want: ""},
		{year: 2026, name: "", want: ""},
	}

	for _, tt := range tests {
		if got := graphqlSeason(tt.year, tt.name); got != tt.want {
			t.Errorf("graphqlSeason(%d, %q) = %q, want %q", tt.year, tt.name, got, tt.want)
		}
	}
}

func TestSearchWorks_GraphQL401DoesNotRetry(t *testing.T) {
	attempts := 0
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer graphql.Close()

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":4,"title":"REST作品3"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, _, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if attempts != 1 {
		t.Errorf("graphql server received %d attempts, want 1 (401 must not be retried)", attempts)
	}
	if len(works) != 1 || works[0].Title != "REST作品3" {
		t.Errorf("SearchWorks() = %+v, want fallback REST result", works)
	}
}

func TestSearchWorksWithoutTokenDoesNotContactAPI(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "should not be contacted", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClientWithURLs("", server.URL, server.URL+"/graphql")
	if _, _, err := client.SearchWorks("作品"); err == nil {
		t.Fatal("SearchWorks() error = nil, want missing-token error")
	}
	if requests != 0 {
		t.Errorf("API requests = %d, want 0 without an access token", requests)
	}
}

func TestSearchWorks_RESTFallbackReturnsNoEpisodes(t *testing.T) {
	graphql := alwaysFailServer(t)
	defer graphql.Close()

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":2,"title":"REST作品"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	_, episodesByWork, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if episodesByWork != nil {
		t.Errorf("episodesByWork = %+v, want nil for the REST fallback path", episodesByWork)
	}
}

func TestSearchWorks_FallsBackToRESTOnGraphQLError(t *testing.T) {
	graphql := alwaysFailServer(t)
	defer graphql.Close()

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/works" {
			t.Errorf("unexpected REST path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("filter_title"); got == "" {
			t.Errorf("expected filter_title query param to be set")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":2,"title":"REST作品"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, _, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if len(works) != 1 || works[0].Title != "REST作品" {
		t.Errorf("SearchWorks() = %+v, want fallback REST result", works)
	}
}

func TestSearchWorks_FallsBackToRESTOnEmptyGraphQLResult(t *testing.T) {
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[]}}}`)
	}))
	defer graphql.Close()

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":3,"title":"REST作品2"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithURLs("token", rest.URL, graphql.URL)
	works, _, err := client.SearchWorks("作品")
	if err != nil {
		t.Fatalf("SearchWorks() error = %v", err)
	}
	if len(works) != 1 || works[0].Title != "REST作品2" {
		t.Errorf("SearchWorks() = %+v, want fallback REST result on empty GraphQL edges", works)
	}
}

func TestGetEpisodes_PaginatesAndSetsWorkID(t *testing.T) {
	var requestedPages []string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			episodes := make([]string, 50)
			for i := range episodes {
				episodes[i] = fmt.Sprintf(`{"id":%d,"number":%d,"title":"ep%d"}`, i+1, i+1, i+1)
			}
			fmt.Fprintf(w, `{"episodes":[%s]}`, joinJSON(episodes))
			return
		}
		// The REST API documents episode numbers as quoted decimals; the
		// client must accept that form as well as GraphQL's JSON numbers.
		fmt.Fprint(w, `{"episodes":[{"id":51,"number":"51","title":"ep51"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	episodes, err := client.GetEpisodes(42)
	if err != nil {
		t.Fatalf("GetEpisodes() error = %v", err)
	}
	if len(episodes) != 51 {
		t.Fatalf("GetEpisodes() returned %d episodes, want 51", len(episodes))
	}
	for _, ep := range episodes {
		if ep.WorkID != 42 {
			t.Errorf("episode %d has WorkID=%d, want 42", ep.ID, ep.WorkID)
		}
	}
	if len(requestedPages) != 2 || requestedPages[0] != "1" || requestedPages[1] != "2" {
		t.Errorf("requested pages = %v, want [1 2]", requestedPages)
	}
}

func joinJSON(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

func TestGetPrograms_SendsDateRangeAndWorkID(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	since := time.Date(2026, 8, 12, 0, 0, 0, 0, jst)
	until := time.Date(2026, 8, 14, 0, 0, 0, 0, jst)

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me/programs" {
			t.Errorf("request path = %q, want /me/programs", r.URL.Path)
		}
		q := r.URL.Query()
		if got := q.Get("filter_work_ids"); got != "7" {
			t.Errorf("filter_work_ids = %q, want 7", got)
		}
		if got := q.Get("filter_started_at_gt"); got != since.UTC().Format("2006/01/02 15:04") {
			t.Errorf("filter_started_at_gt = %q, want %q", got, since.UTC().Format("2006/01/02 15:04"))
		}
		if got := q.Get("filter_started_at_lt"); got != until.UTC().Format("2006/01/02 15:04") {
			t.Errorf("filter_started_at_lt = %q, want %q", got, until.UTC().Format("2006/01/02 15:04"))
		}
		// Regression: "episode" must be in the requested field list, or
		// Annict's real API omits it from the response entirely, leaving
		// Program.Episode.ID at zero and silently breaking
		// findMatchingProgram's episode-ID matching.
		fields := strings.Split(q.Get("fields"), ",")
		if !slices.Contains(fields, "episode") {
			t.Errorf("fields = %q, must include \"episode\"", q.Get("fields"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"programs":[{"id":1,"is_rebroadcast":false,"episode":{"id":42}}]}`)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	programs, err := client.GetPrograms(7, since, until)
	if err != nil {
		t.Fatalf("GetPrograms() error = %v", err)
	}
	if len(programs) == 1 && programs[0].Episode.ID != 42 {
		t.Errorf("programs[0].Episode.ID = %d, want 42", programs[0].Episode.ID)
	}
	if len(programs) != 1 {
		t.Fatalf("GetPrograms() returned %d programs, want 1", len(programs))
	}
}

func TestGet_RetriesOnTransientFailureThenSucceeds(t *testing.T) {
	attempts := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":9,"title":"再試行"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	works, err := client.searchWorksREST("作品")
	if err != nil {
		t.Fatalf("searchWorksREST() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("server received %d attempts, want 2", attempts)
	}
	if len(works) != 1 || works[0].Title != "再試行" {
		t.Errorf("searchWorksREST() = %+v, want single work after retry", works)
	}
}

func TestGet_ExhaustsRetriesAndReturnsError(t *testing.T) {
	attempts := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	_, err := client.searchWorksREST("作品")
	if err == nil {
		t.Fatal("searchWorksREST() error = nil, want error after exhausting retries")
	}
	if attempts != 3 {
		t.Errorf("server received %d attempts, want 3", attempts)
	}
}

func TestGet_DoesNotRetryPermanentClientError(t *testing.T) {
	attempts := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	_, err := client.searchWorksREST("作品")
	if err == nil {
		t.Fatal("searchWorksREST() error = nil, want error on 401")
	}
	if attempts != 1 {
		t.Errorf("server received %d attempts, want 1 (401 must not be retried)", attempts)
	}
}

func TestGet_RetriesRateLimitStatus(t *testing.T) {
	attempts := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"works":[{"id":10,"title":"レート制限後"}]}`)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	works, err := client.searchWorksREST("作品")
	if err != nil {
		t.Fatalf("searchWorksREST() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("server received %d attempts, want 2 (429 must be retried)", attempts)
	}
	if len(works) != 1 || works[0].Title != "レート制限後" {
		t.Errorf("searchWorksREST() = %+v, want single work after retry", works)
	}
}

func TestRetryDelay(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		attempt    int
		retryAfter string
		want       time.Duration
	}{
		{name: "default backoff", attempt: 2, want: 2 * time.Second},
		{name: "server seconds", attempt: 1, retryAfter: "5", want: 5 * time.Second},
		{name: "short server delay keeps backoff", attempt: 2, retryAfter: "1", want: 2 * time.Second},
		{name: "HTTP date", attempt: 1, retryAfter: now.Add(4 * time.Second).Format(http.TimeFormat), want: 4 * time.Second},
		{name: "server delay is bounded", attempt: 1, retryAfter: "3600", want: maxRetryDelay},
		{name: "invalid header", attempt: 1, retryAfter: "later", want: time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryDelay(tt.attempt, tt.retryAfter, now); got != tt.want {
				t.Errorf("retryDelay() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGetTruncatesPermanentErrorResponse(t *testing.T) {
	attempts := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, strings.Repeat("x", maxErrorBodyBytes+100))
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	_, err := client.searchWorksREST("作品")
	if err == nil || !strings.Contains(err.Error(), "[truncated]") {
		t.Fatalf("searchWorksREST() error = %v, want truncated response marker", err)
	}
	if attempts != 1 {
		t.Errorf("server received %d attempts, want 1 for permanent 400", attempts)
	}
	if len(err.Error()) > maxErrorBodyBytes+200 {
		t.Errorf("error length = %d, want bounded diagnostic", len(err.Error()))
	}
}

func TestReadResponseBodyLimitsSuccessfulResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBodyBytes+1))),
	}
	body, truncated, err := readResponseBody(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(body) != maxResponseBodyBytes {
		t.Errorf("readResponseBody() length=%d truncated=%v, want %d,true", len(body), truncated, maxResponseBodyBytes)
	}
}

func TestAdvancePage(t *testing.T) {
	if _, more, err := advancePage(1, pageSize-1); err != nil || more {
		t.Errorf("advancePage(short page) = more %v, error %v; want false,nil", more, err)
	}
	if next, more, err := advancePage(1, pageSize); err != nil || !more || next != 2 {
		t.Errorf("advancePage(full page) = %d,%v,%v; want 2,true,nil", next, more, err)
	}
	if _, _, err := advancePage(maxPaginationPages, pageSize); err == nil {
		t.Error("advancePage() at safety limit should fail")
	}
}
