package wtsync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Verdict is Preflight's answer.
type Verdict int

// The verdicts. Not Go/Skip/Refuse: Refuse is already the refusal
// constructor in conflict.go.
const (
	Proceed   Verdict = iota // rebase it
	SkipRun                  // nothing to do; say why
	RefuseRun                // must not be touched; say why
)

// rebaseEnv keeps every rebase step from ever prompting.
var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

// rebaseConfig pins the behaviour the loop is written against, whatever the
// user's configuration says: the merge backend (rebase-merge bookkeeping),
// no merge preservation, no autostash, no ref updating.
var rebaseConfig = []string{
	"-c", "rebase.backend=merge",
	"-c", "rebase.rebaseMerges=false",
	"-c", "rebase.autoStash=false",
	"-c", "rebase.updateRefs=false",
	// Not passing --rerere-autoupdate does not disable autoupdate: a user
	// with rerere.autoupdate=true would still get cached resolutions staged,
	// which removes them from ls-files -u before any strategy sees them and
	// before a handover can record what it staged. The run plan believed the
	// flag's absence was enough; it is not.
	"-c", "rerere.autoupdate=false",
}

// Preflight decides from a triage assessment whether a run may start. The
// checks that make a worktree untouchable come before the class, so a dirty
// worktree is refused for its dirt whatever its class.
func Preflight(a Assessment) (Verdict, string) {
	switch {
	case a.Err != nil:
		return RefuseRun, "assessment failed: " + a.Err.Error()
	// Below the error, so the resume advice cannot hide a failed
	// assessment; above the dirt, because a handover's staged conflicts
	// are what a person is finishing, not dirt.
	case a.Paused && a.Rebasing:
		return RefuseRun, "left mid-rebase by an earlier run: " + WayOut(a.Way())
	case a.Paused:
		// The handover is there and its rebase is not: finished or aborted
		// by hand, and the sentence says which and what runs the rest.
		return RefuseRun, WayOut(a.Way())
	case a.Class == Detached:
		return RefuseRun, "no branch"
	case a.NoConfig:
		return RefuseRun, "no declaration on trunk"
	case a.Dirty:
		return RefuseRun, "tracked changes in the worktree"
	case len(a.Sessions.Busy()) > 0:
		return RefuseRun, "an agent session is busy in it: " + a.Sessions.Label(agentLabel)
	}
	switch a.Class {
	case Current:
		return SkipRun, "already on trunk"
	case Stale:
		return SkipRun, "nothing ahead of trunk"
	case Divergent:
		reason := "divergent"
		if len(a.Divergent) > 0 {
			reason += ": " + a.Divergent[0].String()
		}
		return RefuseRun, reason
	case Contested:
		// A contested stop is handed over rather than refused: the run
		// rebases up to it, resolves what it can, and leaves the rest with
		// a plan file. Where it stops is printed by the caller.
		return Proceed, ""
	case Clean, Recipe:
		return Proceed, ""
	}
	return RefuseRun, "class unknown"
}

func agentLabel(a *Agent) string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Kind != "":
		return a.Kind
	}
	return "?"
}

// Request names one rebase.
type Request struct {
	Path     string // the worktree
	Branch   string
	Trunk    string // the ref the declaration and scripts are read from; never a parent's tip
	Onto     string // what to rebase onto: Trunk, or a stack parent's new tip
	Upstream string // "" for a plain rebase; the parent's old tip for a stack child
	Epoch    int64
	// Work is what a person calls this worktree, for the messages a run puts
	// in front of them: every other line of wt sync names the work, not the
	// branch. Empty falls back to the branch.
	Work string
	// Stacked says this branch has descendants in the same run. A stop a
	// person owns is then restored rather than handed over: a parent left
	// mid-rebase strands every child on a base that is about to be
	// rewritten, which is the half-applied stack §4 forbids.
	Stacked bool
	// Total is how many commits the run set out to replay, for a resume
	// that finds the rebase finished by hand: more on the branch than that
	// were committed inside it. Zero, as a run passes, is not checked.
	Total int
}

