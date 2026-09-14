package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

const usageText = `Claudex runs Claude Code against a Codex subscription through a local gateway.

Usage:
  claudex [flags]                 run the gateway in the foreground
  claudex -init                   create ~/.config/claudex/config.json and a client key
  claudex -login [-no-browser]    sign in with the Codex subscription
  claudex install [-dashboard] [-no-start]
                                  macOS: install and start the launchd user service
  claudex service <command>       macOS: start|stop|restart|status|models|login [-no-browser]
  claudex launch [claude args]    start the installed claude CLI through the gateway
  claudex status [-config PATH]   check config, sign-in, gateway, service, and Claude Code

Flags:
`

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), usageText)
	flag.PrintDefaults()
}

// subcommand dispatches install, service, launch, and status. It reports whether the
// arguments named a subcommand; gateway flags are handled by run otherwise.
func subcommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "install", "service", "launch", "status":
	default:
		return false, nil
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return true, err
	}
	service := launcher.NewService(paths)
	switch args[0] {
	case "install":
		set := flag.NewFlagSet("claudex install", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		dashboard := set.Bool("dashboard", false, "Enable the local usage dashboard")
		noStart := set.Bool("no-start", false, "Install without starting the service")
		if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
			return true, usageError{"usage: claudex install [-dashboard] [-no-start]"}
		}
		return true, launcher.Install(service, launcher.InstallOptions{Dashboard: *dashboard, NoStart: *noStart})
	case "service":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return true, service.Run(ctx, args[1:])
	case "status":
		set := flag.NewFlagSet("claudex status", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
		if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
			return true, usageError{"usage: claudex status [-config PATH]"}
		}
		if launcher.PrintChecks(os.Stdout, launcher.NewStatus(paths, *path).Run()) {
			return true, errors.New("one or more checks failed")
		}
		return true, nil
	default:
		return true, launcher.NewLauncher(service).Run(args[1:])
	}
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
