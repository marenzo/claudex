package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestDrainFinishesActiveRequestAndReopensGateway(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		select {
		case <-started:
		default:
			close(started)
			<-finish
		}
		return upstreamResponse(200, sse(created, `{"type":"response.completed","response":{"id":"r1","model":"gpt-6-astra","output":[`+messageItem+`]}}`)), nil
	})
	body := `{"messages":[{"role":"user","content":"hello"}],"stream":false}`
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- request(s, "/v1/messages", body) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach upstream")
	}
	control := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
		r.Header.Set("X-Claudex-Drain-Token", token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest("GET", "/_claudex/status", nil))
	if unauthorized.Code != 401 {
		t.Fatalf("control status without key: %d", unauthorized.Code)
	}
	w := control(http.MethodPost, "/_claudex/drain", "")
	token := gjson.Get(w.Body.String(), "token").String()
	if w.Code != 200 || token == "" || gjson.Get(w.Body.String(), "active").Int() != 1 {
		t.Fatalf("drain did not see active request: %d %s", w.Code, w.Body.String())
	}
	if w := request(s, "/v1/messages", body); w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("new request admitted during drain: %d %s", w.Code, w.Body.String())
	}
	if w := control(http.MethodPost, "/_claudex/drain", "wrong-owner"); w.Code != 409 {
		t.Fatalf("another owner renewed lease: %d", w.Code)
	}
	close(finish)
	if w := <-first; w.Code != 200 {
		t.Fatalf("in-flight request failed: %d %s", w.Code, w.Body.String())
	}
	w = control(http.MethodPost, "/_claudex/drain", token)
	if w.Code != 200 || gjson.Get(w.Body.String(), "active").Int() != 0 {
		t.Fatalf("drain did not finish: %d %s", w.Code, w.Body.String())
	}
	if w := control(http.MethodDelete, "/_claudex/drain", token); w.Code != 204 {
		t.Fatalf("release failed: %d", w.Code)
	}
	if w := request(s, "/v1/messages", body); w.Code != 200 {
		t.Fatalf("gateway stayed drained: %d %s", w.Code, w.Body.String())
	}
}

func TestExpiredDrainLeaseRestoresAdmission(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return upstreamResponse(200, sse(created, `{"type":"response.completed","response":{"id":"r1","model":"gpt-6-astra","output":[`+messageItem+`]}}`)), nil
	})
	s.control.owner = "abandoned"
	s.control.expires = time.Now().Add(-time.Second)
	if w := request(s, "/v1/messages", `{"messages":[]}`); w.Code != 200 {
		t.Fatalf("expired lease blocked traffic: %d", w.Code)
	}
}