// base is what the rebase replays from: the parent's old tip for a stack
// child, and the rebase target itself otherwise.
func (r Request) base() string {
	if r.Upstream != "" {
		return r.Upstream
	}
	return r.Onto
}

// StopResult is one place the rebase stopped and what happened there.
type StopResult struct {
	Index, Total int
	Commit       string
	Subject      string
	Files        []FileOutcome
}

// Handover is a stop the run left for a person: where the rebase is, the
// three blobs of every conflict there, what the strategies answered, the
// blob each resolved path was staged with, the paths a strategy resolved by
// deleting, and the paths a person owns. Staged and Deleted are what resume
// compares against to prove nothing was hand-merged where a strategy owns
// the file.
type Handover struct {
	Index, Total int
	Commit       string
	Subject      string
	Conflicts    []Conflict
	Files        []FileOutcome
	Staged       map[string]string
	Deleted      []string
	Left         []string
}

// Result is what a rebase did.
type Result struct {
	Branch, OldTip, NewTip string
	Safety                 Safety
	Replayed               int
	Stops                  []StopResult
	Restored               bool // an unresolved stop: aborted and verified back at OldTip
	SignaturesDropped      int
	// Left is the stop the run handed over to a person: the rebase is still
	// in progress in the worktree, with every strategy's answer staged.
	// Nil when the rebase finished or was restored.
	Left *Handover
}

// driver is one rebase in flight: Rebase starts one and drives it, Resume
// re-enters the same loop for one a previous run left stopped.
type driver struct {
	mainRoot string
	cfg      *Config
	req      Request
	log      io.Writer
	old      string
	// keep forbids the restore. A run's restore throws away only the tool's
	// own work; a resume's would throw away a person's, so a resume that
	// fails leaves the worktree exactly as it is and says so.
	keep bool
	res  Result
}

func (d *driver) git(args ...string) (string, error) {
	if d.log != nil && os.Getenv("WT_SYNC_TRACE") != "" {
		fmt.Fprintf(d.log, "  $ git %s\n", strings.Join(args, " "))
	}
	return gitEnv(d.req.Path, rebaseEnv, nil, args...)
}

// resetToSafety puts the worktree back at the tip the run pinned before it
// touched anything. A reset that will not go is the end of the restore: the
// message names the tip, since nothing else will put it back.
func (d *driver) resetToSafety() error {
	if _, err := d.git("reset", "--hard", d.res.Safety.Ref); err != nil {
		return fmt.Errorf("not restored: reset failed: %w; the old tip is %s", err, d.res.Safety.Ref)
	}
	return nil
}

// restore puts the worktree back where the run found it: no rebase in
// progress, HEAD on the branch at the old tip, nothing left in the index.
func (d *driver) restore() error {
	_, _ = d.git("rebase", "--abort")
	if busy, _ := RebaseInProgress(d.req.Path); busy {
		_, _ = d.git("rebase", "--quit")
		// --quit drops the sequencer's state and nothing else: HEAD is
		// left detached where the rebase stopped, the conflict is still
		// in the index, and the branch ref never moved. So reset first,
		// unconditionally, while HEAD is still detached (the guarded
		// reset below compares HEAD with the branch's own tip, which
		// re-attaching first would make equal and skip), and only then
		// put HEAD back on the branch.
		if err := d.resetToSafety(); err != nil {
			return err
		}
		_, _ = d.git("symbolic-ref", "HEAD", "refs/heads/"+d.req.Branch)
	}
	if head, _ := d.git("rev-parse", "HEAD"); head != d.old {
		if err := d.resetToSafety(); err != nil {
			return err
		}
	}
	if busy, _ := RebaseInProgress(d.req.Path); busy {
		return fmt.Errorf("not restored: a rebase is still in progress; the old tip is %s", d.res.Safety.Ref)
	}
	if ref, _ := d.git("symbolic-ref", "--quiet", "HEAD"); ref != "refs/heads/"+d.req.Branch {
		return fmt.Errorf("not restored: HEAD is %q, not %s; the old tip is %s", ref, d.req.Branch, d.res.Safety.Ref)
	}
	if head, _ := d.git("rev-parse", "HEAD"); head != d.old {
		return fmt.Errorf("not restored: HEAD is %s, not %s; the old tip is %s", git.ShortID(head, 7), git.ShortID(d.old, 7), d.res.Safety.Ref)
	}
	// A status that cannot be read is not a clean status: this check
	// fails closed, since its whole job is to prove the worktree is
	// untouched.
	status, serr := d.git("--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if serr != nil {
		return fmt.Errorf("not restored: status could not be read: %w; the old tip is %s", serr, d.res.Safety.Ref)
	}
	if status != "" {
		return fmt.Errorf("not restored: tracked changes remain; the old tip is %s", d.res.Safety.Ref)
	}
	return nil
}

