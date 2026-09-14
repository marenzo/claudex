package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
)

// Usage is the service subcommand help line.
const Usage = "usage: claudex service {start|stop|restart|status|models|login [-no-browser]}"

// Service manages the launchd user service of an installation.
type Service struct {
	Paths   Paths
	Launchd Launchd
	HTTP    *http.Client
	Out     io.Writer
	Timeout time.Duration // how long start/stop wait for launchd and the gateway
	Sleep   func(time.Duration)
	// Login signs in against the credential store; overridable in tests.
	Login func(ctx context.Context, cfg config.Config, noBrowser bool, out io.Writer) error
}

// NewService returns a manager bound to the real launchd and loopback HTTP.
func NewService(p Paths) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // never route loopback health checks through a proxy
	return &Service{Paths: p, Launchd: NewLaunchd(), HTTP: &http.Client{Transport: transport, Timeout: 3 * time.Second},
		Out: os.Stdout, Timeout: 30 * time.Second, Sleep: time.Sleep, Login: login}
}

func login(ctx context.Context, cfg config.Config, noBrowser bool, out io.Writer) error {
	return auth.NewStore(cfg.AuthFile).Login(ctx, noBrowser, out)
}

// ErrUnauthorized reports that the configured port answered with another key.
var ErrUnauthorized = errors.New("the configured port rejected this installation's local key; check for another proxy")

// Run executes one service subcommand.
func (s *Service) Run(ctx context.Context, args []string) error {
	command := "status"
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "-h", "--help", "help":
		fmt.Fprintln(s.Out, Usage)
		return nil
	case "start", "stop", "restart", "status", "models", "login":
	default:
		return usageError{}
	}
	noBrowser := false
	if len(args) > 1 {
		if command != "login" || len(args) != 2 || (args[1] != "-no-browser" && args[1] != "--no-browser") {
			return usageError{}
		}
		noBrowser = true
	}
	if goos != "darwin" {
		return errors.New("service controls require macOS; run claudex directly or use Docker elsewhere")
	}
	// Stopping a broken installation must not depend on its config or key.
	if command == "stop" {
		if err := s.Stop(); err != nil {
			return err
		}
		fmt.Fprintln(s.Out, "Claudex stopped.")
		return nil
	}
	cfg, err := s.config()
	if err != nil {
		return err
	}
	switch command {
	case "start":
		return s.report(cfg)
	case "restart":
		if err := s.Stop(); err != nil {
			return err
		}
		return s.report(cfg)
	case "status":
		if !s.Launchd.Loaded(Label) {
			fmt.Fprintln(s.Out, "Claudex service is not running. Start it with: claudex service start")
			return nil
		}
		return s.report(cfg)
	case "models":
		models, err := s.Models(cfg)
		if err != nil {
			return err
		}
		for _, model := range models {
			fmt.Fprintln(s.Out, model)
		}
		return nil
	default: // login
		running := s.Launchd.Loaded(Label)
		if running {
			// Stop the service so login and token refresh never race on the credential file.
			if err := s.Stop(); err != nil {
				return err
			}
		}
		errLogin := s.Login(ctx, cfg, noBrowser, s.Out)
		if running {
			if _, errStart := s.Start(cfg); errStart != nil && errLogin == nil {
				return errStart
			}
		}
		if errLogin != nil {
			return errLogin
		}
		fmt.Fprintln(s.Out, "Codex subscription sign-in saved.")
		return nil
	}
}

type usageError struct{}

func (usageError) Error() string { return Usage }

// IsUsage reports whether err is a usage error (exit status 2).
func IsUsage(err error) bool { var u usageError; return errors.As(err, &u) }

func (s *Service) config() (config.Config, error) {
	cfg, err := config.Load(s.Paths.ConfigFile())
	if err != nil {
		return cfg, fmt.Errorf("cannot read gateway settings at %s; repair the config or rerun the installer: %w", s.Paths.ConfigFile(), err)
	}
	return cfg, nil
}

func (s *Service) report(cfg config.Config) error {
	models, err := s.Start(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "Claudex ready at http://%s; %d models. Authentication: Codex subscription.\n", cfg.Listen, len(models))
	if _, err := os.Stat(cfg.AuthFile); err != nil {
		fmt.Fprintln(s.Out, "Complete sign-in with: claudex service login")
	}
	return nil
}

// Stop unloads the service and waits until launchd no longer lists it.
func (s *Service) Stop() error {
	if !s.Launchd.Loaded(Label) {
		return nil
	}
	if err := s.Launchd.Bootout(Label); err != nil && s.Launchd.Loaded(Label) {
		return fmt.Errorf("could not stop the user service: %w", err)
	}
	deadline := time.Now().Add(s.Timeout)
	for s.Launchd.Loaded(Label) {
		if time.Now().After(deadline) {
			return errors.New("the user service is still unloading; retry after it stops")
		}
		s.Sleep(100 * time.Millisecond)
	}
	return nil
}

// Start bootstraps the service if needed and waits for a healthy gateway.
func (s *Service) Start(cfg config.Config) ([]string, error) {
	deadline := time.Now().Add(s.Timeout)
	var last error
	for time.Now().Before(deadline) {
		// launchd can still report a service as loaded while a previous bootout
		// is pending, so recheck on every attempt.
		if !s.Launchd.Loaded(Label) {
			if err := s.Launchd.Bootstrap(s.Paths.ServicePlist()); err != nil && !s.Launchd.Loaded(Label) {
				last = fmt.Errorf("could not start the user service: %w", err)
				s.Sleep(250 * time.Millisecond)
				continue
			}
		}
		err := s.Health(cfg)
		if err == nil {
			return s.Models(cfg)
		}
		if errors.Is(err, ErrUnauthorized) {
			return nil, err
		}
		last = err
		s.Sleep(250 * time.Millisecond)
	}
	detail := ""
	if last != nil {
		detail = " Last error: " + last.Error()
	}
	return nil, fmt.Errorf("gateway did not become ready; inspect %s.%s", s.Paths.LogFile(), detail)
}

func (s *Service) get(cfg config.Config, path string, into any) error {
	key, err := os.ReadFile(cfg.ClientKeyFile)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+cfg.Listen+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

// Health verifies that the configured port serves a healthy Claudex gateway.
func (s *Service) Health(cfg config.Config) error {
	var result struct{ Status, Product, Version string }
	if err := s.get(cfg, "/healthz", &result); err != nil {
		return err
	}
	if result.Status != "ok" || result.Product != "claudex" || result.Version == "" {
		return errors.New("the port is not serving a healthy Claudex proxy")
	}
	return nil
}

// Models lists the model IDs the gateway advertises.
func (s *Service) Models(cfg config.Config) ([]string, error) {
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := s.get(cfg, "/v1/models", &result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Data))
	for _, model := range result.Data {
		ids = append(ids, model.ID)
	}
	return ids, nil
}
