package kit

import (
	"os"
	"runtime"
)

// Machine is what the machine running wt gives a command beyond its
// repository. A zero field is the machine's own; a test sets the fields on
// its Context, so that tests running side by side each have their own.
type Machine struct {
	// GOOS is the platform; "" is runtime.GOOS.
	GOOS string
	// Home is the person's home directory; "" is os.UserHomeDir.
	Home string
	// Launchctl is the launchctl the keeper drives; "" is the one on the
	// PATH, looked up each time it runs.
	Launchctl string
}

// Platform is the operating system wt runs on.
func (m Machine) Platform() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return runtime.GOOS
}

// HomeDir is the person's home directory.
func (m Machine) HomeDir() (string, error) {
	if m.Home != "" {
		return m.Home, nil
	}
	return os.UserHomeDir()
}
