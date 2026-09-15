package codex

import (
	"context"
	"errors"
	"github.com/marenzo/claudex/internal/auth"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func clientFixture(t *testing.T) *Client {
	t.Helper()
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"type":"codex","account_id":"a","access_token":"token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return NewClient(auth.NewStore(p))
}

func TestQuotaProbeHasDeadlineAndPropagatesCancellation(t *testing.T) {
	c := clientFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("quota probe has no bounded deadline")
		}
		return &http.Response{StatusCode: 200, Body: &cancelledBody{ctx: r.Context(), cancel: cancel}}, nil
	})
	allowed, err := c.QuotaAvailable(ctx, "a")
	if allowed || !errors.Is(err, context.Canceled) {
		t.Fatalf("stalled quota body returned %t, %v", allowed, err)
	}
}

type cancelledBody struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *cancelledBody) Read([]byte) (int, error) {
	b.cancel()
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (*cancelledBody) Close() error { return nil }

func TestQuota401RefreshesOnceWithoutCrossingAccounts(t *testing.T) {
	for _, name := range []string{"new_token", "new_account", "still_unauthorized"} {
		t.Run(name, func(t *testing.T) {
			c := clientFixture(t)
			calls := 0
			c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				status, body := 401, `{}`
				if calls == 1 {
					account := "a"
					if name == "new_account" {
						account = "other"
					}
					if err := auth.WritePrivate(c.Auth.Path, []byte(`{"type":"codex","account_id":"`+account+`","access_token":"rotated"}`)); err != nil {
						t.Fatal(err)
					}
				} else {
					if r.Header.Get("Authorization") != "Bearer rotated" || r.Header.Get("Chatgpt-Account-Id") != "a" {
						t.Fatal("quota retry used the wrong credential")
					}
					if name != "still_unauthorized" {
						status, body = 200, `{"rate_limit":{"allowed":true,"limit_reached":false}}`
					}
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			allowed, err := c.QuotaAvailable(context.Background(), "a")
			if allowed != (name == "new_token") {
				t.Fatalf("recovery allowed = %t, error = %v", allowed, err)
			}
			wantCalls := 2
			if name == "new_account" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("made %d requests, expected %d", calls, wantCalls)
			}
		})
	}
}

func TestInferenceHasNoProbeDeadline(t *testing.T) {
	c := clientFixture(t)
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); ok {
			t.Fatal("inference inherited a control-plane timeout")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	response, _, err := c.Responses(context.Background(), auth.Credential{AccessToken: "token", AccountID: "a"}, []byte(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestQuotaReadOnlyAndConservative(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       bool
	}{
		{"available", `{"rate_limit":{"allowed":true,"limit_reached":false}}`, 200, true},
		{"limited", `{"rate_limit":{"allowed":false,"limit_reached":true}}`, 200, false},
		{"malformed", `{`, 200, false}, {"missing", `{}`, 200, false}, {"unauthorized", `{}`, 401, false}, {"redirect", `{}`, 302, false}, {"oversized", strings.Repeat(" ", 65537), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clientFixture(t)
			calls := 0
			c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.String() != BaseURL+"/wham/usage" || r.Header.Get("Chatgpt-Account-Id") != "a" {
					t.Fatal("wrong quota endpoint")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			got, _ := c.QuotaAvailable(context.Background(), "a")
			if got != tc.want || calls != 1 {
				t.Fatal(got, calls)
			}
			if got, _ := c.QuotaAvailable(context.Background(), "other"); got || calls != 1 {
				t.Fatal("probed wrong account")
			}
		})
	}
}
func TestResponse401UsesNewLoginOnce(t *testing.T) {
	c := clientFixture(t)
	calls := 0
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if err := auth.WritePrivate(c.Auth.Path, []byte(`{"type":"codex","account_id":"a","access_token":"rotated"}`)); err != nil {
				t.Fatal(err)
			}
		} else if r.Header.Get("Authorization") != "Bearer rotated" {
			t.Fatal("did not reload rotated token")
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	response, _, err := c.Responses(context.Background(), auth.Credential{AccessToken: "token", AccountID: "a"}, []byte(`{}`), "session")
	if err != nil || response.StatusCode != 401 || calls != 2 {
		t.Fatal("wrong retry policy", calls, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestResponse401DoesNotRetryAcrossAccounts(t *testing.T) {
	c := clientFixture(t)
	calls := 0
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := auth.WritePrivate(c.Auth.Path, []byte(`{"type":"codex","account_id":"other","access_token":"rotated"}`)); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	_, _, err := c.Responses(context.Background(), auth.Credential{AccessToken: "token", AccountID: "a"}, []byte(`{}`), "session")
	if !errors.Is(err, ErrAccountChanged) || calls != 1 {
		t.Fatalf("retried across accounts: calls=%d err=%v", calls, err)
	}
}
