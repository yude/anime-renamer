package syobocal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("Command") != "ChLookup" || r.Header.Get("User-Agent") != userAgent {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, `<ChLookupResponse><ChItems><ChItem><ChID>71</ChID><ChName>BS日テレ</ChName></ChItem></ChItems><Result><Code>200</Code></Result></ChLookupResponse>`)
	}))
	defer server.Close()
	channels, err := NewClientWithBaseURL(server.URL).GetChannels()
	if err != nil || len(channels) != 1 || channels[0].ID != 71 || channels[0].Name != "BS日テレ" {
		t.Fatalf("GetChannels() = %+v, %v", channels, err)
	}
}

func TestGetChannelsRejectsIncompleteResponses(t *testing.T) {
	for _, body := range []string{
		`<ChLookupResponse><Result><Code>500</Code></Result></ChLookupResponse>`,
		`<ChLookupResponse>`,
		`{}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
		if _, err := NewClientWithBaseURL(server.URL).GetChannels(); err == nil {
			t.Errorf("GetChannels() accepted %q", body)
		}
		server.Close()
	}
}
