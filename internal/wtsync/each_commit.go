package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// EachCommitTimeout bounds one each_commit check at one commit. The run's
// own git calls are bounded by GitTimeout; a check is not one of them, and
// is bounded on its own.
const EachCommitTimeout = 30 * time.Minute

// checkTail is how many lines of a failing check's output are kept.
const checkTail = 20

// CheckFailure is an each_commit check that failed at a replayed commit:
// the step, the commit it ran at, where that commit is in the rebase, what
// the step's exit said and the tail of its output.
type CheckFailure struct {
	Step         string
	Commit       string
	Subject      string
	Index, Total int
	Err          string
	Output       string
}

// Line is the failure in one line: the step, the commit and why.
func (c CheckFailure) Line() string {
	return fmt.Sprintf("check %q failed at %s %q: %s", c.Step, git.ShortID(c.Commit, 7), c.Subject, c.Err)
}

// Unchecked is an each_commit check a run did not run because a need of it
// failed its probe: owed for the whole branch.
type Unchecked struct {
	Step string
	Why  string
}

// Owed is how the run names the unchecked step among the owed ones.
func (u Unchecked) Owed() string { return u.Step + " (not checked: " + u.Why + ")" }

// PlanChecks lists, oldest first, the commits of base...branch a rebase
// would check: each one that changes a path some each_commit step lists,
// or every one when a step lists none. A commit trunk already carries is
// left out, since the rebase drops it, unless a lifting rule claims a file
// it changes, which the run replays and lifts.
func PlanChecks(wtPath string, cfg *Config, base, branch string) ([]Commit, error) {
	commits, _, err := checksDue(wtPath, cfg, base, branch)
	return commits, err
}

// checksDue is PlanChecks with the each_commit steps that run at one of
// the commits at least, in the order declared.
func checksDue(wtPath string, cfg *Config, base, branch string) ([]Commit, []EachCommit, error) {
	if cfg == nil || len(cfg.EachCommit) == 0 {
		return nil, nil, nil
	}
	commits, err := logWithPaths(wtPath, "--right-only", "--no-merges", base+"..."+branch, "--")
	if err != nil {
		return nil, nil, err
	}
	lift := cfg.liftPatterns()
	ran := make([]bool, len(cfg.EachCommit))
	var out []Commit
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		if c.OnTrunk && !touchesAny(lift, c.Paths) {
			continue
		}
		checked := false
		for j, s := range cfg.EachCommit {
			if len(s.Paths) == 0 || touchesAny(s.Paths, c.Paths) {
				ran[j], checked = true, true
			}
		}
		if checked {
			out = append(out, Commit{SHA: c.SHA, Subject: c.Subject})
		}
	}
	var steps []EachCommit
	for j, s := range cfg.EachCommit {
		if ran[j] {
			steps = append(steps, s)
		}
	}
	return out, steps, nil
}

// checksFor is the each_commit steps a commit changing paths runs.
func (c *Config) checksFor(paths []string) []EachCommit {
	var steps []EachCommit
	for _, s := range c.EachCommit {
		if len(s.Paths) == 0 || touchesAny(s.Paths, paths) {
			steps = append(steps, s)
		}
	}
	return steps
}

// planChecks fills in the driver's list of commits to check.
func (d *driver) planChecks() error {
	commits, err := PlanChecks(d.req.Path, d.cfg, d.req.base(), d.req.Branch)
	if err != nil {
		return err
	}
	d.checks = nil
	for _, c := range commits {
		d.checks = append(d.checks, c.SHA)
	}
	return nil
}

