package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/dashboard"
	"github.com/tidwall/gjson"
)

type apiError struct {
	Status     int
	Type       string
	Message    string
	QuotaUntil time.Time
	Details    *dashboard.ErrorDetails
}

func (e *apiError) body() []byte {
	data, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": e.Type, "message": e.Message}})
	return data
}

func parseUpstreamError(status int, data []byte, now time.Time) *apiError {
	upstreamStatus := status
	if status < 400 {
		status = 502
	}
	root := gjson.ParseBytes(data)
	errNode := root.Get("error")
	if !errNode.IsObject() {
		errNode = root.Get("response.error")
	}
	if !errNode.IsObject() {
		errNode = root
	}
	kind, code := errNode.Get("type").String(), errNode.Get("code").String()
	message := errNode.Get("message").String()
	detailMessage := message
	if detailMessage == "" && !gjson.ValidBytes(data) {
		detailMessage = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = "Codex returned " + http.StatusText(status)
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	e := &apiError{Status: status, Type: "api_error", Message: message,
		Details: &dashboard.ErrorDetails{Source: "upstream", Type: kind, Code: code, Message: detailMessage, Status: upstreamStatus}}
	if kind == "usage_limit_reached" || code == "usage_limit_reached" {
		e.Status, e.Type = 429, "rate_limit_error"
		e.QuotaUntil = now.Add(time.Minute)
		if epoch := errNode.Get("resets_at").Int(); epoch > now.Unix() && epoch-now.Unix() < 366*24*3600 {
			e.QuotaUntil = time.Unix(epoch, 0)
		} else if seconds := errNode.Get("resets_in_seconds").Int(); seconds > 0 && seconds < 366*24*3600 {
			e.QuotaUntil = now.Add(time.Duration(seconds) * time.Second)
		}
		return e
	}
	if kind == "rate_limit_error" || code == "rate_limit_exceeded" {
		e.Status = 429
	} else if code == "context_length_exceeded" || code == "context_too_large" || strings.Contains(strings.ToLower(message), "context length") {
		e.Status = 400
	} else if kind == "overloaded_error" || kind == "service_unavailable_error" || code == "server_is_overloaded" || status == 503 || status == 529 {
		// Codex can report overload inside an HTTP 200 SSE stream. Translate
		// the error semantics to Claude's contract, retaining the original
		// upstream status/type/code in Details for diagnosis.
		e.Status = 529
		if errNode.Get("message").String() == "" {
			e.Message = "Codex is temporarily overloaded. Please try again later."
		}
	}
	switch e.Status {
	case 400, 413, 422:
		e.Type = "invalid_request_error"
	case 401:
		e.Type = "authentication_error"
	case 403:
		e.Type = "permission_error"
	case 404:
		e.Type = "not_found_error"
	case 429:
		e.Type = "rate_limit_error"
	case 529:
		e.Type = "overloaded_error"
	}
	return e
}
