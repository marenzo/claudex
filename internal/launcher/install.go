package launcher

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

// InstallOptions controls Install.
type InstallOptions struct {
	Source  string          // claudex binary to install; defaults to the running executable
	NoStart bool            // install without starting the service
	Context context.Context // cancellation while waiting for active requests
}

// Install copies the gateway into the user's state directory, writes the
// config, the claudex command on PATH and launchd agent, then
// starts the service. Every replaced file is backed up first; a failure
// restores it.
func Install(s *Service, opts InstallOptions) error {
	// Reject malformed config before the lock creates any state.
	if _, err := installConfig(s.Paths); err != nil {
		return err
	}
	return WithControlLock(s.Paths, func() error { return install(s, opts) })
}

// InstallLocked is used by setup after it has locked initialization and sign-in.
func InstallLocked(s *Service, opts InstallOptions) error { return install(s, opts) }

func install(s *Service, opts InstallOptions) (err error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
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
	encodedConfig, err := encodeJSON(cfg)
	if err != nil {
		return err
	}

	backup := filepath.Join(p.Backups(), time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	targets := []string{p.Binary(), p.ServicePlist(), p.ConfigFile(), p.Command(), p.Agent}
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
	if running {
		oldConfig, errLoad := config.Load(p.ConfigFile())
		if errLoad != nil {
			return errLoad
		}
		var release func()
		release, err = s.Drain(ctx, oldConfig)
		if err != nil {
			return err
		}
		defer release()
	}
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
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = s.Stop(); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
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
	// Quote for the shell, not Go: double quotes still expand dollars and backticks.
	quotedBinary := "'" + strings.ReplaceAll(p.Binary(), "'", "'\"'\"'") + "'"
	command := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", quotedBinary)
	if err = atomicWrite(p.Command(), []byte(command), 0o755); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p.LogFile()), 0o700); err != nil {
		return err
	}
	encodedPlist := plist(p)
	if err = atomicWrite(p.ServicePlist(), encodedPlist, 0o600); err != nil {
		return err
	}
	if err = atomicWrite(p.Agent, encodedPlist, 0o600); err != nil {
		return err
	}
	if !opts.NoStart {
		if err = s.Report(cfg); err != nil {
			return err
		}
	}
	fmt.Fprintf(s.Out, "Installed. Ensure %s is on PATH, then run: claudex\n", p.Bin)
	if _, errStat := os.Stat(cfg.AuthFile); errStat != nil {
		fmt.Fprintln(s.Out, "Sign in first: claudex ctl setup --login")
	}
	if cfg.Dashboard {
		fmt.Fprintf(s.Out, "Dashboard: http://%s/dashboard\n", cfg.Listen)
	}
	fmt.Fprintln(s.Out, "Backup:", backup)
	return nil
}

// Uninstall removes the installed service, binary, and claudex command. It keeps
// config and credentials so a reinstall picks them back up.
func Uninstall(s *Service) error {
	if goos != "darwin" {
		return errors.New("uninstall supports macOS; remove the binary manually elsewhere")
	}
	p := s.Paths
	if s.Launchd.Loaded(Label) {
		if err := s.Stop(); err != nil {
			return err
		}
	}
	for _, path := range []string{
		p.Binary(),
		p.ServicePlist(),
		p.Command(),
		p.Agent,
	} {
		os.Remove(path)
	}
	os.Remove(filepath.Join(p.State, "bin"))
	os.Remove(filepath.Join(p.State, "logs"))
	os.Remove(p.State)
	fmt.Fprintf(s.Out, "Uninstalled. Config and credentials kept in %s\n", p.Config)
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

// exists reports whether an installed service file is present.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
