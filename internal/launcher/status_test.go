package launcher

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

func statusFixture(t *testing.T, listen, key string) (*Status, config.Config, *fakeLaunchd) {
	t.Helper()
	s, launchd := testService(t)
	cfg := writeConfig(t, s, listen, key)
	status := &Status{ConfigPath: s.Paths.ConfigFile(), Paths: s.Paths, Launchd: launchd, HTTP: s.HTTP,
		LookPath: func(string) (string, error) { return "/fixture/claude", nil },
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }}
	return status, cfg, launchd
}

func checkLevels(checks []Check) map[string]string {
	levels := map[string]string{}
	for _, check := range checks {
		levels[check.Name] = check.Level
	}
	return levels
}

func TestStatusHealthyInstallation(t *testing.T) {
	key := "fixture-key-0123456789abcdef"
	server := gateway(t, key)
	status, cfg, launchd := statusFixture(t, strings.TrimPrefix(server.URL, "http://"), key)
	if err := os.MkdirAll(filepath.Dir(cfg.AuthFile), 0o700); err != nil {
		t.Fatal(err)
	}
	credential := `{"type":"codex","access_token":"t","account_id":"a","expired":"2026-01-01T01:00:00Z"}`
	if err := os.WriteFile(cfg.AuthFile, []byte(credential), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(status.Paths.Agent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(status.Paths.Agent, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchd.loaded = true
	checks := status.Run()
	var out bytes.Buffer
	if PrintChecks(&out, checks) {
		t.Fatalf("healthy installation reported failures:\n%s", out.String())
	}
	levels := checkLevels(checks)
	for _, name := range []string{"config", "client key", "codex sign-in", "gateway", "client key accepted", "macos service", "claude code"} {
		if levels[name] != LevelOK {
			t.Errorf("%s = %q\n%s", name, levels[name], out.String())
		}
	}
}

func TestStatusReportsActionableProblems(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close() // nothing answers on this port now
	status, cfg, _ := statusFixture(t, address, "fixture-key")
	if err := os.Chmod(cfg.ClientKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	status.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	checks := status.Run()
	var out bytes.Buffer
	if !PrintChecks(&out, checks) {
		t.Fatalf("missing sign-in not reported as a failure:\n%s", out.String())
	}
	levels := checkLevels(checks)
	want := map[string]string{"config": LevelOK, "client key": LevelWarn, "codex sign-in": LevelFail,
		"gateway": LevelWarn, "macos service": LevelOK, "claude code": LevelWarn}
	for name, level := range want {
		if levels[name] != level {
			t.Errorf("%s = %q, want %q\n%s", name, levels[name], level, out.String())
		}
	}
	if !strings.Contains(out.String(), "claudex -login") {
		t.Errorf("sign-in failure lacks a next step:\n%s", out.String())
	}
}

func TestStatusWithoutConfig(t *testing.T) {
	s, launchd := testService(t)
	status := &Status{ConfigPath: filepath.Join(t.TempDir(), "missing.json"), Paths: s.Paths, Launchd: launchd, HTTP: s.HTTP,
		LookPath: func(string) (string, error) { return "/fixture/claude", nil }, Now: time.Now}
	checks := status.Run()
	levels := checkLevels(checks)
	if levels["config"] != LevelFail || levels["gateway"] != "" || levels["codex sign-in"] != "" {
		t.Fatalf("checks after a missing config: %+v", checks)
	}
	if !strings.Contains(checks[0].Detail, "-init") {
		t.Errorf("config failure lacks a next step: %q", checks[0].Detail)
	}
}
