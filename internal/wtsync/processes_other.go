//go:build !darwin && !linux

package wtsync

// processArgv has no way to read another process's arguments here.
func processArgv(int) ([]string, bool) { return nil, false }
