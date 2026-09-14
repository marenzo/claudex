package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marenzo/claudex/internal/dashboard"
)

func TestDashboardCapturesServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name, events, message, code string
		upstream, status            int
	}{
		{"http", `{"error":{"type":"server_error","code":"capacity_exceeded","message":"No capacity available; retry later."},"input":"PRIVATE_PROMPT"}`, "No capacity available; retry later.", "capacity_exceeded", 503, 529},
		{"stream", sse(created, `{"type":"response.failed","response":{"error":{"type":"server_error","code":"inference_failed","message":"Inference stopped."}}}`), "Inference stopped.", "inference_failed", 200, 502},
		{"empty_stream_error", sse(created, `{"type":"response.failed","response":{"error":{"type":"server_error"}}}`), "Codex returned Bad Gateway", "", 200, 502},
		{"plain", "upstream maintenance", "upstream maintenance", "", 502, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) { return upstreamResponse(tc.upstream, tc.events), nil })
			s.EnableDashboard()
			request(s, "/v1/messages", `{"messages":[],"stream":true}`)
			r := s.metrics.Snapshot().Recent[0]
			if r.Status != tc.status || r.Error == nil || r.Error.Source != "upstream" || r.Error.Status != tc.upstream || r.Error.Message != tc.message || r.Error.Code != tc.code {
				t.Fatalf("incorrect server diagnostic: %+v / %+v", r, r.Error)
			}
			data, _ := json.Marshal(r)
			if strings.Contains(string(data), "PRIVATE_PROMPT") {
				t.Fatal("retained data beyond the error excerpt")
			}
		})
	}
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached upstream")
		return nil, nil
	})
	s.EnableDashboard()
	request(s, "/v1/messages", `null`)
	r := s.metrics.Snapshot().Recent[0]
	if r.Status != 400 || r.Error == nil || r.Error.Source != "proxy" || r.Error.Message == "" {
		t.Fatal("missing proxy error details")
	}
}

func TestErrorDetailsRedactCredentialsAndBoundText(t *testing.T) {
	message := "Reader failed: Bearer opaque-secret; access_token=other-secret& account_id=acct-private " +
		"test@example.com sk-abcdefghijklmnop eyJheader.payload.signature EXACT_KEY\n" + strings.Repeat("語", 2000)
	detail := &dashboard.ErrorDetails{Source: "transport", Type: "EXACT_KEY", Message: message}
	got := redactErrorDetails(detail, []string{"EXACT_KEY"})
	if got == detail || !got.Truncated || len(got.Message) > errorMessageLimit || !utf8.ValidString(got.Message) || !strings.Contains(got.Message, "Reader failed:") || !strings.Contains(got.Message, "\n") {
		t.Fatal("message lost useful context, exceeded its bound, or reused input")
	}
	encoded, _ := json.Marshal(got)
	for _, secret := range []string{"opaque-secret", "other-secret", "acct-private", "test@example.com", "sk-abcdefghijklmnop", "eyJheader", "EXACT_KEY"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("retained credential %s", secret)
		}
	}
}

func TestDashboardRedactsKnownAccountAndClientCredentials(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		message := "Failure for test-account; token fixture; key " + strings.Repeat("k", 32)
		body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message}})
		return upstreamResponse(500, string(body)), nil
	})
	s.EnableDashboard()
	request(s, "/v1/messages", `{"messages":[]}`)
	message := s.metrics.Snapshot().Recent[0].Error.Message
	if !strings.Contains(message, "Failure for") || strings.Contains(message, "test-account") || strings.Contains(message, "fixture") || strings.Contains(message, strings.Repeat("k", 32)) {
		t.Fatalf("known secrets not redacted: %s", message)
	}
}
