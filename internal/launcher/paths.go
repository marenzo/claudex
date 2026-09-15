// Package launcher installs and manages the macOS user service and starts
// Claude Code against the local gateway. It replaces the former Python scripts.
package launcher

import (
	"os"
	"path/filepath"
)

// Label is the launchd service label.
const Label = "local.claudex.proxy"

// Paths describes every location the installer, service manager, and launcher
// touch. Tests substitute temporary directories.
type Paths struct {
	Home   string // user home directory
	State  string // ~/.local/share/claudex
	Config string // ~/.config/claudex
	Bin    string // ~/.local/bin
	Agent  string // ~/Library/LaunchAgents/<Label>.plist
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Home:   home,
		State:  filepath.Join(home, ".local", "share", "claudex"),
		Config: filepath.Join(home, ".config", "claudex"),
		Bin:    filepath.Join(home, ".local", "bin"),
		Agent:  filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
	}, nil
}

func (p Paths) Binary() string       { return filepath.Join(p.State, "bin", "claudex") }
func (p Paths) ConfigFile() string   { return filepath.Join(p.Config, "config.json") }
func (p Paths) SettingsFile() string { return filepath.Join(p.Config, "claude-settings.json") }
func (p Paths) ServicePlist() string { return filepath.Join(p.State, "service.plist") }
func (p Paths) LogFile() string      { return filepath.Join(p.State, "logs", "service.log") }
func (p Paths) Backups() string      { return filepath.Join(p.State, "backups") }
func (p Paths) Wrapper(name string) string {
	return filepath.Join(p.Bin, name)
}

// atomicWrite replaces path with data using a private temporary file.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
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
