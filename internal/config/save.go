package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Save validates and atomically replaces a private configuration file.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, append(data, '\n'), 0o600)
}

// WriteFile replaces path with data using a private temporary file.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	cleanup := func(err error) error {
		_ = temporary.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return cleanup(err)
	}
	if err := temporary.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if err := temporary.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(name, path); err != nil {
		return cleanup(err)
	}
	return nil
}
