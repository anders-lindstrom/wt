package wtsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// PlanName is the brief a run leaves in a worktree's own git dir when it
// stops at a conflict a person owns (spec §6).
const PlanName = "wt-sync-plan.md"

// StateName is the machine-readable half of the same handover, and the
// marker the tool acts on: what resume needs in order to prove the worktree
// is still what the run left, and to continue that run rather than start a
// new one. The markdown is for a person; this is for the tool, so a person
// deleting the markdown does not make a handed-over rebase unrecoverable.
const StateName = "wt-sync-state.json"

// PlanPath is where the brief lives for a worktree's git dir.
func PlanPath(gitDir string) string { return filepath.Join(gitDir, PlanName) }

// StatePath is where the sidecar lives for a worktree's git dir.
func StatePath(gitDir string) string { return filepath.Join(gitDir, StateName) }

// LeftLock is the lock a run left behind when it handed a stop over, so the
// resume or undo that continues that run can take it over and nothing else
// can.
type LeftLock struct {
	PID     int   `json:"pid"`
	Started int64 `json:"started"`
}

// State is everything resume and undo need. The trunk SHA is the run's, not
// whatever origin/<trunk> means later: a resume that read a newer
// declaration would apply strategies the stopped rebase was never planned
// with.
type State struct {
	Branch   string            `json:"branch"`
	Work     string            `json:"work"`
	Trunk    string            `json:"trunk"`
	TrunkRef string            `json:"trunk_ref"`
	Onto     string            `json:"onto"`
	Upstream string            `json:"upstream"`
	Epoch    int64             `json:"epoch"`
	Safety   string            `json:"safety"`
	OldTip   string            `json:"old_tip"`
	Stop     int               `json:"stop"`
	Total    int               `json:"total"`
	Resolved map[string]string `json:"resolved"`
	Strategy map[string]string `json:"strategy"`
	Deleted  []string          `json:"deleted"`
	Left     []string          `json:"left"`
	// Stopped is every path the rebase stopped on up to this handover,
	// across every earlier handover of the same run.
	Stopped []string `json:"stopped"`
	Lock    LeftLock `json:"lock"`
}

// writeAtomic writes data to a temp file in the same directory and renames
// it into place, so a crash or a full disk cannot leave half a handover. The
// temp file is fsynced before the rename: without that the rename can reach
// the disk before the bytes do, and a power loss then leaves a zero-length
// sidecar that ReadState rejects as malformed JSON.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// WritePlanFile writes the human brief.
func WritePlanFile(gitDir, plan string) error {
	return writeAtomic(PlanPath(gitDir), []byte(plan))
}

// WriteState writes the sidecar. Write the brief first: this is the marker,
// and a marker present without its brief is worse than the reverse.
func WriteState(gitDir string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(StatePath(gitDir), append(data, '\n'))
}

// ReadState reads the sidecar; ok is false when there is none. The maps and
// slices come back non-nil even when the JSON carried null or omitted them,
// so a caller continuing a run can write to them without checking first: a
// nil map assignment panics, and the sidecar is exactly the place a resume
// records what it resolved.
func ReadState(gitDir string) (State, bool, error) {
	data, err := os.ReadFile(StatePath(gitDir))
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, fmt.Errorf("%s: %w", StatePath(gitDir), err)
	}
	if s.Resolved == nil {
		s.Resolved = map[string]string{}
	}
	if s.Strategy == nil {
		s.Strategy = map[string]string{}
	}
	if s.Deleted == nil {
		s.Deleted = []string{}
	}
	if s.Left == nil {
		s.Left = []string{}
	}
	return s, true, nil
}

