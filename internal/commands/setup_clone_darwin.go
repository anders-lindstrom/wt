package commands

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// cloneTreeCOW is clonefile(2), which clones a whole directory tree in one
// call on APFS and leaves nothing behind when it fails.
func cloneTreeCOW(from, to string) error {
	err := unix.Clonefile(from, to, unix.CLONE_NOFOLLOW)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EXDEV):
		return errCloneUnsupported{"the source is on another volume"}
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
		return errCloneUnsupported{"this filesystem cannot clone"}
	}
	return &os.PathError{Op: "clonefile", Path: from, Err: err}
}

// removeStaleClones has nothing to remove: clonefile(2) leaves no partial tree.
func removeStaleClones(_, _ string) error { return nil }
