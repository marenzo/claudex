package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const reviewContext = `{"v":1,"permission_mode":"auto","platform":"macos","live_cwd":"/fixture","home_dir":"/home/fixture","rules":{"allow":[],"deny":[],"ask":[]},"auto_mode":{"allow":[],"soft_deny":[],"hard_deny":[],"environment":[]},"trusted_directories":{"primary":{"path":"/fixture","resolved":["/fixture"]},"additional":[],"network":[],"block_reads_outside_working_directories":true},"restricted":false,"is_remote_mode":false,"classify_all_shell":true,"case_insensitive_paths":false,"artifact_consent_holdback":true}`

func reviewRequest(stream bool) string {
	return fmt.Sprintf(`{"model":"astra","stream":%t,"messages":[{"role":"user","content":"Print a fixture marker"}],"tools":[{"name":"Bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],"safeguards":[{"type":"dangerous_tool_use","classifier_context":%s}]}`, stream, reviewContext)
}

const reviewTool = `{"type":"function_call","id":"fc_1","call_id":"toolu_test","name":"Bash","arguments":"{\"command\":\"printf SAFE_PROBE\"}"}`

func toolResponse() string {
	return sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+reviewTool+`}`, completed)
}

func classifierResponse(verdicts string) string {
	encoded, _ := json.Marshal(verdicts)
	return sse(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + string(encoded) + `}]}],"usage":{"input_tokens":40,"output_tokens":10}}}`)
}

func TestSafeguardsStreamAndJSON(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, outcome := range []string{"flagged", "not_flagged"} {
			t.Run(fmt.Sprint(stream)+outcome, func(t *testing.T) {
				calls := 0
				s := fixture(t, func(r *http.Request) (*http.Response, error) {
					body, _ := io.ReadAll(r.Body)
					calls++
					if calls == 1 {
						return upstreamResponse(200, toolResponse()), nil
					}
					if gjson.GetBytes(body, "model").String() != "gpt-5.6-sol" || len(gjson.GetBytes(body, "tools").Array()) != 0 || gjson.GetBytes(body, "tool_choice").String() != "none" || gjson.GetBytes(body, "instructions").String() != classifierPolicy {
						t.Errorf("classifier isolation: %s", body)
					}
					if r.Header.Get("Session_id") != "" {
						t.Error("classifier reused generation session")
					}
					payload := gjson.GetBytes(body, "input.0.content.0.text").String()
					if gjson.Get(payload, "tool_uses.0.id").String() != "toolu_test" || gjson.Get(payload, "tool_uses.0.input.command").String() != "printf SAFE_PROBE" || gjson.Get(payload, "classifier_context.v").Int() != 1 {
						t.Errorf("classifier payload: %s", payload)
					}
					return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"`+outcome+`","explanation":"Fixture review"}]}`)), nil
				})
				s.Config.AutoModeClassifierModel = "sol"
				s.EnableDashboard()
				w := request(s, "/v1/messages", reviewRequest(stream))
				if w.Code != 200 || calls != 2 {
					t.Fatalf("status %d calls %d body %s", w.Code, calls, w.Body)
				}
				var results gjson.Result
				if stream {
					for _, line := range strings.Split(w.Body.String(), "\n") {
						if strings.HasPrefix(line, "data: ") && gjson.Get(line[6:], "type").String() == "message_delta" {
							results = gjson.Get(line[6:], "delta.safeguard_results")
						}
					}
				} else {
					results = gjson.Get(w.Body.String(), "safeguard_results")
				}
				if results.Get("0.type").String() != safeguardType || results.Get("0.status.tool_uses.toolu_test.outcome").String() != outcome {
					t.Fatalf("missing verdict: %s", w.Body)
				}
				if snapshot := s.metrics.Snapshot(); snapshot.Totals.Requests != 2 || snapshot.Totals.Tokens.Input != 70 {
					t.Errorf("classifier usage missing: %+v", snapshot.Totals)
				}
			})
		}
	}
}

func TestSafeguardFailureReleasesNoToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing", classifierResponse(`{"verdicts":[]}`), 200},
		{"unknown_id", classifierResponse(`{"verdicts":[{"id":"other","outcome":"not_flagged","explanation":"ok"}]}`), 200},
		{"invalid_outcome", classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"allow","explanation":"ok"}]}`), 200},
		{"refusal", sse(`{"type":"response.refusal.delta","delta":"No"}`), 200},
		{"truncated", sse(`{"type":"response.incomplete","response":{}}`), 200},
		{"upstream", `{"error":"fixture"}`, 429},
		{"malformed", sse(`not json`), 200},
		{"no_terminal", sse(`{"type":"response.output_text.delta","delta":"{}"}`), 200},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+fmt.Sprint(stream), func(t *testing.T) {
				calls := 0
				s := fixture(t, func(r *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return upstreamResponse(200, toolResponse()), nil
					}
					return upstreamResponse(tc.status, tc.body), nil
				})
				s.Config.AutoModeClassifierModel = "astra"
				w := request(s, "/v1/messages", reviewRequest(stream))
				if w.Code != 502 || strings.Contains(w.Body.String(), "content_block_start") || strings.Contains(w.Body.String(), "toolu_test") || strings.Contains(w.Body.String(), "not_flagged") {
					t.Fatalf("unsafe response %d %s", w.Code, w.Body)
				}
				if len(s.classifierSlots) != 0 {
					t.Error("review slot leaked")
				}
			})
		}
	}
}

