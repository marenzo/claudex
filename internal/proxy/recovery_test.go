package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/metrics"
	"github.com/tidwall/gjson"
)

func retryTimeout() error {
	return &net.OpError{Op: "read", Err: syscall.ETIMEDOUT}
}

func interrupted(prefix string, err error) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.MultiReader(strings.NewReader(prefix), failedStreamReader{err}))}
}

func TestGenerationRecoversBeforeDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, prefix            string
		stream, review, connect bool
	}{
		{name: "connection", stream: true, connect: true},
		{name: "handshake", stream: true, prefix: sse(created)},
		{name: "json_partial_text", prefix: sse(created, `{"type":"response.output_text.delta","delta":"DISCARDED_PARTIAL","output_index":0,"content_index":0}`)},
		{name: "held_tool", stream: true, review: true, prefix: sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+strings.ReplaceAll(reviewTool, "toolu_test", "toolu_discarded")+`}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generation, reviews := 0, 0
			var firstBody []byte
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				if gjson.GetBytes(body, "text.format.name").String() == "claudex_permission_verdicts" {
					reviews++
					if strings.Contains(string(body), "toolu_discarded") {
						t.Error("discarded tool entered review")
					}
					return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"not_flagged","explanation":"fixture"}]}`)), nil
				}
				generation++
				if generation == 1 {
					firstBody = body
					if tc.connect {
						return nil, retryTimeout()
					}
					return interrupted(tc.prefix, retryTimeout()), nil
				}
				if !bytes.Equal(body, firstBody) {
					t.Error("retry changed request body")
				}
				if tc.review {
					return upstreamResponse(200, toolResponse()), nil
				}
				return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
			})
			body := `{"stream":` + map[bool]string{true: "true", false: "false"}[tc.stream] + `,"messages":[{"role":"user","content":"go"}]}`
			if tc.review {
				s.Config.AutoModeClassifierModel = "sol"
				body = reviewRequest(tc.stream)
			}
			w := request(s, "/v1/messages", body)
			if w.Code != 200 || generation != 2 || strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "toolu_discarded") || strings.Contains(w.Body.String(), "DISCARDED_PARTIAL") {
				t.Fatalf("recovery failed: calls=%d status=%d body=%s", generation, w.Code, w.Body)
			}
			if tc.stream && (strings.Count(w.Body.String(), "event: message_start\n") != 1 || strings.Count(w.Body.String(), "event: message_stop\n") != 1) {
				t.Fatal("duplicated or missing message boundaries")
			}
			if tc.review && reviews != 1 {
				t.Fatalf("review calls=%d", reviews)
			}
			for _, row := range s.metrics.Snapshot().Recent {
				if row.Role == metrics.Generation && (row.UpstreamAttempts != 2 || row.Status != 200 || row.ErrorKind != "") {
					t.Fatalf("wrong recovered metrics: %+v", row)
				}
			}
		})
	}
}

func TestGenerationNeverReplaysDeliveredContent(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.output_text.delta","delta":"PARTIAL","output_index":0,"content_index":0}`,
		`{"type":"response.output_text.delta","delta":"","output_index":0,"content_index":0}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + reviewTool + `}`,
	} {
		calls := 0
		s := fixture(t, func(*http.Request) (*http.Response, error) {
			calls++
			return interrupted(sse(created, event), retryTimeout()), nil
		})
		w := request(s, "/v1/messages", `{"stream":true,"messages":[]}`)
		if calls != 1 || w.Code != 200 || !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
			t.Fatalf("replayed delivered message or hid failure: calls=%d %d %s", calls, w.Code, w.Body)
		}
	}
}

func TestClassifierRecoversWithoutRegeneratingOrReusingPartialVerdict(t *testing.T) {
	for _, outcome := range []string{"flagged", "not_flagged"} {
		for _, stream := range []bool{false, true} {
			t.Run(outcome+map[bool]string{true: "_stream", false: "_json"}[stream], func(t *testing.T) {
				generation, reviews := 0, 0
				var first []byte
				var deadline time.Time
				s := fixture(t, func(r *http.Request) (*http.Response, error) {
					body, _ := io.ReadAll(r.Body)
					if gjson.GetBytes(body, "text.format.name").String() == "" {
						generation++
						return upstreamResponse(200, toolResponse()), nil
					}
					reviews++
					if reviews == 1 {
						first = body
						deadline, _ = r.Context().Deadline()
						return interrupted(sse(`{"type":"response.output_text.delta","delta":"{\"verdicts\":[{\"id\":\"DISCARDED_VERDICT"}`), retryTimeout()), nil
					}
					current, _ := r.Context().Deadline()
					if !bytes.Equal(body, first) || !current.Equal(deadline) {
						t.Error("review retry changed evidence or reset deadline")
					}
					return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"`+outcome+`","explanation":"fixture"}]}`)), nil
				})
				s.Config.AutoModeClassifierModel = "sol"
				w := request(s, "/v1/messages", reviewRequest(stream))
				if w.Code != 200 || generation != 1 || reviews != 2 || !strings.Contains(w.Body.String(), `"outcome":"`+outcome+`"`) || strings.Contains(w.Body.String(), "DISCARDED_VERDICT") {
					t.Fatalf("unsafe or failed recovery: gen=%d reviews=%d %d %s", generation, reviews, w.Code, w.Body)
				}
				for _, row := range s.metrics.Snapshot().Recent {
					if row.Role == metrics.Review && (row.UpstreamAttempts != 2 || row.ErrorKind != "" || row.Status != 200) {
						t.Fatalf("bad review metrics: %+v", row)
					}
				}
			})
		}
	}
}

