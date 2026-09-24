package commands

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// networkTimeout bounds the calls that reach the network: a run's fetch, and
// the push it ends with. It is shorter than the default git deadline: one
// that has not finished by now is not going to, and every worktree in the run
// is waiting on it.
const networkTimeout = 5 * time.Minute

// RunOptions tunes SyncRun for callers and tests.
type RunOptions struct {
	NoFetch bool
	// Unattended is a run nobody is watching: wt sync keep run. With nothing
	// named it leaves a worktree with any session in it alone, idle or busy,
	// since there is nobody there to ask on its behalf.
	Unattended bool
	// IfReady rebases only what will go through without handing anything
	// to a person: clean, or every stop resolved by a verified strategy —
	// what the overview files under ready. A named worktree that is not is
	// refused, with its stack, and makes the run fail; with nothing named,
	// every worktree left behind trunk does.
	IfReady bool
	verbOptions
	pushOptions
}

// participant is one worktree of a run: the branch it is on, what triage
// made of it, the lock the run holds on it, and what its rebase came to.
type participant struct {
	wt      repo.Worktree
	work    string
	a       wtsync.Assessment
	verdict wtsync.Verdict
	reason  string
	// refused is why the run does not rebase this worktree, printed under
	// its row: its own refusal at triage or at the lock, a stack member
	// refused before anything moved, which poisons the whole stack (never
	// half-apply, spec §4), or a parent that failed or was restored, which
	// takes what sits on top of it with it. Empty means it goes ahead.
	refused string
	lock    *wtsync.Lock
	result  *wtsync.Result
	head    string // HEAD after the rebase and the deferred steps; what a child rebases onto
}

// runPlan is one wt sync run: the trunk it rebases onto, the worktrees the
// arguments named and the stacks they belong to, and what each of them came
// to. SyncRun drives it through its phases in order: selectBranches, triage,
// lockAll, then rebaseOne per worktree.
type runPlan struct {
	ctx     *Context
	opts    RunOptions
	w       io.Writer
	tracker *rebaseTracker

	trunk    string // trunk as a person says it, "main"
	onto     string // what the run rebases onto, "origin/main"
	trunkSHA string // onto's tip, one SHA for the whole run
	cfg      *wtsync.Config

	parents   map[string]string
	ambiguous map[string][]string // branches that merge two ancestors; not handled
	branches  []string            // every member of every named stack, parents first
	parts     map[string]*participant
	epoch     int64

	// Listed once for the run: selectReady and triage read the same list, and
	// the lock lists again with the lock in hand.
	agents       []wtsync.Agent
	agentsListed bool
	// all is a run with nothing named, which picked every ready worktree
	// itself: it asks before moving even one, since nobody named it. assessed
	// is what selectReady saw, by branch, so triage does not simulate the same
	// rebases again.
	all      bool
	assessed map[string]wtsync.Assessment

	// What each worktree came to: a count per outcome for the summary a run
	// of more than one ends with, the line under it for each that did not
	// finish, how the closing error names it, and what may be pushed.
	outcomes   map[string]int
	unfinished []string
	failures   []string
	pushable   []pushTarget
	// What the keeper records of a run with nothing named: the worktrees it
	// left alone with what holds them, the ones it rebased, the ones it
	// pushed.
	left, rebased, pushed []string
	// notReady is the work name of every worktree selectReady left behind
	// trunk, for a run that must say so: IfReady fails on them.
	notReady []string
}

// SyncRun rebases the named worktrees (and the stacks they belong to) onto
// origin/<trunk>: safety ref, strategies at each stop, deferred steps once
// at the end. With nothing named it takes every worktree the overview calls
// ready, less recipe?, and asks first. Anything refused, restored, failed or
// owed is reported and makes the returned error non-nil, so a script sees it.
func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error {
	_, err := runSync(ctx, works, opts, w)
	return err
}

// runSync is SyncRun with the plan handed back, for the keeper's record of
// what a pass did.
func runSync(ctx *Context, works []string, opts RunOptions, w io.Writer) (*runPlan, error) {
	r := &runPlan{ctx: ctx, opts: opts, w: w, tracker: &rebaseTracker{}, trunk: ctx.Config.MainBranch, outcomes: map[string]int{}}
	err := r.run(works)
	return r, err
}

