package repo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// statusDeadline bounds each status a removal reads. A status that has not
// answered by then is not an answer, and a removal refuses on it.
var statusDeadline = 2 * time.Minute

// StrictStatus is what a removal needs to know about a checkout's files
// before it deletes them.
type StrictStatus struct {
	// Dirty is anything uncommitted: a tracked change, a staged one, an
	// untracked file, an edit to a file status is told not to look at, or
	// any of those inside an initialised submodule.
	Dirty bool
	// Hidden are unedited files marked assume-unchanged, or skip-worktree
	// and still on disk: status is told not to look at them. Paths are
	// relative to the checkout.
	Hidden []string
	// Moved are submodules checked out at another commit than the one the
	// checkout records, with nothing else changed in them.
	Moved []string
}

// DirtyStrict reads the checkout at path the way a removal has to: it fails
// rather than guesses, and no configuration can hide a change from it.
// --untracked-files=normal overrides status.showUntrackedFiles,
// --ignore-submodules=none overrides submodule.<name>.ignore and
// diff.ignoreSubmodules, and core.fsmonitor is off, because git takes a
// filesystem monitor's word that a file did not change. A file marked
// assume-unchanged or skip-worktree is hashed and compared with the index.
// Each initialised submodule is then read the same way in its own right, so
// a submodule's own config cannot hide anything. Ignored files are not
// counted: they are what a build leaves.
func DirtyStrict(path string) (StrictStatus, error) {
	var s StrictStatus
	err := readStrict(path, "", &s)
	return s, err
}

func readStrict(path, prefix string, s *StrictStatus) error {
	out, err := git.Exec(git.Opts{Dir: path, Timeout: statusDeadline}, "--no-optional-locks",
		"-c", "core.fsmonitor=false", "status", "--porcelain=v2", "-z",
		"--ignore-submodules=none", "--untracked-files=normal")
	if err != nil {
		return err
	}
	readEntries(string(out), prefix, s)
	index, err := git.Exec(git.Opts{Dir: path, Timeout: statusDeadline}, "-c", "core.fsmonitor=false",
		"ls-files", "-v", "-s", "-z")
	if err != nil {
		return err
	}
	var submodules []string
	var hidden []indexEntry
	for _, e := range indexEntries(index) {
		if e.mode == "160000" {
			if _, err := os.Stat(filepath.Join(path, e.path, ".git")); err == nil {
				submodules = append(submodules, e.path)
			}
			continue
		}
		// A lowercase tag is assume-unchanged; S and s are skip-worktree.
		marked := e.tag == "S" || e.tag != strings.ToUpper(e.tag)
		if !marked {
			continue
		}
		// Not on disk is nothing to lose: a sparse checkout leaves files out.
		if _, err := os.Lstat(filepath.Join(path, e.path)); err == nil {
			hidden = append(hidden, e)
		}
	}
	edited, err := editedSince(path, hidden)
	if err != nil {
		return err
	}
	for _, e := range hidden {
		if edited[e.path] {
			s.Dirty = true
		} else {
			s.Hidden = append(s.Hidden, prefix+e.path)
		}
	}
	for _, sm := range submodules {
		if err := readStrict(filepath.Join(path, sm), prefix+sm+"/", s); err != nil {
			return fmt.Errorf("submodule %s: %w", prefix+sm, err)
		}
	}
	return nil
}

// readEntries sorts a porcelain v2 -z status into s: a submodule whose only
// change is its checked-out commit is Moved, anything else is Dirty.
func readEntries(out, prefix string, s *StrictStatus) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if e == "" {
			continue
		}
		f := strings.SplitN(e, " ", 9)
		switch {
		case f[0] == "2":
			i++ // a rename or copy is followed by the path it came from
		case f[0] == "1" && len(f) == 9 && f[1] == ".M" && f[2] == "SC..":
			s.Moved = append(s.Moved, prefix+f[8])
			continue
		}
		s.Dirty = true
	}
}

// indexEntry is one line of ls-files -v -s.
type indexEntry struct{ tag, mode, oid, path string }

func indexEntries(out []byte) []indexEntry {
	var list []indexEntry
	for _, entry := range strings.Split(string(out), "\x00") {
		// "<tag> <mode> <oid> <stage>\t<path>"
		meta, name, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			continue
		}
		list = append(list, indexEntry{tag: f[0], mode: f[1], oid: f[2], path: name})
	}
	return list
}

