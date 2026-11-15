package annict

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArtworkRequiresExactWorkID(t *testing.T) {
	for i, body := range []string{
		`{"works":[{"id":42,"title":"作品","season_name":"2026-winter","images":{"facebook":{"og_image_url":"https://example.org/ogp.png"}}}]}`,
		`{"works":[]}`,
		`{"works":[{"id":43,"title":"別作品"}]}`,
		`{"works":[{"id":42,"title":"作品"},{"id":42,"title":"作品"}]}`,
		`{"works":[{"id":42,"title":""}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/works" || r.URL.Query().Get("filter_ids") != "42" || r.URL.Query().Has("filter_title") || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("unexpected artwork lookup: %s", r.URL)
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			work, err := NewClientWithBaseURL("token", server.URL).GetArtwork(context.Background(), 42)
			if body == `{"works":[]}` {
				if !errors.Is(err, ErrWorkNotFound) {
					t.Fatalf("missing work: %v", err)
				}
			} else if i == 0 {
				if err != nil || work == nil || work.ID != 42 || work.Images.Facebook.OGImageURL != "https://example.org/ogp.png" {
					t.Fatalf("artwork fields: %+v, %v", work, err)
				}
			} else if err == nil {
				t.Fatal("accepted a mismatched, duplicated, or empty work")
			}
		})
	}
	client := NewClient("token")
	if _, err := client.GetArtwork(context.Background(), 0); !errors.Is(err, ErrWorkNotFound) {
		t.Fatal("missing ID was not skipped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetArtwork(ctx, 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("artwork request ignored cancellation: %v", err)
	}
}