func (r *runPlan) run(works []string) error {
	w, opts := r.w, r.opts
	defer watchSignals(w, r.tracker)()

	if err := r.declare(); err != nil {
		return err
	}
	if len(works) == 0 {
		ready, err := r.selectReady()
		if err != nil {
			return err
		}
		if opts.IfReady {
			for _, l := range r.notReady {
				r.failures = append(r.failures, l+" (not ready)")
			}
		}
		if len(ready) == 0 {
			if len(r.failures) > 0 {
				return fmt.Errorf("not completed: %s", strings.Join(r.failures, ", "))
			}
			return nil
		}
		works = ready
	}
	if err := r.selectBranches(works); err != nil {
		return err
	}
	if proceed, err := r.triage(); err != nil || !proceed {
		if err == nil && len(r.failures) > 0 {
			return fmt.Errorf("not completed: %s", strings.Join(r.failures, ", "))
		}
		return err
	}
	// The backstop for a run that returns early: whatever lockAll and
	// rebaseOne have not released by then. A handover drops its handle
	// before this runs, so the lock it leaves behind for resume and undo is
	// not among them.
	defer r.releaseAll()
	if err := r.lockAll(); err != nil {
		return err
	}
	for _, b := range r.branches {
		r.rebaseOne(b)
	}
	if len(r.branches) > 1 {
		var counts []string
		for _, outcome := range []string{"rebased", "skipped", "needs you", "refused", "restored", "failed"} {
			if n := r.outcomes[outcome]; n > 0 {
				counts = append(counts, fmt.Sprintf("%d %s", n, outcome))
			}
		}
		fmt.Fprintf(w, "\n%s\n", strings.Join(counts, " · "))
		for _, line := range r.unfinished {
			fmt.Fprintln(w, line)
		}
	}
	pushed, pushFailed, err := offerPush(w, opts.Push, opts.ConfirmPush, r.pushable)
	if err != nil {
		return err
	}
	r.pushed = pushed
	r.failures = append(r.failures, pushFailed...)
	if len(r.failures) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(r.failures, ", "))
	}
	return nil
}

// declare fetches trunk unless told not to, pins the one SHA the whole run
// works against, prints the run's first line and reads the declaration
// there.
func (r *runPlan) declare() error {
	ctx := r.ctx
	var fetched string
	if r.opts.NoFetch {
		fetched = lastFetchedParen(ctx.Repo.MainRoot, r.opts.now())
	} else {
		if _, err := git.RunTimeout(ctx.Repo.MainRoot, networkTimeout, "fetch", "--quiet", "origin", r.trunk); err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		fetched = "(fetched)"
	}
	// One SHA for the whole run: the declaration, the scripts and every
	// rebase target are the same trunk, whatever someone else fetches
	// underneath us mid-run.
	onto, trunkSHA, err := trunkTip(ctx)
	if err != nil {
		return err
	}
	r.onto, r.trunkSHA = onto, trunkSHA
	fmt.Fprintf(r.w, "wt sync run  onto %s %s %s\n", onto, git.ShortID(trunkSHA, 7), fetched)
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, trunkSHA)
	if errors.Is(err, wtsync.ErrNoConfig) {
		return undeclaredError{fmt.Sprintf("%s declares no %s on %s: nothing is rebased", ctx.Repo.Name, wtsync.ConfigFile, onto)}
	}
	if err != nil {
		return err
	}
	r.cfg = cfg
	return nil
}

// undeclaredError is a trunk with no .wt-sync.yaml: nothing a run can do, and
// in a run across repositories not a failure — that repository has not opted
// in.
type undeclaredError struct{ msg string }

func (e undeclaredError) Error() string { return e.msg }

// listAgents lists the other agent sessions once for the run.
func (r *runPlan) listAgents() ([]wtsync.Agent, error) {
	if !r.agentsListed {
		agents, err := r.opts.agents(rebasedNothing)
		if err != nil {
			return nil, err
		}
		r.agents, r.agentsListed = agents, true
	}
	return r.agents, nil
}

