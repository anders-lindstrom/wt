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
	type entry struct{ mode, oid string }
	stages := map[string]map[int]entry{}
	var order []string
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(f[2])
		if stages[path] == nil {
			stages[path] = map[int]entry{}
			order = append(order, path)
		}
		stages[path][stage] = entry{mode: f[0], oid: f[1]}
	}
	var cs []Conflict
	for _, path := range order {
		c := Conflict{Path: path}
		s := stages[path]
		missing := 0
		for i := 1; i <= 3; i++ {
			if _, ok := s[i]; !ok {
				missing++
			}
		}
		switch {
		case missing > 0 && s[1].oid == "" && s[2].oid != "" && s[3].oid != "":
			c.Incomplete = "both sides added it"
		case missing > 0:
			c.Incomplete = "one side deleted or renamed it"
		}
		for i := 1; i <= 3; i++ {
			e, ok := s[i]
			if !ok {
				continue
			}
			if e.mode != "100644" && e.mode != "100755" {
				c.Incomplete = "not a regular file (mode " + e.mode + ")"
			}
		}
		if c.Incomplete != "" {
			cs = append(cs, c)
			continue
		}
		read := func(oid string) ([]byte, error) { return catFileRaw(wtPath, oid) }
		if c.Base, err = read(s[1].oid); err != nil {
			return nil, err
		}
		if c.Trunk, err = read(s[2].oid); err != nil {
			return nil, err
		}
		if c.Branch, err = read(s[3].oid); err != nil {
			return nil, err
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
// (head-name) and the commit it replays onto (onto).
type RebaseTarget struct {
	HeadName string
	Onto     string
}

// ReadRebaseTarget reads a merge-backend rebase's head-name and onto. ok is
// false when there is no rebase-merge directory: no rebase at all, or one on
// the apply backend, which no run ever starts.
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
	_, err := gitEnv(wtPath, nil, nil, "add", "--", c.Path)
	return err
}
