package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

const RedirectURI = "http://localhost:1455/auth/callback"

func randomString() string {
	var random [32]byte
	if _, errRead := rand.Read(random[:]); errRead != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(random[:])
}

func AuthorizationURL(state, verifier string) string {
	challenge := sha256.Sum256([]byte(verifier))
	values := url.Values{
		"client_id": {ClientID}, "response_type": {"code"}, "redirect_uri": {RedirectURI},
		"scope": {"openid email profile offline_access"}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
		"prompt": {"login"}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"},
	}
	return "https://auth.openai.com/oauth/authorize?" + values.Encode()
}

func (s *Store) Login(parent context.Context, noBrowser bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	state, verifier := randomString(), randomString()
	if state == "" || verifier == "" {
		return fmt.Errorf("could not generate OAuth state")
	}
	listener, errListen := net.Listen("tcp4", "127.0.0.1:1455")
	if errListen != nil {
		return fmt.Errorf("start Codex callback listener: %w", errListen)
	}
	code := make(chan string, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid login state.", http.StatusBadRequest)
			return
		}
		value := r.URL.Query().Get("code")
		if value == "" {
			http.Error(w, "Login did not return a code. Retry in the terminal.", http.StatusBadRequest)
			return
		}
		once.Do(func() { code <- value })
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, errWrite := io.WriteString(w, "Authorization received. Return to the terminal to finish signing in.\n"); errWrite != nil {
			slog.Debug("OAuth callback response write failed")
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	defer func() {
		if errClose := server.Close(); errClose != nil {
			slog.Debug("OAuth callback listener close failed")
		}
	}()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	loginURL := AuthorizationURL(state, verifier)
	if _, errWrite := fmt.Fprintf(out, "Sign in with your Codex subscription:\n%s\n", loginURL); errWrite != nil {
		return errWrite
	}
	if !noBrowser {
		var command *exec.Cmd
		if runtime.GOOS == "darwin" {
			command = exec.CommandContext(ctx, "open", loginURL)
		} else if runtime.GOOS == "linux" {
			command = exec.CommandContext(ctx, "xdg-open", loginURL)
		}
		if command != nil {
			done := startBrowser(ctx, command)
			defer func() {
				cancel()
				<-done
			}()
		}
	}
	select {
	case value := <-code:
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		unlock, errLock := s.lock(ctx)
		if errLock != nil {
			return errLock
		}
		defer unlock()
		_, errExchange := s.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {ClientID}, "code": {value}, "redirect_uri": {RedirectURI}, "code_verifier": {verifier}}, nil)
		return errExchange
	case errServe := <-served:
		return errServe
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Some browser openers stay running until the browser exits. Reap the opener
// without making receipt of the OAuth callback depend on that process exiting.
func startBrowser(ctx context.Context, command *exec.Cmd) <-chan struct{} {
	done := make(chan struct{})
	if err := command.Start(); err != nil {
		slog.Info("Open the printed sign-in URL in your browser")
		close(done)
		return done
	}
	go func() {
		defer close(done)
		if err := command.Wait(); err != nil && ctx.Err() == nil {
			slog.Info("Open the printed sign-in URL in your browser")
		}
	}()
	return done
}
