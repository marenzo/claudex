package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

const overloaded = `{"type":"response.failed","response":{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`

func TestOverloadUsesClaudeErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		status        int
	}{
		{"http_503", `{"error":{"message":"Capacity unavailable."}}`, 503},
		{"http_529", `{"error":{"type":"overloaded_error","message":"Capacity unavailable."}}`, 529},
		{"http_code", `{"error":{"code":"server_is_overloaded","message":"Capacity unavailable."}}`, 502},
		{"sse_code", `{"error":{"code":"server_is_overloaded"}}`, 200},
		{"sse_type", `{"error":{"type":"service_unavailable_error"}}`, 200},
		{"sse_response", overloaded, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := parseUpstreamError(tc.status, []byte(tc.payload), time.Now())
			if e.Status != 529 || e.Type != "overloaded_error" || !e.QuotaUntil.IsZero() {
				t.Fatalf("wrong overload classification: %+v", e)
			}
			if e.Details.Status != tc.status || e.Message == "" || strings.Contains(e.Message, "Bad Gateway") {
				t.Fatalf("lost upstream status or useful fallback: %+v", e)
			}
		})
	}
	for _, payload := range []string{
		`{"error":{"type":"usage_limit_reached"}}`,
		`{"error":{"code":"rate_limit_exceeded"}}`,
	} {
		if got := parseUpstreamError(200, []byte(payload), time.Now()); got.Status != 429 || got.Type != "rate_limit_error" {
			t.Fatalf("quota or rate limit changed to overload: %+v", got)
		}
	}
}

func TestOverloadBeforeAndAfterOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stream       bool
		partial      bool
		upstreamHTTP int
	}{
		{"http", true, false, 503},
		{"json", false, false, 200},
		{"early_stream", true, false, 200},
		{"partial_stream", true, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				body := overloaded
				if tc.upstreamHTTP == 200 {
					events := []string{created}
					if tc.partial {
						events = append(events, `{"type":"response.output_text.delta","delta":"partial output"}`)
					}
					body = sse(append(events, overloaded)...)
				}
				return upstreamResponse(tc.upstreamHTTP, body), nil
			})
			s.EnableDashboard()
			w := request(s, "/v1/messages", fmt.Sprintf(`{"messages":[],"stream":%t}`, tc.stream))
			if tc.partial {
				if w.Code != 200 || !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
					t.Fatalf("partial stream became a successful turn: %d %s", w.Code, w.Body)
				}
			} else if w.Code != 529 || gjson.Get(w.Body.String(), "error.type").String() != "overloaded_error" {
				t.Fatalf("wrong early error: %d %s", w.Code, w.Body)
			}
			if !strings.Contains(w.Body.String(), `"type":"overloaded_error"`) || calls != 1 {
				t.Fatal("error type lost or upstream request replayed")
			}
			r := s.metrics.Snapshot().Recent[0]
			if r.Status != 529 || r.Error.Source != "upstream" || r.Error.Status != tc.upstreamHTTP || r.Error.Type != "service_unavailable_error" || r.Error.Code != "server_is_overloaded" {
				t.Fatalf("lost original diagnostics: %+v / %+v", r, r.Error)
			}
			if s.Quota.Remaining("test-account", time.Now()) > 0 {
				t.Fatal("overload incorrectly blocked the subscription quota")
			}
		})
	}
}
