package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StagedConflicts reads a stopped rebase's unmerged paths from the
// worktree's index. Stage 1 is the base, stage 2 is trunk (HEAD while a
// rebase replays), stage 3 is the replayed commit. A path missing a stage or
// carrying a non-regular mode is Incomplete, exactly as the simulation marks
// it, so no strategy ever sees it.
func StagedConflicts(wtPath string) ([]Conflict, error) {
	out, err := gitEnv(wtPath, nil, nil, "ls-files", "-u", "-z")
	if err != nil {
		return nil, fmt.Errorf("ls-files -u: %w", err)
	}
	entries, _ := parseStages(strings.Split(out, "\x00"))
	cs := conflictsFrom(entries)
	if err := readBlobs(wtPath, cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// Progress is where a stopped rebase is: the pick it is at among the picks,
// and the last command the sequencer ran, which is what stopped it — pick
// for a conflict, and the edit or break a run puts in the list around a pick
// that may need a version lift.
type Progress struct {
	Index   int
	Total   int
	Commit  string
	Subject string
	Command string
	// Applied says the sequencer stopped after applying the pick, for an
	// edit: HEAD is past the pick (or, when git dropped the pick as empty,
	// unmoved), and there is nothing unmerged. It is git's amend marker; an
	// edit that stopped at a conflict is not applied, and has none.
	Applied bool
}

// sequencerDirs resolves the sequencer's directories by name, in one git and
// in the order given, each made absolute. Where they live is git's to say, so
// they are asked for rather than built from the git dir: a worktree's
// sequencer state does not sit where a plain join would put it.
func sequencerDirs(wtPath string, names ...string) ([]string, error) {
	args := []string{"rev-parse"}
	for _, name := range names {
		args = append(args, "--git-path", name)
	}
	out, err := gitEnv(wtPath, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	dirs := strings.Split(out, "\n")
	if len(dirs) != len(names) {
		return nil, fmt.Errorf("rev-parse --git-path: %d paths for %d names", len(dirs), len(names))
	}
	for i, dir := range dirs {
		if !filepath.IsAbs(dir) {
			dirs[i] = filepath.Join(wtPath, dir)
		}
	}
	return dirs, nil
}

// sequencerDir is sequencerDirs for one name.
func sequencerDir(wtPath, name string) (string, error) {
	dirs, err := sequencerDirs(wtPath, name)
	if err != nil {
		return "", err
	}
	return dirs[0], nil
}

func rebaseDir(wtPath string) (string, error) {
	return sequencerDir(wtPath, "rebase-merge")
}

// RebaseInProgress reports whether the worktree is mid-rebase under either
// backend: run forces the merge backend (rebase-merge), but a rebase someone
// started by hand with the apply backend (rebase-apply) must block too. The
// drive loop asks this at every stop, so both names are resolved in one git.
func RebaseInProgress(wtPath string) (bool, error) {
	dirs, err := sequencerDirs(wtPath, "rebase-merge", "rebase-apply")
	if err != nil {
		return false, err
	}
	for _, dir := range dirs {
		_, err := os.Stat(dir)
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

// RebaseProgress reads the sequencer's own bookkeeping: the commands done
// and the commands left for the position, stopped-sha for the commit,
// message for its subject. The position counts picks, not lines: msgnum and
// end count every line of the list, and a run's list carries a break before
// each pick that may need a version lift, which would put every stop after
// it one line further along than the commit it is at.
func RebaseProgress(wtPath string) (Progress, error) {
	dir, err := rebaseDir(wtPath)
	if err != nil {
		return Progress{}, err
	}
	var p Progress
	done, err := os.ReadFile(filepath.Join(dir, "done"))
	if err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	todo, err := os.ReadFile(filepath.Join(dir, "git-rebase-todo"))
	if err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	var left int
	p.Index, p.Command = countPicks(string(done))
	left, _ = countPicks(string(todo))
	p.Total = p.Index + left
	if b, err := os.ReadFile(filepath.Join(dir, "stopped-sha")); err == nil {
		p.Commit = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "message")); err == nil {
		p.Subject, _, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "amend")); err == nil {
		p.Applied = true
	}
	return p, nil
}

// countPicks counts the commands in a sequencer list that apply a commit,
// and names the last command of any kind, "" for an empty list. Comments
// and blank lines are neither.
func countPicks(list string) (picks int, last string) {
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		last = f[0]
		switch f[0] {
		case "pick", "p", "edit", "e", "reword", "r", "squash", "s", "fixup", "f", "merge", "m":
			picks++
		}
	}
	return picks, last
}

// RebaseTarget is what a merge-backend rebase in progress was started with,
// as the sequencer recorded it: the ref it moves when it finishes
// (head-name), the commit it replays onto (onto), and the tip it started
// from (orig-head).
type RebaseTarget struct {
	HeadName string
	Onto     string
	OrigHead string
}

// ReadRebaseTarget reads a merge-backend rebase's head-name, onto and
// orig-head. ok is false when there is no rebase-merge directory: no rebase
// at all, or one on the apply backend, which no run ever starts.
func ReadRebaseTarget(wtPath string) (RebaseTarget, bool, error) {
	dir, err := rebaseDir(wtPath)
	if err != nil {
		return RebaseTarget{}, false, err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return RebaseTarget{}, false, nil
	} else if err != nil {
		return RebaseTarget{}, false, err
	}
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("rebase %s: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	var t RebaseTarget
	if t.HeadName, err = read("head-name"); err != nil {
		return t, true, err
	}
	if t.Onto, err = read("onto"); err != nil {
		return t, true, err
	}
	if t.OrigHead, err = read("orig-head"); err != nil {
		return t, true, err
	}
	return t, true, nil
}

// Apply writes a strategy's answer over the conflicted working file, leaving
// the mode git merged, and stages it, which clears the unmerged entries.
func Apply(wtPath string, c Conflict, content []byte) error {
	full := filepath.Join(wtPath, filepath.FromSlash(c.Path))
	// WriteFile's permission argument applies only when it creates the file.
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return err
	}
	_, err := gitEnv(wtPath, nil, nil, "--literal-pathspecs", "add", "--", c.Path)
	return err
}
