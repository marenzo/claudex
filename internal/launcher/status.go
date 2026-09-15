package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
)

// Check levels reported by claudex status. Only LevelFail makes the command
// exit with a non-zero status.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// Check is one line of claudex status output.
type Check struct {
	Level  string
	Name   string
	Detail string
}

// Status inspects an installation without changing anything.
type Status struct {
	ConfigPath string
	Paths      Paths
	Launchd    Launchd
	HTTP       *http.Client
	LookPath   func(string) (string, error)
	Now        func() time.Time
}

// NewStatus inspects the config at configPath using the real launchd and a
// proxy-free loopback HTTP client.
func NewStatus(p Paths, configPath string) *Status {
	service := NewService(p)
	return &Status{ConfigPath: configPath, Paths: p, Launchd: service.Launchd, HTTP: service.HTTP,
		LookPath: exec.LookPath, Now: time.Now}
}

// Run performs every check in order. Checks that need a config are skipped
// when it cannot be loaded.
func (s *Status) Run() []Check {
	var checks []Check
	cfg, err := config.Load(s.ConfigPath)
	if err != nil {
		checks = append(checks, Check{LevelFail, "config", err.Error()})
	} else {
		checks = append(checks, Check{LevelOK, "config", s.ConfigPath}, s.clientKey(cfg), s.signIn(cfg))
		checks = append(checks, s.gateway(cfg)...)
	}
	if goos == "darwin" {
		checks = append(checks, s.service())
	}
	return append(checks, s.claude())
}

func (s *Status) clientKey(cfg config.Config) Check {
	info, err := os.Stat(cfg.ClientKeyFile)
	if err != nil {
		return Check{LevelFail, "client key", fmt.Sprintf("cannot read %s: %v", cfg.ClientKeyFile, err)}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Check{LevelWarn, "client key", fmt.Sprintf("%s is readable by other users; run chmod 600 on it", cfg.ClientKeyFile)}
	}
	return Check{LevelOK, "client key", cfg.ClientKeyFile}
}

func (s *Status) signIn(cfg config.Config) Check {
	expires, err := auth.NewStore(cfg.AuthFile).Check()
	if err != nil {
		return Check{LevelFail, "codex sign-in", err.Error()}
	}
	detail := "signed in"
	switch {
	case expires.IsZero():
	case expires.After(s.Now()):
		detail += fmt.Sprintf("; access token valid until %s and refreshed automatically", expires.Local().Format(time.DateTime))
	default:
		detail += "; access token expired and is refreshed on the next request"
	}
	return Check{LevelOK, "codex sign-in", detail}
}

func (s *Status) gateway(cfg config.Config) []Check {
	base := "http://" + cfg.Listen
	key, _ := os.ReadFile(cfg.ClientKeyFile)
	get := func(path string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
		return s.HTTP.Do(req)
	}
	resp, err := get("/healthz")
	if err != nil {
		return []Check{{LevelWarn, "gateway", fmt.Sprintf("not answering at %s; start it with claudex run or claudex start", base)}}
	}
	var health struct{ Product, Status, Version string }
	errDecode := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health)
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return []Check{{LevelFail, "gateway", fmt.Sprintf("%s rejects this client key; another gateway may own the port", base)}}
	case resp.StatusCode != http.StatusOK || errDecode != nil || health.Product != "claudex":
		return []Check{{LevelFail, "gateway", fmt.Sprintf("%s is served by something other than Claudex", base)}}
	}
	checks := []Check{{LevelOK, "gateway", fmt.Sprintf("running at %s, version %s", base, health.Version)}}
	resp, err = get("/v1/models")
	if err != nil {
		return append(checks, Check{LevelWarn, "client key accepted", err.Error()})
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return append(checks, Check{LevelFail, "client key accepted", fmt.Sprintf("gateway answered HTTP %d; it uses a different key than %s", resp.StatusCode, cfg.ClientKeyFile)})
	}
	return append(checks, Check{LevelOK, "client key accepted", "yes"})
}

func (s *Status) service() Check {
	if _, err := os.Stat(s.Paths.Agent); err != nil {
		return Check{LevelOK, "macos service", "not installed; claudex install sets it up"}
	}
	if s.Launchd.Loaded(Label) {
		return Check{LevelOK, "macos service", "installed and loaded"}
	}
	return Check{LevelWarn, "macos service", "installed but not loaded; run claudex start"}
}

func (s *Status) claude() Check {
	if path, err := s.LookPath("claude"); err == nil {
		return Check{LevelOK, "claude code", path}
	}
	native := filepath.Join(s.Paths.Home, ".local", "bin", "claude")
	if info, err := os.Stat(native); err == nil && info.Mode().Perm()&0o111 != 0 {
		return Check{LevelOK, "claude code", native}
	}
	return Check{LevelWarn, "claude code", "claude is not on PATH; install Claude Code to use claude-gpt"}
}

// PrintChecks writes aligned check lines and reports whether any check failed.
func PrintChecks(w io.Writer, checks []Check) bool {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	failed := false
	for _, check := range checks {
		fmt.Fprintf(table, "%s\t%s\t%s\n", check.Level, check.Name, check.Detail)
		failed = failed || check.Level == LevelFail
	}
	_ = table.Flush()
	return failed
}
