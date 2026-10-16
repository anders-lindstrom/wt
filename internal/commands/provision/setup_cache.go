package provision

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// errCloneUnsupported is a clone the filesystem cannot make: another volume,
// or a filesystem without copy-on-write. The cache is skipped, never copied,
// since a byte copy of node_modules costs what the clone was meant to save.
type errCloneUnsupported struct{ why string }

func (e errCloneUnsupported) Error() string { return e.why }

// cloneDir clones the directory from to the path to, which does not exist
// yet, copy-on-write. It returns an errCloneUnsupported when the filesystem
// cannot, and leaves nothing at to on any failure. A variable so tests can
// stand in for a filesystem that cannot clone.
var cloneDir = cloneTreeCOW

var errEscapes = errors.New("escapes the worktree")

// makeParents makes the directories above to, and returns the topmost one it
// made, or "" if none. It refuses with errEscapes when the deepest directory
// that exists resolves outside target, as a symlinked parent does, so nothing
// is made or cloned through the link.
func makeParents(target, to string) (made string, err error) {
	dir := filepath.Dir(to)
	have := dir
	for {
		if _, err := os.Lstat(have); err == nil {
			break
		}
		parent := filepath.Dir(have)
		if parent == have {
			break
		}
		have = parent
	}
	root, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(have)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		return "", errEscapes
	}
	if have != dir {
		rel, _ := filepath.Rel(have, dir)
		made = filepath.Join(have, strings.Split(rel, string(filepath.Separator))[0])
	}
	return made, os.MkdirAll(dir, 0o755)
}

// sweepStaleClones removes the temporary trees a killed run left beside to,
// whether or not to is there now. It leaves a directory that does not exist,
// or that resolves outside target, alone.
func sweepStaleClones(target, to string) error {
	dir := filepath.Dir(to)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	root, err := filepath.EvalSymlinks(target)
	if err != nil || !within(root, resolved) {
		return nil
	}
	return removeStaleClones(dir, filepath.Base(to))
}

// removeParents removes the empty directories from dir up to made, which
// makeParents made for a cache that did not arrive.
func removeParents(made, dir string) {
	for made != "" {
		if os.Remove(dir) != nil || dir == made {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// cloneCacheDirs clones each WORKTREE_CACHE_DIRS entry from src into target,
// and returns what the cache step came to.
func cloneCacheDirs(ctx *kit.Context, src, target string, opts SetupOptions, w io.Writer) (result, reason string) {
	dirs := ctx.Config.CacheDirs
	switch {
	case len(dirs) == 0:
		return kit.StepSkipped, "no WORKTREE_CACHE_DIRS"
	case opts.NoCache:
		fmt.Fprintln(w, "⏭ build caches skipped (--no-cache)")
		return kit.StepSkipped, "--no-cache"
	}
	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(w, " - source %s not found; skipping build caches\n", src)
		return kit.StepSkipped, "source " + src + " not found"
	}
	var failed, unclonable []string
	cloned := 0
	for _, d := range dirs {
		from, to := filepath.Join(src, d), filepath.Join(target, d)
		if !within(target, to) {
			fmt.Fprintf(w, " ! %s escapes the worktree, refusing to clone it\n", d)
			failed = append(failed, d+" escapes the worktree")
			continue
		}
		if err := sweepStaleClones(target, to); err != nil {
			fmt.Fprintf(w, " ! cannot remove an earlier clone of %s: %v\n", d, err)
			failed = append(failed, fmt.Sprintf("%s: %v", d, err))
			continue
		}
		st, err := os.Lstat(from)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(w, " - %s not in source, skipping\n", d)
			continue
		case err != nil:
			fmt.Fprintf(w, " ! cannot look up %s in the source: %v\n", d, err)
			failed = append(failed, fmt.Sprintf("%s: %v", d, err))
			continue
		case st.Mode()&fs.ModeSymlink != 0:
			fmt.Fprintf(w, " - %s is a symlink in the source, not followed, skipping\n", d)
			continue
		case !st.IsDir():
			fmt.Fprintf(w, " - %s is not a directory in the source, skipping\n", d)
			continue
		}
		if _, err := os.Lstat(to); err == nil {
			fmt.Fprintf(w, " - %s already exists, skipping\n", d)
			continue
		}
		made, err := makeParents(target, to)
		if errors.Is(err, errEscapes) {
			fmt.Fprintf(w, " ! %s escapes the worktree through a symlink, refusing to clone it\n", d)
			failed = append(failed, d+" escapes the worktree")
			continue
		}
		if err == nil {
			err = cloneDir(from, to)
		}
		if err != nil {
			removeParents(made, filepath.Dir(to))
		}
		var unsupported errCloneUnsupported
		switch {
		case errors.As(err, &unsupported):
			fmt.Fprintf(w, " - %s not cloned: %s; skipped rather than copied\n", d, unsupported.why)
			unclonable = append(unclonable, d+": "+unsupported.why)
		case err != nil:
			fmt.Fprintf(w, " ! failed to clone %s: %v\n", d, err)
			failed = append(failed, fmt.Sprintf("%s: %v", d, err))
		default:
			fmt.Fprintf(w, " ✓ cloned %s\n", d)
			cloned++
		}
	}
	switch {
	case len(failed) > 0:
		return kit.StepFailed, strings.Join(append(failed, unclonable...), "; ")
	case cloned == 0 && len(unclonable) > 0:
		return kit.StepSkipped, strings.Join(unclonable, "; ")
	}
	return kit.StepDone, strings.Join(unclonable, "; ")
}
