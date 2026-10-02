package wtsync

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// processArgv is a process's arguments as it was given them, from the
// kernel: argc, the executable's path, padding, then the arguments, each
// ended by a NUL. False when the process is gone or not ours to read.
func processArgv(pid int) ([]string, bool) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 || argc <= 0 {
		return nil, false
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	var argv []string
	for len(argv) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, false
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, true
}
