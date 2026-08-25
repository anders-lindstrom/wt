// Package naming converts between a piece of work, its branch and its path.
//
// The layout is <parent>/<repo><suffix>/<type><suffix>/<work>, so the path tail
// below <repo><suffix>/ is character-for-character the branch name. Path and
// branch therefore convert to each other with no rules to remember, and
// `git worktree list` reads identically to the directory tree.
package naming

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// BranchName builds the branch for a piece of work, e.g. fix_wt/login-crash.
func BranchName(typ, work, suffix string) string {
	return typ + suffix + "/" + work
}

// ParseBranch splits a worktree branch into its type and work name. ok is false
// for any branch that does not follow the convention.
func ParseBranch(branch, suffix string) (typ, work string, ok bool) {
	head, rest, found := strings.Cut(branch, "/")
	if !found || rest == "" {
		return "", "", false
	}
	typ, ok = strings.CutSuffix(head, suffix)
	if !ok || typ == "" {
		return "", "", false
	}
	return typ, rest, true
}

// StripPrefix returns the work name, or the branch unchanged when it does not
// follow the convention.
func StripPrefix(branch, suffix string) string {
	if _, work, ok := ParseBranch(branch, suffix); ok {
		return work
	}
	return branch
}

// WorktreeDir returns the canonical absolute path for a piece of work.
func WorktreeDir(parent, repoName, typ, work, suffix string) string {
	return filepath.Join(parent, repoName+suffix, typ+suffix, work)
}

// ParseSpec reads a "<type>/<work>" argument, or a bare "<work>" which takes
// defaultType so that every pre-existing invocation stays valid.
func ParseSpec(spec, defaultType string) (typ, work string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("no work name given")
	}
	if strings.Count(spec, "/") > 1 {
		return "", "", fmt.Errorf("%q has too many slashes; expected <type>/<work> or <work>", spec)
	}
	if head, rest, found := strings.Cut(spec, "/"); found {
		if head == "" || rest == "" {
			return "", "", fmt.Errorf("%q is not a valid <type>/<work>", spec)
		}
		return head, rest, nil
	}
	return defaultType, spec, nil
}