// selectReady is what a run with nothing named rebases: every worktree the
// overview files under ready, less recipe?, whose run may stop and hand over
// a plan nobody asked for, and less a stack with a member held back, which
// would refuse every one of them at triage. The rest is named with what
// holds it, in the overview's words, and left as it is. The assessments are
// kept for triage, which would otherwise simulate the same rebases again.
func (r *runPlan) selectReady() ([]string, error) {
	ctx := r.ctx
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, err
	}
	worktrees := slices.DeleteFunc(slices.Clone(all), func(wt repo.Worktree) bool { return wt.IsMain })
	agents, err := r.listAgents()
	if err != nil {
		return nil, err
	}
	assessments := assessAll(ctx.Repo.MainRoot, r.trunkSHA, r.cfg, worktrees, agents, assessWorkers)
	r.parents, r.ambiguous, err = wtsync.Parents(ctx.Repo.MainRoot, r.trunkSHA, all)
	if err != nil {
		return nil, err
	}
	r.all = true
	r.assessed = map[string]wtsync.Assessment{}
	for i, wt := range worktrees {
		if wt.Branch != "" {
			r.assessed[wt.Branch] = assessments[i]
		}
	}
	var ready, left []string
	for i, wt := range worktrees {
		a := assessments[i]
		work := worktreeName(ctx, wt.Branch, wt.Path)
		switch {
		case a.Class == wtsync.Current && a.Err == nil:
			// On trunk already; the overview leaves it out too.
		case !r.ready(a):
			left = append(left, leftLabel(work, a))
			// Detached and stale are lifecycle questions, not a rebase that
			// would not go through.
			if a.Class != wtsync.Detached && a.Class != wtsync.Stale {
				r.notReady = append(r.notReady, work)
			}
		default:
			if hold := r.stackHold(wt.Branch); hold != "" {
				left = append(left, work+" "+classLabel(a)+", "+hold)
				r.notReady = append(r.notReady, work)
				continue
			}
			ready = append(ready, work)
		}
	}
	if len(ready) == 0 {
		fmt.Fprintln(r.w, "nothing is ready to rebase")
	} else {
		fmt.Fprintf(r.w, "every ready worktree: %s\n", strings.Join(ready, ", "))
	}
	if len(left) > 0 {
		fmt.Fprintf(r.w, "left as they are: %s\n", strings.Join(left, " · "))
	}
	r.left = left
	return ready, nil
}

// ready is what a run with nothing named takes: readyToRun, and unattended
// nothing with a session in it, idle included. An idle session is a person
// away from their desk; a run they started may ask on its behalf, a keeper
// has nobody to ask.
func (r *runPlan) ready(a wtsync.Assessment) bool {
	return readyToRun(a) && (!r.opts.Unattended || len(a.Sessions) == 0)
}

// stackHold is why a ready worktree is not taken on its own: a member of its
// stack that triage would refuse, or one that would be run and handed over
// or put back, so the whole stack would go with it. A member that is only
// skipped (on trunk, or nothing ahead of it) holds nothing. Empty means the
// stack goes as a whole.
func (r *runPlan) stackHold(branch string) string {
	if anc, ok := r.ambiguous[branch]; ok {
		return "merges " + strings.Join(anc, " and ")
	}
	for _, m := range wtsync.Members(r.parents, branch) {
		if m == branch {
			continue
		}
		a, ok := r.assessed[m]
		if ok {
			if v, _ := wtsync.Preflight(a); v == wtsync.SkipRun || (v == wtsync.Proceed && r.ready(a)) {
				continue
			}
		}
		return "stacked with " + workName(r.ctx, m)
	}
	return ""
}

