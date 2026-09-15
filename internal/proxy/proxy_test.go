package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T, upstream roundTripFunc) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{Listen: "127.0.0.1:8317", AuthFile: filepath.Join(dir, "auth.json"), ClientKeyFile: filepath.Join(dir, "key"), Model: "gpt-6-astra", ReasoningEffort: "high", ContextWindow: 1000000, CompactWindow: 900000}
	if err := os.WriteFile(cfg.AuthFile, []byte(`{"type":"codex","access_token":"fixture","account_id":"test-account"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ClientKeyFile, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	client := codex.NewClient(auth.NewStore(cfg.AuthFile))
	client.HTTP.Transport = upstream
	s, err := New(cfg, client, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Server, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func upstreamResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func sse(events ...string) string { return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n" }

const created = `{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`
const messageItem = `{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"OK"}]}`
const completed = `{"type":"response.completed","response":{"id":"r1","model":"gpt-6-astra","output":[],"usage":{"input_tokens":30,"output_tokens":2}}}`

func TestPrepareEffortsAndTools(t *testing.T) {
	cfg := config.Config{Model: "gpt-6-astra", ReasoningEffort: "high"}
	for input, want := range map[string]string{"": "high", "low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "ultracode": "xhigh", "ultra": "xhigh", "max": "max"} {
		t.Run("effort_"+input, func(t *testing.T) {
			raw := `{"model":"gpt-6-astra","messages":[{"role":"user","content":"go"}],"metadata":{"user_id":"session-1"}}`
			if input != "" {
				raw = raw[:len(raw)-1] + `,"output_config":{"effort":"` + input + `"}}`
			}
			body, session, err := prepareRequest([]byte(raw), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(body, "reasoning.effort").String(); got != want {
				t.Fatalf("got %s want %s", got, want)
			}
			if session == "" || session != gjson.GetBytes(body, "prompt_cache_key").String() {
				t.Fatal("missing stable session")
			}
		})
	}
	raw := `{"messages":[],"tools":[{"name":"Artifact"},{"name":"WebSearch","input_schema":{"type":"object"}},{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"WebSearch"}}`
	body, _, err := prepareRequest([]byte(raw), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Artifact") || strings.Contains(string(body), `"name":"WebSearch"`) {
		t.Fatalf("client tools leaked: %s", body)
	}
	if gjson.GetBytes(body, "tools.0.type").String() != "web_search" || gjson.GetBytes(body, "tool_choice.type").String() != "web_search" {
		t.Fatalf("search not native: %s", body)
	}
	if !strings.Contains(gjson.GetBytes(body, "include").Raw, "web_search_call.action.sources") {
		t.Fatal("missing search sources")
	}
	for _, raw := range []string{`{"messages":[],"tool_choice":{"type":"tool","name":"Artifact"}}`, `{"messages":[],"output_config":{"effort":"bogus"}}`, `{"messages":[],"model":"other"}`, `null`} {
		if _, _, err := prepareRequest([]byte(raw), cfg); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestHTTPStreamLifecycle(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("Chatgpt-Account-Id") != "test-account" {
					t.Fatal("incorrect Codex routing")
				}
				body, _ := io.ReadAll(r.Body)
				if !gjson.GetBytes(body, "stream").Bool() || gjson.GetBytes(body, "store").Bool() {
					t.Fatal("wrong subscription request flags")
				}
				return upstreamResponse(200, sse(created, `{"type":"response.output_text.delta","delta":"OK","output_index":0,"content_index":0}`, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
			})
			w := request(s, "/v1/messages", fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream))
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body)
			}
			if stream {
				for _, part := range []string{"event: message_start", "OK", "event: message_stop", `"stop_reason":"end_turn"`} {
					if !strings.Contains(w.Body.String(), part) {
						t.Fatalf("missing %s: %s", part, w.Body)
					}
				}
			} else if gjson.Get(w.Body.String(), "content.0.text").String() != "OK" {
				t.Fatalf("output hydration failed: %s", w.Body)
			}
		})
	}
}

func TestErrorsDoNotBecomeSuccessfulTurns(t *testing.T) {
	cases := []struct {
		name, events string
		status       int
		cooling      bool
	}{
		{"quota_bootstrap", sse(created, `{"type":"response.failed","response":{"error":{"type":"usage_limit_reached","resets_in_seconds":518400}}}`), 429, true},
		{"ordinary_rate", sse(`{"type":"error","error":{"code":"rate_limit_exceeded","message":"busy"}}`), 429, false},
		{"early_eof", sse(created), 502, false},
		{"malformed", sse("not JSON"), 502, false},
		{"empty_incomplete", sse(created, `{"type":"response.incomplete","response":{"output":[],"usage":{"output_tokens":0}}}`), 502, false},
		{"artifact_item", sse(created, `{"type":"response.output_item.added","item":{"type":"function_call","name":"Artifact","call_id":"a"}}`), 502, false},
		{"artifact_terminal", sse(created, `{"type":"response.completed","response":{"output":[{"type":"function_call","name":"Artifact","call_id":"a"}]}}`), 502, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) { return upstreamResponse(200, tc.events), nil })
			w := request(s, "/v1/messages", `{"stream":true,"messages":[{"role":"user","content":"go"}]}`)
			if w.Code != tc.status || gjson.Get(w.Body.String(), "type").String() != "error" {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if cooling := s.Quota.Remaining("test-account", time.Now()) > 0; cooling != tc.cooling {
				t.Fatalf("cooling %t", cooling)
			}
		})
	}
	t.Run("partial_eof", func(t *testing.T) {
		s := fixture(t, func(*http.Request) (*http.Response, error) {
			return upstreamResponse(200, sse(created, `{"type":"response.output_text.delta","delta":"partial"}`)), nil
		})
		w := request(s, "/v1/messages", `{"stream":true,"messages":[]}`)
		if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
			t.Fatalf("partial failure hidden: %s", w.Body)
		}
	})
}

func TestLocalEndpointsDoNotSpendQuota(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected inference"); return nil, nil })
	s.Quota.Limited("test-account", time.Now().Add(6*24*time.Hour))
	if err := os.Remove(s.Config.AuthFile); err != nil {
		t.Fatal(err)
	}
	w := request(s, "/v1/messages/count_tokens", `{"messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != 200 || gjson.Get(w.Body.String(), "input_tokens").Int() <= 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/models", "/healthz", "/v1/models/gpt-6-astra"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/v1/chat/completions", "/v0/management/config", "/dashboard", "/v1/responses"} {
		if w := request(s, path, `{}`); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestStreamReaderCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan streamEvent)
	done := make(chan struct{})
	go func() { readEvents(ctx, strings.NewReader(sse(created)), events); close(done) }()
	<-done
}

