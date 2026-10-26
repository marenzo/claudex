package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/feedback"
	"github.com/marenzo/claudex/internal/handoff"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func LogPath(path string) string { return filepath.Join(path+".runtime", "gateway.log") }

// Ensure starts at most one worker, without registering any operating-system service.
func Ensure(ctx context.Context, path string, c *control.Client) (control.Status, error) {
	var status control.Status
	err := config.WithLock(ctx, path+".startup", func() error {
		var err error
		status, err = c.Status(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("cannot reuse gateway: %w", err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		if _, err := auth.NewStore(cfg.AuthFile).Check(); err != nil {
			return err
		}
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(binary, "ctl", "run", "--config", path)
		cmd.Env = append(os.Environ(), "CLAUDEX_SESSION_WORKER=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer null.Close()
		cmd.Stdin = null
		cmd.Stdout = null
		cmd.Stderr = null
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(75 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return ctx.Err()
			case <-deadline.C:
				_ = cmd.Process.Kill()
				return errors.New("gateway startup timed out; check claudex ctl logs")
			case err := <-done:
				return fmt.Errorf("gateway exited during startup (%v); check claudex ctl logs", err)
			case <-ticker.C:
				status, err = c.Status(ctx)
				if err == nil {
					return nil
				}
				if !errors.Is(err, syscall.ECONNREFUSED) {
					return err
				}
			}
		}
	})
	return status, err
}

func ClaudePath() (string, error) {
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".local", "bin", "claude")
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0111 != 0 {
		return path, nil
	}
	return "", errors.New("install Claude Code and put claude on PATH")
}

var stripped = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_MANTLE"}

func Environment(settings ClaudeSettings, address, key string) []string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
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
	env["ANTHROPIC_BASE_URL"] = address
	env["ANTHROPIC_AUTH_TOKEN"] = key
	out := make([]string, 0, len(env))
	for name, value := range env {
		out = append(out, name+"="+value)
	}
	return out
}

var ErrAttach = errors.New("gateway attachment failed")

// Run keeps the native CLI on the same terminal and retains the gateway session
// until the CLI exits. Terminal interrupts already reach the child process group.
func Run(claude string, args []string, cfg config.Config, c *control.Client) error {
	settings, err := Settings(cfg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lost, err := c.Attach(ctx, "claude")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAttach, err)
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	pluginPath, err := handoff.InstallPlugin(filepath.Join(filepath.Dir(cfg.ClientKeyFile), "claudex.runtime"))
	if err != nil {
		return fmt.Errorf("prepare built-in Codex skill: %w", err)
	}
	launchArgs := []string{"--plugin-dir", pluginPath, "--disallowedTools", "Artifact,SendFeedback,SendUserFeedback", "--settings", string(data)}
	if cfg.Feedback.Enabled {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		mcp, err := FeedbackMCPConfig(binary, c.URL, cfg.ClientKeyFile, hex.EncodeToString(id))
		if err != nil {
			return err
		}
		launchArgs = append(launchArgs, "--mcp-config", string(mcp))
	}
	cmd := exec.Command(claude, append(launchArgs, RewriteArgs(args)...)...)
	cmd.Env = append(Environment(settings, c.URL, c.Key), "CLAUDEX_BINARY="+binary)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			return err
		case sig := <-signals:
			if sig != os.Interrupt {
				_ = cmd.Process.Signal(sig)
			}
		case <-lost:
			// Keep the CLI alive; an external persistent gateway can restart. Reattach
			// ownership when it returns, while leaving Claude's retries in charge.
			lost = nil
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
						if next, e := c.Attach(ctx, "claude"); e == nil {
							select {
							case <-next:
							case <-ctx.Done():
								return
							}
						}
					}
				}
			}()
		}
	}
}

// FeedbackMCPConfig adds a session-only server without editing the user's MCP
// files or putting the gateway key in process arguments.
func FeedbackMCPConfig(binary, address, keyFile, launchID string) ([]byte, error) {
	return json.Marshal(map[string]any{"mcpServers": map[string]any{feedback.MCPName: map[string]any{
		"type": "stdio", "command": binary,
		"args":       []string{"ctl", "feedback-mcp", "--url", address, "--key-file", keyFile, "--launch-id", launchID},
		"alwaysLoad": true,
	}}})
}
func NativeCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "--version" || args[0] == "-v") {
		return true
	}
	switch args[0] {
	case "auth", "doctor", "gateway", "import", "install", "logs", "mcp", "plugin", "plugins", "project", "rm", "setup-token", "stop", "kill", "update", "upgrade":
		return true
	}
	return false
}