// HasPlan reports whether a run left a handover in this git dir.
func HasPlan(gitDir string) (bool, error) {
	_, err := os.Stat(StatePath(gitDir))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// RemovePlan deletes both halves of a handover, and any temp file a crashed
// write left. A run that ends — completed, undone, or restored — has nothing
// left to hand over, and a stale marker would report the worktree as waiting
// on somebody forever.
//
// The sidecar goes first, mirroring the write order that puts the marker
// last, and every path is attempted: a markdown file an editor has locked or
// a read-only mount holds must not be able to leave the marker behind. The
// errors are joined so the caller still hears about what would not go.
func RemovePlan(gitDir string) error {
	var errs []error
	for _, p := range []string{StatePath(gitDir), PlanPath(gitDir), StatePath(gitDir) + ".tmp", PlanPath(gitDir) + ".tmp"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PlanHolders lists the worktrees holding a handover. The main checkout is
// never a rebase target, so it is not looked at.
func PlanHolders(worktrees []repo.Worktree) ([]repo.Worktree, error) {
	var out []repo.Worktree
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		has, err := HasPlan(gitDir)
		if err != nil {
			return nil, err
		}
		if has {
			out = append(out, wt)
		}
	}
	return out, nil
}

// NeedsYouLine is the after-the-fact protocol line of spec §5. Generated
// here and relayed verbatim; never composed by hand.
func NeedsYouLine(work string, left []string) string {
	files := "-"
	if len(left) > 0 {
		files = path.Base(left[0])
		if n := len(left) - 1; n > 0 {
			files += " +" + strconv.Itoa(n)
		}
	}
	return fmt.Sprintf("wt: %s needs you. %d left after resolvers: %s · wt sync resume %s",
		work, len(left), files, work)
}

// RebasedLine is spec §5's after-the-fact line for a rebase that finished:
// how many trunk commits it took in and, for a session whose picture of the
// worktree is now out of date, the files the rebase stopped on.
func RebasedLine(work, trunk string, landed int, check []string) string {
	line := fmt.Sprintf("wt: %s rebased on %s (+%d)", work, trunk, landed)
	if len(check) == 0 {
		return line
	}
	shown := check[:min(len(check), 3)]
	names := make([]string, len(shown))
	for i, p := range shown {
		names[i] = path.Base(p)
	}
	line += ". yours to check: " + strings.Join(names, ", ")
	if n := len(check) - len(shown); n > 0 {
		line += " +" + strconv.Itoa(n)
	}
	return line
}

// PlanInput is everything the brief is rendered from.
type PlanInput struct {
	MainRoot string
	Work     string
	Branch   string
	TrunkRef string // origin/<trunk>, for the title
	Trunk    string // the trunk SHA, for the per-file log
	Base     string // the merge base, for the per-file log
	Landing  Landing
	Handover Handover
	Config   *Config
}

// RenderPlan writes the brief of spec §6: what landed, what the strategies
// already did and must not be re-opened, what is left for a person with the
// trunk-side reason for each file, where hand-merging is forbidden outright,
// and what runs once the rebase completes. Every section removes a specific
// expense; none of it costs a model anything to produce.
func RenderPlan(in PlanInput) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# rebase %s onto %s\n", in.Work, in.TrunkRef)
	scopes := in.Landing.ScopeLine()
	if scopes == "" {
		scopes = "none named"
	}
	fmt.Fprintf(&b, "%d landed. scopes: %s\n", in.Landing.Commits, scopes)
	fmt.Fprintf(&b, "stopped at stop %d/%d", in.Handover.Index, in.Handover.Total)
	if in.Handover.Subject != "" {
		fmt.Fprintf(&b, ", replaying %q", oneLinePlan(in.Handover.Subject))
	}
	b.WriteString("\n")

	conflicts := map[string]Conflict{}
	for _, c := range in.Handover.Conflicts {
		conflicts[c.Path] = c
	}
	outcomes := map[string]FileOutcome{}
	var resolved []FileOutcome
	for _, f := range in.Handover.Files {
		outcomes[f.Path] = f
		if f.Resolved {
			resolved = append(resolved, f)
		}
	}

	// Left is the set of paths handed to a person, and NeedsYouLine counts
	// the same field: deriving this section from Files instead would let the
	// file say "2 files" while the terminal says "3 left". A path Left names
	// that Files has no outcome for is listed by name alone — a handover must
	// not fail because two lists disagree. An empty Left falls back to the
	// unresolved outcomes, for a caller that does not set it.
	var left []FileOutcome
	if len(in.Handover.Left) > 0 {
		for _, p := range in.Handover.Left {
			f, ok := outcomes[p]
			if !ok {
				f = FileOutcome{Path: p}
			}
			left = append(left, f)
		}
	} else {
		for _, f := range in.Handover.Files {
			if !f.Resolved {
				left = append(left, f)
			}
		}
	}

	if len(resolved) > 0 {
		b.WriteString("\n## already resolved — do not re-open\n")
		for _, f := range resolved {
			fmt.Fprintf(&b, "%-40s %s\n", f.Path, f.Strategy)
		}
	}

	fmt.Fprintf(&b, "\n## yours — %d file%s\n", len(left), pluralPlan(len(left)))
	for _, f := range left {
		note := f.Note
		switch {
		case f.Strategy != "":
			// A file a declaration claims, whose strategy refused it: it is
			// a person's after all, and saying which strategy refused and
			// why is the whole reason they can trust that.
			note = fmt.Sprintf("%s refused it: %s", f.Strategy, f.Note)
		case f.Note == "unclaimed":
			if c, ok := conflicts[f.Path]; ok {
				note = shapeOf(in.MainRoot, c)
			}
		}
		// A path Left names but Files does not describe has no note at all,
		// and a line of padding with nothing after it is noise.
		b.WriteString(strings.TrimRight(fmt.Sprintf("%-40s %s", f.Path, note), " ") + "\n")
		subject, err := trunkSubject(in.MainRoot, in.Base, in.Trunk, f.Path)
		if err != nil {
			return "", err
		}
		if subject != "" {
			fmt.Fprintf(&b, "    trunk: %s\n", oneLinePlan(subject))
		}
	}

	if in.Config != nil {
		// A glob that matches a file this stop handed over is not listed:
		// telling a person both to resolve a file and never to touch it is
		// worse than saying nothing.
		asked := map[string]bool{}
		for _, f := range left {
			asked[f.Path] = true
		}
		claims := func(pattern string) bool {
			for p := range asked {
				if MatchGlob(pattern, p) {
					return true
				}
			}
			return false
		}
		var owned []string
		for _, r := range in.Config.Conflicts {
			for _, p := range r.Paths {
				if !claims(p) {
					owned = append(owned, fmt.Sprintf("%-40s ->  %s", p, r.Strategy))
				}
			}
		}
		for _, d := range in.Config.Defer {
			for _, p := range d.Paths {
				if !claims(p) {
					owned = append(owned, fmt.Sprintf("%-40s ->  the deferred `%s` owns it", p, d.Run))
				}
			}
		}
		sort.Strings(owned)
		if len(owned) > 0 {
			b.WriteString("\n## never hand-merge here\n")
			for _, l := range owned {
				b.WriteString(l + "\n")
			}
		}
		if len(in.Config.Defer) > 0 {
			b.WriteString("\n## deferred, runs when the rebase completes\n")
			for _, d := range in.Config.Defer {
				b.WriteString(d.Run + "\n")
			}
		}
	}

	fmt.Fprintf(&b, "\nresolve what is yours, `git add` it, then: wt sync resume %s\n", in.Work)
	fmt.Fprintf(&b, "or put everything back: wt sync undo %s\n", in.Work)
	return b.String(), nil
}

// shapeOf describes one unresolved conflict the way §6 does: both sides only
// added lines (a five-second read), or they overlap. A blob git will not
// diff as text is reported as overlapping, which is the cautious answer.
func shapeOf(mainRoot string, c Conflict) string {
	tAdd, tDel, terr := blobDiff(mainRoot, c.Base, c.Trunk)
	bAdd, bDel, berr := blobDiff(mainRoot, c.Base, c.Branch)
	if terr != nil || berr != nil || tDel != 0 || bDel != 0 {
		return "same hunk both sides"
	}
	return fmt.Sprintf("additive only (trunk +%d, ours +%d)", tAdd, bAdd)
}

// blobDiff counts the lines added and removed between two blobs with git's
// own numstat over objects it already holds. The output is
// "<added>\t<removed>\t<oidA> => <oidB>" (verified 2026-09-09) and "-\t-\t…"
// for a blob git treats as binary, which is reported as not textual.
func blobDiff(mainRoot string, from, to []byte) (added, removed int, err error) {
	a, err := hashObject(mainRoot, from)
	if err != nil {
		return 0, 0, err
	}
	b, err := hashObject(mainRoot, to)
	if err != nil {
		return 0, 0, err
	}
	out, err := gitEnv(mainRoot, nil, nil, "diff", "--numstat", a, b)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return 0, 0, nil
	}
	added, aerr := strconv.Atoi(f[0])
	removed, derr := strconv.Atoi(f[1])
	if aerr != nil || derr != nil {
		return 0, 0, fmt.Errorf("not a textual diff")
	}
	return added, removed, nil
}

// trunkSubject is why trunk changed this file: the newest subject in
// base..trunk touching it (spec §6). Empty when trunk did not touch it.
func trunkSubject(mainRoot, base, trunk, path string) (string, error) {
	out, err := gitEnv(mainRoot, nil, nil, "--literal-pathspecs", "log", "-1", "--format=%s", base+".."+trunk, "--", path)
	if err != nil {
		return "", fmt.Errorf("trunk subject for %s: %w", path, err)
	}
	return out, nil
}

func oneLinePlan(s string) string { return strings.Join(strings.Fields(s), " ") }

func pluralPlan(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
