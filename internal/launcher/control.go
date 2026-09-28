package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

// WithControlLock serializes changes to the installed service and config.
func WithControlLock(p Paths, run func() error) error {
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(p.State, "control.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return run()
}

type ControlStatus struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Active    int    `json:"active"`
	Draining  bool   `json:"draining"`
	Dashboard bool   `json:"dashboard"`
}

func Revision(cfg config.Config) string {
	data, _ := json.Marshal(cfg)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) control(ctx context.Context, cfg config.Config, method, path, token string, result any) error {
	key, err := os.ReadFile(cfg.ClientKeyFile)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+cfg.Listen+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	if token != "" {
		req.Header.Set("X-Claudex-Drain-Token", token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		if resp.StatusCode == http.StatusNotFound {
			return errors.New("the running gateway does not support controlled restarts; stop it before applying changes")
		}
		return fmt.Errorf("gateway control returned HTTP %d", resp.StatusCode)
	}
	if result == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
}

func (s *Service) ReadControl(ctx context.Context, cfg config.Config) (ControlStatus, error) {
	var status ControlStatus
	err := s.control(ctx, cfg, http.MethodGet, "/_claudex/status", "", &status)
	return status, err
}

// Drain waits for current generation requests to finish. The returned release
// function is safe after a service stop, when the old server is already gone.
func (s *Service) Drain(ctx context.Context, cfg config.Config) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	status, err := s.ReadControl(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if status.Revision != Revision(cfg) {
		return nil, errors.New("the running gateway uses different config; stop it before applying changes")
	}
	var lease struct {
		Token  string `json:"token"`
		Active int    `json:"active"`
	}
	if err := s.control(ctx, cfg, http.MethodPost, "/_claudex/drain", "", &lease); err != nil {
		return nil, err
	}
	release := func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = s.control(cleanup, cfg, http.MethodDelete, "/_claudex/drain", lease.Token, nil)
	}
	if lease.Token == "" {
		return nil, errors.New("gateway returned no drain lease")
	}
	if lease.Active > 0 {
		fmt.Fprintf(s.Out, "Waiting for %d active gateway request(s) to finish...\n", lease.Active)
	}
	for lease.Active > 0 {
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if err := s.control(ctx, cfg, http.MethodPost, "/_claudex/drain", lease.Token, &lease); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

// ApplyConfig changes one persisted source of truth and restores old state if
// the new service fails its readiness check.
func ApplyConfig(ctx context.Context, s *Service, path string, old, next config.Config) error {
	if old == next {
		return nil
	}
	if err := next.Validate(); err != nil {
		return err
	}
	return WithControlLock(s.Paths, func() (err error) {
		current, err := config.Load(path)
		if err != nil {
			return err
		}
		if current != old {
			return errors.New("config changed in another command; read it again and retry")
		}
		running := goos == "darwin" && path == s.Paths.ConfigFile() && s.Launchd.Loaded(Label)
		var release func()
		if running {
			release, err = s.Drain(ctx, old)
			if err != nil {
				return err
			}
			defer release()
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = s.Stop(); err != nil {
				return err
			}
		}
		oldPlist, plistErr := os.ReadFile(s.Paths.ServicePlist())
		oldAgent, agentErr := os.ReadFile(s.Paths.Agent)
		defer func() {
			if err == nil {
				return
			}
			_ = config.Save(path, old)
			if plistErr == nil {
				_ = atomicWrite(s.Paths.ServicePlist(), oldPlist, 0o600)
			}
			if agentErr == nil {
				_ = atomicWrite(s.Paths.Agent, oldAgent, 0o600)
			}
			if running {
				_, recovery := s.Start(old)
				err = errors.Join(err, recovery)
			}
		}()
		if err = config.Save(path, next); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if goos == "darwin" && path == s.Paths.ConfigFile() {
			if err = atomicWrite(s.Paths.ServicePlist(), plist(s.Paths), 0o600); err != nil {
				return err
			}
			if err = atomicWrite(s.Paths.Agent, plist(s.Paths), 0o600); err != nil {
				return err
			}
		}
		if running {
			if _, err = s.Start(next); err != nil {
				return err
			}
			var actual ControlStatus
			actual, err = s.ReadControl(ctx, next)
			if err != nil {
				return err
			}
			if actual.Revision != Revision(next) || actual.Dashboard != next.Dashboard {
				return errors.New("gateway started with different settings")
			}
		}
		return nil
	})
}
