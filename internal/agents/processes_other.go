//go:build !darwin && !linux

package agents

// processArgv has no way to read another process's arguments here.
func processArgv(int) ([]string, bool) { return nil, false }
