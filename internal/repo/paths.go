package repo

import (
	"path/filepath"
	"strings"
)

// SamePath compares two paths, resolving symlinks only if the plain comparison
// fails — macOS puts temporary directories behind /var -> /private/var, and git
// and the shell do not always agree on which side of it a worktree lives.
func SamePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// Inside reports whether p is dir itself or a path under it.
//
// resolve asks the question again against both paths with their symlinks
// resolved when the plain comparison says no, the way SamePath does. It is a
// parameter rather than the rule because the callers genuinely differ: a cwd
// and a worktree path can sit on opposite sides of a /var -> /private/var
// link, while a path built by joining a configured name onto a directory
// never can, and resolving it would only cost a stat of something that may
// not exist yet.
func Inside(dir, p string, resolve bool) bool {
	if under(dir, p) {
		return true
	}
	if !resolve {
		return false
	}
	rdir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	return under(rdir, rp)
}

func under(dir, p string) bool {
	dir, p = filepath.Clean(dir), filepath.Clean(p)
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}