// editedSince hashes each file the way git add would and names the ones
// whose content is no longer what the index has.
func editedSince(path string, files []indexEntry) (map[string]bool, error) {
	edited := map[string]bool{}
	var regular []indexEntry
	for _, e := range files {
		if e.mode != "120000" {
			regular = append(regular, e)
			continue
		}
		target, err := os.Readlink(filepath.Join(path, e.path))
		if err != nil {
			edited[e.path] = true
			continue
		}
		out, err := git.Exec(git.Opts{Dir: path, Stdin: strings.NewReader(target)}, "hash-object", "--stdin", "--no-filters")
		if err != nil {
			return nil, err
		}
		edited[e.path] = strings.TrimSpace(string(out)) != e.oid
	}
	if len(regular) == 0 {
		return edited, nil
	}
	var in bytes.Buffer
	for _, e := range regular {
		in.WriteString(e.path + "\n")
	}
	out, err := git.Exec(git.Opts{Dir: path, Stdin: &in, Timeout: statusDeadline}, "hash-object", "--stdin-paths")
	if err != nil {
		return nil, err
	}
	oids := strings.Fields(string(out))
	if len(oids) != len(regular) {
		return nil, fmt.Errorf("git hash-object answered for %d of %d files", len(oids), len(regular))
	}
	for i, e := range regular {
		edited[e.path] = oids[i] != e.oid
	}
	return edited, nil
}