// checkAt reports whether the break the sequencer stopped at is a check
// stop, the break placed after the pick of a commit to check, and which
// commit (its full id) that pick replayed. The lift's own breaks come
// before a pick, never directly after a checked one, since the check's
// break is written first.
func (d *driver) checkAt() (sha string, edited bool, err error) {
	if len(d.checks) == 0 {
		return "", false, nil
	}
	dir, err := rebaseDir(d.req.Path)
	if err != nil {
		return "", false, err
	}
	done, err := os.ReadFile(filepath.Join(dir, "done"))
	if err != nil {
		return "", false, err
	}
	var cmds [][]string
	for _, line := range strings.Split(string(done), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		cmds = append(cmds, f)
	}
	if len(cmds) < 2 || cmds[len(cmds)-1][0] != "break" {
		return "", false, nil
	}
	prev := cmds[len(cmds)-2]
	if (prev[0] != "pick" && prev[0] != "edit") || len(prev) < 2 {
		return "", false, nil
	}
	for _, c := range d.checks {
		if strings.HasPrefix(c, prev[1]) {
			return c, prev[0] == "edit", nil
		}
	}
	return "", false, nil
}

// pickCommitted reports whether the pick of sha left a commit at HEAD:
// git's own record of what it rewrote names sha and HEAD, or, at a pick
// the run stopped around (edited), HEAD moved past where the pick was
// applied: the lift commits a dropped pick's bump itself. A pick git
// dropped as empty has neither.
func (d *driver) pickCommitted(sha, head string, edited bool) (bool, error) {
	if edited && d.before != "" && head != d.before {
		return true, nil
	}
	dir, err := rebaseDir(d.req.Path)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "rewritten-list"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	f := strings.Fields(lines[len(lines)-1])
	return len(f) >= 2 && f[0] == sha && f[1] == head, nil
}

// atCheck runs the each_commit steps at the check stop for sha. failed is
// the first step that failed, nil when every one passed or none was due.
func (d *driver) atCheck(sha string, edited bool, p Progress, forced bool) (*CheckFailure, error) {
	head, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if !forced {
		ok, err := d.pickCommitted(sha, head, edited)
		if err != nil || !ok {
			return nil, err
		}
	}
	paths, err := d.git("diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--root", "HEAD")
	if err != nil {
		return nil, err
	}
	if forced {
		// A resume after a person's fix: HEAD may be a commit they added on
		// top of the one that failed, so the checks of that one run too.
		own, err := d.git("diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--root", sha)
		if err != nil {
			return nil, err
		}
		paths += "\x00" + own
	}
	var changed []string
	for _, p := range strings.Split(paths, "\x00") {
		if p != "" {
			changed = append(changed, p)
		}
	}
	steps := d.cfg.checksFor(changed)
	subject, err := d.git("log", "-1", "--format=%s", "HEAD")
	if err != nil {
		return nil, err
	}
	var run []EachCommit
	for _, s := range steps {
		if why := d.unavailable(s.Needs); why != "" {
			d.uncheck(s.Run, why)
			continue
		}
		run = append(run, s)
	}
	if len(run) == 0 {
		return nil, nil
	}
	d.res.Checked++
	if d.log != nil {
		at := slices.Index(d.checks, sha) + 1
		if at == 0 {
			at = d.res.Checked
		}
		fmt.Fprintf(d.log, "  ▸ check %d/%d %s %s\n", at, max(len(d.checks), at), git.ShortID(head, 7), subject)
	}
	for _, s := range run {
		why, output := runCheck(d.req.Path, s.Run, EachCommitTimeout)
		if why == "" {
			continue
		}
		f := &CheckFailure{Step: s.Run, Commit: head, Subject: subject, Index: p.Index, Total: p.Total, Err: why, Output: output}
		if d.log != nil {
			fmt.Fprintf(d.log, "  ✗ %s: %s\n", s.Run, why)
			for _, line := range strings.Split(output, "\n") {
				if line != "" {
					fmt.Fprintf(d.log, "    %s\n", line)
				}
			}
		}
		return f, nil
	}
	return nil, nil
}

// unavailable is why a step with needs cannot run in this run: the probe
// answer of the first need the run was told is missing, "" when none is.
func (d *driver) unavailable(needs []string) string {
	return unavailableWhy(needs, d.req.Unavailable)
}

// uncheck records a check the run did not run, once per step.
func (d *driver) uncheck(step, why string) {
	for _, u := range d.res.Unchecked {
		if u.Step == step {
			return
		}
	}
	d.res.Unchecked = append(d.res.Unchecked, Unchecked{Step: step, Why: why})
}

