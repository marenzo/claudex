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
	"strconv"
	"syscall"
	"time"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/proxy"
)

var Version = "dev"
var Commit = "local"
var BuildDate = "unknown"

// runGateway serves the proxy in the foreground until interrupted.
func runGateway(args []string) error {
	set := flag.NewFlagSet("run", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	dashboard := set.Bool("dashboard", false, "Enable the authenticated HTTP usage dashboard")
	listen := set.String("listen", "", "Override bind IP:port; non-loopback requires this explicit flag")
	if err := parseFlags(set, args, "usage: claudex run [-config PATH] [-dashboard] [-listen IP:PORT]"); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
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
	// Bind before any sign-in prompt so a second gateway fails fast.
	listener, errListen := net.Listen("tcp", address)
	if errListen != nil {
		if errors.Is(errListen, syscall.EADDRINUSE) {
			return fmt.Errorf("%s is already in use; another claudex (or the installed service) may be running: try \"claudex status\" or change \"listen\" in the config", address)
		}
		return errListen
	}
	defer listener.Close()
	ctx, stop := signalContext()
	defer stop()
	store := auth.NewStore(cfg.AuthFile)
	if _, errSignIn := store.Check(); errSignIn != nil {
		// launchd and containers have nobody to finish a browser sign-in.
		if !interactive() {
			slog.Warn("Codex sign-in unavailable; requests will fail until you run claudex login", "reason", errSignIn.Error())
		} else {
			fmt.Println("No Codex sign-in found; signing in first.")
			if errLogin := store.Login(ctx, false, os.Stdout); errLogin != nil {
				return errLogin
			}
			fmt.Println("Codex subscription sign-in saved.")
		}
	}
	gateway, errGateway := proxy.New(cfg, codex.NewClient(store), Version)
	if errGateway != nil {
		return errGateway
	}
	if *dashboard {
		gateway.EnableDashboard()
	}
	server := &http.Server{Addr: address, Handler: gateway.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
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

// interactive reports whether a person at a terminal can complete a browser sign-in.
func interactive() bool {
	for _, file := range []*os.File{os.Stdin, os.Stdout} {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
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
	err := dispatch(os.Args[1:])
	if err == nil {
		return
	}
	var serving serveError
	if errors.As(err, &serving) {
		slog.Error("claudex stopped", "error", serving.err)
		os.Exit(1)
	}
	// Startup and command-line errors are read by a person at a terminal.
	fmt.Fprintln(os.Stderr, "claudex:", err)
	var misuse usageError
	if errors.As(err, &misuse) {
		os.Exit(2)
	}
	os.Exit(1)
}
