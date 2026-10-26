// Package handoff prepares local Claude history for Codex's native session importer.
package handoff

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:plugin
var plugin embed.FS

// InstallPlugin materializes immutable bundled assets under private runtime storage.
func InstallPlugin(runtime string) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(plugin, "plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := plugin.ReadFile(path)
		if err != nil {
			return err
		}
		hash.Write([]byte(path))
		hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	root := filepath.Join(runtime, "plugins", hex.EncodeToString(hash.Sum(nil))[:16])
	err = fs.WalkDir(plugin, "plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, strings.TrimPrefix(path, "plugin"))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := plugin.ReadFile(path)
		if err != nil {
			return err
		}
		return privateFile(target, data)
	})
	return root, err
}

func Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// CaptureSession writes shell-quoted values only to Claude's own hook environment file.
func CaptureSession(in io.Reader, envFile string) error {
	if envFile == "" {
		return nil
	} // Non-Bash clients need the explicit --source form.
	var event struct {
		Transcript string `json:"transcript_path"`
		CWD        string `json:"cwd"`
	}
	decoder := json.NewDecoder(io.LimitReader(in, 64<<10))
	if err := decoder.Decode(&event); err != nil {
		return fmt.Errorf("read session hook: %w", err)
	}
	if !filepath.IsAbs(event.Transcript) || !filepath.IsAbs(event.CWD) || strings.ContainsAny(event.Transcript+event.CWD, "\x00\r\n") {
		return fmt.Errorf("session hook requires absolute transcript and cwd paths")
	}
	if err := os.MkdirAll(filepath.Dir(envFile), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(envFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\nexport CLAUDEX_TRANSCRIPT_PATH=%s\nexport CLAUDEX_SESSION_CWD=%s\n", Quote(event.Transcript), Quote(event.CWD))
	return err
}

func privateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
