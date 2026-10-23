package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/feedback"
	"github.com/tidwall/gjson"
)

func feedbackFixtureBody() string {
	sub := feedback.Submission{Source: feedback.Source{LaunchID: strings.Repeat("a", 32)}, Report: feedback.Report{Category: "product_defect", Status: "unresolved", Title: "Valid request fails", Observed: "Valid fixture request returned 500. Bearer fixture-secret", Expected: "Return a successful result", Impact: "Repeatedly blocks review", Evidence: []string{"fixture.log: valid request returned 500 twice"}, Reproduction: "verified", FailureMode: "none"}}
	raw, _ := json.Marshal(sub)
	return string(raw)
}
func TestFeedbackAuthenticationToggleAndPersistence(t *testing.T) {
	s := settingsFixture(t)
	body := feedbackFixtureBody()
	req := httptest.NewRequest("POST", "/_claudex/feedback", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("unauthenticated report", w.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/_claudex/feedback", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthenticated access", method, w.Code)
		}
	}
	if w := request(s, "/_claudex/feedback", body); w.Code != 403 {
		t.Fatal("off by default", w.Code)
	}
	set := func(enabled bool) {
		t.Helper()
		old := s.currentConfig()
		p := control.PreferencesFor(old)
		p.Feedback.Enabled = enabled
		raw, _ := json.Marshal(p)
		w := controlRequest(s, "PUT", "/_claudex/config", string(raw), old.Revision())
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	set(true)
	disk, err := config.Load(s.configPath)
	if err != nil || !disk.Feedback.Enabled {
		t.Fatal("not persisted", err)
	}
	first := request(s, "/_claudex/feedback", body)
	if first.Code != 200 || gjson.Get(first.Body.String(), "state").String() != "untriaged" {
		t.Fatal(first.Code, first.Body.String())
	}
	second := request(s, "/_claudex/feedback", body)
	if !gjson.Get(second.Body.String(), "duplicate").Bool() {
		t.Fatal("not idempotent")
	}
	set(false)
	if w := request(s, "/_claudex/feedback", body); w.Code != 403 {
		t.Fatal("disable not immediate")
	}
	list := controlRequest(s, "GET", "/_claudex/feedback", "", "")
	if list.Code != 200 || len(gjson.Get(list.Body.String(), "records").Array()) != 1 {
		t.Fatal("existing reports lost")
	}
	if strings.Contains(list.Body.String(), "fixture-secret") {
		t.Fatal("secret persisted")
	}
	// A fresh server reads the same durable store, without needing inference.
	fresh := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("feedback used upstream"); return nil, nil })
	fresh.Configure(s.configPath, "session")
	if list := controlRequest(fresh, "GET", "/_claudex/feedback", "", ""); len(gjson.Get(list.Body.String(), "records").Array()) != 1 {
		t.Fatal("lost on restart")
	}
}
func TestFeedbackRejectsInvalidOversizedAndWriteFailure(t *testing.T) {
	s := settingsFixture(t)
	s.Config.Feedback.Enabled = true
	for _, body := range []string{`{"source":{"launch_id":"../escape"}}`, strings.Repeat(" ", 20<<10), strings.Replace(feedbackFixtureBody(), `"none"`, `"tool_misuse"`, 1)} {
		if w := request(s, "/_claudex/feedback", body); w.Code != 400 {
			t.Fatal("accepted invalid", w.Code)
		}
	}
	if err := os.WriteFile(s.feedbackStore().Dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "/_claudex/feedback", feedbackFixtureBody()); w.Code != 500 {
		t.Fatal("false save acknowledgement", w.Code)
	}
}

func TestFeedbackPagination(t *testing.T) {
	s := settingsFixture(t)
	var sub feedback.Submission
	if err := json.Unmarshal([]byte(feedbackFixtureBody()), &sub); err != nil {
		t.Fatal(err)
	}
	for i := range 103 {
		sub.Report.Title = fmt.Sprintf("Incident %d", i)
		if _, err := s.feedbackStore().Put(context.Background(), sub, "test", "test"); err != nil {
			t.Fatal(err)
		}
	}
	first := controlRequest(s, "GET", "/_claudex/feedback", "", "")
	records := gjson.Get(first.Body.String(), "records").Array()
	cursor := gjson.Get(first.Body.String(), "next_cursor").String()
	if first.Code != 200 || len(records) != 100 || cursor == "" {
		t.Fatal(first.Code, len(records), cursor)
	}
	second := controlRequest(s, "GET", "/_claudex/feedback?before="+cursor, "", "")
	rest := gjson.Get(second.Body.String(), "records").Array()
	if second.Code != 200 || len(rest) != 3 || gjson.Get(second.Body.String(), "next_cursor").String() != "" {
		t.Fatal(second.Code, second.Body.String())
	}
	seen := map[string]bool{}
	for _, r := range append(records, rest...) {
		id := r.Get("id").String()
		if seen[id] {
			t.Fatal("duplicate across pages", id)
		}
		seen[id] = true
	}
	if bad := controlRequest(s, "GET", "/_claudex/feedback?before=missing", "", ""); bad.Code != 400 {
		t.Fatal("accepted expired cursor", bad.Code)
	}
}
