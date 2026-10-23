package proxy

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/marenzo/claudex/internal/feedback"
)

func (s *Server) feedbackStore() feedback.Store {
	// Namespace by config file so gateways sharing a credential directory do not
	// silently share a collection. Unconfigured test servers use the key directory.
	if s.configPath != "" {
		return feedback.Store{Dir: s.configPath + ".feedback"}
	}
	return feedback.Store{Dir: filepath.Join(filepath.Dir(s.Config.ClientKeyFile), "feedback")}
}
func (s *Server) collectFeedback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.control.admit() {
		writeError(w, &apiError{Status: 503, Type: "overloaded_error", Message: "Gateway is shutting down."})
		return
	}
	defer s.control.finish()
	// Serialize the acceptance boundary with settings changes. Once disabling
	// returns, no later call can persist feedback under the previous setting.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg := s.currentConfig()
	if !cfg.Feedback.Enabled {
		writeError(w, &apiError{Status: 403, Type: "permission_error", Message: "Feedback collection is disabled."})
		return
	}
	var sub feedback.Submission
	if err := decodeControl(w, r, &sub); err != nil || sub.Validate() != nil {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: "Invalid feedback submission."})
		return
	}
	sub.Report = feedback.Redact(sub.Report, string(s.key))
	receipt, err := s.feedbackStore().Put(r.Context(), sub, s.version, cfg.Model)
	if err != nil {
		status := 500
		message := "Could not persist feedback."
		if errors.Is(err, feedback.ErrFull) {
			status = 507
			message = feedback.ErrFull.Error()
		}
		writeError(w, &apiError{Status: status, Type: "feedback_error", Message: message})
		return
	}
	writeJSON(w, 200, receipt)
}
func (s *Server) listFeedback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.control.admit() {
		writeError(w, &apiError{Status: 503, Type: "overloaded_error", Message: "Gateway is shutting down."})
		return
	}
	defer s.control.finish()
	records, err := s.feedbackStore().List(r.Context())
	if err != nil {
		writeError(w, &apiError{Status: 500, Type: "feedback_error", Message: "Could not read feedback."})
		return
	}
	if cursor := r.URL.Query().Get("before"); cursor != "" {
		found := false
		for i, record := range records {
			if record.ID == cursor {
				records = records[i+1:]
				found = true
				break
			}
		}
		if !found {
			writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: "Feedback cursor expired or unknown; restart listing."})
			return
		}
	}
	next := ""
	if len(records) > 100 {
		next = records[99].ID
		records = records[:100]
	}
	writeJSON(w, 200, map[string]any{"records": records, "next_cursor": next})
}
