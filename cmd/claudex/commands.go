package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

const usageText = `Claudex runs Claude Code against a Codex subscription through a local gateway.

Usage:
  claudex <command> [flags]

Commands:
  setup        Initialize, sign in, and install the service (macOS)
  run          Run the gateway in the foreground
  init         Create the config and a client key
  login        Sign in with a Codex subscription
  install      Install and start the launchd service (macOS)
  start        Start the installed service (macOS)
  stop         Stop the installed service (macOS)
  restart      Restart the installed service (macOS)
  status       Check config, sign-in, gateway, and service
  models       List models from the running gateway
  launch       Start Claude Code through the gateway (macOS)
  uninstall    Remove the installed service and wrappers (macOS)
  version      Print build version
  help         Show this help

Run claudex <command> -help for command-specific flags.
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

// errHelpShown ends a command after it printed its own -help output.
var errHelpShown = errors.New("help shown")

// dispatch runs the command named by args; no arguments prints help.
func dispatch(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	if err := command(args[0], args[1:]); !errors.Is(err, errHelpShown) {
		return err
	}
	return nil
}

func command(name string, args []string) error {
	switch name {
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
		return nil
	case "setup":
		return cmdSetup(args)
	case "run":
		return runGateway(args)
	case "init":
		return cmdInit(args)
	case "login":
		return cmdLogin(args)
	case "install":
		return cmdInstall(args)
	case "start", "stop", "restart", "models":
		return cmdService(name, args)
	case "status":
		return cmdStatus(args)
	case "launch":
		return cmdLaunch(args)
	case "uninstall":
		return cmdUninstall(args)
	case "version":
		if err := parseFlags(flag.NewFlagSet(name, flag.ContinueOnError), args, "usage: claudex version"); err != nil {
			return err
		}
		fmt.Printf("claudex %s (%s; %s)\n", buildVersion(), Commit, BuildDate)
		return nil
	default:
		return usageError{fmt.Sprintf("unknown command %q; run claudex help", name)}
	}
}

// parseFlags parses a command's flags. It rejects positional arguments, and
// -help prints the usage line with the flag defaults.
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
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func cmdInit(args []string) error {
	set := flag.NewFlagSet("init", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	if err := parseFlags(set, args, "usage: claudex init [-config PATH]"); err != nil {
		return err
	}
	if err := config.Initialize(*path); err != nil {
		return err
	}
	next := "claudex login"
	if *path != config.DefaultPath() {
		next += fmt.Sprintf(" -config %q", *path)
	}
	fmt.Printf("Claudex config ready: %s\nSign in next with: %s\n", *path, next)
	return nil
}

func cmdLogin(args []string) error {
	set := flag.NewFlagSet("login", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	noBrowser := set.Bool("no-browser", false, "Print the sign-in URL without opening a browser")
	if err := parseFlags(set, args, "usage: claudex login [-no-browser] [-config PATH]"); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	return signIn(ctx, paths, *path, cfg, *noBrowser)
}

// signIn pauses the installed macOS service while signing in to its credential
// store, so sign-in and token refresh never race on the same file.
func signIn(ctx context.Context, paths launcher.Paths, path string, cfg config.Config, noBrowser bool) error {
	if runtime.GOOS == "darwin" && path == paths.ConfigFile() {
		return launcher.NewService(paths).SignIn(ctx, cfg, noBrowser)
	}
	if err := auth.NewStore(cfg.AuthFile).Login(ctx, noBrowser, os.Stdout); err != nil {
		return err
	}
	fmt.Println("Codex subscription sign-in saved.")
	return nil
}

func cmdSetup(args []string) error {
	set := flag.NewFlagSet("setup", flag.ContinueOnError)
	noBrowser := set.Bool("no-browser", false, "Print the sign-in URL without opening a browser")
	dashboard := set.Bool("dashboard", false, "Enable the local usage dashboard")
	noStart := set.Bool("no-start", false, "Install without starting the service")
	if err := parseFlags(set, args, "usage: claudex setup [-no-browser] [-dashboard] [-no-start]"); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("setup installs the macOS service; elsewhere run claudex init, claudex login, then claudex run")
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	path := paths.ConfigFile()
	if err := config.Initialize(path); err != nil && !errors.Is(err, config.ErrExists) {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	if _, errCheck := auth.NewStore(cfg.AuthFile).Check(); errCheck == nil {
		fmt.Println("Already signed in to Codex.")
	} else if err := signIn(ctx, paths, path, cfg, *noBrowser); err != nil {
		return err
	}
	return launcher.Install(launcher.NewService(paths), launcher.InstallOptions{Dashboard: *dashboard, NoStart: *noStart})
}

func cmdInstall(args []string) error {
	set := flag.NewFlagSet("install", flag.ContinueOnError)
	dashboard := set.Bool("dashboard", false, "Enable the local usage dashboard")
	noStart := set.Bool("no-start", false, "Install without starting the service")
	if err := parseFlags(set, args, "usage: claudex install [-dashboard] [-no-start]"); err != nil {
		return err
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	return launcher.Install(launcher.NewService(paths), launcher.InstallOptions{Dashboard: *dashboard, NoStart: *noStart})
}

func cmdService(name string, args []string) error {
	if err := parseFlags(flag.NewFlagSet(name, flag.ContinueOnError), args, "usage: claudex "+name); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" && name != "models" {
		return fmt.Errorf("claudex %s manages the macOS service; elsewhere use claudex run", name)
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	service := launcher.NewService(paths)
	// Stopping a broken installation must not depend on its config or key.
	if name == "stop" {
		if err := service.Stop(); err != nil {
			return err
		}
		fmt.Println("Claudex stopped.")
		return nil
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return fmt.Errorf("cannot read gateway settings at %s; repair the config or rerun claudex install: %w", paths.ConfigFile(), err)
	}
	switch name {
	case "models":
		models, err := service.Models(cfg)
		if err != nil {
			return err
		}
		for _, model := range models {
			fmt.Println(model)
		}
		return nil
	case "restart":
		if err := service.Stop(); err != nil {
			return err
		}
	}
	return service.Report(cfg)
}

func cmdStatus(args []string) error {
	set := flag.NewFlagSet("status", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	if err := parseFlags(set, args, "usage: claudex status [-config PATH]"); err != nil {
		return err
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	if launcher.PrintChecks(os.Stdout, launcher.NewStatus(paths, *path).Run()) {
		return errors.New("one or more checks failed")
	}
	return nil
}

func cmdLaunch(args []string) error {
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	return launcher.NewLauncher(launcher.NewService(paths)).Run(args)
}

func cmdUninstall(args []string) error {
	if err := parseFlags(flag.NewFlagSet("uninstall", flag.ContinueOnError), args, "usage: claudex uninstall"); err != nil {
		return err
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	return launcher.Uninstall(launcher.NewService(paths))
}

// buildVersion falls back to the Go module version when no -ldflags stamp is set.
func buildVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}

// usageError is a command-line mistake; main exits with status 2.
type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

// serveError happened after the gateway started serving; main logs it as JSON.
type serveError struct{ err error }

func (e serveError) Error() string { return e.err.Error() }

func (e serveError) Unwrap() error { return e.err }
