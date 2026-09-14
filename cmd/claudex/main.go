package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
	"github.com/marenzo/claudex/internal/proxy"
)

var Version = "dev"
var Commit = "local"
var BuildDate = "unknown"

func run() error {
	path := flag.String("config", config.DefaultPath(), "Path to the local JSON config")
	initialize := flag.Bool("init", false, "Create a private default config and local client key")
	login := flag.Bool("login", false, "Sign in with a Codex subscription")
	noBrowser := flag.Bool("no-browser", false, "Print the sign-in URL without opening a browser")
	dashboard := flag.Bool("dashboard", false, "Enable the authenticated HTTP usage dashboard")
	listen := flag.String("listen", "", "Override bind IP:port; non-loopback requires this explicit flag")
	version := flag.Bool("version", false, "Print build version")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 0 {
		return usageError{fmt.Sprintf("unexpected argument %q; commands are install, service, launch, and status; use -help for flags", flag.Arg(0))}
	}
	if (*initialize && *login) || (*version && (*initialize || *login)) {
		return usageError{"use only one of -init, -login, or -version"}
	}
	if *noBrowser && !*login {
		return usageError{"-no-browser requires -login"}
	}
	if *version {
		fmt.Printf("claudex %s (%s; %s)\n", buildVersion(), Commit, BuildDate)
		return nil
	}
	if *initialize {
		if err := config.Initialize(*path); err != nil {
			return err
		}
		fmt.Printf("Claudex config ready: %s\nSign in next with: claudex -config %q -login\n", *path, *path)
		return nil
	}
	cfg, errConfig := config.Load(*path)
	if errConfig != nil {
		return errConfig
	}
	address := cfg.Listen
	if *listen != "" {
		if err := validateListen(*listen); err != nil {
			return err
		}
		address = *listen
	}
	if host, _, errSplit := net.SplitHostPort(address); errSplit == nil {
		if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
			slog.Warn("listening beyond loopback: the client key travels over plaintext HTTP, so expose this port only to trusted networks", "listen", address)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store := auth.NewStore(cfg.AuthFile)
	if *login {
		if errLogin := store.Login(ctx, *noBrowser, os.Stdout); errLogin != nil {
			return errLogin
		}
		fmt.Println("Codex subscription sign-in saved.")
		return nil
	}
	if _, errSignIn := store.Check(); errSignIn != nil {
		slog.Warn("Codex sign-in unavailable; requests will fail until you run claudex -login or claudex service login", "reason", errSignIn.Error())
	}
	gateway, errGateway := proxy.New(cfg, codex.NewClient(store), Version)
	if errGateway != nil {
		return errGateway
	}
	if *dashboard {
		gateway.EnableDashboard()
	}
	server := &http.Server{Addr: address, Handler: gateway.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	listener, errListen := net.Listen("tcp", address)
	if errListen != nil {
		if errors.Is(errListen, syscall.EADDRINUSE) {
			return fmt.Errorf("%s is already in use; another claudex (or the installed service) may be running: try \"claudex service status\" or change \"listen\" in the config", address)
		}
		return errListen
	}
	go gateway.RunRecovery(ctx)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	slog.Info("claudex started", "version", Version, "listen", address, "model", cfg.Model, "quota_recheck", "1m", "dashboard", *dashboard)
	select {
	case errServe := <-served:
		if errors.Is(errServe, http.ErrServerClosed) {
			return nil
		}
		return serveError{errServe}
	case <-ctx.Done():
		// Create the shutdown deadline at shutdown, never at service startup.
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if errShutdown := server.Shutdown(shutdown); errShutdown != nil {
			if errClose := server.Close(); errClose != nil {
				slog.Debug("server close failed")
			}
			return serveError{errShutdown}
		}
		return nil
	}
}

func validateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return fmt.Errorf("listen override must contain an IP and numeric port, such as 0.0.0.0:8317")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("listen port must be between 1 and 65535")
	}
	return nil
}

func main() {
	if handled, err := subcommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudex:", err)
			var misuse usageError
			if launcher.IsUsage(err) || errors.As(err, &misuse) {
				os.Exit(2)
			}
			os.Exit(1)
		}
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if errRun := run(); errRun != nil {
		var serving serveError
		if errors.As(errRun, &serving) {
			slog.Error("claudex stopped", "error", serving.err)
			os.Exit(1)
		}
		// Startup and command-line errors are read by a person at a terminal.
		fmt.Fprintln(os.Stderr, "claudex:", errRun)
		var misuse usageError
		if errors.As(errRun, &misuse) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