// work is the name to put in front of a person: the caller's, or the branch
// when the caller named none.
func (d *driver) work() string {
	if d.req.Work != "" {
		return d.req.Work
	}
	return d.req.Branch
}

func (d *driver) fail(err error) (Result, error) {
	if d.keep {
		// Not "untouched": a resume that reached a stop may already have
		// applied a strategy's answer before it failed. What is true is
		// that nothing was put back, which is the whole point.
		return d.res, fmt.Errorf("%w; the rebase is left where it stopped: %s", err, WayOut(Way{Work: d.work(), Plan: true, Rebasing: true}))
	}
	rerr := d.restore()
	if rerr == nil {
		d.res.Restored = true
	}
	return d.res, errors.Join(err, rerr)
}

// Rebase rebases one worktree, applying the declared strategies at each
// stop. A stop no strategy resolves is left in place with what the
// strategies did resolve already staged, and reported as a handover
// (Result.Left): the worktree stays mid-rebase for a person, and
// `wt sync resume` or `wt sync undo` is what moves next. A stack parent is
// the exception: its stop is restored, because leaving it would strand its
// children. An error is a git failure, after a restore to the safety ref,
// or a restore that could not be verified.
func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error) {
	d := &driver{mainRoot: mainRoot, cfg: cfg, req: req, log: log, res: Result{Branch: req.Branch}}
	old, err := d.git("rev-parse", "--verify", req.Branch)
	if err != nil {
		return d.res, err
	}
	d.old, d.res.OldTip = old, old
	if d.res.Safety, err = WriteSafety(mainRoot, req.Branch, old, req.Epoch); err != nil {
		return d.res, err
	}
	if d.res.SignaturesDropped, err = signedCount(req.Path, req.base(), old); err != nil {
		return d.res, err
	}
	args := append(append([]string{}, rebaseConfig...), "rebase", "--no-update-refs", "--no-gpg-sign")
	if req.Upstream != "" {
		args = append(args, "--onto", req.Onto, req.Upstream)
	} else {
		args = append(args, req.Onto)
	}
	_, err = d.git(args...)
	return d.drive(err)
}

// Resume drives a rebase a previous run left stopped. The caller has already
// verified the worktree against the run's sidecar. A rebase that is no
// longer in progress is checked before it is believed finished: HEAD on the
// branch, the run's onto an ancestor of it, the tip moved, and moved from the
// old tip. An aborted, reset or restarted rebase is not this run's result,
// and certifying it would let a later undo discard commits the run never
// made.
func Resume(mainRoot string, cfg *Config, req Request, old string, safety Safety, log io.Writer) (Result, error) {
	d := &driver{mainRoot: mainRoot, cfg: cfg, req: req, log: log, old: old, keep: true,
		res: Result{Branch: req.Branch, OldTip: old, Safety: safety}}
	var err error
	if d.res.SignaturesDropped, err = signedCount(req.Path, req.base(), old); err != nil {
		return d.res, err
	}
	busy, err := RebaseInProgress(req.Path)
	if err != nil {
		return d.res, err
	}
	if !busy {
		if err := d.verifyFinished(); err != nil {
			return d.res, err
		}
		return d.finish()
	}
	_, cerr := d.git("rebase", "--continue")
	return d.drive(cerr)
}

