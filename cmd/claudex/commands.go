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
	"strings"
	"syscall"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

const usageText = `Claudex starts Claude Code with GPT through a local Codex gateway.

  claudex [Claude arguments]       Start Claude Code (for example --resume)
  claudex ctl                      Show gateway health and preferences
  claudex ctl setup                Set up or repair Claudex
  claudex ctl config               Change preferences
  claudex ctl logs                 Read gateway logs
  claudex ctl restart              Restart the gateway

Also available: ctl start, stop, run, uninstall, --version, --help.
Claude options and commands, including --help and --version, pass through.
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

// errHelpShown ends a command after it printed its own -help output.
var errHelpShown = errors.New("help shown")

// Only ctl belongs to Claudex. Other arguments belong to Claude Code.
func dispatch(args []string) error {
	if len(args) > 0 && args[0] == "ctl" {
		if err := command(args[1:]); !errors.Is(err, errHelpShown) {
			return err
		}
		return nil
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") && interactive() {
		fmt.Fprintln(os.Stderr, "Claudex controls: claudex ctl --help")
	}
	return cmdLaunch(args)
}

func command(args []string) error {
	if len(args) == 0 {
		return cmdOverview(nil)
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
		return nil
	case "setup":
		return cmdSetup(rest)
	case "run":
		return runGateway(rest)
	case "config":
		return cmdConfig(rest)
	case "logs":
		return cmdLogs(rest)
	case "start", "stop", "restart":
		return cmdService(name, rest)
	case "uninstall":
		return cmdUninstall(rest)
	case "--version", "-v":
		if err := parseFlags(flag.NewFlagSet(name, flag.ContinueOnError), rest, "usage: claudex ctl --version"); err != nil {
			return err
		}
		fmt.Printf("claudex %s (%s; %s)\n", buildVersion(), Commit, BuildDate)
		return nil
	default:
		if strings.HasPrefix(name, "-") {
			return cmdOverview(args)
		}
		return usageError{fmt.Sprintf("unknown control command %q; run claudex ctl --help", name)}
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
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	noBrowser := set.Bool("no-browser", false, "Print the sign-in URL without opening a browser")
	noStart := set.Bool("no-start", false, "Install without starting the service")
	noLogin := set.Bool("no-login", false, "Initialize without signing in")
	forceLogin := set.Bool("login", false, "Sign in again even when credentials are valid")
	if err := parseFlags(set, args, "usage: claudex ctl setup [--config PATH] [--no-browser] [--no-login] [--login] [--no-start]"); err != nil {
		return err
	}
	if *noLogin && *forceLogin {
		return usageError{"choose either --login or --no-login"}
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	return launcher.WithControlLock(paths, func() error {
		if err := config.Initialize(*path); err != nil && !errors.Is(err, config.ErrExists) {
			return err
		}
		cfg, err := config.Load(*path)
		if err != nil {
			return err
		}
		ctx, stop := signalContext()
		defer stop()
		if *noLogin {
			fmt.Println("Codex sign-in skipped.")
		} else if _, errCheck := auth.NewStore(cfg.AuthFile).Check(); errCheck == nil && !*forceLogin {
			fmt.Println("Already signed in to Codex.")
		} else if !interactive() && !*noBrowser {
			return errors.New("Codex sign-in needs a terminal; use claudex ctl setup --no-browser or --no-login")
		} else if err := signIn(ctx, paths, *path, cfg, *noBrowser); err != nil {
			return err
		}
		if runtime.GOOS != "darwin" || *path != paths.ConfigFile() {
			fmt.Println("Config ready. Start the foreground gateway with: claudex ctl run")
			return nil
		}
		return launcher.InstallLocked(launcher.NewService(paths), launcher.InstallOptions{NoStart: *noStart, Context: ctx})
	})
}

func cmdService(name string, args []string) error {
	if err := parseFlags(flag.NewFlagSet(name, flag.ContinueOnError), args, "usage: claudex ctl "+name); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("claudex ctl %s manages the macOS service; elsewhere use claudex ctl run", name)
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
		return fmt.Errorf("cannot read gateway settings at %s; repair the config or rerun claudex ctl setup: %w", paths.ConfigFile(), err)
	}
	switch name {
	case "restart":
		ctx, cancel := signalContext()
		defer cancel()
		if service.Launchd.Loaded(launcher.Label) {
			release, err := service.Quiesce(ctx, cfg)
			if err != nil {
				return err
			}
			defer release()
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := service.Stop(); err != nil {
			return err
		}
	}
	return service.Report(cfg)
}

func cmdLaunch(args []string) error {
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	if len(args) == 0 && interactive() && runtime.GOOS == "darwin" {
		if _, err := os.Stat(paths.ServicePlist()); os.IsNotExist(err) {
			fmt.Println("Claudex will prepare your Codex sign-in and macOS service.")
			if err := cmdSetup(nil); err != nil {
				return err
			}
		}
	}
	return launcher.NewLauncher(launcher.NewService(paths)).Run(args)
}

func cmdUninstall(args []string) error {
	if err := parseFlags(flag.NewFlagSet("uninstall", flag.ContinueOnError), args, "usage: claudex ctl uninstall"); err != nil {
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
