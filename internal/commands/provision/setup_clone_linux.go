package provision

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// cloneOneFile clones one regular file. A variable so tests can run the walk
// on a filesystem that cannot clone.
var cloneOneFile = cloneFile

// cloneTreeCOW walks the tree and clones each file with the FICLONE ioctl,
// as `cp --reflink=always` does: Btrfs, XFS and the like share the blocks.
// The tree is built in a temporary sibling and renamed into place, so an
// interrupted or failed clone never leaves a partial tree at to; what a
// killed run left is removed by the next setup that reaches the cache, by
// removeStaleClones.
func cloneTreeCOW(from, to string) (err error) {
	parent, base := filepath.Dir(to), filepath.Base(to)
	tmp, err := os.MkdirTemp(parent, clonePrefix(base)+strconv.Itoa(os.Getpid())+"-")
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			err = withNote(err, fmt.Sprintf("%s could not be removed: %v", tmp, rmErr))
		}
	}()
	err = filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case rel == ".":
			return nil
		case d.IsDir():
			// Writable by its owner, or a read-only directory could not
			// take its own contents.
			return os.Mkdir(dst, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		case d.Type().IsRegular():
			return cloneOneFile(path, dst, info.Mode().Perm())
		}
		return nil // sockets and fifos are not cache content
	})
	if err != nil {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

func clonePrefix(base string) string { return "." + base + ".wt-clone-" }

// removeStaleClones removes the temporary trees of base that a killed run
// left in dir. A tree is named for the process cloning into it, and one whose
// process is still running is another setup's and is kept.
func removeStaleClones(dir, base string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := clonePrefix(base)
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		if pidText, _, _ := strings.Cut(rest, "-"); processRuns(pidText) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// processRuns reports whether pidText names a running process. Text that is
// no pid names none.
func processRuns(pidText string) bool {
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return false
	}
	err = unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

// withNote adds a note to err's message, keeping an errCloneUnsupported one
// as such so it is still a skip.
func withNote(err error, note string) error {
	var unsupported errCloneUnsupported
	if errors.As(err, &unsupported) {
		return errCloneUnsupported{unsupported.why + "; " + note}
	}
	return fmt.Errorf("%w; %s", err, note)
}

func cloneFile(from, to string, perm fs.FileMode) (err error) {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); err == nil {
			err = closeErr
		}
	}()
	err = unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())) //nolint:gosec // file descriptors fit an int
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EXDEV):
		return errCloneUnsupported{"the source is on another filesystem"}
	case errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOTTY), errors.Is(err, unix.EINVAL):
		return errCloneUnsupported{"this filesystem cannot clone"}
	}
	return &os.PathError{Op: "FICLONE", Path: from, Err: err}
}
