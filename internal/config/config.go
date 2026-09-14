package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
)

type Config struct {
	Listen          string `json:"listen"`
	AuthFile        string `json:"auth_file"`
	ClientKeyFile   string `json:"client_key_file"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	ContextWindow   int    `json:"context_window"`
	CompactWindow   int    `json:"compact_window"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "claudex", "config.json")
}

func defaults() Config {
	return Config{Listen: "127.0.0.1:8317", Model: "gpt-6-astra", ReasoningEffort: "high", ContextWindow: 1000000, CompactWindow: 900000}
}

// DefaultsFor returns the defaults for a fresh installation whose config lives in dir.
func DefaultsFor(dir string) Config {
	cfg := defaults()
	cfg.AuthFile = filepath.Join(dir, "auth", "codex.json")
	cfg.ClientKeyFile = filepath.Join(dir, "client-key")
	return cfg
}

func Load(path string) (Config, error) {
	cfg := defaults()
	data, errOpen := os.ReadFile(path)
	if errOpen != nil {
		if os.IsNotExist(errOpen) {
			return cfg, fmt.Errorf("no config at %s; create one with: claudex -config %q -init", path, path)
		}
		return cfg, errOpen
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if errDecode := decoder.Decode(&cfg); errDecode != nil {
		return cfg, fmt.Errorf("read proxy config: %w", errDecode)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return cfg, fmt.Errorf("config contains trailing JSON")
	}
	if errValidate := cfg.Validate(); errValidate != nil {
		return cfg, errValidate
	}
	model, _ := FindModel(cfg.Model)
	cfg.Model = model.ID
	return cfg, nil
}

func (c Config) Validate() error {
	listen, err := netip.ParseAddrPort(c.Listen)
	if err != nil || listen.Port() == 0 || !listen.Addr().IsLoopback() || listen.Addr().Zone() != "" {
		return fmt.Errorf("listen must be a loopback IP and numeric port from 1 to 65535, such as 127.0.0.1:8317")
	}
	if !filepath.IsAbs(c.AuthFile) || !filepath.IsAbs(c.ClientKeyFile) {
		return fmt.Errorf("auth_file and client_key_file must be absolute paths")
	}
	if _, ok := FindModel(c.Model); !ok {
		return fmt.Errorf("model must be gpt-6-astra, gpt-5.6-terra, gpt-5.6-sol, or gpt-5.6-luna")
	}
	switch c.ReasoningEffort {
	case "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return fmt.Errorf("unsupported reasoning_effort")
	}
	if c.ContextWindow <= 0 || c.CompactWindow <= 0 || c.CompactWindow >= c.ContextWindow {
		return fmt.Errorf("compact_window must be positive and below context_window")
	}
	return nil
}
