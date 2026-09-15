package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/quota"
)

const BaseURL = "https://chatgpt.com/backend-api"

type Client struct {
	Auth    *auth.Store
	HTTP    *http.Client
	baseURL string
}

func NewClient(store *auth.Store) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Bound the wait for response headers; body inactivity is bounded by the proxy stream reader.
	transport.ResponseHeaderTimeout = 2 * time.Minute
	return &Client{Auth: store, HTTP: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, baseURL: BaseURL}
}

func (c *Client) request(ctx context.Context, credential auth.Credential, method, path string, body []byte, session string) (*http.Response, error) {
	req, errRequest := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if errRequest != nil {
		return nil, errRequest
	}
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("Chatgpt-Account-Id", credential.AccountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex-tui/0.154.0")
	req.Header.Set("Originator", "codex_cli_rs")
	if method == http.MethodPost {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if session != "" {
		req.Header.Set("Session_id", session)
	}
	return c.HTTP.Do(req)
}

// ErrAccountChanged reports that refreshing after a 401 produced a sign-in for a
// different Codex account, so the request was not retried.
var ErrAccountChanged = errors.New("sign-in switched to a different Codex account during the request")

func (c *Client) Responses(ctx context.Context, credential auth.Credential, body []byte, session string) (*http.Response, auth.Credential, error) {
	return c.withRefresh(ctx, credential, http.MethodPost, "/codex/responses", body, session)
}

// withRefresh sends a request and, after a 401, refreshes the credential once and
// retries with it. A refresh that returns another account is not retried.
func (c *Client) withRefresh(ctx context.Context, credential auth.Credential, method, path string, body []byte, session string) (*http.Response, auth.Credential, error) {
	response, errRequest := c.request(ctx, credential, method, path, body, session)
	if errRequest != nil || response.StatusCode != http.StatusUnauthorized {
		return response, credential, errRequest
	}
	if errClose := response.Body.Close(); errClose != nil {
		slog.Debug("unauthorized response close failed")
	}
	refreshed, errRefresh := c.Auth.Refresh(ctx, credential.AccessToken)
	if errRefresh != nil {
		return nil, credential, errRefresh
	}
	if refreshed.AccountID != credential.AccountID {
		return nil, refreshed, ErrAccountChanged
	}
	response, errRequest = c.request(ctx, refreshed, method, path, body, session)
	return response, refreshed, errRequest
}

func (c *Client) QuotaAvailable(ctx context.Context, account string) (bool, error) {
	// This control-plane check must finish before the next recovery tick. The
	// inference client itself remains unlimited for long reasoning streams.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	credential, errAuth := c.Auth.Get(ctx)
	if errAuth != nil {
		return false, errAuth
	}
	if credential.AccountID != account {
		return false, nil
	}
	resp, _, errRequest := c.withRefresh(ctx, credential, http.MethodGet, "/wham/usage", nil, "")
	if errors.Is(errRequest, ErrAccountChanged) {
		return false, nil
	}
	if errRequest != nil {
		return false, errRequest
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			slog.Debug("quota response close failed")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("quota check returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 64 * 1024
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if errRead != nil {
		return false, errRead
	}
	if len(body) > maxBody {
		return false, fmt.Errorf("quota response exceeds size limit")
	}
	return quota.CodexUsageAllowsRecovery(body), nil
}
