package commands

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// withFileCloner stands in for the FICLONE of one file: a byte copy, which is
// what lets the walk run on a filesystem that cannot clone.
func withFileCloner(t *testing.T, f func(from, to string, perm fs.FileMode) error) {
	t.Helper()
	saved := cloneOneFile
	cloneOneFile = f
	t.Cleanup(func() { cloneOneFile = saved })
}

func byteCopy(from, to string, perm fs.FileMode) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, perm) //nolint:gosec // a test stand-in for the clone, keeping the source mode
}

// nothingBeside is the directory with no entry at all: no tree, and no
// temporary sibling of one.
func nothingBeside(t *testing.T, dir string) {
	t.Helper()
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("%s holds %v (%v); want nothing", dir, entries, err)
	}
}

func TestCloneTreeWalkMakesDirsLinksAndFiles(t *testing.T) {
	withFileCloner(t, byteCopy)
	from := filepath.Join(t.TempDir(), "node_modules")
	mustWrite(t, filepath.Join(from, "pkg", "index.js"), "x")
	if err := os.Symlink("pkg/index.js", filepath.Join(from, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(from, "pkg", "index.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(from, 0o750); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	to := filepath.Join(parent, "node_modules")
	if err := cloneTreeCOW(from, to); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(to, "pkg", "index.js")); got != "x" {
		t.Errorf("file = %q", got)
	}
	if got, err := os.Readlink(filepath.Join(to, "link")); err != nil || got != "pkg/index.js" {
		t.Errorf("link = %q, %v", got, err)
	}
	if st, err := os.Stat(filepath.Join(to, "pkg", "index.js")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("file mode = %v, %v", st, err)
	}
	if st, err := os.Stat(to); err != nil || st.Mode().Perm() != 0o750 {
		t.Errorf("root mode = %v, %v", st, err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Errorf("%s holds %v; want the clone alone", parent, entries)
	}
}

// A failure deep in the tree, or a filesystem that cannot clone, leaves
// nothing: not the tree and not the temporary one it was built in.
func TestCloneTreeFailureLeavesNothing(t *testing.T) {
	for name, fail := range map[string]error{
		"failed":      errors.New("disk on fire"),
		"unsupported": errCloneUnsupported{"this filesystem cannot clone"},
	} {
		t.Run(name, func(t *testing.T) {
			n := 0
			withFileCloner(t, func(from, to string, perm fs.FileMode) error {
				if n++; n == 2 {
					return fail
				}
				return byteCopy(from, to, perm)
			})
			from := filepath.Join(t.TempDir(), "node_modules")
			mustWrite(t, filepath.Join(from, "a", "1.js"), "1")
			mustWrite(t, filepath.Join(from, "a", "2.js"), "2")
			mustWrite(t, filepath.Join(from, "b", "3.js"), "3")
			parent := t.TempDir()
			err := cloneTreeCOW(from, filepath.Join(parent, "node_modules"))
			if err == nil {
				t.Fatal("no error")
			}
			var unsupported errCloneUnsupported
			if errors.As(err, &unsupported) != (name == "unsupported") {
				t.Errorf("error %v", err)
			}
			nothingBeside(t, parent)
		})
	}
}

// deadPID is the pid of a process that has exited.
func deadPID(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(cmd.Process.Pid)
}

// A tree a killed run left behind is removed; one named for a running
// process, or this one, is another setup's and stays.
func TestRemoveStaleClonesKeepsTheLiveOnes(t *testing.T) {
	parent := t.TempDir()
	stale := ".node_modules.wt-clone-" + deadPID(t) + "-1234"
	old := ".node_modules.wt-clone-nopid"
	live := ".node_modules.wt-clone-" + strconv.Itoa(os.Getpid()) + "-4321"
	other := ".gradle.wt-clone-" + deadPID(t) + "-1234"
	for _, n := range []string{stale, old, live, other} {
		mustWrite(t, filepath.Join(parent, n, "half.js"), "half")
	}
	if err := removeStaleClones(parent, "node_modules"); err != nil {
		t.Fatal(err)
	}
	for n, wantThere := range map[string]bool{stale: false, old: false, live: true, other: true} {
		if _, err := os.Stat(filepath.Join(parent, n)); (err == nil) != wantThere {
			t.Errorf("%s: present = %v, want %v", n, err == nil, wantThere)
		}
	}
}

// The clone makes its tree under a name carrying its own pid, so a second
// setup sweeping the same directory leaves it alone.
func TestCloneTreeNamesItsTemporaryTreeForItsPID(t *testing.T) {
	var seen string
	withFileCloner(t, func(from, to string, perm fs.FileMode) error {
		seen = filepath.Base(filepath.Dir(to))
		return byteCopy(from, to, perm)
	})
	from := filepath.Join(t.TempDir(), "node_modules")
	mustWrite(t, filepath.Join(from, "a.js"), "a")
	if err := cloneTreeCOW(from, filepath.Join(t.TempDir(), "node_modules")); err != nil {
		t.Fatal(err)
	}
	if want := ".node_modules.wt-clone-" + strconv.Itoa(os.Getpid()) + "-"; !strings.HasPrefix(seen, want) {
		t.Errorf("temporary tree %q, want prefix %q", seen, want)
	}
}

// Leftovers are swept for a cache that is already in the worktree too, since
// nothing else would reach them once npm install made it.
func TestSetupSweepsStaleClonesBesideAnExistingCache(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	mustMkdir(t, filepath.Join(target, "node_modules"))
	stale := filepath.Join(target, ".node_modules.wt-clone-"+deadPID(t)+"-1")
	mustWrite(t, filepath.Join(stale, "half.js"), "half")
	withCloner(t, func(_, to string) error { return os.Mkdir(to, 0o755) })
	var buf bytes.Buffer
	cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale clone is still there: %v", err)
	}
}
