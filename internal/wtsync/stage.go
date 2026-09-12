package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	var cs []Conflict
	for _, sc := range conflictsFrom(entries) {
		c := sc.Conflict
		if c.Incomplete == "" {
			if err := readStages(wtPath, &c, sc.OID); err != nil {
				return nil, err
			}
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// Progress is where a stopped rebase is.
type Progress struct {
	Index   int
	Total   int
	Commit  string
	Subject string
}

func rebaseDir(wtPath string) (string, error) {
	return gitEnv(wtPath, nil, nil, "rev-parse", "--git-path", "rebase-merge")
}

// RebaseInProgress reports whether the worktree is mid-rebase under either
// backend: run forces the merge backend (rebase-merge), but a rebase someone
// started by hand with the apply backend (rebase-apply) must block too.
func RebaseInProgress(wtPath string) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		dir, err := gitEnv(wtPath, nil, nil, "rev-parse", "--git-path", name)
		if err != nil {
			return false, err
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(wtPath, dir)
		}
		_, err = os.Stat(dir)
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

// RebaseProgress reads the sequencer's own bookkeeping: msgnum/end for the
// position, stopped-sha for the commit, message for its subject.
func RebaseProgress(wtPath string) (Progress, error) {
	dir, err := rebaseDir(wtPath)
	if err != nil {
		return Progress{}, err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wtPath, dir)
	}
	readInt := func(name string) (int, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(b)))
	}
	var p Progress
	if p.Index, err = readInt("msgnum"); err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	if p.Total, err = readInt("end"); err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stopped-sha")); err == nil {
		p.Commit = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "message")); err == nil {
		p.Subject, _, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	}
	return p, nil
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
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wtPath, dir)
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
