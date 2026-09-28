package quarantine

import "golang.org/x/sys/unix"

// renameNoReplace is rename(2) that fails when the destination exists, in
// the one system call, so nothing can appear there in between.
func renameNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
