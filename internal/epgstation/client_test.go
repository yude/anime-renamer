package epgstation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFindRecordingUsesExactFilenameAndCachesSearch(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/epg/api/recorded" || r.URL.Query().Get("isHalfWidth") != "false" || r.URL.Query().Get("keyword") != "もっとらぶらぶ作戦です" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		fmt.Fprintf(w, `{"total":2,"records":[
			{"id":1,"channelId":400141,"startAt":%d,"endAt":%d,"name":"アニメ ガールズ＆パンツァー もっとらぶらぶ作戦です！","videoFiles":[{"filename":"original/アニメ ガールズ＆パンツァー もっとらぶらぶ作戦です！ (20261101).m2ts"}]},
			{"id":2,"channelId":400141,"startAt":%d,"endAt":%d,"name":"似たタイトル","videoFiles":[{"filename":"別の番組 (20261101).m2ts"}]}
		]}`, date.UnixMilli(), date.Add(30*time.Minute).UnixMilli(), date.UnixMilli(), date.Add(30*time.Minute).UnixMilli())
	}))
	defer server.Close()
	client, err := NewClient(server.URL + "/epg/")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		record, err := client.FindRecording("/transcoded/アニメ ガールズ&パンツァー もっとらぶらぶ作戦です! (20261101).mp4", "ガールズ&パンツァー もっとらぶらぶ作戦です!", date)
		if err != nil || record == nil || record.ID != 1 {
			t.Fatalf("FindRecording() = %+v, %v", record, err)
		}
	}
	if requests != 1 {
		t.Errorf("search requests = %d, want 1", requests)
	}
	if record, err := client.FindRecording("似たタイトル (20261101).mp4", "ガールズ&パンツァー もっとらぶらぶ作戦です!", date); record != nil || err != nil {
		t.Fatalf("title-only similarity must not match: %+v, %v", record, err)
	}
}

func TestRecordingSearchFailsClosed(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	base := fmt.Sprintf(`{"id":1,"channelId":400141,"startAt":%d,"endAt":%d,"name":"作品","videoFiles":[{"filename":"作品 (20261101).m2ts"}]}`, date.UnixMilli(), date.Add(30*time.Minute).UnixMilli())
	for _, tt := range []struct{ name, body string }{
		{"multiple source records", `{"total":2,"records":[` + base + `,` + strings.Replace(base, `"id":1,`, `"id":2,`, 1) + `]}`},
		{"recording in progress", `{"total":1,"records":[` + strings.Replace(base, `"name":`, `"isRecording":true,"name":`, 1) + `]}`},
		{"encoding in progress", `{"total":1,"records":[` + strings.Replace(base, `"name":`, `"isEncoding":true,"name":`, 1) + `]}`},
		{"missing channel", `{"total":1,"records":[` + strings.Replace(base, `"channelId":400141`, `"channelId":0`, 1) + `]}`},
		{"date conflict", `{"total":1,"records":[` + strings.Replace(base, fmt.Sprint(date.UnixMilli()), fmt.Sprint(date.AddDate(0, 0, -1).UnixMilli()), 1) + `]}`},
		{"incomplete pages", `{"total":2,"records":[]}`},
		{"missing total", `{"records":[]}`},
		{"invalid JSON", `<html>login</html>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tt.body) }))
			defer server.Close()
			client, _ := NewClient(server.URL)
			if record, err := client.FindRecording("作品 (20261101).mp4", "作品", date); err == nil || record != nil {
				t.Fatalf("FindRecording() = %+v, %v; want safe failure", record, err)
			}
		})
	}
}

func TestSearchPaginatesBeforeChoosingARecord(t *testing.T) {
	for _, change := range []string{"valid", "duplicate source on later page", "overlapping page", "total changed", "later page failed"} {
		t.Run(change, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var records []map[string]any
				if r.URL.Query().Get("offset") == "0" {
					for i := 1; i <= 100; i++ {
						records = append(records, map[string]any{"id": i})
					}
				} else {
					if r.URL.Query().Get("offset") != "100" {
						t.Errorf("unexpected offset: %s", r.URL)
					}
					if change == "later page failed" {
						http.Error(w, "unavailable", 503)
						return
					}
					records = []map[string]any{{"id": 101}}
					if change == "overlapping page" {
						records[0]["id"] = 1
					}
				}
				if r.URL.Query().Get("offset") == "0" || change == "duplicate source on later page" {
					records[0]["channelId"] = 400141
					records[0]["name"] = "作品"
					records[0]["startAt"] = 1793458800000
					records[0]["endAt"] = 1793460600000
					records[0]["videoFiles"] = []map[string]string{{"filename": "作品 (20261101).m2ts"}}
				}
				total := 101
				if change == "total changed" && requests == 2 {
					total++
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"total": total, "records": records})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			date := time.UnixMilli(1793458800000)
			record, err := client.FindRecording("作品 (20261101).mp4", "作品", date)
			if change == "valid" {
				if err != nil || record == nil || requests != 2 {
					t.Fatalf("FindRecording() = %+v, %v, requests=%d", record, err, requests)
				}
			} else if err == nil || record != nil {
				t.Fatalf("FindRecording() = %+v, %v; want safe failure", record, err)
			}
		})
	}
}

func TestRejectsInvalidURLAndRedirects(t *testing.T) {
	for _, raw := range []string{"", "file:///tmp/recorded.json", "localhost:8888", "https://user:secret@example.com", "https://example.com?token=secret"} {
		if _, err := NewClient(raw); err == nil {
			t.Errorf("NewClient(%q) accepted invalid URL", raw)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", 302) }))
	defer server.Close()
	client, _ := NewClient(server.URL)
	if _, err := client.GetChannel(400141); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("GetChannel() error = %v", err)
	}
}

func TestGetChannelCachesCatalogueAndPreservesServiceID(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/channels" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, `[{"id":3274101064,"name":"テレビ朝日"},{"id":400141,"name":"ＢＳ日テレ"}]`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	for _, id := range []int64{3274101064, 400141} {
		channel, err := client.GetChannel(id)
		if err != nil || channel.ID != id {
			t.Fatalf("GetChannel(%d) = %+v, %v", id, channel, err)
		}
	}
	if _, err := client.GetChannel(999); err == nil {
		t.Fatal("unknown service ID was accepted")
	}
	if requests != 1 {
		t.Errorf("catalogue requests = %d, want 1", requests)
	}
}
