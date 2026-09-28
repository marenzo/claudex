package launcher

import (
	"context"
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
	if nativeClaudeCommand(args) {
		return l.Exec(claude, append([]string{claude}, args...), l.Environ())
	}
	p := l.Service.Paths
	if goos != "darwin" {
		return errors.New("the Claude launcher uses the macOS service; on this platform start claudex ctl run and connect Claude Code using the documented environment")
	}
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return fmt.Errorf("cannot read gateway settings at %s; run claudex ctl setup: %w", p.ConfigFile(), err)
	}
	if !exists(p.ServicePlist()) {
		return errors.New("the macOS service is not installed; run claudex ctl setup")
	}
	settings, err := Settings(cfg)
	if err != nil {
		return err
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	quiet := *l.Service
	quiet.Out = io.Discard
	if _, err := quiet.Start(cfg); err != nil {
		return err
	}
	control, err := quiet.ReadControl(context.Background(), cfg)
	if err != nil {
		return err
	}
	if control.Revision != cfg.Revision() {
		return errors.New("gateway settings differ from the config; run claudex ctl restart")
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
	argv := append([]string{claude, "--disallowedTools", "Artifact", "--settings", string(settingsJSON)}, args...)
	return l.Exec(claude, argv, environ)
}

// These Claude commands manage its own installation or local state. They work
// even when Claudex has not been configured or its gateway is unavailable.
func nativeClaudeCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h", "--version", "-v":
			return true
		}
	}
	switch args[0] {
	case "auth", "doctor", "gateway", "import", "install", "logs", "mcp", "plugin", "plugins", "project", "rm", "setup-token", "stop", "kill", "update", "upgrade":
		return true
	}
	return false
}
