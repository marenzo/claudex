package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type cancellationBody struct {
	cancel context.CancelFunc
	body   string
	err    error
}

func (b cancellationBody) Read(p []byte) (int, error) {
	b.cancel()
	return copy(p, b.body), b.err
}

func (cancellationBody) Close() error { return nil }

type trackedResponseWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *trackedResponseWriter) WriteHeader(status int) {
	w.writes++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *trackedResponseWriter) Write(data []byte) (int, error) {
	w.writes++
	return w.ResponseRecorder.Write(data)
}

func TestHandlerCancellationBeforeStreaming(t *testing.T) {
	for _, phase := range []string{"request_body", "credentials", "credential_refresh", "upstream_headers", "upstream_error_body"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstreamCalls, refreshCalls := 0, 0
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				upstreamCalls++
				if phase == "upstream_error_body" {
					return &http.Response{StatusCode: 503, Header: make(http.Header), Body: cancellationBody{cancel: cancel, err: io.ErrUnexpectedEOF}}, nil
				}
				if phase != "upstream_headers" {
					t.Fatal("canceled request reached inference")
				}
				cancel()
				return nil, context.Canceled
			})
			s.EnableDashboard()
			if phase == "credential_refresh" {
				if err := os.WriteFile(s.Config.AuthFile, []byte(`{"type":"codex","access_token":"old","account_id":"test-account","refresh_token":"refresh-fixture","expired":"2000-01-01T00:00:00Z"}`), 0600); err != nil {
					t.Fatal(err)
				}
				s.Client.Auth.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					refreshCalls++
					cancel()
					return nil, context.Canceled
				})
			}
			var body io.Reader = strings.NewReader(`{"messages":[],"stream":true}`)
			if phase == "request_body" {
				body = cancellationBody{cancel: cancel, err: context.Canceled}
			}
			if phase == "credentials" {
				// Complete the body normally, then let Auth.Get observe cancellation.
				body = cancellationBody{cancel: cancel, body: `{"messages":[],"stream":true}`, err: io.EOF}
			}
			r := httptest.NewRequest("POST", "/v1/messages", body).WithContext(ctx)
			r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
			w := &trackedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
			s.Handler().ServeHTTP(w, r)
			if w.writes != 0 || w.Body.Len() != 0 {
				t.Fatal("wrote an error response to a canceled caller")
			}
			snapshot := s.metrics.Snapshot()
			if snapshot.Active != 0 || snapshot.Totals.Requests != 1 || len(snapshot.Recent) != 1 {
				t.Fatal("canceled request was not finalized exactly once")
			}
			record := snapshot.Recent[0]
			if record.Status != 499 || record.ErrorKind != "canceled" || record.Error == nil || record.Error.Source != "proxy" || record.Error.Type != "canceled" {
				t.Fatalf("cancellation misattributed: %+v", record)
			}
			wantUpstream, wantRefresh := 0, 0
			if phase == "upstream_headers" || phase == "upstream_error_body" {
				wantUpstream = 1
			}
			if phase == "credential_refresh" {
				wantRefresh = 1
			}
			if upstreamCalls != wantUpstream || refreshCalls != wantRefresh {
				t.Fatalf("unexpected request counts: inference=%d refresh=%d", upstreamCalls, refreshCalls)
			}
		})
	}
}

func TestChildTimeoutDoesNotBecomeCallerCancellation(t *testing.T) {
	for _, phase := range []string{"credential_refresh", "upstream_headers"} {
		t.Run(phase, func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				if phase != "upstream_headers" {
					t.Fatal("failed credential acquisition reached inference")
				}
				return nil, context.DeadlineExceeded
			})
			s.EnableDashboard()
			if phase == "credential_refresh" {
				if err := os.WriteFile(s.Config.AuthFile, []byte(`{"type":"codex","access_token":"old","account_id":"test-account","refresh_token":"refresh-fixture","expired":"2000-01-01T00:00:00Z"}`), 0600); err != nil {
					t.Fatal(err)
				}
				s.Client.Auth.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, context.DeadlineExceeded
				})
			}
			w := request(s, "/v1/messages", `{"messages":[],"stream":true}`)
			want := 502
			if phase == "credential_refresh" {
				want = 401
			}
			record := s.metrics.Snapshot().Recent[0]
			if w.Code != want || record.Status != want || record.ErrorKind == "canceled" || record.Error == nil || record.Error.Type == "canceled" {
				t.Fatalf("child timeout misattributed to caller: HTTP %d / %+v", w.Code, record)
			}
		})
	}
}

func TestCanceledHTTPQuotaErrorDoesNotSetCooldown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: cancellationBody{
			cancel: cancel, err: io.EOF,
			body: `{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`,
		}}, nil
	})
	s.EnableDashboard()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"messages":[],"stream":true}`)).WithContext(ctx)
	r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
	w := &trackedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	if got := s.Quota.Remaining("test-account", time.Now()); got > 0 {
		t.Fatalf("canceled caller set %v account cooldown", got)
	}
	if w.writes != 0 || w.Header().Get("Retry-After") != "" || s.metrics.Snapshot().Recent[0].Status != 499 {
		t.Fatal("canceled quota response was not abandoned")
	}
}
