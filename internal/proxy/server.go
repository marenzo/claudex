package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/dashboard"
	"github.com/marenzo/claudex/internal/quota"
	"github.com/marenzo/claudex/internal/tokens"
	"github.com/tidwall/gjson"
)

const maxRequestBytes = 64 * 1024 * 1024

type Server struct {
	Config  config.Config
	Client  *codex.Client
	Quota   quota.State
	key     []byte
	version string
	metrics *dashboard.Store
}

func New(cfg config.Config, client *codex.Client, version string) (*Server, error) {
	key, errRead := os.ReadFile(cfg.ClientKeyFile)
	if errRead != nil {
		return nil, fmt.Errorf("read local client key: %w", errRead)
	}
	key = []byte(strings.TrimSpace(string(key)))
	if len(key) < 24 {
		return nil, fmt.Errorf("local client key must contain at least 24 characters")
	}
	return &Server{Config: cfg, Client: client, key: key, version: version}, nil
}

// EnableDashboard must be called before serving requests. Disabled by default.
func (s *Server) EnableDashboard() { s.metrics = dashboard.New() }

func (s *Server) RunRecovery(ctx context.Context) {
	s.Quota.Run(ctx, s.Client.QuotaAvailable, func() {
		slog.Info("Codex quota available again; cleared stale cooldown")
		if s.metrics != nil {
			s.metrics.Recovered()
		}
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.metrics != nil {
		mux.HandleFunc("GET /dashboard/api", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, s.metrics.Snapshot())
		})
	}
	mux.HandleFunc("POST /v1/messages", s.messages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.countTokens)
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("GET /v1/models/{id}", s.model)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"product": "claudex", "status": "ok", "version": s.version, "model": s.Config.Model})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{Status: 404, Type: "not_found_error", Message: "Unsupported endpoint"})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics != nil && r.Method == http.MethodGet && dashboard.IsAssetPath(r.URL.Path) {
			dashboard.Static(w, r)
			return
		}
		// Liveness probes need no key: HEAD /api/hello for Claude Code, and
		// GET /healthz, served by the mux, for Docker, launchd, and claudex status.
		if r.Method == http.MethodHead && r.URL.Path == "/api/hello" {
			w.WriteHeader(200)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		if !s.authorized(r) {
			slog.Info("request rejected", "path", r.URL.Path, "reason", "invalid_client_key")
			writeError(w, &apiError{Status: 401, Type: "authentication_error", Message: "Invalid client key. Use the value in client_key_file, not a Codex token."})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// authorized accepts the client key as a Bearer token, with the scheme matched
// regardless of case, or as X-Api-Key. Each header is checked on its own.
func (s *Server) authorized(r *http.Request) bool {
	matches := func(candidate string) bool {
		return candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), s.key) == 1
	}
	if scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") && matches(strings.TrimSpace(token)) {
		return true
	}
	return matches(r.Header.Get("X-Api-Key"))
}

func (s *Server) modelInfo(model config.Model) map[string]any {
	return map[string]any{
		"id": model.ID, "type": "model", "object": "model", "owned_by": "openai", "display_name": model.Name,
		"description": "Codex subscription; select with --model " + model.Alias,
		"created_at":  "2026-09-14T00:00:00Z", "max_input_tokens": s.Config.ContextWindow, "max_output_tokens": 128000,
		"context_length": s.Config.ContextWindow, "max_completion_tokens": 128000,
		"capabilities": map[string]any{
			"thinking": map[string]any{"supported": true},
			"effort":   map[string]any{"supported": true, "levels": model.Efforts},
		},
	}
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	catalog := config.Models()
	data := make([]any, 0, len(catalog))
	for _, model := range catalog {
		data = append(data, s.modelInfo(model))
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data, "has_more": false, "first_id": catalog[0].ID, "last_id": catalog[len(catalog)-1].ID})
}

func (s *Server) model(w http.ResponseWriter, r *http.Request) {
	model, ok := config.FindModel(r.PathValue("id"))
	if !ok {
		writeError(w, &apiError{Status: 404, Type: "not_found_error", Message: "Unknown model"})
		return
	}
	writeJSON(w, 200, s.modelInfo(model))
}

// bodyReadTimeout bounds how long a client may take to send the request body, so
// a stalled client cannot hold a connection open. Tests shorten it.
var bodyReadTimeout = 2 * time.Minute

func readRequest(w http.ResponseWriter, r *http.Request) ([]byte, *apiError) {
	controller := http.NewResponseController(w)
	// Writers without deadline support, such as test recorders, return an error
	// here; the limit then does not apply.
	_ = controller.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err == nil {
		// Clear the deadline so it cannot interrupt a long streamed response.
		_ = controller.SetReadDeadline(time.Time{})
		return raw, nil
	}
	// The rest of the body will never be read. Close the connection after the
	// error response; otherwise the server first waits to drain the body.
	w.Header().Set("Connection", "close")
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, &apiError{Status: 413, Type: "invalid_request_error", Message: "Request body exceeds size limit"}
	}
	return nil, &apiError{Status: 400, Type: "invalid_request_error", Message: "Request body could not be read"}
}