// selectBranches turns the arguments into the branches the run rebases: each
// named worktree's branch, every other member of its stack, parents first.
func (r *runPlan) selectBranches(works []string) error {
	ctx := r.ctx
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if !wt.IsMain && wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	var named []string
	for _, arg := range works {
		wt, err := locateBranch(ctx, arg)
		if err != nil {
			return err
		}
		named = append(named, wt.Branch)
	}
	if r.parents == nil {
		r.parents, r.ambiguous, err = wtsync.Parents(ctx.Repo.MainRoot, r.trunkSHA, worktrees)
		if err != nil {
			return err
		}
	}
	parents, ambiguous := r.parents, r.ambiguous
	for _, b := range named {
		if anc, ok := ambiguous[b]; ok {
			return fmt.Errorf("%s merges %s; that shape is not handled", workName(ctx, b), strings.Join(anc, " and "))
		}
	}
	seen := map[string]bool{}
	var branches []string
	for _, b := range named {
		var added []string
		for _, m := range wtsync.Members(parents, b) {
			if seen[m] {
				continue
			}
			seen[m] = true
			branches = append(branches, m)
			if m != b {
				added = append(added, workName(ctx, m))
			}
		}
		if len(added) > 0 {
			fmt.Fprintf(r.w, "%s is a stack with %s: rebasing all of them\n", workName(ctx, b), strings.Join(added, ", "))
		}
	}
	r.branches = wtsync.Order(parents, branches)
	r.parts = map[string]*participant{}
	for _, b := range r.branches {
		r.parts[b] = &participant{wt: byBranch[b], work: workName(ctx, b)}
	}
	return nil
}

