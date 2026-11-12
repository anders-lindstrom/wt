package wtsync

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
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

// StackRequest is a rebase of a stack held on the line: every layer of Line
// gets a marker in the sequencer's list. Autosquash folds the line's
// fixup!, squash! and amend! commits onto its own base instead of rebasing
// it onto trunk.
type StackRequest struct {
	Line       Line
	Autosquash bool
}

// StackMapPrefix is where a rebase of a stack held on one line has git
// write each layer's new tip: refs/wt-stack-map/<epoch>/<n>, n from 1 at the
// bottom. Not under refs/wt-sync/, where any ref ending in a number is read
// as a safety record.
const StackMapPrefix = "refs/wt-stack-map/"

// StackMapRef names the marker of layer n (from 1) in the run of epoch.
func StackMapRef(epoch int64, n int) string {
	return StackMapPrefix + strconv.FormatInt(epoch, 10) + "/" + strconv.Itoa(n)
}

// The ways a layer's marker can land, as LayerPlan.Mapped says it.
const (
	MappedTip        = "tip"        // after the layer's own tip
	MappedTipDropped = "tipDropped" // its tip was left out; after its last commit left
	MappedEmptied    = "emptied"    // none of its commits is left; where the layer below ends
)

// LayerPlan is what a rebase of the line planned for one layer, read from
// the list git started it with: the marker, the commit the marker follows
// (empty when it is the rebase's base itself), the count of commits from
// the new base the layer must have, and the subject its new tip must carry.
type LayerPlan struct {
	Branch, Old, Ref string
	After            string
	Expect           int
	Subject          string
	Mapped           string
	// Folded is how many fixup!, squash! and amend! commits of the
	// layer's own prefix were folded into an earlier commit.
	Folded int
	// LeftOut is how many commits of the layer's own commits are not in the
	// list: trunk already carries them, patch for patch.
	LeftOut int
}

// LayerTip is one layer's new tip, read back from its marker and checked.
type LayerTip struct {
	LayerPlan
	New string
}

// todoEntry is one command of a sequencer list that names a commit.
type todoEntry struct {
	start bool // pick or edit: starts a group
	amend bool // fixup -C or -c: the group takes this commit's message
	sha   string
}

// readStackTodo reads the commands of a list that name a commit of the line,
// resolving each abbreviated id among the line's commits.
func readStackTodo(todo string, commits []string) []todoEntry {
	var out []todoEntry
	for _, line := range strings.Split(todo, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		e := todoEntry{}
		id := f[1]
		switch f[0] {
		case "pick", "edit":
			e.start = true
		case "fixup", "squash":
			if f[1] == "-C" || f[1] == "-c" {
				if len(f) < 3 {
					continue
				}
				e.amend, id = true, f[2]
			}
		default:
			continue
		}
		for _, c := range commits {
			if strings.HasPrefix(c, id) {
				e.sha = c
				break
			}
		}
		if e.sha != "" {
			out = append(out, e)
		}
	}
	return out
}

// planLayers reads, from the list a rebase of the line started with, what
// each layer's marker will follow and what its new tip must be.
func planLayers(dir string, epoch int64, line Line, todo string) ([]LayerPlan, error) {
	pos := map[string]int{}
	for i, c := range line.Commits {
		pos[c] = i + 1
	}
	entries := readStackTodo(todo, line.Commits)
	inTodo := map[string]bool{}
	for _, e := range entries {
		inTodo[e.sha] = true
	}
	plans := make([]LayerPlan, len(line.Layers))
	prev := 0
	for i, l := range line.Layers {
		p := LayerPlan{Branch: l.Branch, Old: l.Tip, Ref: StackMapRef(epoch, i+1), Mapped: MappedTip}
		last, lastAmend, own := -1, "", 0
		for j, e := range entries {
			if !e.start {
				continue
			}
			if pos[e.sha] <= l.Count {
				p.Expect++
				last, lastAmend = j, ""
				if pos[e.sha] > prev {
					own++
				}
			}
		}
		if last >= 0 {
			p.After = entries[last].sha
			for _, e := range entries[last+1:] {
				if e.start {
					break
				}
				if e.amend {
					lastAmend = e.sha
				}
			}
		}
		for _, c := range line.Commits[prev:l.Count] {
			if !inTodo[c] {
				p.LeftOut++
			}
		}
		for _, e := range entries {
			if !e.start && pos[e.sha] <= l.Count {
				p.Folded++
			}
		}
		switch {
		case own == 0 && l.Count > prev:
			p.Mapped = MappedEmptied
		case own == 0:
			// At the same commit as the layer below: it ends where that one does.
		case !isStart(entries, l.Tip):
			p.Mapped = MappedTipDropped
		}
		if p.After != "" {
			subject, err := plannedSubject(dir, p.After, lastAmend)
			if err != nil {
				return nil, err
			}
			p.Subject = subject
		}
		plans[i] = p
		prev = l.Count
	}
	return plans, nil
}

