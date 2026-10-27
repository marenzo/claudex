package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/handoff"
	"github.com/marenzo/claudex/internal/launcher"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const usageText = `claudex runs Claude Code with GPT through your Codex subscription.

  claudex [Claude arguments]     Start Claude with an automatic gateway
  claudex ctl                    Open the terminal dashboard
  claudex ctl config             Open dashboard settings
  claudex ctl status [--json]     Inspect the running gateway
  claudex ctl feedback on|off    Enable or disable local feedback collection
  claudex ctl feedback list      Read collected observations as JSON
  claudex ctl handoff --to HOST  Continue in native Codex or Claude via claudex
  claudex ctl imagegen           Generate or edit an image through Codex
  claudex ctl setup              Prepare config, Codex skill, and sign-in
  claudex ctl run                Run a persistent foreground gateway
  claudex ctl logs [--follow]    Read gateway logs
  claudex ctl --version          Show gateway version

Only ctl belongs to claudex; other arguments pass through to Claude Code.
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

var errHelpShown = errors.New("help shown")

func dispatch(args []string) error {
	if len(args) > 0 && args[0] == "ctl" {
		err := command(args[1:])
		if errors.Is(err, errHelpShown) {
			return nil
		}
		return err
	}
	return cmdLaunch(args)
}
func command(args []string) error {
	if len(args) == 0 {
		return cmdTUI(nil, false)
	}
	rest := args[1:]
	switch args[0] {
	case "help", "--help", "-h":
		usage(os.Stdout)
		return nil
	case "setup":
		return cmdSetup(rest)
	case "run":
		return runGateway(rest)
	case "config":
		return cmdTUI(rest, true)
	case "status":
		return cmdStatus(rest)
	case "logs":
		return cmdLogs(rest)
	case "feedback":
		return cmdFeedback(rest)
	case "handoff":
		return cmdHandoff(rest)
	case "imagegen":
		return cmdImagegen(rest)
	case "handoff-session":
		if len(rest) != 0 {
			return usageError{"handoff-session takes hook input on stdin"}
		}
		return handoff.CaptureSession(os.Stdin, os.Getenv("CLAUDE_ENV_FILE"))
	case "feedback-mcp":
		return cmdFeedbackMCP(rest)
	case "--version":
		if err := parseFlags(flag.NewFlagSet("version", flag.ContinueOnError), rest, "usage: claudex ctl --version"); err != nil {
			return err
		}
		fmt.Printf("claudex %s (%s; %s)\n", buildVersion(), Commit, BuildDate)
		return nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return cmdTUI(args, false)
		}
		return usageError{fmt.Sprintf("unknown control command %q; run claudex ctl --help", args[0])}
	}
}
func parseFlags(set *flag.FlagSet, args []string, line string) error {
	set.SetOutput(io.Discard)
	err := set.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(line)
		set.SetOutput(os.Stdout)
		set.PrintDefaults()
		return errHelpShown
	}
	if err != nil || set.NArg() != 0 {
		return usageError{line}
	}
	return nil
}
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}
func cmdSetup(args []string) error {
	set := flag.NewFlagSet("setup", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Configuration path")
	noBrowser := set.Bool("no-browser", false, "Print sign-in URL without opening a browser")
	noLogin := set.Bool("no-login", false, "Prepare configuration without sign-in")
	login := set.Bool("login", false, "Sign in again")
	if err := parseFlags(set, args, "usage: claudex ctl setup [--config PATH] [--no-browser] [--no-login|--login]"); err != nil {
		return err
	}
	if *noLogin && *login {
		return usageError{"choose either --login or --no-login"}
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	err = config.WithLock(ctx, absolute, func() error {
		err := config.Initialize(absolute)
		if errors.Is(err, config.ErrExists) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	cfg, err := config.Load(absolute)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	skill, err := handoff.InstallCodexSkill(ctx, home, binary)
	if err != nil {
		return fmt.Errorf("install Codex handoff skill: %w", err)
	}
	store := auth.NewStore(cfg.AuthFile)
	if !*noLogin {
		_, err = store.Check()
		if err != nil || *login {
			if !interactive() && !*noBrowser {
				return errors.New("sign-in needs a terminal; use ctl setup --no-browser or --no-login")
			}
			if err := store.Login(ctx, *noBrowser, os.Stdout); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Codex handoff skill ready: %s\n", skill)
	fmt.Println("Configuration ready. Run claudex, or claudex ctl run for a persistent foreground gateway.")
	return nil
}
func cmdLaunch(args []string) error {
	claude, err := launcher.ClaudePath()
	if err != nil {
		return err
	}
	if launcher.NativeCommand(args) {
		return syscall.Exec(claude, append([]string{claude}, args...), os.Environ())
	}
	path, err := filepath.Abs(config.DefaultPath())
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if !interactive() {
			return errors.New("run claudex ctl setup before noninteractive use")
		}
		if err := cmdSetup([]string{"--config", path}); err != nil {
			return err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	c, err := control.New("http://"+cfg.Listen, cfg.ClientKeyFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for attempt := 0; attempt < 10; attempt++ {
		status, err := launcher.Ensure(ctx, path, c)
		if errors.Is(err, auth.ErrNotSignedIn) && interactive() {
			if err := cmdSetup([]string{"--config", path, "--login"}); err != nil {
				return err
			}
			continue
		}
		if err == nil {
			var active config.Config
			active, err = status.Preferences.Apply(cfg)
			if err == nil {
				err = launcher.Run(claude, args, active, c)
			}
		}
		var response *control.HTTPError
		transient := errors.As(err, &response) && response.Status == 503
		transient = transient || errors.Is(err, syscall.ECONNREFUSED)
		if !transient {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("gateway was shutting down repeatedly; retry claudex")

}
func buildVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}

type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

type serveError struct{ err error }

func (e serveError) Error() string { return e.err.Error() }
func (e serveError) Unwrap() error { return e.err }
