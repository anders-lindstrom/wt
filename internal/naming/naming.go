// Package naming converts between a piece of work, its branch and its path.
//
// The layout is <parent>/<repo><suffix>/<type><suffix>/<work>, so the path tail
// below <repo><suffix>/ is character-for-character the branch name. Path and
// branch therefore convert to each other with no rules to remember, and
// `git worktree list` reads identically to the directory tree.
//
// A branch may carry a different suffix from the folders, or none at all —
// feature/login-crash under feature_wt/login-crash — when a repository or the
// person running wt says so. The layout on disk is unchanged by that: the
// folders are wt's, and only the name the branch goes by moves.
package naming

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Scheme is one repository's half of the layout: where its worktrees sit, what
// it is called, and the suffix that marks a type. Every conversion between a
// piece of work, its branch and its path needs all three, so they travel
// together rather than as three strings a caller can swap by accident.
type Scheme struct {
	// Parent is the directory holding <Repo><Suffix>.
	Parent string
	// Repo is the repository's name.
	Repo string
	// Suffix marks a type in a branch name, e.g. "_wt". It is empty where
	// the branches are to read feature/login-crash.
	Suffix string
	// DirSuffix marks a type in a worktree path. It is Suffix where it is
	// not given, and DefaultSuffix where that is empty too: <Parent>/<Repo>
	// is the main checkout, so worktrees under no suffix at all would be
	// created inside it. The folders are wt's layout, not a name anybody
	// types, which is why they always carry one.
	DirSuffix string
}

// Branch builds the branch for a piece of work, e.g. fix_wt/login-crash.
func (s Scheme) Branch(typ, work string) string {
	return typ + s.Suffix + "/" + work
}

// Parse splits a worktree branch into its type and work name. ok is false for
// any branch that does not follow the convention.
func (s Scheme) Parse(branch string) (typ, work string, ok bool) {
	return parseBranch(branch, s.Suffix)
}

// Dir returns the canonical absolute path for a piece of work.
func (s Scheme) Dir(typ, work string) string {
	return filepath.Join(s.Parent, RepoDirName(s.Repo, s.dirSuffix()), typ+s.dirSuffix(), work)
}

// Classify reports which layout path follows for the given piece of work.
func (s Scheme) Classify(path, typ, work string) Layout {
	switch path {
	case s.Dir(typ, work):
		return Canonical
	case SupersetDir(s.Parent, s.Repo, typ, work, s.dirSuffix()):
		return Superset
	default:
		return Foreign
	}
}

// ClassifyBranch reads a worktree's branch and path together: the type and work
// name the branch carries, and the layout its path follows for them. ok is
// false for a branch outside the convention, which leaves the path nothing to
// be classified against.
func (s Scheme) ClassifyBranch(path, branch string) (typ, work string, l Layout, ok bool) {
	typ, work, ok = s.Parse(branch)
	if !ok {
		return "", "", Foreign, false
	}
	return typ, work, s.Classify(path, typ, work), true
}

// ClassifyPath reads the type and work name a path carries, when the path is
// one this scheme spells: <Parent>/<Repo><Suffix>/<typ><Suffix>/<work>.
//
// It names the worktrees whose branch follows no convention: `wt checkout` of
// somebody else's branch, and `wt pr checkout`. Superset's tree has one
// segment more, so its paths do not match.
func (s Scheme) ClassifyPath(path string) (typ, work string, ok bool) {
	sep := string(filepath.Separator)
	rest, found := strings.CutPrefix(filepath.Clean(path), filepath.Join(s.Parent, RepoDirName(s.Repo, s.dirSuffix()))+sep)
	if !found {
		return "", "", false
	}
	head, work, found := strings.Cut(rest, sep)
	if !found || work == "" || strings.Contains(work, sep) {
		return "", "", false
	}
	typ, ok = strings.CutSuffix(head, s.dirSuffix())
	if !ok || typ == "" {
		return "", "", false
	}
	return typ, work, true
}

// dirSuffix is what the folders carry, which is never nothing: the branch's
// suffix where they were given none of their own, and DefaultSuffix where the
// branches carry none either.
func (s Scheme) dirSuffix() string {
	switch {
	case s.DirSuffix != "":
		return s.DirSuffix
	case s.Suffix != "":
		return s.Suffix
	}
	return DefaultSuffix
}

func parseBranch(branch, suffix string) (typ, work string, ok bool) {
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

// DefaultSuffix marks a type in a worktree path, and in the branch that goes
// with it, unless a repository says otherwise.
const DefaultSuffix = "_wt"

// RepoDirName is the folder holding one repository's worktrees:
// <repo><suffix>, or <repo>_wt when it is given no suffix.
func RepoDirName(repoName, suffix string) string {
	if suffix == "" {
		suffix = DefaultSuffix
	}
	return repoName + suffix
}

// StripPrefix returns the work name, or the branch unchanged when it does not
// follow the convention. It takes a bare branch suffix because `wt find` reads
// the branches of repositories it holds nothing else about.
func StripPrefix(branch, suffix string) string {
	if _, work, ok := parseBranch(branch, suffix); ok {
		return work
	}
	return branch
}

// SupersetDir returns the path Superset builds for a piece of work. Like every
// path here it takes the folders' suffix, never a branch's. It differs
// from the canonical one by a repeated repository name, because Superset joins
// its per-project worktree base directory with <repo>/<branch> and that base is
// already <parent>/<repo><suffix>. No setting on either side removes the extra
// segment, so the two tools cannot be made to agree on one path.
func SupersetDir(parent, repoName, typ, work, suffix string) string {
	return filepath.Join(SupersetRoot(parent, repoName, suffix), typ+suffix, work)
}

// SupersetRoot is the directory Superset puts every worktree of a repository
// under.
func SupersetRoot(parent, repoName, suffix string) string {
	return filepath.Join(parent, RepoDirName(repoName, suffix), repoName)
}

// UnderSuperset reports whether a path lies inside Superset's tree. Classify
// answers the same question for one piece of work; this one needs no work
// name, so it also recognises the worktrees whose branch wt cannot parse.
func UnderSuperset(path, parent, repoName, suffix string) bool {
	root := SupersetRoot(parent, repoName, suffix)
	return strings.HasPrefix(filepath.Clean(path), root+string(filepath.Separator))
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
