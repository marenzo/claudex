package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Launchd is the subset of launchctl used by the installer and service manager.
type Launchd interface {
	Loaded(label string) bool
	Bootout(label string) error
	Bootstrap(plist string) error
}

type launchctl struct{ domain string }

// NewLaunchd talks to the current user's launchd GUI domain.
func NewLaunchd() Launchd { return launchctl{domain: fmt.Sprintf("gui/%d", os.Getuid())} }

func (l launchctl) Loaded(label string) bool {
	return exec.Command("/bin/launchctl", "print", l.domain+"/"+label).Run() == nil
}

func (l launchctl) Bootout(label string) error {
	output, err := exec.Command("/bin/launchctl", "bootout", l.domain+"/"+label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootout: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (l launchctl) Bootstrap(plist string) error {
	output, err := exec.Command("/bin/launchctl", "bootstrap", l.domain, plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// goos is overridden by tests so the macOS-only paths run on every platform.
var goos = runtime.GOOS
