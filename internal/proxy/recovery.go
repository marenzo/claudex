package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/metrics"
	"github.com/tidwall/gjson"
)

const upstreamAttempts = 2

var streamPing = []byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")

// Retry only recognized transport failures, never arbitrary errors, HTTP errors,
// refusals, malformed responses or permission decisions.
func transientTransport(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	switch streamErrorKind(err) {
	case "read_timeout", "unexpected_eof", "connection_reset",
		"http2_stream_internal_error", "http2_stream_refused_stream", "http2_stream_cancel",
		"http2_connection_internal_error", "http2_connection_protocol_error",
		"http2_goaway_no_error", "http2_goaway_internal_error":
		return true
	}
	return false
}

func waitForRetry(ctx context.Context) error {
	// Jitter prevents concurrent streams on one failed connection from retrying
	// together. The caller's cancellation/deadline bounds this wait too.
	timer := time.NewTimer(time.Duration(250+rand.IntN(250)) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Keep headers and keep-alive state across attempts. Any message event, even an
// empty message_start, commits a streaming reply and prevents generation replay.
type generationWriter struct {
	http.ResponseWriter
	stream    bool
	started   bool
	committed bool
}

func (w *generationWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *generationWriter) WriteHeader(status int) {
	if !w.started {
		w.started = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *generationWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if !bytes.Equal(data, streamPing) {
		w.committed = true
	}
	return w.ResponseWriter.Write(data)
}
func (w *generationWriter) fail(e *apiError) {
	if w.stream && w.started {
		_, _ = w.Write(append(append([]byte("event: error\ndata: "), e.body()...), '\n', '\n'))
		_ = http.NewResponseController(w).Flush()
		return
	}
	writeError(w, e)
}

type streamRecovery struct {
	writer  *generationWriter
	allowed bool
	err     error
}

func (s *Server) generate(w http.ResponseWriter, r *http.Request, raw, body []byte, session string, cfg config.Config, credential auth.Credential, record *metrics.Request, inputTokens func() (int64, error), review *safeguardReview, secrets *[]string) int {
	delivery := &generationWriter{ResponseWriter: w, stream: gjson.GetBytes(raw, "stream").Bool()}
	fail := func(e *apiError) int {
		if r.Context().Err() != nil {
			record.ErrorKind = "canceled"
			return 499
		}
		recordAPIError(record, e)
		delivery.fail(e)
		return e.Status
	}
	account := credential.AccountID
	var earlierUsage metrics.Usage
	earlierReported := false
	defer func() {
		if earlierReported {
			record.Usage.Input += earlierUsage.Input
			record.Usage.Cached += earlierUsage.Cached
			record.Usage.Output += earlierUsage.Output
			record.Usage.Reasoning += earlierUsage.Reasoning
			record.UsageReported = true
		}
	}()
	for attempt := range upstreamAttempts {
		if attempt > 0 {
			var err error
			credential, err = s.Client.Auth.Get(r.Context())
			*secrets = append(*secrets, credential.AccessToken, credential.RefreshToken, credential.AccountID)
			if err != nil || credential.AccountID != account {
				return fail(&apiError{Status: 401, Type: "authentication_error", Message: "Codex sign-in changed or became unavailable before retry. Retry after checking claudex ctl."})
			}
			// Discard failed-attempt translator, tool buffers and timing estimates.
			// Rebuild from the same validated request and configuration snapshot.
			review, err = newSafeguardReview(raw, cfg)
			if err != nil {
				return fail(&apiError{Status: 400, Type: "invalid_request_error", Message: err.Error()})
			}
			if review != nil {
				review.parentTraceID = record.TraceID
			}
			if record.UsageReported {
				earlierUsage, earlierReported = record.Usage, true
			}
			record.UpstreamFirstOutputMS = nil
			record.Usage, record.UsageReported = metrics.Usage{}, false
		}
		ctx, cancel := context.WithCancel(r.Context())
		record.UpstreamAttempts++
		response, updated, err := s.Client.Responses(ctx, credential, body, session)
		credential = updated
		*secrets = append(*secrets, credential.AccessToken, credential.RefreshToken, credential.AccountID)
		recovery := &streamRecovery{writer: delivery, allowed: attempt+1 < upstreamAttempts}
		if err == nil && response.StatusCode == http.StatusOK {
			status := s.consumeAttempt(delivery, r.WithContext(ctx), response.Body, raw, body, account, delivery.stream, record, inputTokens, review, recovery)
			_ = response.Body.Close()
			cancel()
			if recovery.err == nil {
				return status
			}
			err = recovery.err
		} else if err == nil {
			errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
			_ = response.Body.Close()
			cancel()
			e := parseUpstreamError(response.StatusCode, errorBody, time.Now())
			if r.Context().Err() == nil && !e.QuotaUntil.IsZero() {
				s.Quota.Limited(account, e.QuotaUntil)
				delivery.Header().Set("Retry-After", "60")
			}
			return fail(e)
		} else {
			cancel()
		}
		if errors.Is(err, codex.ErrAccountChanged) {
			return fail(&apiError{Status: 401, Type: "authentication_error", Message: "The Codex sign-in switched to a different account during this request. Retry the request."})
		}
		if !recovery.allowed || delivery.committed || !transientTransport(err) {
			record.ErrorKind = streamErrorKind(err)
			return fail(&apiError{Status: 502, Type: "api_error", Message: "Could not complete the Codex response (" + record.ErrorKind + ").",
				Details: &metrics.ErrorDetails{Source: "transport", Type: fmt.Sprintf("%T", err), Message: err.Error()}})
		}
		if r.Context().Err() != nil {
			record.ErrorKind = "canceled"
			return 499
		}
		slog.Info("upstream retry", "trace_id", record.TraceID, "role", metrics.Generation, "kind", streamErrorKind(err), "attempt", attempt+2)
		if waitForRetry(r.Context()) != nil {
			record.ErrorKind = "canceled"
			return 499
		}
	}
	panic("unreachable generation attempt")
}
