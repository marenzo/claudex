package launcher

import (
	"context"
	"os"
	"path/filepath"
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
}
