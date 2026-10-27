package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupInstallsAndUpdatesCodexSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, "config", "claudex.json")
	for range 2 {
		if err := cmdSetup([]string{"--config", configPath, "--no-login"}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home, ".agents", "skills", "claudex-handoff")
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		t.Fatal("setup did not materialize user skill", err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("user customization"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdSetup([]string{"--config", configPath, "--no-login"}); err == nil {
		t.Fatal("setup silently replaced customization")
	}
}