func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	raw, errRead := readRequest(w, r)
	if errRead != nil {
		writeError(w, errRead)
		return
	}
	if _, _, errPrepare := prepareRequest(raw, s.Config); errPrepare != nil {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: errPrepare.Error()})
		return
	}
	count, errCount := tokens.CountClaudeInputTokens(raw)
	if errCount != nil {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: errCount.Error()})
		return
	}
	writeJSON(w, 200, map[string]int64{"input_tokens": count})
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := 200
	errorType := ""
	model := s.Config.Model
	record := dashboard.Request{Started: started, Model: model}
	errorSecrets := []string{string(s.key), s.Config.AuthFile, s.Config.ClientKeyFile}
	if s.metrics != nil {
		s.metrics.Begin()
	}
	defer func() {
		record.Status, record.Model, record.DurationMS = status, model, time.Since(started).Milliseconds()
		if s.metrics != nil {
			if status == 499 {
				record.ErrorKind = "canceled"
				record.Error = &dashboard.ErrorDetails{Source: "proxy", Type: "canceled", Message: "The client canceled or disconnected before completion."}
			}
			record.Error = redactErrorDetails(record.Error, errorSecrets)
			s.metrics.Finish(record)
		}
		attrs := []any{"status", status, "duration_ms", record.DurationMS, "model", model}
		if status >= 400 {
			if errorType == "" && record.Error != nil {
				errorType = record.Error.Type
			}
			if errorType != "" {
				attrs = append(attrs, "error_type", errorType)
			}
			if record.ErrorKind != "" {
				attrs = append(attrs, "error_kind", record.ErrorKind)
			}
		}
		slog.Info("messages", attrs...)
	}()
	fail := func(e *apiError) {
		// A canceled caller can surface as a body, credential, or transport
		// failure. Child-operation deadlines do not cancel the caller's context.
		if r.Context().Err() != nil {
			status = 499
			record.ErrorKind = "canceled"
			record.Error = nil
			return
		}
		status = e.Status
		errorType = e.Type
		recordAPIError(&record, e)
		writeError(w, e)
	}
	raw, errRead := readRequest(w, r)
	if errRead != nil {
		fail(errRead)
		return
	}
	body, session, errPrepare := prepareRequest(raw, s.Config)
	if errPrepare != nil {
		fail(&apiError{Status: 400, Type: "invalid_request_error", Message: errPrepare.Error()})
		return
	}
	model = gjson.GetBytes(body, "model").String()
	record.Effort = gjson.GetBytes(body, "reasoning.effort").String()
	// Count locally in parallel with sign-in and the upstream request, so the
	// message_start estimate adds no delay before the first streamed byte.
	var inputTokens func() (int64, error)
	if gjson.GetBytes(raw, "stream").Bool() {
		inputTokens = countInputTokensAsync(raw)
	}
	credential, errAuth := s.Client.Auth.Get(r.Context())
	errorSecrets = append(errorSecrets, credential.AccessToken, credential.RefreshToken, credential.AccountID)
	if errAuth != nil {
		// Keep file paths and token endpoint details out of the client response.
		// The service log and the dashboard keep the detail.
		message := "Codex sign-in could not be used or refreshed. Run claudex -login if this continues."
		if errors.Is(errAuth, auth.ErrNotSignedIn) {
			message = "Not signed in to Codex. Run claudex -login, or claudex service login for the macOS service."
		}
		if r.Context().Err() == nil {
			slog.Warn("Codex sign-in unavailable", "error", errAuth.Error())
		}
		fail(&apiError{Status: 401, Type: "authentication_error", Message: message,
			Details: &dashboard.ErrorDetails{Source: "proxy", Type: fmt.Sprintf("%T", errAuth), Message: errAuth.Error()}})
		return
	}
	if remaining := s.Quota.Remaining(credential.AccountID, time.Now()); remaining > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(min(60, max(1, int(remaining.Seconds())))))
		fail(&apiError{Status: 429, Type: "rate_limit_error", Message: "Codex quota is unavailable. The proxy checks for recovery automatically every minute."})
		return
	}
	response, credential, errUpstream := s.Client.Responses(r.Context(), credential, body, session)
	errorSecrets = append(errorSecrets, credential.AccessToken, credential.RefreshToken, credential.AccountID)
	if errors.Is(errUpstream, codex.ErrAccountChanged) {
		fail(&apiError{Status: 401, Type: "authentication_error", Message: "The Codex sign-in switched to a different account during this request. Retry the request."})
		return
	}
	if errUpstream != nil {
		record.ErrorKind = streamErrorKind(errUpstream)
		fail(&apiError{Status: 502, Type: "api_error", Message: "Could not connect to Codex: " + errUpstream.Error(),
			Details: &dashboard.ErrorDetails{Source: "transport", Type: fmt.Sprintf("%T", errUpstream), Message: errUpstream.Error()}})
		return
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			slog.Debug("Codex response close failed")
		}
	}()
	if response.StatusCode != 200 {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		e := parseUpstreamError(response.StatusCode, errorBody, time.Now())
		if r.Context().Err() != nil {
			fail(e)
			return
		}
		if !e.QuotaUntil.IsZero() {
			s.Quota.Limited(credential.AccountID, e.QuotaUntil)
			w.Header().Set("Retry-After", "60")
		}
		fail(e)
		return
	}
	stream := gjson.GetBytes(raw, "stream").Bool()
	status = s.consume(w, r, response.Body, raw, body, credential.AccountID, stream, &record, inputTokens)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	encoded, errMarshal := json.Marshal(data)
	if errMarshal != nil {
		writeError(w, &apiError{Status: 500, Type: "api_error", Message: "Could not encode response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, errWrite := w.Write(encoded); errWrite != nil {
		slog.Debug("downstream JSON response write failed")
	}
}

func writeError(w http.ResponseWriter, e *apiError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	if _, errWrite := w.Write(e.body()); errWrite != nil {
		slog.Debug("downstream error response write failed")
	}
}
