//go:build !darwin && !linux

package quarantine

import "os"

// renameNoReplace is a plain rename where the system has no exclusive one;
// Move has looked for the destination first.
func renameNoReplace(from, to string) error {
	return os.Rename(from, to)
}