func (d *driver) verifyFinished() error {
	return VerifyFinished(d.req.Path, d.req.Branch, d.work(), d.req.Onto, d.old, d.req.Total)
}

// VerifyFinished proves a rebase nobody is in the middle of actually
// completed, rather than having been aborted, quit or reset: HEAD on the
// branch, onto an ancestor of HEAD, HEAD moved off old, the branch's last
// move made from old, and no more commits on top of onto than the total
// the run set out to replay (fewer is a pick dropped as empty; zero means
// the total is not known and is not checked). A rebase finished from any
// other tip, or with a commit made inside it, carries commits the run
// never made, and certifying it would let a later undo discard them. work
// is the name the refusals give undo. Resume checks it itself; a caller
// that must refuse before it takes a lock checks it first.
func VerifyFinished(wtPath, branch, work, onto, old string, total int) error {
	restart := WayOut(Way{Work: work, Plan: true, Rebasing: true, Restart: true})
	aborted := WayOut(Way{Work: work, Plan: true, Aborted: true})
	moved := WayOut(Way{Work: work, Plan: true, Moved: true})
	ref, err := gitEnv(wtPath, rebaseEnv, nil, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || ref != "refs/heads/"+branch {
		return fmt.Errorf("HEAD is %q, not %s: this is not the rebase that was left here; %s", ref, branch, restart)
	}
	head, err := gitEnv(wtPath, rebaseEnv, nil, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head == old {
		// A run refuses while the plan is there, so wt sync run alone is not
		// a way out: undo clears the plan first.
		return fmt.Errorf("%s is back at the tip the run started from: %s", branch, aborted)
	}
	if _, code, err := gitEnvAllow(wtPath, rebaseEnv, nil, 1, "merge-base", "--is-ancestor", onto, "HEAD"); err != nil {
		return err
	} else if code == 1 {
		// HEAD is on the branch and off the old tip, and the run pinned no
		// result, so a plain undo would refuse the moved branch.
		return fmt.Errorf("%s is not on top of what the run was rebasing onto: the rebase did not finish as this run; %s", branch, moved)
	}
	// A rebase that finishes moves the branch once, from the tip it started
	// at, so the reflog entry before HEAD is that tip. Anything else — a
	// commit and a rebase of the person's own, a pull --rebase, or the commit
	// of a deferred step on an earlier try whose finish failed — cannot be
	// told apart from here, so none is certified. A reflog that cannot be
	// read proves nothing, so it refuses too.
	prev, err := gitEnv(wtPath, rebaseEnv, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"@{1}")
	if err != nil {
		return fmt.Errorf("%s's reflog cannot show that the rebase started from the run's tip (%s); %s", branch, git.ShortID(old, 7), moved)
	}
	if prev != old {
		return fmt.Errorf("%s was last moved from %s, not from the tip the run started from (%s): something besides the rebase committed on it, a person or a deferred step on an earlier try, and resume cannot tell which; %s", branch, git.ShortID(prev, 7), git.ShortID(old, 7), moved)
	}
	if total <= 0 {
		return nil
	}
	// A rebase finished from the run's tip onto the run's onto still has
	// room for a commit made inside it before the continue: the reflog
	// shows one move, from old, and the extra commit rides in it. The count
	// is the only thing that shows it. Fewer than total is a pick dropped
	// as empty, which is not a person's commit.
	out, err := gitEnv(wtPath, rebaseEnv, nil, "rev-list", "--count", onto+"..HEAD")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return fmt.Errorf("counting %s..HEAD: %w", git.ShortID(onto, 7), err)
	}
	if n > total {
		return fmt.Errorf("%s carries %d commits on top of what the run rebased onto, and the run set out to replay %d: something was committed inside the rebase; %s", branch, n, total, moved)
	}
	return nil
}

// VerifyLeft is VerifyFinished's twin for a rebase still in progress: it
// proves the one in wtPath is the one the handover st describes — moving
// st.Branch, replaying onto st.Onto, started from st.OldTip — and not a
// person's own, started after aborting the run's, perhaps from a commit of
// theirs. Continuing such a rebase would pin its result under the run's
// epoch, and aborting it would discard that commit with nothing pinning it.
// The onto and orig-head values are resolved to commits before they are
// compared, so an abbreviated or symbolic one can neither refuse the run's
// own rebase nor pass somebody else's. The error names what differs and
// nothing else; the caller says what it did not do.
func VerifyLeft(wtPath string, st State) error {
	notOurs := func(why string) error {
		return fmt.Errorf("the rebase in progress is not the one wt sync run left: %s", why)
	}
	t, ok, err := ReadRebaseTarget(wtPath)
	if err != nil {
		return err
	}
	if !ok {
		return notOurs("it is not a merge-backend rebase")
	}
	if t.HeadName != "refs/heads/"+st.Branch {
		return notOurs(fmt.Sprintf("it moves %s, not refs/heads/%s", t.HeadName, st.Branch))
	}
	want, err := commitOf(wtPath, st.Onto)
	if err != nil {
		return fmt.Errorf("the handover's onto %q is not a commit here: %w", st.Onto, err)
	}
	if got, err := commitOf(wtPath, t.Onto); err != nil || got != want {
		return notOurs(fmt.Sprintf("it replays onto %s, not %s", git.ShortID(t.Onto, 7), git.ShortID(want, 7)))
	}
	if got, err := commitOf(wtPath, t.OrigHead); err != nil || got != st.OldTip {
		return notOurs(fmt.Sprintf("it started from %s, not the tip the run started from (%s)", git.ShortID(t.OrigHead, 7), git.ShortID(st.OldTip, 7)))
	}
	return nil
}

// Commit is one commit named to a person: its id and its subject.
type Commit struct{ SHA, Subject string }

// Inside is what a handed-over rebase carries that an abort would discard
// and only HEAD's reflog would then name.
type Inside struct {
	// Head is the rebase's detached HEAD now: what an abort discards, and
	// what a forced undo pins.
	Head string
	// Commits is what was committed inside the rebase since the handover,
	// oldest first: a person's own commit of the stop's resolution, the
	// pick their own git rebase --continue committed before stopping again,
	// or an amend of the pick before the stop.
	Commits []Commit
	// Unproven says the sidecar predates the recorded head, so nothing
	// separates the run's own picks from a person's commits. Commits is
	// then every commit on top of onto, and even none of them proves
	// nothing: a pick dropped as empty and a commit made by hand cancel
	// out in a count. Undo treats an unproven handover as carrying
	// somebody's work.
	Unproven bool
}

// CommittedInside is what a handed-over rebase carries beyond the run's own
// picks, by comparing HEAD with the head the handover st recorded. A HEAD
// behind the recorded head on the run's own line (a reset backwards inside
// the rebase) is an error: what is there is not known to be the run's
// picks plus somebody's commits, so nothing can be pinned. An amend of the
// pick before the stop is not that: the amended commit is a sibling of the
// recorded head, not reachable from it, so it is reported as an inside
// commit, pinned and restorable like any other.
func CommittedInside(wtPath string, st State) (Inside, error) {
	head, err := gitEnv(wtPath, nil, nil, "rev-parse", "HEAD")
	if err != nil {
		return Inside{}, err
	}
	in := Inside{Head: head}
	if st.Head == "" {
		in.Unproven = true
		in.Commits, err = commitsIn(wtPath, st.Onto+"..HEAD")
		return in, err
	}
	if head == st.Head {
		return in, nil
	}
	if in.Commits, err = commitsIn(wtPath, st.Head+"..HEAD"); err != nil {
		return Inside{}, err
	}
	if len(in.Commits) == 0 {
		return Inside{}, fmt.Errorf("HEAD is at %s, behind where the run left the rebase (%s)", git.ShortID(head, 7), git.ShortID(st.Head, 7))
	}
	return in, nil
}

// commitsIn lists the commits of a revision range, oldest first.
func commitsIn(wtPath, rng string) ([]Commit, error) {
	out, err := gitEnv(wtPath, nil, nil, "log", "--reverse", "--format=%H %s", rng)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		sha, subject, _ := strings.Cut(line, " ")
		commits = append(commits, Commit{SHA: sha, Subject: subject})
	}
	return commits, nil
}

