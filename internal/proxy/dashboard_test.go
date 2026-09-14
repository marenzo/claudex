package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestDashboardIsOptInAndAuthenticated(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("dashboard spent inference"); return nil, nil })
	get := func(path, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Api-Key", key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/dashboard", "/dashboard/api"} {
		if w := get(path, strings.Repeat("k", 32)); w.Code != 404 {
			t.Fatal("dashboard enabled by default", w.Code)
		}
	}
	s.EnableDashboard()
	w := get("/dashboard", "")
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing dashboard shell or CSP")
	}
	if strings.Contains(w.Body.String(), strings.Repeat("k", 32)) {
		t.Fatal("key in HTML")
	}
	for _, path := range []string{"/dashboard/app.js", "/dashboard/analytics.js", "/dashboard/vendor/echarts-6.1.0.min.js", "/dashboard/vendor/ECHARTS-LICENSE.txt"} {
		asset := get(path, "")
		if asset.Code != 200 || asset.Body.Len() == 0 {
			t.Fatalf("public dashboard asset missing: %s (%d)", path, asset.Code)
		}
	}
	if w := get("/dashboard/api", ""); w.Code != 401 {
		t.Fatal("unauthenticated metrics exposed")
	}
	w = get("/dashboard/api", strings.Repeat("k", 32))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("metrics access/cache")
	}
	if gjson.Get(w.Body.String(), "totals.requests").Int() != 0 {
		t.Fatal("dashboard counts itself")
	}
}

func TestDashboardRecordsUsageAndStreamFailures(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				terminal := `{"type":"response.completed","response":{"id":"r1","model":"gpt-6-astra","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":30},"output_tokens_details":{"reasoning_tokens":10}},"output":[` + messageItem + `]}}`
				return upstreamResponse(200, sse(created, terminal)), nil
			})
			s.EnableDashboard()
			body := `{"messages":[{"role":"user","content":"PRIVATE_PROMPT"}],"stream":false}`
			if stream {
				body = strings.Replace(body, "false", "true", 1)
			}
			w := request(s, "/v1/messages", body)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			snap := s.metrics.Snapshot()
			if snap.Active != 0 || snap.Totals.Tokens.Input != 100 || snap.Totals.Tokens.Cached != 30 || snap.Totals.Tokens.Output != 20 || snap.Totals.Tokens.Reasoning != 10 || snap.Totals.UsageReported != 1 {
				t.Fatalf("usage: %+v", snap)
			}
		})
	}
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return upstreamResponse(200, sse(created, `{"type":"response.output_text.delta","delta":"partial"}`)), nil
	})
	s.EnableDashboard()
	request(s, "/v1/messages", `{"messages":[],"stream":true}`)
	snap := s.metrics.Snapshot()
	if snap.Totals.Errors != 1 || snap.Recent[0].Status != 502 || snap.Totals.UsageReported != 0 {
		t.Fatal("partial failure reported as success", snap)
	}
}

func TestDashboardIgnoresAbsentUsage(t *testing.T) {
	for _, usage := range []string{`{}`, `{"input_tokens":"100","output_tokens":10}`, `null`} {
		s := fixture(t, func(*http.Request) (*http.Response, error) {
			return upstreamResponse(200, sse(created, `{"type":"response.completed","response":{"output":[`+messageItem+`],"usage":`+usage+`}}`)), nil
		})
		s.EnableDashboard()
		request(s, "/v1/messages", `{"messages":[]}`)
		if s.metrics.Snapshot().Totals.UsageReported != 0 {
			t.Fatalf("accepted missing usage %s", usage)
		}
	}
}
