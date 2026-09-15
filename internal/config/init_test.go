package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeProtectsExistingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")
	if err := Initialize(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(cfg.ClientKeyFile)
	if err != nil || len(key) != 65 {
		t.Fatalf("key: %v", err)
	}
	if err := Initialize(path); err == nil {
		t.Fatal("overwrote configuration")
	}
	after, _ := os.ReadFile(cfg.ClientKeyFile)
	if string(after) != string(key) {
		t.Fatal("replaced client key")
	}
	for _, p := range []string{path, cfg.ClientKeyFile} {
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("private file permissions", p)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A leftover key from a removed config is reused rather than blocking -init.
	if err := Initialize(path); err != nil {
		t.Fatalf("orphaned key blocked init: %v", err)
	}
	after, _ = os.ReadFile(cfg.ClientKeyFile)
	if string(after) != string(key) {
		t.Fatal("replaced existing key")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("config not recreated")
	}
}