// commitOf resolves rev to the commit it names in dir; an empty rev is an
// error rather than HEAD.
func commitOf(dir, rev string) (string, error) {
	if rev == "" {
		return "", errors.New("empty")
	}
	return gitEnv(dir, nil, nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// drive runs the stop-resolve-continue loop until the rebase finishes, fails
// or reaches a stop a person owns. err is what the command that got the
// rebase moving returned: nil means it is already finished.
func (d *driver) drive(err error) (Result, error) {
	lastIndex, lastUnmerged := -1, ""
	stops, limit := 0, 0
	for err != nil {
		busy, perr := RebaseInProgress(d.req.Path)
		if perr != nil {
			return d.fail(perr)
		}
		if !busy {
			return d.fail(fmt.Errorf("rebase: %w", err))
		}
		p, perr := RebaseProgress(d.req.Path)
		if perr != nil {
			return d.fail(perr)
		}
		conflicts, cerr := StagedConflicts(d.req.Path)
		if cerr != nil {
			return d.fail(cerr)
		}
		// The guard below compares one stop with the one before it, which an
		// alternating unmerged set would walk straight past; this cap bounds
		// the loop whatever the sequencer does. Two stops per commit plus
		// slack, or a flat 64 when the sequencer reports no total.
		stops++
		if limit == 0 {
			if limit = 2*p.Total + 2; p.Total == 0 {
				limit = 64
			}
		}
		if stops > limit {
			return d.fail(fmt.Errorf("rebase did not converge after %d stops", limit))
		}
		unmerged := pathsOf(conflicts)
		if p.Index == lastIndex && (len(conflicts) == 0 || unmerged == lastUnmerged) {
			return d.fail(fmt.Errorf("rebase did not advance at %d/%d: %w", p.Index, p.Total, err))
		}
		lastIndex, lastUnmerged = p.Index, unmerged
		stop := StopResult{Index: p.Index, Total: p.Total, Commit: p.Commit, Subject: p.Subject}
		unresolved := false
		for _, c := range conflicts {
			r, rerr := resolveConflict(d.mainRoot, d.req.Trunk, d.cfg, c, d.req.Path)
			if rerr != nil {
				stop.Files = append(stop.Files, r.Outcome)
				logStop(d.log, stop)
				d.res.Stops = append(d.res.Stops, stop)
				return d.fail(rerr)
			}
			if r.Outcome.Resolved && !r.InPlace {
				if aerr := Apply(d.req.Path, c, r.Content); aerr != nil {
					return d.fail(aerr)
				}
			}
			if !r.Outcome.Resolved {
				unresolved = true
			}
			stop.Files = append(stop.Files, r.Outcome)
		}
		if len(conflicts) == 0 {
			// Stopped with nothing unmerged: rerere or a hook did it all, or
			// the sequencer stopped for a reason we do not handle. One
			// --continue is the honest move; the guard above catches a
			// second stop in the same place.
			stop.Files = append(stop.Files, messagesOutcome("stopped with nothing unmerged"))
		}
		logStop(d.log, stop)
		d.res.Stops = append(d.res.Stops, stop)
		if unresolved {
			if d.req.Stacked {
				// A resume never restores, and a stack parent is no
				// exception: the reset would throw away a person's own
				// resolution, which is worse than the stranded children the
				// restore exists to prevent. Say so and leave it alone.
				if d.keep {
					return d.fail(errors.New("this branch has descendants in the run, and resuming into a stop nobody claims cannot put it back without discarding your work"))
				}
				rerr := d.restore()
				d.res.Restored = rerr == nil
				return d.res, rerr
			}
			h, herr := d.handover(stop, conflicts)
			if herr != nil {
				return d.fail(herr)
			}
			d.res.Left = h
			return d.res, nil
		}
		_, err = d.git("rebase", "--continue")
	}
	// The command reported success, so the sequencer must be finished. If it
	// is not, the rebase did not complete and the worktree goes back.
	busy, perr := RebaseInProgress(d.req.Path)
	if perr != nil {
		return d.fail(perr)
	}
	if busy {
		return d.fail(errors.New("rebase reported success but is still in progress"))
	}
	return d.finish()
}

// finish reads back what the completed rebase produced. Past this point the
// rewrite happened and is what the branch now is: failing to read it back is
// a reporting failure, not a reason to throw the work away, so nothing here
// restores.
func (d *driver) finish() (Result, error) {
	unread := func(err error) (Result, error) {
		return d.res, fmt.Errorf("rebased but could not read the result: %w; the old tip is %s", err, d.res.Safety.Ref)
	}
	var err error
	if d.res.NewTip, err = d.git("rev-parse", "HEAD"); err != nil {
		return unread(err)
	}
	count, err := d.git("rev-list", "--count", d.req.Onto+"..HEAD")
	if err != nil {
		return unread(err)
	}
	if d.res.Replayed, err = strconv.Atoi(count); err != nil {
		return unread(err)
	}
	return d.res, nil
}

// handover records the stop the run is leaving: what a person owns, and what
// each strategy staged — a blob id, or a deletion, which is a resolution a
// script is allowed to make (ResolveInWorktree accepts any outcome that
// leaves nothing unmerged). Resume compares against this to prove nothing
// was hand-merged where a strategy owns the file.
func (d *driver) handover(stop StopResult, conflicts []Conflict) (*Handover, error) {
	h := &Handover{
		Index: stop.Index, Total: stop.Total, Commit: stop.Commit, Subject: stop.Subject,
		Conflicts: conflicts, Files: stop.Files, Staged: map[string]string{},
	}
	for _, f := range stop.Files {
		if !f.Resolved {
			h.Left = append(h.Left, f.Path)
			continue
		}
		// Literal: as a pathspec, a name like v[1].txt also matches v1.txt,
		// and the first record would be the wrong file's blob.
		out, err := d.git("--literal-pathspecs", "ls-files", "--stage", "-z", "--", f.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: reading what %s staged: %w", f.Path, f.Strategy, err)
		}
		rec, _, _ := strings.Cut(out, "\x00")
		if rec == "" {
			h.Deleted = append(h.Deleted, f.Path)
			continue
		}
		meta, _, _ := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s: cannot read its index entry (%q)", f.Path, rec)
		}
		h.Staged[f.Path] = fields[1]
	}
	return h, nil
}

func pathsOf(cs []Conflict) string {
	var ps []string
	for _, c := range cs {
		ps = append(ps, c.Path)
	}
	sort.Strings(ps)
	return strings.Join(ps, "\x00")
}

func logStop(log io.Writer, s StopResult) {
	if log == nil {
		return
	}
	parts := []string{fmt.Sprintf("  stop %d/%d %q", s.Index, s.Total, s.Subject)}
	for _, f := range s.Files {
		mark, note := "✗", f.Note
		if f.Resolved {
			mark, note = "✓", f.Strategy
		}
		parts = append(parts, strings.TrimSpace(mark+" "+f.Path+" "+note))
	}
	fmt.Fprintln(log, strings.Join(parts, "  "))
}

// signedCount counts the commits in base..tip that carry a signature, which a
// rewrite drops (spec §4).
func signedCount(wtPath, base, tip string) (int, error) {
	out, err := gitEnv(wtPath, nil, nil, "log", "--format=%G?", base+".."+tip)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if l != "" && l != "N" {
			n++
		}
	}
	return n, nil
}

// StopPaths is every path a rebase stopped on, stop by stop, leaving out the
// stand-in for a stop with nothing unmerged.
func StopPaths(stops []StopResult) []string {
	var paths []string
	for _, s := range stops {
		for _, f := range s.Files {
			if f.Path != messagesPath {
				paths = append(paths, f.Path)
			}
		}
	}
	return paths
}
