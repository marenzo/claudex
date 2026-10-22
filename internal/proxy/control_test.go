package proxy

import (
	"context"
	"encoding/json"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func controlRequest(s *Server, method, path, body, rev string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	if rev != "" {
		r.Header.Set("If-Match", `"`+s.instance+":"+rev+`"`)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func settingsFixture(t *testing.T) *Server {
	t.Helper()
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return upstreamResponse(200, sse(created, completed)), nil
	})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, s.Config); err != nil {
		t.Fatal(err)
	}
	s.Configure(path, "session")
	return s
}
func TestLivePreferencesAtomicConflictAndStrictFields(t *testing.T) {
	s := settingsFixture(t)
	old := s.currentConfig()
	prefs := control.PreferencesFor(old)
	prefs.Model = "sol"
	raw, _ := json.Marshal(prefs)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Go(func() { codes <- controlRequest(s, "PUT", "/_claudex/config", string(raw), old.Revision()).Code })
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatal("lost update", counts)
	}
	if s.currentConfig().Model != "gpt-6.1-sol" {
		t.Fatal("settings not normalized")
	}
	disk, err := config.Load(s.configPath)
	if err != nil || disk != s.currentConfig() {
		t.Fatal("disk/runtime disagreement", err)
	}
	if w := controlRequest(s, "PUT", "/_claudex/config", `{"auth_file":"/untrusted"}`, disk.Revision()); w.Code != 400 {
		t.Fatal("startup field accepted", w.Code)
	}
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), ""); w.Code != 412 {
		t.Fatal("unconditional write", w.Code)
	}
	disk.Model = "gpt-6-luna"
	if err := config.Save(s.configPath, disk); err != nil {
		t.Fatal(err)
	}
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), s.currentConfig().Revision()); w.Code != 412 || s.currentConfig().Model != "gpt-6.1-sol" {
		t.Fatal("external edit overwritten")
	}
}
func TestPreferenceFailureLeavesRuntimeUnchanged(t *testing.T) {
	s := settingsFixture(t)
	old := s.currentConfig()
	prefs := control.PreferencesFor(old)
	prefs.Effort = "bogus"
	raw, _ := json.Marshal(prefs)
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), old.Revision()); w.Code != 400 {
		t.Fatal(w.Code)
	}
	prefs.Effort = "low"
	raw, _ = json.Marshal(prefs)
	if err := os.Remove(s.configPath); err != nil {
		t.Fatal(err)
	}
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), old.Revision()); w.Code != 500 || s.currentConfig() != old {
		t.Fatal("failed save applied", w.Code)
	}
	s.Configure("", "session")
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), old.Revision()); w.Code != 403 {
		t.Fatal("read-only gateway", w.Code)
	}
}

func TestSettingsRevisionCannotCrossGatewayRestart(t *testing.T) {
	s := settingsFixture(t)
	old := s.settings()
	s.instance = "fixture-new-instance"
	if old.Revision == s.settings().Revision {
		t.Fatal("revision did not change with process")
	}
	raw, _ := json.Marshal(old.Preferences)
	r := httptest.NewRequest("PUT", "/_claudex/config", strings.NewReader(string(raw)))
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	r.Header.Set("If-Match", `"`+old.Revision+`"`)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 412 {
		t.Fatal("draft from old process accepted", w.Code)
	}
}
func TestSessionsOwnGatewayAndDisconnectReleases(t *testing.T) {
	s := settingsFixture(t)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	c, err := control.New(server.URL, s.Config.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	first, cancel1 := context.WithCancel(context.Background())
	second, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()
	done1, err := c.Attach(first, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Attach(second, "tui")
	if err != nil {
		t.Fatal(err)
	}
	if s.StopIfIdle(time.Now().Add(time.Hour), 5*time.Second) {
		t.Fatal("stopped with attached sessions")
	}
	cancel1()
	<-done1
	waitFor(t, func() bool { status, _ := c.Status(context.Background()); return status.Clients == 1 })
	cancel2()
	waitFor(t, func() bool { status, _ := c.Status(context.Background()); return status.Clients == 0 })
	if !s.control.admit() {
		t.Fatal("admission closed early")
	}
	if s.StopIfIdle(time.Now().Add(time.Hour), 5*time.Second) {
		t.Fatal("stopped active request")
	}
	s.control.finish()
	if !s.StopIfIdle(time.Now().Add(6*time.Second), 5*time.Second) {
		t.Fatal("idle gateway kept alive")
	}
	if s.control.admit() {
		t.Fatal("admitted after shutdown")
	}
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func TestInFlightReviewKeepsOriginalConfiguration(t *testing.T) {
	reached := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	calls := 0
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			close(reached)
			<-release
			return upstreamResponse(200, toolResponse()), nil
		}
		body, _ := io.ReadAll(r.Body)
		if gjson.GetBytes(body, "model").String() != "gpt-6-astra" {
			t.Error("review model changed mid-request")
		}
		return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"not_flagged","explanation":"safe"}]}`)), nil
	})
	s.Config.AutoModeClassifierModel = "gpt-6-astra"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, s.Config); err != nil {
		t.Fatal(err)
	}
	s.Configure(path, "foreground")
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- request(s, "/v1/messages", reviewRequest(false)) }()
	<-reached
	prefs := control.PreferencesFor(s.currentConfig())
	prefs.Reviewer = "sol"
	raw, _ := json.Marshal(prefs)
	if w := controlRequest(s, "PUT", "/_claudex/config", string(raw), s.currentConfig().Revision()); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	if w := <-completed; w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls != 2 {
		t.Fatal("review not performed", calls)
	}
}
