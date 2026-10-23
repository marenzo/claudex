package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/metrics"
)

func TestReviewedOutputTimingBeginsAfterPermissionDecision(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			calls := 0
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return upstreamResponse(200, sse(created,
						`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"toolu_test","name":"Bash","arguments":""}}`,
						`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"command\":\"printf SAFE_PROBE\"}"}`,
						`{"type":"response.output_item.done","output_index":0,"item":`+reviewTool+`}`, completed)), nil
				}
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"not_flagged","explanation":"fixture"}]}`)), nil
			})
			s.Config.AutoModeClassifierModel = "sol"
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reviewRequest(stream))).WithContext(ctx)
			r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
			go func() { defer close(done); s.Handler().ServeHTTP(w, r) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("review never started")
			}
			if strings.Contains(w.Body.String(), "content_block_start") {
				t.Fatal("tool released before review completed")
			}
			time.Sleep(25 * time.Millisecond)
			close(release)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("response never completed")
			}
			if w.Code != 200 {
				t.Fatal("reviewed response failed", w.Code)
			}
			snap := s.metrics.Snapshot()
			generation, review := snap.Recent[0], snap.Recent[1]
			if generation.Role != metrics.Generation || review.Role != metrics.Review || review.ParentTraceID != generation.TraceID || review.TraceID == generation.TraceID {
				t.Fatal("generation/review links missing")
			}
			if generation.UpstreamFirstOutputMS == nil || generation.ClientFirstOutputMS == nil || generation.ReviewMS == nil {
				t.Fatal("missing timeline stages", generation)
			}
			if *generation.ClientFirstOutputMS-*generation.UpstreamFirstOutputMS < 20 || *generation.ReviewMS < 20 {
				t.Fatal("review delay hidden from client timing", generation)
			}
			if review.ClientFirstOutputMS != nil || review.ToolCalls != 1 || snap.Roles[metrics.Generation].Totals.Requests != 1 || snap.Roles[metrics.Review].Totals.Requests != 1 {
				t.Fatal("review mixed into client response metrics")
			}
		})
	}
}

func TestClientOutputIgnoresPingsAndUsage(t *testing.T) {
	for _, data := range []string{
		"data: {\"type\":\"ping\"}\n\n",
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n",
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}\n\n",
	} {
		if hasClientOutput([]byte(data)) {
			t.Fatal("non-output counted as client output")
		}
	}
}
