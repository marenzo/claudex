// Package control defines the authenticated gateway control protocol.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/metrics"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const APIVersion = 2

type Preferences struct {
	Model         string          `json:"model"`
	Effort        string          `json:"reasoning_effort"`
	Reviewer      string          `json:"auto_mode_classifier_model"`
	ContextWindow int             `json:"context_window"`
	CompactWindow int             `json:"compact_window"`
	Feedback      config.Feedback `json:"feedback"`
}

func PreferencesFor(c config.Config) Preferences {
	return Preferences{c.Model, c.ReasoningEffort, c.AutoModeClassifierModel, c.ContextWindow, c.CompactWindow, c.Feedback}
}
func (p Preferences) Apply(c config.Config) (config.Config, error) {
	c.Model = p.Model
	c.ReasoningEffort = p.Effort
	c.AutoModeClassifierModel = p.Reviewer
	c.ContextWindow = p.ContextWindow
	c.CompactWindow = p.CompactWindow
	c.Feedback = p.Feedback
	if err := c.Validate(); err != nil {
		return c, err
	}
	m, _ := config.FindModel(c.Model)
	c.Model = m.ID
	if c.AutoModeClassifierModel != "" {
		m, _ = config.FindModel(c.AutoModeClassifierModel)
		c.AutoModeClassifierModel = m.ID
	}
	return c, nil
}

type Status struct {
	Product     string      `json:"product"`
	API         int         `json:"api_version"`
	Version     string      `json:"version"`
	Instance    string      `json:"instance_id"`
	Mode        string      `json:"mode"`
	Clients     int         `json:"clients"`
	Active      int         `json:"active"`
	Revision    string      `json:"revision"`
	Preferences Preferences `json:"preferences"`
}
type Settings struct {
	Revision      string      `json:"revision"`
	Preferences   Preferences `json:"preferences"`
	Writable      bool        `json:"writable"`
	Listen        string      `json:"listen"`
	AuthFile      string      `json:"auth_file"`
	ClientKeyFile string      `json:"client_key_file"`
}
type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func New(address, keyFile string) (*Client, error) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("gateway URL must be an HTTP(S) origin without credentials, query or path")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read client key: %w", err)
	}
	if len(strings.TrimSpace(string(key))) < 24 {
		return nil, errors.New("client key must contain at least 24 characters")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	if host := u.Hostname(); host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		transport.Proxy = nil
	}
	return &Client{URL: strings.TrimRight(address, "/"), Key: strings.TrimSpace(string(key)), HTTP: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("gateway HTTP %d: %s", e.Status, e.Message) }
func (c *Client) request(ctx context.Context, method, path string, body any, revision string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	if revision != "" {
		req.Header.Set("If-Match", `"`+revision+`"`)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&e)
		if e.Error.Message == "" {
			e.Error.Message = http.StatusText(resp.StatusCode)
		}
		return nil, &HTTPError{resp.StatusCode, e.Error.Message}
	}
	return resp, nil
}
func (c *Client) JSON(ctx context.Context, method, path string, body any, revision string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.request(ctx, method, path, body, revision)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("gateway response exceeds limit")
	}
	return json.Unmarshal(data, out)
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.JSON(ctx, "GET", "/_claudex/status", nil, "", &s)
	if err == nil && (s.Product != "claudex" || s.API != APIVersion || s.Instance == "") {
		err = errors.New("gateway control protocol mismatch; run the same claudex version on both sides")
	}
	return s, err
}
func (c *Client) Settings(ctx context.Context) (Settings, error) {
	var s Settings
	err := c.JSON(ctx, "GET", "/_claudex/config", nil, "", &s)
	return s, err
}
func (c *Client) Metrics(ctx context.Context) (metrics.Snapshot, error) {
	var s metrics.Snapshot
	err := c.JSON(ctx, "GET", "/_claudex/metrics", nil, "", &s)
	return s, err
}
func (c *Client) Save(ctx context.Context, p Preferences, rev string) (Settings, error) {
	var s Settings
	err := c.JSON(ctx, "PUT", "/_claudex/config", p, rev, &s)
	return s, err
}

// Attach holds ownership until cancellation or connection loss.
func (c *Client) Attach(ctx context.Context, kind string) (<-chan error, error) {
	resp, err := c.request(ctx, "POST", "/_claudex/sessions", map[string]string{"kind": kind}, "")
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		_, err := io.Copy(io.Discard, resp.Body)
		if err == nil {
			err = io.EOF
		}
		done <- err
		close(done)
	}()
	return done, nil
}
