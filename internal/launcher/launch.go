package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/marenzo/claudex/internal/config"
)

// Launcher starts the installed Claude Code CLI against the local gateway.
type Launcher struct {
	Service  *Service
	LookPath func(string) (string, error)
	Environ  func() []string
	Exec     func(path string, argv, env []string) error
}

// NewLauncher returns a launcher that replaces the current process with claude.
func NewLauncher(s *Service) *Launcher {
	return &Launcher{Service: s, LookPath: exec.LookPath, Environ: os.Environ, Exec: syscall.Exec}
}

// stripped are inherited variables that would override or conflict with the gateway.
var stripped = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_MANTLE"}

// Run rewrites arguments, ensures the service is running, and executes claude.
func (l *Launcher) Run(args []string) error {
	claude, err := l.LookPath("claude")
	if err != nil {
		native := filepath.Join(l.Service.Paths.Home, ".local", "bin", "claude")
		if info, errStat := os.Stat(native); errStat == nil && info.Mode().Perm()&0o111 != 0 {
			claude = native
		}
	}
	if claude == "" {
		return errors.New("install Claude Code first and put its claude executable on PATH")
	}
	args = RewriteArgs(args)
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h", "--version", "-v":
			return l.Exec(claude, append([]string{claude}, args...), l.Environ())
		}
	}
	p := l.Service.Paths
	if !exists(p.SettingsFile()) {
		return errors.New("install the launcher settings first: claudex install")
	}
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return fmt.Errorf("cannot read gateway settings at %s; rerun claudex install: %w", p.ConfigFile(), err)
	}
	quiet := *l.Service
	quiet.Out = io.Discard
	if _, err := quiet.Start(cfg); err != nil {
		return err
	}
	raw, err := os.ReadFile(p.SettingsFile())
	if err != nil {
		return err
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("invalid %s; rerun claudex install: %w", p.SettingsFile(), err)
	}
	key, err := os.ReadFile(cfg.ClientKeyFile)
	if err != nil {
		return err
	}
	env := map[string]string{}
	for _, entry := range l.Environ() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			env[name] = value
		}
	}
	for _, name := range stripped {
		delete(env, name)
	}
	for name, value := range settings.Env {
		env[name] = value
	}
	env["ANTHROPIC_AUTH_TOKEN"] = strings.TrimSpace(string(key))
	environ := make([]string, 0, len(env))
	for name, value := range env {
		environ = append(environ, name+"="+value)
	}
	argv := append([]string{claude, "--disallowedTools", "Artifact", "--settings", p.SettingsFile()}, args...)
	return l.Exec(claude, argv, environ)
}
