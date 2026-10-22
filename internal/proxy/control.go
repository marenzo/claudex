package proxy

import (
	"encoding/json"
	"errors"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"io"
	"net/http"
	"sync"
	"time"
)

type controlState struct {
	mu              sync.Mutex
	active, clients int
	closing         bool
	idleSince       time.Time
}

func (c *controlState) admit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return false
	}
	c.active++
	return true
}
func (c *controlState) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	c.idleSince = time.Now()
}
func (s *Server) CloseSessions()                  { s.stopOnce.Do(func() { close(s.shutdown) }) }
func (s *Server) Configure(path, mode string)     { s.configPath = path; s.mode = mode }
func (s *Server) revision(c config.Config) string { return s.instance + ":" + c.Revision() }
func (s *Server) currentConfig() config.Config {
	if c := s.preferences.Load(); c != nil {
		return *c
	}
	return s.Config
}
func (s *Server) StopIfIdle(now time.Time, grace time.Duration) bool {
	c := &s.control
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.mode != "session" || c.active != 0 || c.clients != 0 || now.Sub(c.idleSince) < grace {
		return false
	}
	c.closing = true
	return true
}
func (s *Server) controlStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := s.currentConfig()
	s.control.mu.Lock()
	clients, active := s.control.clients, s.control.active
	s.control.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, control.Status{Product: "claudex", API: control.APIVersion, Version: s.version, Instance: s.instance, Mode: s.mode, Clients: clients, Active: active, Revision: s.revision(cfg), Preferences: control.PreferencesFor(cfg)})
}
func (s *Server) controlSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if err := decodeControl(w, r, &body); err != nil || (body.Kind != "claude" && body.Kind != "tui") {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: "session kind must be claude or tui"})
		return
	}
	s.control.mu.Lock()
	if s.control.closing {
		s.control.mu.Unlock()
		writeError(w, &apiError{Status: 503, Type: "overloaded_error", Message: "Gateway is shutting down."})
		return
	}
	s.control.clients++
	s.control.mu.Unlock()
	defer func() {
		s.control.mu.Lock()
		s.control.clients--
		s.control.idleSince = time.Now()
		s.control.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	beat := func() error {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.WriteString(w, "{}\n"); err != nil {
			return err
		}
		err := rc.Flush()
		_ = rc.SetWriteDeadline(time.Time{})
		return err
	}
	if beat() != nil {
		return
	}
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.shutdown:
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
			if beat() != nil {
				return
			}
		}
	}
}
func decodeControl(w http.ResponseWriter, r *http.Request, out any) error {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func (s *Server) settings() control.Settings {
	c := s.currentConfig()
	return control.Settings{Revision: s.revision(c), Preferences: control.PreferencesFor(c), Writable: s.configPath != "", Listen: c.Listen, AuthFile: c.AuthFile, ClientKeyFile: c.ClientKeyFile}
}
func (s *Server) controlConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		out := s.settings()
		w.Header().Set("ETag", `"`+out.Revision+`"`)
		writeJSON(w, 200, out)
		return
	}
	if r.Method != "PUT" {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(405)
		return
	}
	if s.configPath == "" {
		writeError(w, &apiError{Status: 403, Type: "permission_error", Message: "Gateway has no writable configuration source."})
		return
	}
	var prefs control.Preferences
	if err := decodeControl(w, r, &prefs); err != nil {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: "Invalid settings JSON."})
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	old := s.currentConfig()
	if r.Header.Get("If-Match") != `"`+s.revision(old)+`"` {
		writeError(w, &apiError{Status: 412, Type: "conflict_error", Message: "Settings changed; reload before saving."})
		return
	}
	next, err := prefs.Apply(old)
	if err != nil {
		writeError(w, &apiError{Status: 400, Type: "invalid_request_error", Message: err.Error()})
		return
	}
	conflict := false
	err = config.WithLock(r.Context(), s.configPath, func() error {
		disk, err := config.Load(s.configPath)
		if err != nil {
			return err
		}
		if disk.Revision() != old.Revision() {
			conflict = true
			return errors.New("config changed on disk")
		}
		if err := config.Save(s.configPath, next); err != nil {
			return err
		}
		s.preferences.Store(&next)
		return nil
	})
	if err != nil {
		status := 500
		message := "Could not persist settings; running settings are unchanged."
		if conflict {
			status = 412
			message = "Configuration changed on disk; restart the gateway to load it."
		}
		writeError(w, &apiError{Status: status, Type: "config_error", Message: message})
		return
	}
	out := s.settings()
	w.Header().Set("ETag", `"`+out.Revision+`"`)
	writeJSON(w, 200, out)
}
