package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Root is a directory repositories sit in, one level down, and the name
// --roots selects it by — or a repository on its own, such as ~/dotfiles: a
// root that is itself a main checkout is that one repository, and nothing
// under it is searched.
type Root struct {
	Name string
	Path string // absolute, ~ expanded
}

// RootsFor is where this person's repositories live, and what said so:
// WT_ROOTS when the environment sets it, the [roots] table of the user file,
// or wt's defaults. A root from WT_ROOTS or the defaults is named after its
// directory.
func RootsFor(u *config.User) ([]Root, string) {
	if raw := os.Getenv("WT_ROOTS"); raw != "" {
		return namedByDir(strings.Split(raw, ":")), "WT_ROOTS"
	}
	if u != nil && !u.Unusable && len(u.Roots) > 0 {
		out := make([]Root, 0, len(u.Roots))
		for _, r := range u.Roots {
			out = append(out, Root{Name: r.Name, Path: expandHome(r.Path)})
		}
		return out, "user config"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "default"
	}
	var paths []string
	for _, rel := range strings.Split(defaultRoots, ":") {
		paths = append(paths, filepath.Join(home, rel))
	}
	return namedByDir(paths), "default"
}

// namedByDir names each root after its directory, numbering a name two roots
// share so --roots can still tell them apart.
func namedByDir(paths []string) []Root {
	var out []Root
	seen := map[string]int{}
	for _, p := range paths {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		p = expandHome(p)
		name := filepath.Base(p)
		if seen[name]++; seen[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, seen[name])
		}
		out = append(out, Root{Name: name, Path: p})
	}
	return out
}

// expandHome turns a leading ~ into the home directory, which is how a person
// writes a path in a file a shell never reads.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// RepoTarget is one repository a multi-repo command works on.
type RepoTarget struct {
	Name string // the main checkout's directory name
	Path string // the main checkout
	Root string // the root it sits in, "" for a profile entry outside every root
	// Problem is why the command cannot work on it — a profile entry that is
	// not a repository any more, say — and "" when it can.
	Problem string
}

// Selection is which repositories a command covers: every one under every
// root, the ones under the named roots, or a named profile's.
type Selection struct {
	All     bool
	Roots   []string
	Profile string
}

// Any reports that a selection was asked for at all, which is what turns a
// single-repository command into a multi-repository one.
func (s Selection) Any() bool { return s.All || len(s.Roots) > 0 || s.Profile != "" }

// RepoSet is what a selection found: the repositories wt manages, and the
// checkouts under the same roots it does not.
type RepoSet struct {
	Repos     []RepoTarget
	Unmanaged []string
}

// SelectRepos resolves a selection against the user's roots and profiles. A
// root or profile name the file does not have is an error naming the ones it
// does; a profile entry that is no longer a repository wt manages comes back
// with its Problem, so the command reports it and gets on with the rest.
func SelectRepos(u *config.User, sel Selection) (RepoSet, error) {
	// A file wt cannot read may name roots it cannot see; falling back to the
	// defaults would act on repositories nobody chose.
	if u != nil && (u.Unusable || u.TablesUnreadable) && os.Getenv("WT_ROOTS") == "" {
		return RepoSet{}, fmt.Errorf("%s cannot be read, so wt does not know your roots; wt doctor says why", u.Path)
	}
	roots, _ := RootsFor(u)
	if sel.Roots != nil && sel.Profile != "" {
		return RepoSet{}, fmt.Errorf("--roots and --profile each name the repositories; give one of them")
	}
	if sel.Profile != "" {
		p, ok := u.Profile(sel.Profile)
		if !ok {
			return RepoSet{}, fmt.Errorf("no profile named %q%s", sel.Profile, known("profiles", profileNames(u)))
		}
		var set RepoSet
		for _, path := range p.Repos {
			t := profileTarget(path, roots)
			// The same repository twice, by two spellings, would be fetched
			// and swept twice at once.
			if slices.ContainsFunc(set.Repos, func(o RepoTarget) bool { return repo.SamePath(o.Path, t.Path) }) {
				continue
			}
			set.Repos = append(set.Repos, t)
		}
		return set, nil
	}
	chosen := roots
	if len(sel.Roots) > 0 {
		chosen = nil
		for _, name := range sel.Roots {
			i := slices.IndexFunc(roots, func(r Root) bool { return r.Name == name })
			if i < 0 {
				return RepoSet{}, fmt.Errorf("no root named %q%s", name, known("roots", rootNames(roots)))
			}
			chosen = append(chosen, roots[i])
		}
	}
	return discoverRepos(chosen), nil
}

