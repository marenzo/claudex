package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrExists reports that Initialize found a config and left it unchanged.
var ErrExists = errors.New("config already exists")

// Initialize creates a private configuration without replacing an existing login,
// key, or config. OAuth credentials are created later by claudex login.
func Initialize(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(absolute)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, absolute)
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg := DefaultsFor(dir)
	createdKey := false
	if existing, errKey := os.ReadFile(cfg.ClientKeyFile); errKey == nil {
		if len(strings.TrimSpace(string(existing))) < 24 {
			return fmt.Errorf("existing client key %s is too short; remove it to generate a new one", cfg.ClientKeyFile)
		}
	} else if !os.IsNotExist(errKey) {
		return errKey
	} else {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		if err := writeExclusive(cfg.ClientKeyFile, []byte(hex.EncodeToString(key)+"\n")); err != nil {
			return fmt.Errorf("create client key: %w", err)
		}
		createdKey = true
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		err = writeExclusive(absolute, append(data, '\n'))
	}
	if err != nil {
		if createdKey {
			_ = os.Remove(cfg.ClientKeyFile)
		}
		return fmt.Errorf("create config: %w", err)
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
