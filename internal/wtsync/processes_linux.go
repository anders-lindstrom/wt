package wtsync

import (
	"os"
	"strconv"
	"strings"
)

// processArgv is a process's arguments as it was given them, from /proc.
// False when the process is gone or its command line cannot be read.
func processArgv(pid int) ([]string, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), true
}
