package annict

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRESTWorkSearchIncludesEveryPage(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		fmt.Fprint(w, `{"works":[`)
		if r.URL.Query().Get("page") == "1" {
			for i := 1; i <= pageSize; i++ {
				if i > 1 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%d,"title":"作品"}`, i)
			}
		} else {
			fmt.Fprint(w, `{"id":51,"title":"作品"}`)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer server.Close()
	works, err := NewClientWithBaseURL("token", server.URL).searchWorksREST("作品")
	if err != nil || len(works) != 51 || pages != 2 {
		t.Fatalf("truncated work search: %d works, %d pages, %v", len(works), pages, err)
	}
}

func TestRepeatedEpisodePageIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"episodes":[{"id":1,"number":1},{"id":1,"number":2}]}`)
	}))
	defer server.Close()
	if _, err := NewClientWithBaseURL("token", server.URL).GetEpisodes(1); err == nil {
		t.Fatal("accepted duplicate episode IDs")
	}
}

func TestGraphQLTruncationDoesNotProduceUniqueMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"searchWorks":{"pageInfo":{"hasNextPage":true},"edges":[{"node":{"annictId":1,"title":"作品"}}]}}}`)
	}))
	defer server.Close()
	if _, _, err := NewClientWithURLs("token", server.URL, server.URL).searchWorksGraphQL("作品"); err == nil {
		t.Fatal("accepted an incomplete GraphQL search")
	}
}

func TestCancelledRequestAndRetryWaitReturnPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient("token").WithContext(ctx)
	if _, err := client.GetEpisodes(1); !errors.Is(err, context.Canceled) {
		t.Fatalf("request was not cancelled: %v", err)
	}
	if err := client.waitRetry(time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("retry wait was not cancelled: %v", err)
	}
}