// HeadOf is the commit checked out at path, "" for a branch with no commit
// yet. Any other HEAD that cannot be read is an error, never "no commit".
func HeadOf(path string) (string, error) {
	head, err := git.Run(path, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err == nil && head != "" {
		return head, nil
	}
	if ref, rerr := git.Run(path, "symbolic-ref", "--quiet", "HEAD"); rerr == nil && ref != "" {
		if _, serr := git.Run(path, "show-ref", "--verify", "--quiet", ref); serr != nil {
			return "", nil
		}
	}
	if err == nil {
		err = errors.New("no commit")
	}
	return "", fmt.Errorf("cannot read HEAD: %s", git.Reason(err))
}

// SubmoduleLoss is a submodule of a checkout whose commits live only in the
// git dir the removal deletes.
type SubmoduleLoss struct {
	Path  string // relative to the checkout
	OID   string // its HEAD, or the first branch holding what is lost
	Count int
}

// SubmoduleLosses finds the commits the checkout at path's initialised
// submodules would take with them. A linked worktree keeps each submodule's
// repository in its own admin dir (.git/worktrees/<id>/modules/<name>), so
// removing it deletes that repository, local branches and all. What
// survives is what its remote has, as its remote-tracking refs and tags say,
// and the main checkout's own copy of the submodule. Each submodule's HEAD
// and local branches are counted against those.
func SubmoduleLosses(path string) ([]SubmoduleLoss, error) {
	common, err := git.Run(path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	admin, err := git.Run(path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	return submoduleLosses(path, "", filepath.Join(common, "modules"), []string{path, admin})
}

func submoduleLosses(path, prefix, survivors string, doomed []string) ([]SubmoduleLoss, error) {
	index, err := git.Exec(git.Opts{Dir: path}, "ls-files", "-v", "-s", "-z")
	if err != nil {
		return nil, err
	}
	var lost []SubmoduleLoss
	for _, e := range indexEntries(index) {
		if e.mode != "160000" {
			continue
		}
		dir := filepath.Join(path, e.path)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		gitDir, err := git.Run(dir, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return nil, fmt.Errorf("submodule %s: %w", prefix+e.path, err)
		}
		if !slices.ContainsFunc(doomed, func(d string) bool { return Inside(d, gitDir, true) }) {
			continue // its repository lives elsewhere and stays
		}
		survivor := filepath.Join(survivors, submoduleName(path, e.path))
		l, err := submoduleLoss(dir, survivor)
		if err != nil {
			return nil, fmt.Errorf("submodule %s: %w", prefix+e.path, err)
		}
		if l.Count > 0 {
			l.Path = prefix + e.path
			lost = append(lost, l)
		}
		nested, err := submoduleLosses(dir, prefix+e.path+"/", filepath.Join(survivor, "modules"), doomed)
		if err != nil {
			return nil, err
		}
		lost = append(lost, nested...)
	}
	return lost, nil
}

// submoduleLoss counts what the submodule at dir has on its HEAD and local
// branches that neither its remote-tracking refs and tags nor the surviving
// copy at survivor hold.
func submoduleLoss(dir, survivor string) (SubmoduleLoss, error) {
	refs, err := git.Lines(dir, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return SubmoduleLoss{}, err
	}
	var tips, keep []string
	if head, err := HeadOf(dir); err != nil {
		return SubmoduleLoss{}, err
	} else if head != "" {
		tips = append(tips, head)
	}
	for _, line := range refs {
		oid, ref, _ := strings.Cut(line, " ")
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			tips = append(tips, oid)
		case strings.HasPrefix(ref, "refs/remotes/"), strings.HasPrefix(ref, "refs/tags/"):
			keep = append(keep, oid)
		}
	}
	if len(tips) == 0 {
		return SubmoduleLoss{}, nil
	}
	if _, err := os.Stat(survivor); err == nil {
		theirs, err := git.Lines(dir, "--git-dir="+survivor, "for-each-ref", "--format=%(objectname)")
		if err != nil {
			return SubmoduleLoss{}, err
		}
		keep = append(keep, theirs...)
	}
	var in bytes.Buffer
	for _, t := range tips {
		in.WriteString(t + "\n")
	}
	for _, k := range keep {
		in.WriteString("^" + k + "\n")
	}
	// The surviving copy's refs can name commits this one never fetched.
	out, err := git.Exec(git.Opts{Dir: dir, Stdin: &in}, "rev-list", "--count", "--ignore-missing", "--stdin")
	if err != nil {
		return SubmoduleLoss{}, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return SubmoduleLoss{}, err
	}
	return SubmoduleLoss{OID: tips[0], Count: n}, nil
}

// submoduleName is the name .gitmodules gives the submodule at path, which
// names its repository under modules/; the path itself when it gives none.
func submoduleName(checkout, path string) string {
	out, err := git.Exec(git.Opts{Dir: checkout}, "config", "-z", "-f", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return path
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		key, value, ok := strings.Cut(entry, "\n")
		if ok && value == path {
			return strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		}
	}
	return path
}

// operations are the files in a worktree's git dir that say an operation is
// stopped halfway, and what git calls it. The sequencer directory outlives
// CHERRY_PICK_HEAD and REVERT_HEAD while a range is still being picked.
var operations = []struct{ file, name string }{
	{"rebase-merge", "rebase"},
	{"rebase-apply", "rebase"},
	{"MERGE_HEAD", "merge"},
	{"CHERRY_PICK_HEAD", "cherry-pick"},
	{"REVERT_HEAD", "revert"},
	{"sequencer", "cherry-pick or revert"},
	{"BISECT_LOG", "bisect"},
	{"BISECT_START", "bisect"},
}

// OperationInProgress names the git operation stopped halfway in the
// worktree at path — "rebase", "merge", "cherry-pick", "revert", "bisect" —
// read from the worktree's own git dir; "" when there is none. A git dir that
// cannot be read is an error, never "none".
func OperationInProgress(path string) (string, error) {
	dir, err := gitDirOf(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	for _, op := range operations {
		_, err := os.Lstat(filepath.Join(dir, op.file))
		switch {
		case err == nil:
			return op.name, nil
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
	}
	return "", nil
}

// Survivors is every commit that still holds history after a removal: each
// ref but those named in drop, and the HEAD of every worktree but the one at
// leaving. It is named ref by ref rather than asked of --all, which would
// count the refs about to go.
func (r *Repo) Survivors(drop []string, leaving string) ([]string, error) {
	out, err := git.Exec(git.Opts{Dir: r.MainRoot}, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil, err
	}
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		oid, ref, ok := strings.Cut(line, " ")
		if !ok || slices.Contains(drop, ref) {
			continue
		}
		keep = append(keep, oid)
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWorktreesUnknown, err)
	}
	for _, wt := range worktrees {
		if wt.Head != "" && !SamePath(wt.Path, leaving) {
			keep = append(keep, wt.Head)
		}
	}
	return keep, nil
}

// CountLost counts the commits reachable from tip that none of keep reaches:
// what would become unreachable if tip were the only thing holding them. The
// ids go on stdin, so no number of refs makes the command line too long.
func (r *Repo) CountLost(tip string, keep []string) (int, error) {
	var in bytes.Buffer
	in.WriteString(tip + "\n")
	for _, k := range keep {
		in.WriteString("^" + k + "\n")
	}
	out, err := git.Exec(git.Opts{Dir: r.MainRoot, Stdin: &in}, "rev-list", "--count", "--stdin")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