// triage assesses every branch, refuses what preflight refuses (and the
// stack it belongs to), names the idle sessions the run is about to move
// files under, and asks the one question a run asks. proceed is false for
// the answer no, which is not an error: the line saying so has been printed.
func (r *runPlan) triage() (proceed bool, err error) {
	agents, err := r.listAgents()
	if err != nil {
		return false, err
	}
	for _, b := range r.branches {
		p := r.parts[b]
		if a, ok := r.assessed[b]; ok {
			p.a = a
		} else {
			p.a = wtsync.Assess(r.ctx.Repo.MainRoot, r.trunkSHA, r.cfg, p.wt, agents)
		}
		p.verdict, p.reason = wtsync.Preflight(p.a)
	}
	for _, b := range r.branches {
		if p := r.parts[b]; p.verdict == wtsync.RefuseRun {
			r.refuseStack(b, p.work+": "+p.reason)
		}
	}
	if r.opts.IfReady {
		for _, b := range r.branches {
			p := r.parts[b]
			if p.verdict == wtsync.Proceed && p.refused == "" && !r.ready(p.a) {
				r.refuseStack(b, p.work+" would not sync cleanly: "+notReadyReason(p.a)+"; wt sync "+p.work+" for the detail")
			}
		}
	}
	var going []string
	underIdle := false
	for _, b := range r.branches {
		p := r.parts[b]
		if p.verdict != wtsync.Proceed || p.refused != "" {
			continue
		}
		going = append(going, p.work)
		// Preflight lets sessions through only when every one is idle.
		if len(p.a.Sessions) > 0 {
			fmt.Fprintln(r.w, idleNotice(p.work, p.a.Sessions))
			underIdle = true
		}
	}
	// A run that picked its worktrees itself asks even for one of them.
	if len(going) > 1 || underIdle || (r.all && len(going) > 0) {
		if r.opts.Confirm != nil {
			fmt.Fprintf(r.w, "about to rebase: %s\n", strings.Join(going, ", "))
		}
		// Nothing to re-check here: a run lists the sessions again at the
		// lock, whether or not anything was asked, and compares them there
		// with the lock in hand.
		ok, _, err := askIdle(r.w, r.opts.verbOptions, going, nil, agents, rebasedNothing)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// lockAll locks every member of every proceeding stack before touching any,
// and re-checks what triage saw: the lock is what makes the check hold. A
// worktree that fails here refuses its whole stack, as at triage.
func (r *runPlan) lockAll() error {
	// Listed again now: a question can sit unanswered for as long as it
	// likes, and a triage over a large fleet takes a while.
	fresh, err := r.opts.relist(rebasedNothing)
	if err != nil {
		return err
	}
	r.epoch = r.opts.now().UnixNano()
	for _, b := range r.branches {
		p := r.parts[b]
		if p.refused != "" || p.verdict != wtsync.Proceed {
			continue
		}
		gitDir, err := wtsync.GitDir(p.wt.Path)
		if err != nil {
			return err
		}
		lock, err := wtsync.Acquire(gitDir, r.opts.now())
		if err != nil {
			r.refuseStack(b, p.work+": locked: "+err.Error())
			continue
		}
		p.lock = lock
		// A check that cannot be run is not a passed check: this fails
		// closed, the way the restore check in wtsync does. It says so,
		// rather than reporting the state it never managed to read.
		stand := wtsync.Recheck(p.wt.Path, gitDir, fresh)
		if stand.Err != nil {
			// In git's own words, as this line has always read them: the
			// form wtsync gives its errors adds the exit status.
			msg := stand.Err.Error()
			var gerr *git.Error
			if errors.As(stand.Err, &gerr) {
				msg = gerr.Error()
			}
			r.refuseStack(b, p.work+": changed since triage: could not check: "+msg)
			continue
		}
		if stand.Rebasing {
			why := p.work + ": changed since triage: a rebase is in progress"
			if stand.Plan {
				why = p.work + ": left mid-rebase by an earlier run: " + wtsync.WayOut(wtsync.Way{Work: p.work, Plan: true, Rebasing: true})
			}
			r.refuseStack(b, why)
			continue
		}
		if stand.Dirty {
			r.refuseStack(b, p.work+": changed since triage: tracked changes")
			continue
		}
		if why := sessionsChanged(p.a.Sessions, stand.Sessions); why != "" {
			r.refuseStack(b, p.work+": changed since triage: "+why)
			continue
		}
	}
	// A worktree refused here, or with a stack member refused here, is not
	// touched by the run: its lock goes now, before anything is rebased,
	// so nobody waits on it for the length of the run.
	for _, b := range r.branches {
		if p := r.parts[b]; p.refused != "" {
			r.release(p)
		}
	}
	return nil
}

// release drops p's lock, if the run still holds one.
func (r *runPlan) release(p *participant) {
	if p.lock != nil {
		_ = p.lock.Release()
		p.lock = nil
	}
}

// refuseStack refuses b and every other member of its stack that is not
// already refused: a member refused before anything has moved poisons the
// whole stack, never half-apply (spec §4).
func (r *runPlan) refuseStack(b, why string) {
	for _, m := range wtsync.Members(r.parents, b) {
		if p := r.parts[m]; p != nil && p.refused == "" {
			p.refused = why
		}
	}
}

// refuseAbove refuses what sits on top of b and nothing else. A rebase that
// failed or was restored is different from a refusal: the branch is back
// where it was and its parent and siblings are untouched, so only its
// descendants lose their base.
func (r *runPlan) refuseAbove(b, why string) {
	for _, m := range wtsync.Descendants(r.parents, b) {
		if p := r.parts[m]; p != nil && p.refused == "" {
			p.refused = why
		}
	}
}

// releaseAll drops every lock the run still holds. A handover's lock is
// left behind on purpose and its handle dropped before this runs, so it is
// not touched.
func (r *runPlan) releaseAll() {
	for _, b := range r.branches {
		r.release(r.parts[b])
	}
}

// settle records what one worktree came to: the outcome for the count, the
// line under the summary for one that did not finish, and how the closing
// error names it. Either string may be empty.
func (r *runPlan) settle(outcome, line, failure string) {
	r.outcomes[outcome]++
	if line != "" {
		r.unfinished = append(r.unfinished, "  "+line)
	}
	if failure != "" {
		r.failures = append(r.failures, failure)
	}
}

// rebaseOne prints b's row and, unless it is refused or skipped, rebases it:
// onto trunk, or onto the tip its stack parent ended at. A stop a person
// owns is handed over; a finished rebase runs the deferred steps and pins
// its result. Whatever it comes to is settled, and what sits on top of a
// branch that did not finish is refused.
func (r *runPlan) rebaseOne(b string) {
	ctx, w, p := r.ctx, r.w, r.parts[b]
	// The lock goes as soon as this worktree is done, whichever way, so the
	// next worktree's rebase does not hold it. The one exception is the
	// handover, which drops the handle to leave its lock behind.
	defer r.release(p)
	fmt.Fprintf(w, "\n%s  %s  %d behind · %d ahead\n", p.work, b, p.a.Behind, p.a.Ahead)
	if p.refused != "" {
		fmt.Fprintf(w, "  ✗ refused: %s\n", p.refused)
		r.settle("refused", "✗ "+p.work+"  refused: "+p.refused, p.work)
		return
	}
	if p.verdict == wtsync.SkipRun {
		fmt.Fprintf(w, "  ⏭ skipped: %s\n", p.reason)
		r.settle("skipped", "", "")
		return
	}
	req := wtsync.Request{Path: p.wt.Path, Branch: b, Trunk: r.trunkSHA, Onto: r.trunkSHA, Epoch: r.epoch, Work: p.work}
	req.Stacked = len(wtsync.Descendants(r.parents, b)) > 0
	ontoLabel := r.onto
	if parent, ok := r.parents[b]; ok {
		if pp := r.parts[parent]; pp != nil && pp.result != nil && !pp.result.Restored && pp.head != "" {
			req.Onto, req.Upstream, ontoLabel = pp.head, pp.result.OldTip, pp.work
		}
	}
	if p.a.Class == wtsync.Contested && p.a.Replay.Stop != nil {
		what := "the run stops there and writes a plan"
		if req.Stacked {
			what = "the run stops there and puts the branch back: a stack parent cannot be left waiting"
		}
		fmt.Fprintf(w, "  ⚠ contested at %d/%d: %s\n", p.a.Replay.Stop.Index, p.a.Replay.Stop.Total, what)
	}
	res, rerr := trackedRebase(r.tracker, &rebaseInFlight{work: p.work, path: p.wt.Path, safety: wtsync.SafetyRef(b, r.epoch)}, func() (wtsync.Result, error) {
		return wtsync.Rebase(ctx.Repo.MainRoot, r.cfg, req, w)
	})
	p.result = &res
	if rerr != nil {
		fmt.Fprintf(w, "  ✗ failed: %v\n", rerr)
		clearHandover(w, p.wt.Path)
		r.settle("failed", fmt.Sprintf("✗ %s  failed: %v", p.work, rerr), p.work+" (failed)")
		r.refuseAbove(b, p.work+" failed")
		return
	}
	if res.Left != nil {
		herr := handOver(ctx, w, handoverInput{
			Work: p.work, Branch: b, Path: p.wt.Path, TrunkRef: r.onto, TrunkSHA: r.trunkSHA,
			Onto: req.Onto, Upstream: req.Upstream, Epoch: r.epoch, Cfg: r.cfg, Res: res, Lock: p.lock,
			Tracker: r.tracker,
		})
		if herr != nil {
			fmt.Fprintf(w, "  ✗ failed: %v\n", herr)
			// The rebase is still in the worktree and there is now no
			// plan file to explain it, so say the two things a person
			// cannot see for themselves. Any half-written brief goes:
			// the sidecar is the marker and it was never written, so
			// nothing acts on what is left, and a stale markdown would
			// describe a stop that is not this one.
			clearHandover(w, p.wt.Path)
			// Not wt sync undo: it refuses a mid-rebase worktree, and
			// there is no handover here for it to abort. The branch ref
			// never moved, so the abort is the whole of putting it back.
			way := wtsync.WayOut(wtsync.Way{Work: p.work, Path: p.wt.Path, Rebasing: true})
			fmt.Fprintf(w, "  ⚠ %s is left mid-rebase with no plan: %s\n", p.work, way)
			r.settle("failed", "✗ "+p.work+"  left mid-rebase with no plan: "+way, p.work+" (failed)")
			r.refuseAbove(b, p.work+" failed")
			return
		}
		// The lock is left behind on purpose; dropping the handle here
		// keeps the deferred releases from removing the file.
		p.lock = nil
		r.settle("needs you", "⚠ "+p.work+"  needs you: "+wtsync.WayOut(wtsync.Way{Work: p.work, Plan: true, Rebasing: true, OwesAdd: true}), p.work+" (needs you)")
		r.refuseAbove(b, p.work+" is waiting for you")
		return
	}
	if res.Restored {
		last := res.Stops[len(res.Stops)-1]
		var files []string
		for _, f := range last.Files {
			if !f.Resolved {
				files = append(files, f.Path)
			}
		}
		restored := fmt.Sprintf("restored: %s at %d/%d not resolved; rebase by hand", strings.Join(files, ", "), last.Index, last.Total)
		fmt.Fprintf(w, "  ✗ %s\n", restored)
		clearHandover(w, p.wt.Path)
		r.settle("restored", "✗ "+p.work+"  "+restored, p.work+" (restored)")
		r.refuseAbove(b, p.work+" was restored")
		return
	}
	line := fmt.Sprintf("  ✓ rebased %d commit%s onto %s", res.Replayed, plural(res.Replayed), ontoLabel)
	if res.SignaturesDropped > 0 {
		line += fmt.Sprintf(", %d signature%s dropped", res.SignaturesDropped, plural(res.SignaturesDropped))
	}
	fmt.Fprintln(w, line)
	head, owed, derr := completeRun(ctx, w, r.cfg, r.tracker, p.work, p.wt.Path, func() completeInput {
		return completeInput{
			Branch: b, Epoch: r.epoch, Res: res,
			Tell: p.a.Sessions, TrunkName: r.trunk, Landed: p.a.Behind, Check: pathsOnce(wtsync.StopPaths(res.Stops)),
		}
	})
	p.head = head
	r.failures = append(r.failures, owedBy(p.work, owed)...)
	// The rebase itself stands; only this branch and what sits on it
	// lose their footing, so the rest of the run carries on.
	if derr != nil {
		fmt.Fprintf(w, "  ✗ failed: %v\n", derr)
		r.settle("failed", fmt.Sprintf("✗ %s  failed: %v", p.work, derr), p.work+" (failed)")
		r.refuseAbove(b, p.work+" failed")
		return
	}
	r.rebased = append(r.rebased, p.work)
	if len(owed) > 0 {
		r.settle("rebased", "✗ "+p.work+"  owed: "+strings.Join(owed, ", ")+"; run it by hand, then push", "")
	} else {
		r.settle("rebased", "", "")
		r.pushable = append(r.pushable, pushTarget{Work: p.work, Branch: b, Path: p.wt.Path})
	}
}

func printDeferred(w io.Writer, d wtsync.DeferredResult) {
	switch {
	case !d.Ran:
		fmt.Fprintf(w, "  ⏭ %s  %s\n", d.Step.Run, d.Why)
	case d.Err != nil:
		fmt.Fprintf(w, "  ✗ %s  %s  owed: %v\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Err)
		for _, l := range lastLines(d.Output, 20) {
			fmt.Fprintf(w, "    %s\n", l)
		}
	case d.Commit != "":
		fmt.Fprintf(w, "  ✓ %s  %s  committed %d file%s (+%d −%d) as %s\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Files, plural(d.Files), d.Insertions, d.Deletions, d.Commit)
	default:
		fmt.Fprintf(w, "  ✓ %s  %s\n", d.Step.Run, d.Elapsed.Round(time.Second))
	}
}

func lastLines(s string, n int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// notReadyReason is why a worktree a run could start on is not ready, in the
// words a person decides by: what would stop it, or what holds it.
func notReadyReason(a wtsync.Assessment) string {
	switch {
	case a.Dirty:
		return "it has uncommitted changes"
	case a.Paused:
		return "an earlier run handed it over"
	case a.Unverified:
		return "a script strategy claims a file, and a script can only be checked by a real run"
	case a.Class == wtsync.Contested && a.Replay.Stop != nil:
		return fmt.Sprintf("it would stop at %d/%d with a conflict that is yours", a.Replay.Stop.Index, a.Replay.Stop.Total)
	}
	return "it is " + classLabel(a)
}
