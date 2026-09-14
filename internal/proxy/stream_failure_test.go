package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/dashboard"
	"github.com/marenzo/claudex/internal/tokens"
)

type failedStreamReader struct{ err error }

func (r failedStreamReader) Read([]byte) (int, error) { return 0, r.err }

type cancelingStreamReader struct{ cancel context.CancelFunc }

func (r cancelingStreamReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.ErrUnexpectedEOF
}

func TestClientCancellationWinsOverReaderFailure(t *testing.T) {
	// The reader can make the event channel and request context ready together.
	// Both select paths must attribute the failure to the canceled caller.
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
		record := &dashboard.Request{Started: time.Now()}
		status := (&Server{}).consume(httptest.NewRecorder(), r, cancelingStreamReader{cancel}, nil, nil, "", true, record, nil)
		cancel()
		if status != 499 || record.ErrorKind != "canceled" {
			t.Fatalf("client cancellation reported as %d / %s", status, record.ErrorKind)
		}
	}
}

func TestClientCancellationWinsOverUpstreamErrorEvent(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}`,
		`{"type":"response.failed","response":{"error":{"type":"usage_limit_reached"}}}`,
	} {
		// Cancellation and a complete error event arrive together. Neither
		// scheduling order may write a failure response or enter quota cooldown.
		for range 20 {
			ctx, cancel := context.WithCancel(context.Background())
			r := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
			record := &dashboard.Request{Started: time.Now()}
			w := &trackedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
			s := &Server{}
			body := cancellationBody{cancel: cancel, body: sse(event), err: io.EOF}
			status := s.consume(w, r, body, nil, nil, "test-account", true, record, nil)
			cancel()
			if status != 499 || record.ErrorKind != "canceled" || record.Error != nil || w.writes != 0 {
				t.Fatalf("canceled error event became a failure: %d / %+v", status, record)
			}
			if s.Quota.Remaining("test-account", time.Now()) > 0 {
				t.Fatal("canceled error event established quota cooldown")
			}
		}
	}
}

func TestStreamFailureDiagnosticsPreservePrivacyAndPartialFailure(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, kind string
		err                error
		status             int
	}{
		{"before_output", sse(created), "http2_stream_internal_error", errors.New("stream error: stream ID 7; INTERNAL_ERROR; received from peer"), 502},
		{"after_output", sse(created, `{"type":"response.output_text.delta","delta":"PRIVATE_RESPONSE"}`), "transport_read_error", errors.New("PRIVATE_TRANSPORT_ERROR"), 200},
		{"clean_eof", sse(created), "incomplete_stream", io.EOF, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			calls := 0
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				body := io.MultiReader(strings.NewReader(tc.prefix), failedStreamReader{tc.err})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(body)}, nil
			})
			s.EnableDashboard()
			w := request(s, "/v1/messages", `{"stream":true,"messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.kind) || strings.Contains(w.Body.String(), "event: message_stop") {
				t.Fatalf("failure was not surfaced: %d %s", w.Code, w.Body)
			}
			if calls != 1 {
				t.Fatalf("interrupted request was replayed %d times", calls)
			}
			snapshot := s.metrics.Snapshot()
			if len(snapshot.Recent) != 1 || snapshot.Recent[0].Status != 502 || snapshot.Recent[0].ErrorKind != tc.kind {
				t.Fatalf("missing failure metrics: %+v", snapshot)
			}
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "PRIVATE_") || strings.Contains(string(encoded), "PRIVATE_PROMPT") || strings.Contains(string(encoded), "PRIVATE_RESPONSE") || strings.Contains(w.Body.String(), "PRIVATE_TRANSPORT_ERROR") {
				t.Fatal("private payload or transport error leaked into diagnostics")
			}
			if tc.name == "after_output" && snapshot.Recent[0].Error.Message != "PRIVATE_TRANSPORT_ERROR" {
				t.Fatal("authenticated dashboard is missing the requested transport error excerpt")
			}
			if !strings.Contains(logs.String(), `"kind":"`+tc.kind+`"`) || !strings.Contains(logs.String(), `"received_bytes":`) {
				t.Fatal("missing structured stream diagnostics")
			}
		})
	}
}

type blockingReader struct{ ctx context.Context }

func (r blockingReader) Read([]byte) (int, error) { <-r.ctx.Done(); return 0, r.ctx.Err() }

func TestStalledUpstreamStreamFailsInsteadOfHanging(t *testing.T) {
	previous := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
	record := &dashboard.Request{Started: time.Now()}
	w := httptest.NewRecorder()
	done := make(chan int, 1)
	go func() { done <- (&Server{}).consume(w, r, blockingReader{ctx: ctx}, nil, nil, "", true, record, nil) }()
	select {
	case status := <-done:
		if status != 502 || record.ErrorKind != "upstream_stalled" {
			t.Fatalf("stalled stream reported as %d / %s", status, record.ErrorKind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled upstream stream hung the request")
	}
	if !strings.Contains(w.Body.String(), "stalled") {
		t.Fatalf("client not told about the stall: %s", w.Body.String())
	}
}

func TestAsyncInputTokenCountMatchesSynchronousCount(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"hello there, count these words"}]}`)
	want, err := tokens.CountClaudeInputTokens(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, async := range []func() (int64, error){nil, countInputTokensAsync(raw)} {
		got, err := inputTokenCount(async, raw)
		if err != nil || got != want {
			t.Fatalf("got %d, %v; want %d", got, err, want)
		}
	}
}