func isStart(entries []todoEntry, sha string) bool {
	for _, e := range entries {
		if e.start && e.sha == sha {
			return true
		}
	}
	return false
}

// plannedSubject is the subject the commit a marker follows will carry: its
// own, or, when an amend! commit is folded into it, the subject of the
// message the amend! carries.
func plannedSubject(dir, sha, amend string) (string, error) {
	if amend == "" {
		return gitEnv(dir, nil, nil, "log", "-1", "--format=%s", sha)
	}
	body, err := gitEnv(dir, nil, nil, "log", "-1", "--format=%b", amend)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l, nil
		}
	}
	return "", nil
}

// CheckLayers reads each layer's marker once the rebase of the line has
// finished and checks it against the plan before any branch moves: the
// marker is there, it is on the rebased line (an ancestor of head), it has
// as many commits from onto as the plan expects, and its subject is the
// planned one. Any failure is an error naming the layer, and nothing is
// returned to move.
func CheckLayers(dir, head, onto string, plans []LayerPlan) ([]LayerTip, error) {
	tips := make([]LayerTip, 0, len(plans))
	for _, p := range plans {
		bad := func(why string) ([]LayerTip, error) {
			return nil, fmt.Errorf("layer %s: %s; no layer moved, the line itself is rebased", p.Branch, why)
		}
		tip, ok, err := refTip(dir, p.Ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			return bad("the rebase did not write its marker " + p.Ref)
		}
		if _, code, err := gitEnvAllow(dir, nil, nil, 1, "merge-base", "--is-ancestor", tip, head); err != nil {
			return nil, err
		} else if code == 1 {
			return bad(fmt.Sprintf("its marker %s is not on the rebased line", git.ShortID(tip, 7)))
		}
		out, err := gitEnv(dir, nil, nil, "rev-list", "--count", onto+".."+tip)
		if err != nil {
			return nil, err
		}
		if n, _ := strconv.Atoi(out); n != p.Expect {
			return bad(fmt.Sprintf("its new tip %s has %d commits from the new base, and the plan expects %d", git.ShortID(tip, 7), n, p.Expect))
		}
		if p.After != "" {
			subject, err := gitEnv(dir, nil, nil, "log", "-1", "--format=%s", tip)
			if err != nil {
				return nil, err
			}
			if subject != p.Subject {
				return bad(fmt.Sprintf("its new tip %s is %q, and the plan expects %q", git.ShortID(tip, 7), subject, p.Subject))
			}
		}
		tips = append(tips, LayerTip{LayerPlan: p, New: tip})
	}
	return tips, nil
}

// DropStackMap deletes the markers of plans that are there, each only while
// it is still at the commit it was read at, so a ref written by something
// else is left alone.
func DropStackMap(dir string, plans []LayerPlan) error {
	var errs []error
	for _, p := range plans {
		tip, ok, err := refTip(dir, p.Ref)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if _, err := gitEnv(dir, nil, nil, "update-ref", "-d", p.Ref, tip); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// layerOf numbers every commit of the line by the layer it belongs to, 1
// the bottom; a commit above every layer gets len(Layers)+1.
func (l Line) layerOf() map[string]int {
	out := map[string]int{}
	for i, c := range l.Commits {
		n := len(l.Layers) + 1
		for j, layer := range l.Layers {
			if layer.Count >= i+1 {
				n = j + 1
				break
			}
		}
		out[c] = n
	}
	return out
}

// SamePatch is whether the change from oldBase to old is the same, by git's
// stable patch-id, as the change from newBase to tip: what a layer brings
// to the line, before and after the rebase.
func SamePatch(dir, oldBase, old, newBase, tip string) (bool, error) {
	id := func(a, b string) (string, error) {
		diff, err := gitEnv(dir, nil, nil, "diff", "--no-color", "--no-ext-diff", a, b)
		if err != nil || diff == "" {
			return "", err
		}
		out, err := gitEnv(dir, nil, strings.NewReader(diff+"\n"), "patch-id", "--stable")
		id, _, _ := strings.Cut(out, " ")
		return id, err
	}
	was, err := id(oldBase, old)
	if err != nil {
		return false, err
	}
	is, err := id(newBase, tip)
	return was == is, err
}
