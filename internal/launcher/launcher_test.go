package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

type fakeLaunchd struct {
	mu        sync.Mutex
	loaded    bool
	calls     []string
	bootstrap error
}

func (f *fakeLaunchd) Loaded(string) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.loaded }
func (f *fakeLaunchd) Bootout(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "bootout")
	f.loaded = false
	return nil
}
func (f *fakeLaunchd) Bootstrap(plist string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "bootstrap "+plist)
	if f.bootstrap != nil {
		return f.bootstrap
	}
	f.loaded = true
	return nil
}

func gateway(t *testing.T, key string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "product": "claudex", "version": "test"})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "gpt-6-astra"}, {"id": "gpt-5.6-luna"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testService(t *testing.T) (*Service, *fakeLaunchd) {
	t.Helper()
	goos = "darwin"
	root := t.TempDir()
	paths := Paths{Home: filepath.Join(root, "home"), State: filepath.Join(root, "state"), Config: filepath.Join(root, "config"),
		Bin: filepath.Join(root, "bin"), Agent: filepath.Join(root, "LaunchAgents", Label+".plist")}
	launchd := &fakeLaunchd{}
	service := &Service{Paths: paths, Launchd: launchd, HTTP: &http.Client{Timeout: time.Second}, Out: io.Discard,
		Timeout: 500 * time.Millisecond, Sleep: func(time.Duration) {},
		Login: func(context.Context, config.Config, bool, io.Writer) error { return nil }}
	return service, launchd
}

func writeConfig(t *testing.T, s *Service, listen string, key string) config.Config {
	t.Helper()
	cfg := config.DefaultsFor(s.Paths.Config)
	cfg.Listen = listen
	if err := os.MkdirAll(s.Paths.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if key != "" {
		if err := os.WriteFile(cfg.ClientKeyFile, []byte(key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(s.Paths.ConfigFile(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func fixtureBinary(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "claudex")
	if err := os.WriteFile(source, []byte("fixture binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSettingsNormalizeAliasesAndFamilies(t *testing.T) {
	cfg := config.DefaultsFor("/fixture")
	for alias, canonical := range map[string]string{"terra": "gpt-5.6-terra", " GPT-5.6-TERRA ": "gpt-5.6-terra",
		"claude-sonnet-4-6[1m]": "gpt-5.6-terra", "Opus": "gpt-5.6-sol", "claude-haiku": "gpt-5.6-luna", "FABLE": "gpt-6-astra"} {
		cfg.Model = alias
		settings, err := Settings(cfg)
		if err != nil {
			t.Fatalf("%q: %v", alias, err)
		}
		if settings.Model != canonical || settings.Env["ANTHROPIC_MODEL"] != canonical || settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != canonical {
			t.Errorf("%q resolved to %q", alias, settings.Model)
		}
	}
	cfg.Model = "terra"
	cfg.ReasoningEffort = "ultra"
	settings, _ := Settings(cfg)
	if settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] != "GPT-5.6 Terra (Codex subscription)" {
		t.Errorf("unexpected option name %q", settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"])
	}
	if settings.EffortLevel != "ultracode" || !reflect.DeepEqual(settings.Permissions.Deny, []string{"Artifact"}) {
		t.Errorf("unexpected settings %+v", settings)
	}
	for family, target := range map[string]string{"HAIKU": "gpt-5.6-luna", "SONNET": "gpt-5.6-terra", "OPUS": "gpt-5.6-sol", "FABLE": "gpt-6-astra"} {
		if settings.Env["ANTHROPIC_DEFAULT_"+family+"_MODEL"] != target {
			t.Errorf("%s mapped to %q", family, settings.Env["ANTHROPIC_DEFAULT_"+family+"_MODEL"])
		}
	}
	if settings.Env["ANTHROPIC_SMALL_FAST_MODEL"] != "gpt-5.6-luna" || settings.Env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Errorf("unexpected env %v", settings.Env)
	}
	if _, ok := settings.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("settings file must not contain the client key")
	}
	cfg.Model = "unknown-model"
	if _, err := Settings(cfg); err == nil || !strings.Contains(err.Error(), "unsupported model") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestRewriteArgsPreservesClaudeArguments(t *testing.T) {
	for _, test := range []struct{ in, want []string }{
		{[]string{"--model", "terra", "--effort=ultra"}, []string{"--model", "gpt-5.6-terra", "--effort=ultracode"}},
		{[]string{"--model=opus", "--effort", "max"}, []string{"--model=opus", "--effort", "max"}},
		{[]string{"--model=SOL", "--effort", "ultra"}, []string{"--model=gpt-5.6-sol", "--effort", "ultracode"}},
		{[]string{"--", "--model=sol", "--effort=ultra"}, []string{"--", "--model=sol", "--effort=ultra"}},
	} {
		if got := RewriteArgs(test.in); !reflect.DeepEqual(got, test.want) {
			t.Errorf("RewriteArgs(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestInstallWritesEverythingAndStartsService(t *testing.T) {
	s, launchd := testService(t)
	server := gateway(t, "fixture-key")
	cfg := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-key")
	if err := os.MkdirAll(s.Paths.Bin, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := s.Paths.Wrapper("claude-gpt")
	if err := os.WriteFile(previous, []byte("previous launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), Dashboard: true}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(s.Paths.Binary()); string(data) != "fixture binary" {
		t.Errorf("binary not installed: %q", data)
	}
	if info, _ := os.Stat(s.Paths.Binary()); info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode %v", info.Mode())
	}
	if data, _ := os.ReadFile(cfg.ClientKeyFile); string(data) != "fixture-key\n" {
		t.Errorf("existing key replaced: %q", data)
	}
	loaded, err := config.Load(s.Paths.ConfigFile())
	if err != nil || loaded.Listen != cfg.Listen {
		t.Fatalf("config rewritten incorrectly: %+v %v", loaded, err)
	}
	var settings ClaudeSettings
	raw, _ := os.ReadFile(s.Paths.SettingsFile())
	if err := json.Unmarshal(raw, &settings); err != nil || settings.Env["ANTHROPIC_BASE_URL"] != "http://"+cfg.Listen {
		t.Errorf("settings %s %v", raw, err)
	}
	plist, _ := os.ReadFile(s.Paths.Agent)
	if !strings.Contains(string(plist), "<string>-dashboard</string>") || !strings.Contains(string(plist), Label) {
		t.Errorf("plist:\n%s", plist)
	}
	wrapper, _ := os.ReadFile(s.Paths.Wrapper("claude-gpt"))
	if !strings.Contains(string(wrapper), s.Paths.Binary()+`" launch "$@"`) {
		t.Errorf("wrapper:\n%s", wrapper)
	}
	if !reflect.DeepEqual(launchd.calls, []string{"bootstrap " + s.Paths.ServicePlist()}) {
		t.Errorf("launchd calls %v", launchd.calls)
	}
	manifests, _ := filepath.Glob(filepath.Join(s.Paths.Backups(), "*", "manifest.json"))
	if len(manifests) != 1 {
		t.Fatalf("manifests %v", manifests)
	}
	var manifest map[string]string
	raw, _ = os.ReadFile(manifests[0])
	_ = json.Unmarshal(raw, &manifest)
	backup, _ := os.ReadFile(filepath.Join(filepath.Dir(manifests[0]), manifest[previous]))
	if string(backup) != "previous launcher" {
		t.Errorf("backup %q", backup)
	}
}

func TestInstallFreshCreatesKeyAndSkipsStart(t *testing.T) {
	s, launchd := testService(t)
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(s.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientKeyFile != filepath.Join(s.Paths.Config, "client-key") || cfg.Model != "gpt-6-astra" {
		t.Errorf("config %+v", cfg)
	}
	info, err := os.Stat(cfg.ClientKeyFile)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() < 40 {
		t.Errorf("key %v %v", info, err)
	}
	if len(launchd.calls) != 0 {
		t.Errorf("launchd touched: %v", launchd.calls)
	}
	if plist, _ := os.ReadFile(s.Paths.ServicePlist()); strings.Contains(string(plist), "-dashboard") {
		t.Error("dashboard enabled without flag")
	}
}

func TestInstallRejectsInvalidConfigBeforeChangingFiles(t *testing.T) {
	s, _ := testService(t)
	if err := os.MkdirAll(s.Paths.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Paths.ConfigFile(), []byte(`{"auth_file":"relative.json","client_key_file":"/fixture/key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(s.Paths.State); !os.IsNotExist(err) {
		t.Error("state directory created despite invalid config")
	}
}

func TestFailedStartRestoresPreviousFilesAndService(t *testing.T) {
	s, launchd := testService(t)
	launchd.loaded = true // an older installation is running
	launchd.bootstrap = errors.New("fixture bootstrap failure")
	if err := os.MkdirAll(s.Paths.Bin, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := s.Paths.Wrapper("claude-gpt")
	if err := os.WriteFile(previous, []byte("old launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Paths.Agent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Paths.Agent, []byte("old plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Install(s, InstallOptions{Source: fixtureBinary(t)})
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("unexpected error %v", err)
	}
	if data, _ := os.ReadFile(previous); string(data) != "old launcher" {
		t.Errorf("launcher not restored: %q", data)
	}
	if data, _ := os.ReadFile(s.Paths.Agent); string(data) != "old plist" {
		t.Errorf("agent not restored: %q", data)
	}
	for _, path := range []string{filepath.Join(s.Paths.Config, "client-key"), s.Paths.SettingsFile()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s left behind", path)
		}
	}
	if last := launchd.calls[len(launchd.calls)-1]; last != "bootstrap "+s.Paths.Agent {
		t.Errorf("previous service not restarted: %v", launchd.calls)
	}
}

func TestUninstallRemovesServiceAndKeepsCredentials(t *testing.T) {
	s, launchd := testService(t)
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err != nil {
		t.Fatal(err)
	}
	launchd.loaded = true
	if err := Uninstall(s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launchd.calls, []string{"bootout"}) {
		t.Errorf("calls %v", launchd.calls)
	}
	for _, path := range []string{s.Paths.Binary(), s.Paths.ServicePlist(), s.Paths.SettingsFile(), s.Paths.Wrapper("claude-gpt"), s.Paths.Agent} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s left behind", path)
		}
	}
	for _, path := range []string{s.Paths.ConfigFile(), filepath.Join(s.Paths.Config, "client-key")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s removed: %v", path, err)
		}
	}
}

func TestStopDoesNotReadConfig(t *testing.T) {
	s, launchd := testService(t)
	if err := os.MkdirAll(s.Paths.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Paths.ConfigFile(), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchd.loaded = true
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launchd.calls, []string{"bootout"}) {
		t.Errorf("calls %v", launchd.calls)
	}
}

func TestStartRejectsAnotherInstallationsKeyWithoutRetry(t *testing.T) {
	s, launchd := testService(t)
	server := gateway(t, "other-key")
	cfg := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-key")
	launchd.loaded = true
	attempts := 0
	s.Sleep = func(time.Duration) { attempts++ }
	if _, err := s.Start(cfg); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unexpected error %v", err)
	}
	if attempts != 0 {
		t.Errorf("retried %d times", attempts)
	}
}

func TestLoginRestartsPreviouslyRunningService(t *testing.T) {
	s, launchd := testService(t)
	server := gateway(t, "fixture-key")
	cfg := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-key")
	var order []string
	s.Login = func(context.Context, config.Config, bool, io.Writer) error {
		order = append(order, "login")
		return errors.New("fixture login failure")
	}
	launchd.loaded = true
	err := s.SignIn(context.Background(), cfg, true)
	if err == nil || !strings.Contains(err.Error(), "fixture login failure") {
		t.Fatalf("unexpected error %v", err)
	}
	if !reflect.DeepEqual(launchd.calls, []string{"bootout", "bootstrap " + s.Paths.ServicePlist()}) || !reflect.DeepEqual(order, []string{"login"}) {
		t.Errorf("calls %v order %v", launchd.calls, order)
	}
	// A stopped service stays stopped.
	launchd.calls = nil
	launchd.loaded = false
	s.Login = func(context.Context, config.Config, bool, io.Writer) error { return nil }
	if err := s.SignIn(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(launchd.calls) != 0 {
		t.Errorf("calls %v", launchd.calls)
	}
}

func TestLauncherAppliesLocalAuthAndPreservesArguments(t *testing.T) {
	s, launchd := testService(t)
	server := gateway(t, "fixture-local-key")
	cfg := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-local-key")
	settings, _ := Settings(cfg)
	encoded, _ := encodeJSON(settings)
	if err := os.WriteFile(s.Paths.SettingsFile(), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotArgs, gotEnv []string
	l := &Launcher{Service: s,
		LookPath: func(string) (string, error) { return "/fixture/claude", nil },
		Environ: func() []string {
			return []string{"PATH=/fixture/bin", "KEEP_ME=yes", "ANTHROPIC_API_KEY=old", "CLAUDE_CODE_OAUTH_TOKEN=old", "CLAUDE_CODE_USE_BEDROCK=1"}
		},
		Exec: func(path string, argv, env []string) error { gotPath, gotArgs, gotEnv = path, argv, env; return nil }}
	if err := l.Run([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"/fixture/claude", "--help"}) || len(launchd.calls) != 0 {
		t.Errorf("help passthrough: %v %v", gotArgs, launchd.calls)
	}
	if err := l.Run([]string{"--model", "sol", "--print", "fixture prompt"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/fixture/claude", "--disallowedTools", "Artifact", "--settings", s.Paths.SettingsFile(), "--model", "gpt-5.6-sol", "--print", "fixture prompt"}
	if gotPath != "/fixture/claude" || !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("argv %v", gotArgs)
	}
	env := map[string]string{}
	for _, entry := range gotEnv {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "fixture-local-key" || env["ANTHROPIC_API_KEY"] != "" || env["KEEP_ME"] != "yes" || env["ANTHROPIC_MODEL"] != "gpt-6-astra" {
		t.Errorf("env %v", env)
	}
	for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s leaked", name)
		}
	}
	if !reflect.DeepEqual(launchd.calls, []string{"bootstrap " + s.Paths.ServicePlist()}) {
		t.Errorf("service not started: %v", launchd.calls)
	}
}
