package annict

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestSearchWorks_GraphQLReturnsEmbeddedEpisodes(t *testing.T) {
	graphql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"searchWorks":{"edges":[
			{"node":{"annictId":1,"title":"作品","episodesCount":2,"episodes":{"edges":[
				{"node":{"id":"101","number":1,"sortNumber":1,"title":"第一話"}},
				{"node":{"id":"102","number":2,"sortNumber":2,"title":"第二話"}}
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
		fmt.Fprint(w, `{"episodes":[{"id":51,"number":51,"title":"ep51"}]}`)
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
	since := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("filter_work_ids"); got != "7" {
			t.Errorf("filter_work_ids = %q, want 7", got)
		}
		if got := q.Get("filter_started_at_gt"); got != since.Format("2006/01/02 15:04") {
			t.Errorf("filter_started_at_gt = %q, want %q", got, since.Format("2006/01/02 15:04"))
		}
		if got := q.Get("filter_started_at_lt"); got != until.Format("2006/01/02 15:04") {
			t.Errorf("filter_started_at_lt = %q, want %q", got, until.Format("2006/01/02 15:04"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"programs":[{"id":1,"is_rebroadcast":false}]}`)
	}))
	defer rest.Close()

	client := NewClientWithBaseURL("token", rest.URL)
	programs, err := client.GetPrograms(7, since, until)
	if err != nil {
		t.Fatalf("GetPrograms() error = %v", err)
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
