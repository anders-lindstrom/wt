package quarantine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Identity is what a rename keeps of a directory: its device and inode.
type Identity struct {
	Device uint64
	Inode  uint64
}

// Identify reads path's device and inode, without following a final
// symlink.
func Identify(path string) (Identity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Identity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, fmt.Errorf("cannot read the device of %s", path)
	}
	// Dev is an int32 on darwin and a uint64 on linux; a device number is
	// never negative.
	return Identity{Device: uint64(st.Dev), Inode: st.Ino}, nil //nolint:gosec,unconvert,nolintlint
}

// DeviceOf is the device path leads to, following symlinks: a folder
// that is a symlink onto another volume is on that volume. A var so a test
// can put a path on another volume.
var DeviceOf = func(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read the device of %s", path)
	}
	return uint64(st.Dev), nil //nolint:gosec,unconvert,nolintlint // as in Identify
}

// Check is everything that refuses a quarantine into dir before anything
// changes: dir must be new, its parent there, and every path to be moved on
// the parent's volume, because a rename across volumes is a copy and a
// delete.
func Check(dir string, paths ...string) error {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s is there already: a quarantine goes into a new folder", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if q := InsideQuarantine(dir); q != "" {
		return fmt.Errorf("%s is inside the quarantine %s: a purge of that one would delete it", dir, q)
	}
	parent := filepath.Dir(dir)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("the folder %s would go in is not there: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", parent)
	}
	want, err := DeviceOf(parent)
	if err != nil {
		return err
	}
	for _, p := range paths {
		dev, err := DeviceOf(p)
		if err != nil {
			return err
		}
		if dev != want {
			return fmt.Errorf("%s is on another volume than %s: a quarantine only renames, never copies", p, parent)
		}
	}
	return nil
}

// Move renames from to to, and refuses when anything is at to: a plain
// rename(2) would replace an empty directory there. An error after the
// rename — the fsync that makes it durable — does not undo it.
func Move(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s is there already", to)
	}
	if err := renameNoReplace(from, to); err != nil {
		return fmt.Errorf("move %s to %s: %w", from, to, err)
	}
	// From here the directory has moved, whatever is returned: a caller
	// reads where it is with Locate rather than from the error.
	if AfterRename != nil {
		if err := AfterRename(to); err != nil {
			return fmt.Errorf("moved %s to %s, but: %w", from, to, err)
		}
	}
	if err := syncDir(filepath.Dir(to)); err != nil {
		return fmt.Errorf("moved %s to %s, but: %w", from, to, err)
	}
	return nil
}

// AfterRename runs, when set, between a move's rename and its fsync, and an
// error from it stands for the fsync failing. Tests set it; nothing else
// does.
var AfterRename func(to string) error