// discoverRepos is every main checkout one level under each root, once, and
// the root itself where it is one. A linked worktree at that depth is its
// repository's, which is listed where its own main checkout is; a <repo>_wt
// folder is not a checkout at all.
func discoverRepos(roots []Root) RepoSet {
	var set RepoSet
	seen := map[string]bool{}
	add := func(dir, root string) {
		r, err := repo.Discover(dir)
		if err != nil || !repo.SamePath(r.MainRoot, dir) || seen[r.MainRoot] {
			return
		}
		seen[r.MainRoot] = true
		if !managed(r.MainRoot) {
			set.Unmanaged = append(set.Unmanaged, r.MainRoot)
			return
		}
		set.Repos = append(set.Repos, RepoTarget{Name: filepath.Base(r.MainRoot), Path: r.MainRoot, Root: root})
	}
	for _, root := range roots {
		if isCheckout(root.Path) {
			add(root.Path, root.Name)
			continue
		}
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if dir := filepath.Join(root.Path, e.Name()); isCheckout(dir) {
				add(dir, root.Name)
			}
		}
	}
	return set
}

// isCheckout reports that dir is the top of a checkout: it has a .git, a
// directory in a main checkout and a file in a linked worktree.
func isCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// managed reports that wt manages the repository whose main checkout is at
// path: it has a configuration wt reads, in either format.
func managed(path string) bool {
	for _, name := range []string{"worktree.toml", "worktree.conf"} {
		if _, err := os.Stat(filepath.Join(path, "bin", "worktree", name)); err == nil {
			return true
		}
	}
	return false
}

// profileTarget checks one profile entry and says what is wrong with it, if
// anything: gone, not a repository, a linked worktree rather than the main
// checkout, or not managed by wt.
func profileTarget(path string, roots []Root) RepoTarget {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		abs = expandHome(path)
	}
	t := RepoTarget{Name: filepath.Base(abs), Path: abs, Root: rootOf(abs, roots)}
	if _, err := os.Stat(abs); err != nil {
		t.Problem = "not there any more"
		return t
	}
	r, err := repo.Discover(abs)
	switch {
	case err != nil:
		t.Problem = "not a git repository"
	case !repo.SamePath(r.MainRoot, abs):
		t.Problem = "a worktree of " + r.MainRoot + ", not its main checkout; name that instead"
	case !managed(abs):
		t.Problem = "not managed by wt (no bin/worktree/worktree.conf or worktree.toml)"
	}
	return t
}

// rootOf is the name of the root path sits inside, "" when it is in none.
func rootOf(path string, roots []Root) string {
	for _, r := range roots {
		if repo.Inside(r.Path, path, true) {
			return r.Name
		}
	}
	return ""
}

func rootNames(roots []Root) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Name)
	}
	return out
}

func profileNames(u *config.User) []string {
	out := make([]string, 0, len(u.Profiles))
	for _, p := range u.Profiles {
		out = append(out, p.Name)
	}
	return out
}

// known finishes an unknown-name error with the names there are.
func known(what string, names []string) string {
	if len(names) == 0 {
		return "; there are no " + what + " (wt config set " + strings.TrimSuffix(what, "s") + ".<name> …)"
	}
	return "; the " + what + " are: " + strings.Join(names, " ")
}

// eachRepo runs fn for every target, at most limit at a time, and returns the
// results in the targets' order, so output printed from them afterwards reads
// the same however the work interleaved.
func eachRepo[T any](targets []RepoTarget, limit int, fn func(RepoTarget) T) []T {
	out := make([]T, len(targets))
	sem := make(chan struct{}, max(limit, 1))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = fn(t)
		}()
	}
	wg.Wait()
	return out
}

// repoParallelism is how many repositories a multi-repo command works on at
// once: enough that twenty fetches do not run end to end, few enough that
// GitHub and the disk are not hammered.
const repoParallelism = 4
