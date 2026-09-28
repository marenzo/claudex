package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marenzo/claudex/internal/config"
)

func TestInstallPreservesDashboardConfig(t *testing.T) {
	s, _ := testService(t)
	cfg := writeConfig(t, s, "127.0.0.1:8317", "fixture-key")
	cfg.Dashboard = true
	if err := config.Save(s.Paths.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(s.Paths.ConfigFile())
	if err != nil || !got.Dashboard || got.Model != cfg.Model {
		t.Fatalf("preferences lost: %+v %v", got, err)
	}
	newPlist, _ := os.ReadFile(s.Paths.ServicePlist())
	if !strings.Contains(string(newPlist), "<string>ctl</string>") || strings.Contains(string(newPlist), "-dashboard") {
		t.Fatalf("plist still owns dashboard choice: %s", newPlist)
	}
}

func TestApplyConfigRestoresOldSettingsWhenNewGatewayIsStale(t *testing.T) {
	s, launchd := testService(t)
	server := gateway(t, "fixture-key")
	old := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-key")
	if err := os.MkdirAll(s.Paths.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Paths.Agent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Paths.ServicePlist(), []byte("old service"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Paths.Agent, []byte("old agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchd.loaded = true
	next := old
	next.Model = "gpt-5.6-sol"
	next.Dashboard = true
	if err := ApplyConfig(context.Background(), s, s.Paths.ConfigFile(), old, next); err == nil || !strings.Contains(err.Error(), "different settings") {
		t.Fatalf("stale gateway accepted: %v", err)
	}
	got, err := config.Load(s.Paths.ConfigFile())
	if err != nil || got != old {
		t.Fatalf("config not restored: %+v %v", got, err)
	}
	for path, want := range map[string]string{s.Paths.ServicePlist(): "old service", s.Paths.Agent: "old agent"} {
		bytes, err := os.ReadFile(path)
		if err != nil || string(bytes) != want {
			t.Errorf("%s not restored: %q %v", path, bytes, err)
		}
	}
	if !launchd.loaded {
		t.Error("previous service did not restart")
	}
	// The new gateway must stop before the old settings start, or it keeps serving.
	want := []string{"bootout", "bootstrap " + s.Paths.ServicePlist(), "bootout", "bootstrap " + s.Paths.ServicePlist()}
	if !reflect.DeepEqual(launchd.calls, want) {
		t.Errorf("launchd calls %v, want %v", launchd.calls, want)
	}
}

func TestDrainIgnoresConfigMismatch(t *testing.T) {
	s, _ := testService(t)
	server := gateway(t, "fixture-key")
	cfg := writeConfig(t, s, strings.TrimPrefix(server.URL, "http://"), "fixture-key")
	gatewayRevisions.Store(cfg.Listen, "stale")
	release, err := s.Drain(context.Background(), cfg)
	if err != nil {
		t.Fatalf("restart could not drain a gateway with older settings: %v", err)
	}
	release()
}

func TestInstallStopsGatewayThatCannotAnswerControl(t *testing.T) {
	old := httptest.NewServer(http.NotFoundHandler()) // a release without control endpoints
	t.Cleanup(old.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	for name, listen := range map[string]string{"old release": old.Listener.Addr().String(), "down": down.Listener.Addr().String(), "no key": "127.0.0.1:1"} {
		t.Run(name, func(t *testing.T) {
			s, launchd := testService(t)
			key := "fixture-key"
			if name == "no key" {
				key = ""
			}
			writeConfig(t, s, listen, key)
			launchd.loaded = true
			if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(launchd.calls, []string{"bootout"}) {
				t.Errorf("launchd calls %v", launchd.calls)
			}
		})
	}
}

func TestApplyConfigDoesNotRecreateUninstalledService(t *testing.T) {
	s, launchd := testService(t)
	old := writeConfig(t, s, "127.0.0.1:8317", "fixture-key")
	next := old
	next.Model = "gpt-5.6-sol"
	if err := ApplyConfig(context.Background(), s, s.Paths.ConfigFile(), old, next); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.Paths.ServicePlist(), s.Paths.Agent} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s created after uninstall: %v", path, err)
		}
	}
	if len(launchd.calls) != 0 {
		t.Errorf("launchd calls %v", launchd.calls)
	}
}

func TestInstallKeepsLegacyDashboardFlag(t *testing.T) {
	s, _ := testService(t)
	writeConfig(t, s, "127.0.0.1:8317", "fixture-key")
	if err := os.MkdirAll(filepath.Dir(s.Paths.Agent), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := "<array>\n\t<string>run</string>\n\t<string>-dashboard</string>\n</array>"
	if err := os.WriteFile(s.Paths.Agent, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(s, InstallOptions{Source: fixtureBinary(t), NoStart: true}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(s.Paths.ConfigFile())
	if err != nil || !got.Dashboard {
		t.Fatalf("dashboard dropped on upgrade: %+v %v", got, err)
	}
}