func TestReviewExhaustionFailsClosedWithPrivateDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	reviews := 0
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if gjson.GetBytes(body, "text.format.name").String() == "" {
			return upstreamResponse(200, toolResponse()), nil
		}
		reviews++
		return interrupted("", &net.DNSError{Err: "PRIVATE_DETAIL", Name: "PRIVATE_HOST", IsTimeout: true}), nil
	})
	s.Config.AutoModeClassifierModel = "sol"
	w := request(s, "/v1/messages", reviewRequest(true))
	if reviews != 2 || w.Code != 502 || strings.Contains(w.Body.String(), "toolu_test") || !strings.Contains(w.Body.String(), "classifier_read_timeout") {
		t.Fatalf("failure did not close safely: %d %s", w.Code, w.Body)
	}
	for _, row := range s.metrics.Snapshot().Recent {
		if row.ErrorKind != "classifier_read_timeout" || row.Error == nil {
			t.Fatalf("lost root cause: %+v", row)
		}
	}
	data, _ := json.Marshal(s.metrics.Snapshot())
	if strings.Contains(logs.String()+string(data)+w.Body.String(), "PRIVATE_") {
		t.Fatal("private transport details leaked")
	}
}

func TestReviewFailuresAndDenialsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
	}{
		{"malformed", sse("not json"), "classifier_invalid_response", 200},
		{"invalid_verdict", classifierResponse(`{"verdicts":[]}`), "classifier_invalid_verdict", 200},
		{"refused", sse(`{"type":"response.refusal.delta","delta":"PRIVATE_REFUSAL"}`), "classifier_refused", 200},
		{"http", `{"error":"PRIVATE_BODY"}`, "classifier_http", 503},
		{"upstream", sse(`{"type":"response.failed","response":{"error":{"message":"PRIVATE_BODY"}}}`), "classifier_upstream_response", 200},
		{"tool", sse(`{"type":"response.function_call_arguments.delta","delta":"PRIVATE_TOOL"}`), "classifier_unexpected_action", 200},
		{"limit", strings.Repeat(":", classifierOutputLimit+10), "classifier_output_limit", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviews := 0
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				if gjson.GetBytes(body, "text.format.name").String() == "" {
					return upstreamResponse(200, toolResponse()), nil
				}
				reviews++
				return upstreamResponse(tc.status, tc.body), nil
			})
			s.Config.AutoModeClassifierModel = "sol"
			w := request(s, "/v1/messages", reviewRequest(true))
			if w.Code != 502 || reviews != 1 || strings.Contains(w.Body.String(), "toolu_test") || !strings.Contains(w.Body.String(), tc.kind) || strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatalf("unexpected retry/release/diagnostic: calls=%d %d %s", reviews, w.Code, w.Body)
			}
		})
	}
}

func TestRetryAccountChangesStopBeforeAnotherRequest(t *testing.T) {
	for _, review := range []bool{false, true} {
		calls := 0
		var s *Server
		s = fixture(t, func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			if review && gjson.GetBytes(body, "text.format.name").String() == "" {
				return upstreamResponse(200, toolResponse()), nil
			}
			calls++
			if err := os.WriteFile(s.Config.AuthFile, []byte(`{"type":"codex","access_token":"changed-fixture","account_id":"changed-account"}`), 0600); err != nil {
				t.Fatal(err)
			}
			return nil, retryTimeout()
		})
		body := `{"messages":[]}`
		if review {
			s.Config.AutoModeClassifierModel = "sol"
			body = reviewRequest(true)
		}
		w := request(s, "/v1/messages", body)
		if calls != 1 || w.Code < 400 || strings.Contains(w.Body.String(), "toolu_test") {
			t.Fatalf("account change crossed retry boundary: %d %s", w.Code, w.Body)
		}
	}
}

func TestRetryWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := waitForRetry(ctx); !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("retry ignored cancellation: %v", err)
	}
}

func TestRetryPreservesPingFramingAndFinalError(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected outbound request"); return nil, nil })
	base := httptest.NewRecorder()
	writer := &generationWriter{ResponseWriter: base, stream: true}
	_, _ = writer.Write(streamPing)
	record := &metrics.Request{Started: time.Now()}
	recovery := &streamRecovery{writer: writer, allowed: true}
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	status := s.consumeAttempt(writer, r, failedStreamReader{retryTimeout()}, nil, nil, "", true, record, nil, nil, recovery)
	if status != 502 || recovery.err == nil || writer.committed || base.Body.String() != string(streamPing) {
		t.Fatal("ping incorrectly committed response or retry wrote error")
	}
	writer.fail(&apiError{Status: 502, Type: "api_error", Message: "final failure"})
	if base.Code != 200 || strings.Count(base.Body.String(), "event: error") != 1 || !strings.Contains(base.Body.String(), "final failure") {
		t.Fatalf("lost SSE framing: %d %s", base.Code, base.Body)
	}
}

func TestConcurrentTransportFailuresRecoverIndependently(t *testing.T) {
	const clients = 8
	var mu sync.Mutex
	counts := make(map[string]int)
	var arrived sync.WaitGroup
	arrived.Add(clients)
	ready := make(chan struct{})
	go func() { arrived.Wait(); close(ready) }()
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		id := r.Header.Get("Session_id")
		counts[id]++
		attempt := counts[id]
		mu.Unlock()
		if attempt == 1 {
			arrived.Done()
			<-ready
			return interrupted(sse(created), retryTimeout()), nil
		}
		return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
	})
	var done sync.WaitGroup
	for i := range clients {
		done.Go(func() {
			w := request(s, "/v1/messages", fmt.Sprintf(`{"stream":true,"messages":[],"metadata":{"user_id":"parallel-fixture-%d"}}`, i))
			if w.Code != 200 || strings.Count(w.Body.String(), "event: message_start\n") != 1 || strings.Count(w.Body.String(), "event: message_stop\n") != 1 {
				t.Errorf("concurrent recovery failed: %d %s", w.Code, w.Body)
			}
		})
	}
	done.Wait()
	if len(counts) != clients {
		t.Fatalf("shared request identity: %v", counts)
	}
	for _, n := range counts {
		if n != 2 {
			t.Fatalf("unbounded attempts: %v", counts)
		}
	}
	snapshot := s.metrics.Snapshot()
	if snapshot.Totals.Requests != clients || snapshot.Totals.Errors != 0 {
		t.Fatalf("incorrect concurrent totals: %+v", snapshot.Totals)
	}
	for _, row := range snapshot.Recent {
		if row.UpstreamAttempts != 2 {
			t.Fatal("lost recovery attribution")
		}
	}
}

func TestGenerationCancellationStopsRetryBeforeAnotherRequest(t *testing.T) {
	calls := 0
	s := fixture(t, func(*http.Request) (*http.Response, error) { calls++; return nil, retryTimeout() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"messages":[]}`)).WithContext(ctx)
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w := &trackedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	if calls != 1 || w.writes != 0 || s.metrics.Snapshot().Recent[0].Status != 499 {
		t.Fatalf("cancellation retried or wrote an error: calls=%d writes=%d", calls, w.writes)
	}
}

func TestGenerationRetainsReportedUsageFromInterruptedAttempt(t *testing.T) {
	calls := 0
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return interrupted(sse(created, `{"type":"response.in_progress","response":{"usage":{"input_tokens":100,"output_tokens":5}}}`), retryTimeout()), nil
		}
		return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
	})
	w := request(s, "/v1/messages", `{"messages":[],"stream":true}`)
	row := s.metrics.Snapshot().Recent[0]
	if w.Code != 200 || calls != 2 || !row.UsageReported || row.Usage.Input != 130 || row.Usage.Output != 7 {
		t.Fatalf("lost reported attempt usage: calls=%d status=%d usage=%+v", calls, w.Code, row.Usage)
	}
}
