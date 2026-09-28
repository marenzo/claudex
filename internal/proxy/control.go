package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const drainLease = 30 * time.Second

type controlState struct {
	mu      sync.Mutex
	active  int
	owner   string
	expires time.Time
}

func (c *controlState) admit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner != "" && time.Now().Before(c.expires) {
		return false
	}
	c.owner = ""
	c.active++
	return true
}

func (c *controlState) finish() {
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
}

func (c *controlState) snapshot() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active, c.owner != "" && time.Now().Before(c.expires)
}

func (c *controlState) lease(token string) (string, int, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner != "" && time.Now().Before(c.expires) && token != c.owner {
		return "", 0, time.Time{}, false
	}
	if c.owner == "" || time.Now().After(c.expires) {
		if token != "" {
			return "", 0, time.Time{}, false
		}
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return "", 0, time.Time{}, false
		}
		c.owner = hex.EncodeToString(bytes)
	}
	c.expires = time.Now().Add(drainLease)
	return c.owner, c.active, c.expires, true
}

func (c *controlState) release(token string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token == "" || c.owner != token {
		return false
	}
	c.owner = ""
	c.expires = time.Time{}
	return true
}

func (s *Server) controlStatus(w http.ResponseWriter, _ *http.Request) {
	active, draining := s.control.snapshot()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"version": s.version, "active": active, "draining": draining,
		"revision": s.Config.Revision(), "dashboard": s.metrics != nil})
}

func (s *Server) controlDrain(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Claudex-Drain-Token")
	switch r.Method {
	case http.MethodPost:
		owner, active, expires, ok := s.control.lease(token)
		if !ok {
			writeError(w, &apiError{Status: 409, Type: "conflict_error", Message: "Another Claudex operation is draining the gateway."})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"token": owner, "active": active, "expires_at": expires})
	case http.MethodDelete:
		if !s.control.release(token) {
			writeError(w, &apiError{Status: 409, Type: "conflict_error", Message: "The drain lease is no longer owned by this command."})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