func TestSafeguardContextRejectedBeforeInference(t *testing.T) {
	for _, field := range []string{"v", "rules", "trusted_directories", "auto_mode.hard_deny", "live_cwd", "restricted"} {
		t.Run(field, func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				t.Error("upstream contacted")
				return upstreamResponse(500, ""), nil
			})
			s.Config.AutoModeClassifierModel = "astra"
			raw, _ := sjson.Delete(reviewRequest(true), "safeguards.0.classifier_context."+field)
			w := request(s, "/v1/messages", raw)
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	for _, mutation := range []struct {
		path  string
		value any
	}{
		{"safeguards.0.classifier_context.v", 2},
		{"safeguards.0.classifier_context.truncated", true},
		{"safeguards.0.classifier_context.shed", []string{"prior_turn_context"}},
		{"safeguards.0.type", "unknown"},
		{"messages.0.content", strings.Repeat("x", classifierInputLimit)},
	} {
		raw, _ := sjson.Set(reviewRequest(true), mutation.path, mutation.value)
		if _, err := newSafeguardReview([]byte(raw), config.Config{AutoModeClassifierModel: "astra"}); err == nil {
			t.Errorf("accepted %s", mutation.path)
		}
	}
}

func TestSafeguardOptInAndTextOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		calls := 0
		s := fixture(t, func(*http.Request) (*http.Response, error) {
			calls++
			return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
		})
		if enabled {
			s.Config.AutoModeClassifierModel = "astra"
		}
		w := request(s, "/v1/messages", reviewRequest(true))
		if w.Code != 200 || calls != 1 {
			t.Fatalf("status %d calls %d", w.Code, calls)
		}
		if strings.Contains(w.Body.String(), "safeguard_results") != enabled {
			t.Errorf("opt in %t: %s", enabled, w.Body)
		}
	}
}

func TestSafeguardCancellationAndCapacity(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	calls := 0
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return upstreamResponse(200, toolResponse()), nil
		}
		close(entered)
		<-r.Context().Done()
		close(exited)
		return nil, r.Context().Err()
	})
	s.Config.AutoModeClassifierModel = "astra"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reviewRequest(true))).WithContext(ctx)
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(w, r); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("classifier did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("classifier did not cancel")
	}
	if strings.Contains(w.Body.String(), "toolu_test") || len(s.classifierSlots) != 0 {
		t.Fatal("canceled review released tools or leaked capacity")
	}
	for range cap(s.classifierSlots) {
		s.classifierSlots <- struct{}{}
	}
	w = request(s, "/v1/messages", reviewRequest(true))
	if w.Code != 429 || calls != 2 {
		t.Fatalf("capacity: %d calls %d", w.Code, calls)
	}
}

func TestParseVerdictsExactCoverage(t *testing.T) {
	calls := []reviewedTool{{ID: "a"}, {ID: "b"}}
	for _, raw := range []string{
		`{"verdicts":[{"id":"a","outcome":"not_flagged","explanation":"ok"},{"id":"a","outcome":"not_flagged","explanation":"ok"}]}`,
		`{"verdicts":[{"id":"a","outcome":"not_flagged","explanation":"ok"}]}`,
		`{"verdicts":[{"id":"a","outcome":"not_flagged","explanation":"ok"},{"id":"b","outcome":"flagged","explanation":""}]}`,
		`{"verdicts":[]} {}`,
		`{"verdicts":[{"id":"a","outcome":"flagged","outcome":"not_flagged","explanation":"ok"},{"id":"b","outcome":"flagged","explanation":"blocked"}]}`,
	} {
		if _, err := parseVerdicts([]byte(raw), calls); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestSafeguardRejectsAmbiguousToolArguments(t *testing.T) {
	review, err := newSafeguardReview([]byte(reviewRequest(true)), config.Config{AutoModeClassifierModel: "astra"})
	if err != nil {
		t.Fatal(err)
	}
	if err := review.addCall(reviewedTool{ID: "test", Name: "Bash", Input: json.RawMessage(`{"command":"safe","command":"different"}`)}); err == nil {
		t.Fatal("accepted duplicate tool argument keys")
	}
}
