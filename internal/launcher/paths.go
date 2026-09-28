// Package launcher installs and manages the macOS user service and starts
// Claude Code against the local gateway.
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
func (p Paths) ServicePlist() string { return filepath.Join(p.State, "service.plist") }
func (p Paths) LogFile() string      { return filepath.Join(p.State, "logs", "service.log") }
func (p Paths) Backups() string      { return filepath.Join(p.State, "backups") }

// Command is the claudex shim on PATH that runs the installed binary.
func (p Paths) Command() string { return p.Wrapper("claudex") }
func (p Paths) Wrapper(name string) string {
	return filepath.Join(p.Bin, name)
}
