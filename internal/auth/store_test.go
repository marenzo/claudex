package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRefreshRotationSerializedAndAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	original := `{"type":"codex","account_id":"a","access_token":"old","refresh_token":"refresh-old","expired":"2000-01-01T00:00:00Z","extension":{"keep":true}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(p)
	otherStore := NewStore(p)
	var calls atomic.Int32
	store.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		values, err := url.ParseQuery(string(body))
		if err != nil || values.Get("refresh_token") != "refresh-old" || values.Get("client_id") != ClientID {
			t.Error("bad refresh request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new","refresh_token":"refresh-new","expires_in":3600}`)), Header: make(http.Header)}, nil
	})
	otherStore.Client = store.Client
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			selected := store
			if i%2 == 1 {
				selected = otherStore
			}
			c, err := selected.Get(context.Background())
			if err != nil || c.AccessToken != "new" {
				t.Error("refresh failed", err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate refreshes", calls.Load())
	}
	if _, err := store.Refresh(context.Background(), "old"); err != nil || calls.Load() != 1 {
		t.Fatal("stale 401 rotated again")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if json.Unmarshal(data, &saved) != nil || saved["extension"] == nil || saved["refresh_token"] != "refresh-new" {
		t.Fatal("lost fields")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
}

func TestCredentialLockWaitHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)
	unlock, err := store.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	other := NewStore(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := other.Refresh(ctx, "old")
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lock wait returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("credential lock ignored cancellation")
	}
	info, err := os.Stat(path + ".lock")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("lock file must remain private")
	}
}
func TestFailedRefreshKeepsCredential(t *testing.T) {
	for _, body := range []string{`{}`, `{"access_token":"new","expires_in":0}`, `{"access_token":"new","expires_in":9223372036854775807}`} {
		p := filepath.Join(t.TempDir(), "auth.json")
		original := `{"type":"codex","account_id":"a","access_token":"old","refresh_token":"r"}`
		if err := os.WriteFile(p, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		s := NewStore(p)
		s.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := s.Refresh(context.Background(), "old"); err == nil {
			t.Fatal("accepted incomplete token")
		}
		data, _ := os.ReadFile(p)
		if string(data) != original {
			t.Fatal("overwrote good credential")
		}
	}
}
func TestDisabledAndNewLogin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	s := NewStore(p)
	for _, disabled := range []bool{true, false} {
		data, _ := json.Marshal(Credential{Type: "codex", AccountID: "a", AccessToken: "new-login", Disabled: disabled, Expired: time.Now().Add(time.Hour).Format(time.RFC3339)})
		if err := WritePrivate(p, data); err != nil {
			t.Fatal(err)
		}
		c, err := s.Get(context.Background())
		if disabled && err == nil {
			t.Fatal("disabled account accepted")
		}
		if !disabled && (err != nil || c.AccessToken != "new-login") {
			t.Fatal("login reload failed")
		}
	}
}
func TestPKCEAuthorization(t *testing.T) {
	u, err := url.Parse(AuthorizationURL("state", strings.Repeat("a", 43)))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "auth.openai.com" || q.Get("state") != "state" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("client_id") != ClientID {
		t.Fatal("bad authorization URL")
	}
}

func TestTransientRefreshFailureKeepsValidToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	write := func(expires time.Time) {
		data, _ := json.Marshal(Credential{Type: "codex", AccountID: "a", AccessToken: "current", RefreshToken: "r", Expired: expires.Format(time.RFC3339)})
		if err := WritePrivate(p, data); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(p)
	s.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":"try later"}`))}, nil
	})
	// Inside the refresh window but still valid: keep serving the current token.
	write(time.Now().Add(2 * time.Minute))
	c, err := s.Get(context.Background())
	if err != nil || c.AccessToken != "current" {
		t.Fatalf("valid token dropped on transient refresh failure: %v %+v", err, c)
	}
	// Already expired: the refresh failure must surface.
	write(time.Now().Add(-time.Minute))
	if _, err := s.Get(context.Background()); err == nil {
		t.Fatal("expired token accepted after failed refresh")
	}
}

func TestCheckReportsSignInWithoutRefreshing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	s := NewStore(p)
	s.Client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Check must not refresh")
	})
	if _, err := s.Check(); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("missing sign-in: %v", err)
	}
	// Inside the refresh window: Get would refresh, Check must not.
	expires := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	data, _ := json.Marshal(Credential{Type: "codex", AccountID: "a", AccessToken: "t", RefreshToken: "r", Expired: expires.Format(time.RFC3339)})
	if err := WritePrivate(p, data); err != nil {
		t.Fatal(err)
	}
	got, err := s.Check()
	if err != nil || !got.Equal(expires) {
		t.Fatalf("Check() = %v, %v; want %v", got, err, expires)
	}
	if err := WritePrivate(p, []byte(`{"type":"codex","disabled":true,"access_token":"t","account_id":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("disabled sign-in: %v", err)
	}
}