// runCheck runs one check in the worktree, bounded by timeout. why is ""
// for a pass; otherwise what failed. A check that leaves tracked files
// changed has failed: it must not change the commit it checks.
func runCheck(wtPath, run string, timeout time.Duration) (why, output string) {
	cmd := exec.Command("sh", "-c", run)
	cmd.Dir = wtPath
	cmd.Env = git.Environ(append([]string{"NO_COLOR=1"}, rebaseEnv...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	timedOut, code, err := git.RunBounded(timeout, cmd)
	output = tailLines(strings.TrimSpace(out.String()), checkTail)
	var exit *exec.ExitError
	switch {
	case timedOut:
		return fmt.Sprintf("timed out after %s", timeout), output
	case errors.Is(err, exec.ErrWaitDelay):
	case errors.As(err, &exit):
		return fmt.Sprintf("exit %d", code), output
	case err != nil:
		return err.Error(), output
	}
	status, serr := gitEnv(wtPath, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	switch {
	case serr != nil:
		return "status could not be read: " + serr.Error(), output
	case status != "":
		var files []string
		for _, l := range strings.Split(status, "\n") {
			if len(l) > 3 {
				files = append(files, strings.TrimSpace(l[3:]))
			}
		}
		return "changed tracked files: " + strings.Join(files, ", "), output
	}
	return "", output
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// failCheck ends the rebase at a check that failed: a stack parent is put
// back, as at a stop nobody claims; anything else is handed over at the
// commit, with nothing unmerged and HEAD the commit that failed, for a
// person to amend or add to, and wt sync resume runs the check again.
func (d *driver) failCheck(f *CheckFailure) (Result, error) {
	d.res.CheckFailed = f
	if d.req.Stacked {
		if d.keep {
			return d.fail(errors.New(f.Line() + "; this branch has descendants in the run, and a resume cannot put it back without discarding your work"))
		}
		rerr := d.restore()
		d.res.Restored = rerr == nil
		return d.res, rerr
	}
	h := &Handover{Index: f.Index, Total: f.Total, Commit: f.Commit, Subject: f.Subject, Staged: map[string]string{}, Check: f}
	if err := StripMarks(d.req.Path); err != nil {
		return d.fail(err)
	}
	d.res.Left = h
	return d.res, nil
}

// renderCheckPlan is the brief left at a check that failed: which step, at
// which commit, what it said, and what to do.
func renderCheckPlan(in PlanInput) string {
	c := in.Handover.Check
	var b strings.Builder
	fmt.Fprintf(&b, "# rebase %s onto %s\n", in.Work, in.TrunkRef)
	fmt.Fprintf(&b, "stopped after %d/%d, %s %q: an each_commit check failed there\n",
		c.Index, c.Total, git.ShortID(c.Commit, 7), oneLinePlan(c.Subject))
	fmt.Fprintf(&b, "\n## the check\n%s\n%s\n", c.Step, c.Err)
	if c.Output != "" {
		fmt.Fprintf(&b, "\n## the end of its output\n```\n%s\n```\n", c.Output)
	}
	b.WriteString("\n## yours\n")
	b.WriteString("HEAD is the commit that failed, and nothing is unmerged. Fix it and amend the fix into the\n")
	b.WriteString("commit (git commit --amend), or add a commit of your own, so the worktree is clean again.\n")
	fmt.Fprintf(&b, "wt sync resume %s runs the check again at HEAD and goes on only when it passes;\n", in.Work)
	fmt.Fprintf(&b, "wt sync undo %s puts the branch back where the run found it.\n", in.Work)
	return b.String()
}

// CheckStopOf is a failure as the handover records it.
func CheckStopOf(f *CheckFailure) *CheckStop {
	if f == nil {
		return nil
	}
	return &CheckStop{Step: f.Step, Commit: f.Commit, Subject: f.Subject, Err: f.Err}
}

// CheckFailedLine is what a person is told at a handed-over check.
func CheckFailedLine(work string, f CheckFailure) string {
	return fmt.Sprintf("%s needs you. %s · fix it, amend the commit, then wt sync resume %s (or wt sync undo %s)",
		work, f.Line(), work, work)
}