func TestHydrateOutputOrder(t *testing.T) {
	got := hydrateOutput([]byte(completed), map[int64][]byte{2: []byte(`{"id":"b"}`), 0: []byte(`{"id":"a"}`)}, nil)
	var decoded any
	if json.Unmarshal(got, &decoded) != nil || gjson.GetBytes(got, "response.output.0.id").String() != "a" {
		t.Fatal(string(got))
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("client went away") }
func (brokenBody) Close() error             { return nil }

func TestBodyReadErrorsAreNot413(t *testing.T) {
	s := fixture(t, nil)
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens", brokenBody{})
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("broken body reported as %d: %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(strings.Repeat("x", maxRequestBytes+1)))
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized body reported as %d", w.Code)
	}
}

func TestHealthzIsPublicAndIdentifiesClaudex(t *testing.T) {
	s := fixture(t, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 || gjson.Get(w.Body.String(), "product").String() != "claudex" || gjson.Get(w.Body.String(), "status").String() != "ok" {
		t.Fatalf("unauthenticated health check: %d %s", w.Code, w.Body)
	}
}

func TestClientKeyHeaderForms(t *testing.T) {
	s := fixture(t, nil)
	key := strings.Repeat("k", 32)
	for _, test := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"bearer", map[string]string{"Authorization": "Bearer " + key}, 200},
		{"lowercase bearer", map[string]string{"Authorization": "bearer " + key}, 200},
		{"x-api-key", map[string]string{"X-Api-Key": key}, 200},
		{"x-api-key beside another authorization", map[string]string{"Authorization": "Basic Zm9vOmJhcg==", "X-Api-Key": key}, 200},
		{"wrong key", map[string]string{"Authorization": "Bearer " + strings.Repeat("x", 32)}, 401},
		{"no key", map[string]string{}, 401},
	} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		for name, value := range test.headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Errorf("%s: got %d, want %d", test.name, w.Code, test.want)
		}
	}
}

func TestMissingSignInMessageIsActionableAndPathFree(t *testing.T) {
	s := fixture(t, nil)
	if err := os.Remove(s.Config.AuthFile); err != nil {
		t.Fatal(err)
	}
	w := request(s, "/v1/messages", `{"model":"gpt-6-astra","messages":[{"role":"user","content":"hello"}]}`)
	body := w.Body.String()
	if w.Code != 401 || !strings.Contains(body, "claudex login") || strings.Contains(body, s.Config.AuthFile) {
		t.Fatalf("%d %s", w.Code, body)
	}
}

func TestStalledRequestBodyTimesOut(t *testing.T) {
	previous := bodyReadTimeout
	bodyReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = previous })
	server := httptest.NewServer(fixture(t, nil).Handler())
	t.Cleanup(server.Close)
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Promise 1000 body bytes, send a few, then stall.
	fmt.Fprintf(conn, "POST /v1/messages/count_tokens HTTP/1.1\r\nHost: test\r\nX-Api-Key: %s\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{\"messages\"", strings.Repeat("k", 32))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no response to a stalled body: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("stalled body answered with %d", response.StatusCode)
	}
}
