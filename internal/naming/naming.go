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

// SupersetDir returns the path Superset builds for a piece of work. It differs
// from the canonical one by a repeated repository name, because Superset joins
// its per-project worktree base directory with <repo>/<branch> and that base is
// already <parent>/<repo><suffix>. No setting on either side removes the extra
// segment, so the two tools cannot be made to agree on one path.
func SupersetDir(parent, repoName, typ, work, suffix string) string {
	return filepath.Join(parent, repoName+suffix, repoName, typ+suffix, work)
}

// Layout names the shape a worktree's path follows.
type Layout int

const (
	// Foreign is a path wt does not recognise: a pre-migration <repo>-<work>
	// checkout, or a plain `git worktree add` anywhere at all. Nothing holds
	// these paths, so `wt migrate` is free to move them.
	Foreign Layout = iota
	// Canonical is the path wt itself creates.
	Canonical
	// Superset is the path Superset creates. It is hardcoded in that tool,
	// which also stores the absolute path of every workspace, so moving one of
	// these breaks the workspace that owns it.
	Superset
)

func (l Layout) String() string {
	switch l {
	case Canonical:
		return "canonical"
	case Superset:
		return "superset"
	default:
		return "foreign"
	}
}

// Classify reports which layout path follows for the given piece of work.
func Classify(path, parent, repoName, typ, work, suffix string) Layout {
	switch path {
	case WorktreeDir(parent, repoName, typ, work, suffix):
		return Canonical
	case SupersetDir(parent, repoName, typ, work, suffix):
		return Superset
	default:
		return Foreign
	}
}

// ParseSpec reads a "<type>/<work>" argument, or a bare "<work>" whose type is
// read out of the name when it starts with one, and is defaultType otherwise.
// An explicit type always wins over the name.
func ParseSpec(spec, defaultType string, types []string) (typ, work string, err error) {
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
	if t, rest, ok := InferType(spec, types); ok {
		return t, rest, nil
	}
	return defaultType, spec, nil
}

// InferType splits a leading "<type>_" or "<type>-" off a work name, when that
// prefix is one of the repository's types and something is left after it.
//
// The type is a judgement about the work, and the two tools that create
// worktrees here cannot both be told it: Superset mints every branch from one
// fixed prefix, so the only place the type can travel is inside the name a
// person types. "fix_dev-123" is a fix, not a feature called "fix_dev-123".
func InferType(work string, types []string) (typ, rest string, ok bool) {
	for _, sep := range []string{"_", "-"} {
		head, tail, found := strings.Cut(work, sep)
		if !found || head == "" || tail == "" {
			continue
		}
		for _, t := range types {
			if head == t {
				return head, tail, true
			}
		}
	}
	return "", "", false
}
