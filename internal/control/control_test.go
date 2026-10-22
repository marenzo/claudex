package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clientFor(t *testing.T, address string) *Client {
	t.Helper()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(address, key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestControlRefusesCredentialRedirect(t *testing.T) {
	calls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	c := clientFor(t, origin.URL)
	if _, err := c.Status(context.Background()); err == nil || calls != 0 {
		t.Fatal("followed authenticated redirect", err, calls)
	}
}
func TestControlDeadlineIncludesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := clientFor(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("body read ignored cancellation")
	}
}
func TestControlRejectsInvalidOrigins(t *testing.T) {
	for _, address := range []string{"file:///tmp/key", "http://user:password@localhost", "http://localhost/path", "http://localhost?key=secret"} {
		if _, err := New(address, "/missing"); err == nil {
			t.Fatal("accepted origin", address)
		}
	}
}
