package wtsync

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// LineLayer is one layer of a stack held on one line: a branch whose tip is
// a commit of the line.
type LineLayer struct {
	Branch  string
	Tip     string
	Count   int // commits from the line's base to the tip
	Subject string
	Named   bool // named with --layer rather than found
}

// SkippedBranch is a branch on the line that is not taken as a layer.
type SkippedBranch struct{ Branch, Tip, Why string }

// Line is a stack held on one first-parent line: the commits from where
// branch leaves trunk (Base) to its tip (Head), oldest first, and the layer
// branches whose tips are among them, bottom first.
type Line struct {
	Branch, Head, Base string
	Commits            []string
	Layers             []LineLayer
	Skipped            []SkippedBranch
}

// fold subjects: a commit git's autosquash folds into another.
var foldPrefixes = []string{"fixup! ", "squash! ", "amend! "}

func isFold(subject string) bool {
	for _, p := range foldPrefixes {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

// ReadLine reads the line of branch against trunk. Without named, the layers
// are the local branches other than branch whose tips lie on the line; a
// branch matching one of backups (wt refs sweep's backup patterns) is listed
// under Skipped instead. With named, the layers are exactly those branches,
// and one whose tip is not on the line is refused. A line with a merge
// commit is refused: a rebase replays it without its merge. So is a layer
// whose tip is a fixup!, squash! or amend! commit: where its layer ends once
// that commit is folded is not defined.
func ReadLine(dir, branch, trunk string, named, backups []string) (Line, error) {
	line := Line{Branch: branch}
	var err error
	if line.Head, err = gitEnv(dir, nil, nil, "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
		return line, err
	}
	if line.Base, err = gitEnv(dir, nil, nil, "merge-base", trunk, line.Head); err != nil {
		return line, fmt.Errorf("%s has no commit in common with %s: %w", branch, trunk, err)
	}
	rng := line.Base + ".." + line.Head
	if out, err := gitEnv(dir, nil, nil, "log", "--merges", "--format=%h %s", rng); err != nil {
		return line, err
	} else if out != "" {
		first, _, _ := strings.Cut(out, "\n")
		return line, fmt.Errorf("the line of %s has a merge commit (%s): a stack is rebased as one line only when it has none", branch, first)
	}
	out, err := gitEnv(dir, nil, nil, "rev-list", "--reverse", rng)
	if err != nil {
		return line, err
	}
	pos := map[string]int{}
	for _, sha := range strings.Fields(out) {
		line.Commits = append(line.Commits, sha)
		pos[sha] = len(line.Commits)
	}
	refs, err := gitEnv(dir, nil, nil, "for-each-ref", "--format=%(refname:short) %(objectname) %(contents:subject)", "refs/heads/")
	if err != nil {
		return line, err
	}
	type found struct{ tip, subject string }
	branches := map[string]found{}
	for _, r := range strings.Split(refs, "\n") {
		name, rest, _ := strings.Cut(r, " ")
		tip, subject, _ := strings.Cut(rest, " ")
		if name != "" && name != branch {
			branches[name] = found{tip, subject}
		}
	}
	add := func(name string, f found, isNamed bool) error {
		if isFold(f.subject) {
			return fmt.Errorf("layer %s ends at a commit autosquash folds (%s %q): where the layer ends is not defined; move the branch, or leave it out with --layer", name, git.ShortID(f.tip, 7), f.subject)
		}
		line.Layers = append(line.Layers, LineLayer{Branch: name, Tip: f.tip, Count: pos[f.tip], Subject: f.subject, Named: isNamed})
		return nil
	}
	if len(named) > 0 {
		for _, name := range named {
			f, ok := branches[name]
			if !ok {
				return line, fmt.Errorf("--layer %s: no such local branch", name)
			}
			if pos[f.tip] == 0 {
				return line, fmt.Errorf("--layer %s: its tip %s is not on the line of %s", name, git.ShortID(f.tip, 7), branch)
			}
			if err := add(name, f, true); err != nil {
				return line, err
			}
		}
	} else {
		names := make([]string, 0, len(branches))
		for name := range branches {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			f := branches[name]
			if pos[f.tip] == 0 {
				continue
			}
			if backup(name, backups) {
				line.Skipped = append(line.Skipped, SkippedBranch{Branch: name, Tip: f.tip, Why: "a backup name; name it with --layer to move it"})
				continue
			}
			if err := add(name, f, false); err != nil {
				return line, err
			}
		}
	}
	slices.SortStableFunc(line.Layers, func(a, b LineLayer) int {
		if a.Count != b.Count {
			return a.Count - b.Count
		}
		return strings.Compare(a.Branch, b.Branch)
	})
	return line, nil
}

// backup is whether name matches one of the backup patterns.
func backup(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}
