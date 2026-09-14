package launcher

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

// InstallOptions controls Install.
type InstallOptions struct {
	Source    string // claudex binary to install; defaults to the running executable
	Dashboard bool   // enable the local usage dashboard
	NoStart   bool   // install without starting the service
}

// Install copies the gateway into the user's state directory, writes the
// config, Claude settings, command wrappers and launchd agent, then starts the
// service. Every replaced file is backed up first; a failure restores it.
func Install(s *Service, opts InstallOptions) (err error) {
	if goos != "darwin" {
		return errors.New("the service installer supports macOS; run claudex directly or use Docker elsewhere")
	}
	p := s.Paths
	if opts.Source == "" {
		if opts.Source, err = os.Executable(); err != nil {
			return err
		}
	}
	binary, err := os.ReadFile(opts.Source)
	if err != nil {
		return fmt.Errorf("read gateway binary: %w", err)
	}
	cfg, err := installConfig(p)
	if err != nil {
		return err
	}
	settings, err := Settings(cfg)
	if err != nil {
		return err
	}
	encodedSettings, err := encodeJSON(settings)
	if err != nil {
		return err
	}
	encodedConfig, err := encodeJSON(cfg)
	if err != nil {
		return err
	}

	backup := filepath.Join(p.Backups(), time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	targets := []string{p.Binary(), p.ServicePlist(), p.ConfigFile(), p.SettingsFile(),
		p.Wrapper("claude-gpt"), p.Wrapper("claudex-service"), p.Agent}
	if _, err := os.Stat(cfg.ClientKeyFile); err != nil {
		targets = append(targets, cfg.ClientKeyFile)
	}
	manifest := map[string]string{}
	for index, target := range targets {
		info, err := os.Stat(target)
		if err != nil {
			continue
		}
		name := fmt.Sprintf("%d-%s", index, filepath.Base(target))
		if err := copyFile(target, filepath.Join(backup, name), info.Mode().Perm()); err != nil {
			return err
		}
		manifest[target] = name
	}
	encodedManifest, err := encodeJSON(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(backup, "manifest.json"), encodedManifest, 0o600); err != nil {
		return err
	}

	running := s.Launchd.Loaded(Label)
	defer func() {
		if err == nil {
			return
		}
		if s.Launchd.Loaded(Label) {
			_ = s.Launchd.Bootout(Label)
		}
		for _, target := range targets {
			if name, ok := manifest[target]; ok {
				original := filepath.Join(backup, name)
				info, errStat := os.Stat(original)
				if errStat == nil {
					_ = copyFile(original, target, info.Mode().Perm())
				}
			} else {
				_ = os.Remove(target)
			}
		}
		if running {
			_ = s.Launchd.Bootstrap(p.Agent)
		}
	}()

	if running {
		if err = s.Stop(); err != nil {
			return err
		}
	}
	if _, errStat := os.Stat(cfg.ClientKeyFile); errStat != nil {
		key := make([]byte, 36)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		if err = atomicWrite(cfg.ClientKeyFile, []byte(base64.RawURLEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
			return err
		}
	}
	if err = atomicWrite(p.Binary(), binary, 0o755); err != nil {
		return err
	}
	if err = atomicWrite(p.ConfigFile(), encodedConfig, 0o600); err != nil {
		return err
	}
	if err = atomicWrite(p.SettingsFile(), encodedSettings, 0o600); err != nil {
		return err
	}
	for name, subcommand := range map[string]string{"claude-gpt": "launch", "claudex-service": "service"} {
		wrapper := fmt.Sprintf("#!/bin/sh\nexec %q %s \"$@\"\n", p.Binary(), subcommand)
		if err = atomicWrite(p.Wrapper(name), []byte(wrapper), 0o755); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(filepath.Dir(p.LogFile()), 0o700); err != nil {
		return err
	}
	encodedPlist := plist(p, opts.Dashboard)
	if err = atomicWrite(p.ServicePlist(), encodedPlist, 0o600); err != nil {
		return err
	}
	if err = atomicWrite(p.Agent, encodedPlist, 0o600); err != nil {
		return err
	}
	if !opts.NoStart {
		if err = s.report(cfg); err != nil {
			return err
		}
	}
	fmt.Fprintf(s.Out, "Installed. Ensure %s is on PATH, then run: claude-gpt\n", p.Bin)
	if _, errStat := os.Stat(cfg.AuthFile); errStat != nil {
		fmt.Fprintln(s.Out, "Sign in first: claudex service login")
	}
	if opts.Dashboard {
		fmt.Fprintf(s.Out, "Dashboard: http://%s/dashboard\n", cfg.Listen)
	}
	fmt.Fprintln(s.Out, "Backup:", backup)
	return nil
}

// installConfig loads the existing gateway config or builds the defaults for a
// fresh installation under the config directory.
func installConfig(p Paths) (config.Config, error) {
	path := p.ConfigFile()
	if _, err := os.Stat(path); err == nil {
		cfg, err := config.Load(path)
		if err != nil {
			return cfg, fmt.Errorf("existing config is invalid; fix %s or move it aside: %w", path, err)
		}
		return cfg, nil
	}
	return config.DefaultsFor(p.Config), nil
}

func copyFile(from, to string, mode os.FileMode) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	data, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	return atomicWrite(to, data, mode)
}

// exists reports whether the installation has generated launcher settings.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
