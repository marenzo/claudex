package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	TokenURL = "https://auth.openai.com/oauth/token"
)

type Credential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	Expired      string `json:"expired"`
	Disabled     bool   `json:"disabled"`
}

// Store persists rotated tokens atomically. A sidecar file lock serializes
// rotation and login across Claudex processes sharing the same credential path.
// Other applications must not write that credential without observing the lock.
type Store struct {
	Path     string
	Client   *http.Client
	tokenURL string
}

func NewStore(path string) *Store {
	return &Store{Path: path, Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tokenURL: TokenURL}
}

// ErrNotSignedIn reports a missing, malformed, disabled, or incomplete Codex
// sign-in. Signing in again fixes it.
var ErrNotSignedIn = errors.New("not signed in to Codex; run claudex -login (or claudex service login)")

// Check reports whether a usable sign-in exists, without refreshing it, and the
// access token expiry when the credential records one.
func (s *Store) Check() (time.Time, error) {
	c, _, err := s.read()
	if err != nil {
		return time.Time{}, err
	}
	expires, _ := time.Parse(time.RFC3339, c.Expired)
	return expires, nil
}

func (s *Store) read() (Credential, map[string]json.RawMessage, error) {
	var c Credential
	data, errRead := os.ReadFile(s.Path)
	if errRead != nil {
		if os.IsNotExist(errRead) {
			return c, nil, ErrNotSignedIn
		}
		return c, nil, fmt.Errorf("read Codex sign-in: %w", errRead)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &c) != nil || json.Unmarshal(data, &fields) != nil {
		return c, nil, fmt.Errorf("invalid Codex sign-in file: %w", ErrNotSignedIn)
	}
	if c.Disabled || c.Type != "codex" || c.AccessToken == "" || c.AccountID == "" {
		return c, nil, fmt.Errorf("disabled or incomplete Codex sign-in: %w", ErrNotSignedIn)
	}
	return c, fields, nil
}

func (s *Store) Get(ctx context.Context) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	c, _, errRead := s.read()
	if errRead != nil {
		return c, errRead
	}
	expires, errExpiry := time.Parse(time.RFC3339, c.Expired)
	if errExpiry == nil && time.Until(expires) < 5*time.Minute && c.RefreshToken != "" {
		refreshed, errRefresh := s.Refresh(ctx, c.AccessToken)
		if errRefresh == nil {
			return refreshed, nil
		}
		if ctx.Err() == nil && time.Now().Before(expires) {
			// A transient refresh failure must not fail requests while the current
			// token is still valid; the next request retries the refresh.
			slog.Warn("Codex token refresh failed; using current token until expiry", "error_type", fmt.Sprintf("%T", errRefresh))
			return c, nil
		}
		return refreshed, errRefresh
	}
	return c, nil
}

func (s *Store) Refresh(ctx context.Context, usedToken string) (Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, errLock := s.lock(ctx)
	if errLock != nil {
		return Credential{}, errLock
	}
	defer unlock()
	c, fields, errRead := s.read()
	if errRead != nil {
		return c, errRead
	}
	if c.AccessToken != usedToken {
		return c, nil
	}
	return s.refresh(ctx, c, fields)
}

func (s *Store) refresh(ctx context.Context, c Credential, fields map[string]json.RawMessage) (Credential, error) {
	if c.RefreshToken == "" {
		return c, fmt.Errorf("expired Codex sign-in; run claudex -login")
	}
	form := url.Values{"client_id": {ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "scope": {"openid profile email"}}
	return s.exchange(ctx, form, fields)
}

func (s *Store) exchange(ctx context.Context, form url.Values, fields map[string]json.RawMessage) (Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if errRequest != nil {
		return Credential{}, errRequest
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, errRequest := s.Client.Do(req)
	if errRequest != nil {
		return Credential{}, fmt.Errorf("token exchange with Codex failed: %w", errRequest)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			slog.Debug("token response close failed")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return Credential{}, fmt.Errorf("token exchange with Codex returned HTTP %d; run claudex -login if sign-in has expired", resp.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&token) != nil || token.AccessToken == "" || (token.ExpiresIn <= 0 || token.ExpiresIn > 366*24*3600) {
		return Credential{}, fmt.Errorf("token exchange with Codex returned an incomplete credential")
	}
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	set := func(key string, value any) { fields[key], _ = json.Marshal(value) }
	set("type", "codex")
	set("disabled", false)
	set("access_token", token.AccessToken)
	set("expired", time.Now().Add(time.Duration(token.ExpiresIn)*time.Second).UTC().Format(time.RFC3339))
	set("last_refresh", time.Now().UTC().Format(time.RFC3339))
	if token.RefreshToken != "" {
		set("refresh_token", token.RefreshToken)
	}
	if token.IDToken != "" {
		set("id_token", token.IDToken)
	}
	// These claims come from the token endpoint's authenticated TLS response;
	// decoding them chooses the account header, not an authorization decision.
	for _, jwt := range []string{token.IDToken, token.AccessToken} {
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			continue
		}
		payload, errDecode := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
		if errDecode != nil {
			continue
		}
		var claims struct {
			Email string `json:"email"`
			Auth  struct {
				Account string `json:"chatgpt_account_id"`
			} `json:"https://api.openai.com/auth"`
		}
		if json.Unmarshal(payload, &claims) == nil {
			if claims.Auth.Account != "" {
				set("account_id", claims.Auth.Account)
			}
			if claims.Email != "" {
				set("email", claims.Email)
			}
		}
	}
	data, errMarshal := json.MarshalIndent(fields, "", "  ")
	if errMarshal != nil {
		return Credential{}, errMarshal
	}
	var c Credential
	if json.Unmarshal(data, &c) != nil || c.AccountID == "" {
		return c, fmt.Errorf("token response from Codex did not identify an account")
	}
	if errSave := WritePrivate(s.Path, append(data, '\n')); errSave != nil {
		return c, fmt.Errorf("save Codex sign-in: %w", errSave)
	}
	return c, nil
}

func WritePrivate(path string, data []byte) error {
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0700); errMkdir != nil {
		return errMkdir
	}
	f, errCreate := os.CreateTemp(filepath.Dir(path), ".credential-*")
	if errCreate != nil {
		return errCreate
	}
	name := f.Name()
	defer func() {
		if errRemove := os.Remove(name); errRemove != nil && !os.IsNotExist(errRemove) {
			slog.Debug("temporary credential cleanup failed")
		}
	}()
	if _, errWrite := f.Write(data); errWrite != nil {
		if errClose := f.Close(); errClose != nil {
			slog.Debug("temporary credential close failed")
		}
		return errWrite
	}
	if errSync := f.Sync(); errSync != nil {
		if errClose := f.Close(); errClose != nil {
			slog.Debug("temporary credential close failed")
		}
		return errSync
	}
	if errClose := f.Close(); errClose != nil {
		return errClose
	}
	return os.Rename(name, path)
}
