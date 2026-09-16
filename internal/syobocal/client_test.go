package syobocal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetPrograms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("Command"); got != "ProgLookup" {
			t.Errorf("Command = %q, want ProgLookup", got)
		}
		if got := r.URL.Query().Get("TID"); got != "6373" {
			t.Errorf("TID = %q, want 6373", got)
		}
		if got := r.URL.Query().Get("Range"); got != "20220923_000000-20220924_000000" {
			t.Errorf("Range = %q, want one JST calendar day", got)
		}
		if got := r.URL.Query().Get("JOIN"); got != "SubTitles" {
			t.Errorf("JOIN = %q, want SubTitles", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0"?><ProgLookupResponse><ProgItems><ProgItem id="580331"><PID>580331</PID><TID>6373</TID><StTime>2022-09-23 01:28:00</StTime><EdTime>2022-09-23 01:58:00</EdTime><Count>12</Count><Deleted>0</Deleted><Warn>0</Warn><ChID>5</ChID><STSubTitle>勝って伝えたいので</STSubTitle></ProgItem></ProgItems><Result><Code>200</Code><Message></Message></Result></ProgLookupResponse>`)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)
	date := time.Date(2022, 9, 23, 12, 0, 0, 0, time.UTC)
	programs, err := client.GetPrograms(6373, date)
	if err != nil {
		t.Fatalf("GetPrograms() error = %v", err)
	}
	if len(programs) != 1 {
		t.Fatalf("GetPrograms() = %+v, want one program", programs)
	}
	program := programs[0]
	if program.PID != 580331 || program.TID != 6373 || program.ChannelID != 5 || program.Count != 12 || program.Subtitle != "勝って伝えたいので" || program.Deleted || program.Warn {
		t.Errorf("program = %+v, unexpected fields", program)
	}
	if got := program.StartedAt.Format(time.RFC3339); got != "2022-09-23T01:28:00+09:00" {
		t.Errorf("StartedAt = %q, want JST timestamp", got)
	}
}

func TestGetProgramsRejectsUnsafeResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
		want string
	}{
		{name: "HTTP status", code: http.StatusTooManyRequests, want: "HTTP 429"},
		{name: "API status", body: `<ProgLookupResponse><Result><Code>500</Code><Message>bad query</Message></Result></ProgLookupResponse>`, want: "result 500"},
		{name: "invalid XML", body: `<ProgLookupResponse>`, want: "decode ProgLookup"},
		{name: "invalid timestamp", body: `<ProgLookupResponse><ProgItems><ProgItem><PID>1</PID><TID>1</TID><StTime>bad</StTime><EdTime>2022-09-23 01:00:00</EdTime></ProgItem></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`, want: "start time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.code != 0 {
					w.WriteHeader(tt.code)
					return
				}
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			_, err := NewClientWithBaseURL(server.URL).GetPrograms(1, time.Now())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("GetPrograms() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestGetProgramsValidatesInputsWithoutRequest(t *testing.T) {
	client := NewClientWithBaseURL("http://should-not-be-contacted.invalid")
	if _, err := client.GetPrograms(0, time.Now()); err == nil {
		t.Fatal("GetPrograms(0, date) error = nil")
	}
	if _, err := client.GetPrograms(1, time.Time{}); err == nil {
		t.Fatal("GetPrograms(tid, zero date) error = nil")
	}
}

func TestGetProgramsEnforcesRequestGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<ProgLookupResponse><ProgItems></ProgItems><Result><Code>200</Code></Result></ProgLookupResponse>`)
	}))
	defer server.Close()

	client := newClient(server.URL, time.Second)
	fakeNow := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return fakeNow }
	var sleeps []time.Duration
	client.sleep = func(delay time.Duration) {
		sleeps = append(sleeps, delay)
		fakeNow = fakeNow.Add(delay)
	}

	date := time.Date(2022, 9, 23, 0, 0, 0, 0, jst)
	if _, err := client.GetPrograms(1, date); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetPrograms(1, date); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Errorf("sleeps = %v, want [1s]", sleeps)
	}
}

func TestGetProgramsRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", maxResponseBodyBytes+1))
	}))
	defer server.Close()

	_, err := NewClientWithBaseURL(server.URL).GetPrograms(1, time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("GetPrograms() error = %v, want size-limit error", err)
	}
}
