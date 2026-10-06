package commands

import (
	"cmp"
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
	// NoFFTrunk leaves local trunk where it is after the fetch, as the user
	// setting ff_trunk false does.
	NoFFTrunk bool
	// Unattended is a run nobody is watching: wt sync keep once. With nothing
	// named it leaves a worktree with any session in it alone, idle or busy,
	// since there is nobody there to ask on its behalf.
	Unattended bool
	// IfReady rebases only what will go through without handing anything
	// to a person: clean, or every stop resolved by a verified strategy —
	// what the overview files under ready. A named worktree that is not is
	// refused, with its stack, and makes the run fail; with nothing named,
	// every worktree left behind trunk does.
	IfReady bool
	// AllowDiverged rebases a branch that has diverged from its own remote
	// as it stands: the remote has commits the branch never had, and the
	// push after it would replace them. It lifts nothing else.
	AllowDiverged bool
	// Force rebases a named worktree even with an agent session in it, busy
	// or idle: the sessions are named, not checked. Nothing else it refuses
	// is lifted — dirt, a handover, another run's lock. A run with nothing
	// named does not take it: that would roll over every session at once.
	Force bool
	// Journal records what the run does, participant by participant, for
	// --json; nil records nothing.
	Journal *RunJournal
	// Expect is the token wt sync --json gave for the overview: the run
	// refuses, touching nothing, when what it would start on is no longer
	// what the overview showed. Empty checks nothing.
	Expect string
	// planExpect is wt up's: Expect is the token wt status --json gave for
	// the plan, and names trunk, the configuration and the stack.
	planExpect bool
	// label is the run's first word, "wt up" for the short form.
	label string
	// undeclaredOK lets a trunk with no .wt-sync.yaml be rebased onto, with
	// nothing declared: no strategies, no deferred steps. With IfReady that
	// takes a conflict-free rebase and nothing else, which is all wt up asks.
	undeclaredOK bool
	// own is the remotes as a caller that already fetched read them: the
	// keeper, and a run across repositories, which fetch before the run
	// they then start with NoFetch.
	own *wtsync.Own
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
	// forced is the sessions Force took the run past: named before anything
	// moves, and told at the finish like an idle one.
	forced wtsync.Sessions
	// notRun is a refusal that came from another member of the stack, not
	// from this worktree: --json reports it as not run.
	notRun bool
	// found is the branch's tip as the run found it, when the run moved it
	// to its own remote's commit before rebasing: what the safety ref pins
	// and a restore goes back to.
	found string
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
	// own is every branch against its own remote, read after the fetch.
	own *wtsync.Own

	parents   map[string]string
	ambiguous map[string][]string // branches that merge two ancestors; not handled
	branches  []string            // every member of every named stack, parents first
	parts     map[string]*participant
	epoch     int64

	// Listed once for the run: selectReady and triage read the same list, and
	// the lock lists again with the lock in hand.
	agents       []wtsync.Agent
	agentsListed bool
	// survey is the overview --expect recomputed, whose assessments
	// selectReady and triage use rather than simulate the same rebases again.
	survey *survey
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
	// pushed, and the ones it only fast-forwarded to their own remote.
	left, rebased, pushed []string
	forwarded             []string
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
	return journaled(opts.Journal, func() error {
		_, err := runSync(ctx, works, opts, w)
		return err
	})
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

	if err := r.declare(works); err != nil {
		return err
	}
	if opts.Expect != "" && !opts.planExpect {
		if err := r.expectOverview(); err != nil {
			return err
		}
	}
	if len(works) == 0 && opts.Force {
		return errors.New("--force takes a run past the sessions in a worktree you name; name it")
	}
	if len(works) == 0 && opts.AllowDiverged {
		return errors.New("--allow-diverged rebases a worktree you name over commits its remote has; name it")
	}
	// wt up's token names the stack, which is only known further down.
	if opts.Expect == "" || !opts.planExpect {
		r.syncTrunk()
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
	r.fetchMissed()
	if opts.Expect != "" && opts.planExpect {
		if now := planToken(r.ctx, r.trunk, r.branches, r.diverged()); now != opts.Expect {
			return fmt.Errorf("the plan changed since it was read (trunk %s, the configuration, the stack %s "+
				"or a remote the stack has diverged from is not what wt status --json saw); nothing is "+
				"rebased: read the plan again", r.trunk, strings.Join(r.branches, ", "))
		}
		r.syncTrunk()
	}
	for _, b := range r.branches {
		p := r.parts[b]
		before, _ := r.ctx.Repo.ResolveRef("refs/heads/" + b)
		opts.Journal.join(p.work, b, p.wt.Path, before, ownRemoteSyncOf(ownStateOf(r.own, b)))
	}
	if proceed, err := r.triage(); err != nil || !proceed {
		if err == nil {
			opts.Journal.fail(errors.New("not confirmed: " + rebasedNothing.line))
		}
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
		for _, outcome := range []string{"rebased", "fast-forwarded", "skipped", "needs you", "refused", "restored", "failed"} {
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
	for _, t := range r.pushable {
		if slices.Contains(pushed, t.Work) {
			opts.Journal.set(t.Branch, func(jp *UpParticipant) { jp.Pushed = true })
		}
	}
	r.failures = append(r.failures, pushFailed...)
	if len(r.failures) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(r.failures, ", "))
	}
	return nil
}

// declare fetches trunk unless told not to, and with it the own remote of
// every branch works may bring into the run, pins the one SHA the whole run
// works against, prints the run's first line and reads the declaration
// there. Trunk that cannot be fetched refuses the run; a branch whose own
// remote cannot is refused by itself, at triage.
func (r *runPlan) declare(works []string) error {
	ctx := r.ctx
	var fetched string
	if r.opts.NoFetch {
		fetched = lastFetchedParen(ctx.Repo.MainRoot, r.opts.now())
		if r.own = r.opts.own; r.own == nil {
			own, err := wtsync.ReadOwn(ctx.Repo.MainRoot, r.trunk)
			if err != nil {
				return err
			}
			r.own = own
		} else if err := r.own.Refresh(); err != nil {
			// Read again: a question may have waited since the caller's fetch.
			return err
		}
	} else {
		// An overview's token covers every worktree, so its run reads them
		// all, as a run with nothing named does.
		if r.opts.Expect != "" && !r.opts.planExpect {
			works = nil
		}
		own, err := fetchRemotes(ctx, networkTimeout, ownCandidates(ctx, works))
		if err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		r.own = own
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
	r.opts.Journal.repo(ctx.Repo.MainRoot)
	r.opts.Journal.trunk(r.trunk, ctx.trunkSource(), onto, trunkSHA, !r.opts.NoFetch)
	label := r.opts.label
	if label == "" {
		label = "wt sync run"
	}
	fmt.Fprintf(r.w, "%s  onto %s %s %s\n", label, onto, git.ShortID(trunkSHA, 7), fetched)
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, trunkSHA)
	if errors.Is(err, wtsync.ErrNoConfig) && r.opts.undeclaredOK {
		fmt.Fprintf(r.w, "no %s on %s: nothing is declared, so only a conflict-free rebase goes\n", wtsync.ConfigFile, onto)
		r.cfg = &wtsync.Config{}
		return nil
	}
	if errors.Is(err, wtsync.ErrNoConfig) {
		// Nothing to rebase, but trunk was fetched: local trunk follows,
		// unless an --expect is waiting to refuse the run anyway.
		if r.opts.Expect == "" {
			r.syncTrunk()
		}
		return undeclaredError{fmt.Sprintf("%s declares no %s on %s: nothing is rebased", ctx.Repo.Name, wtsync.ConfigFile, onto)}
	}
	if err != nil {
		return err
	}
	r.cfg = cfg
	return nil
}

// syncTrunk brings local trunk to the trunk the run fetched, when it did,
// and says what came of it. It runs once an --expect has held: a refused
// run touches nothing, local trunk included.
func (r *runPlan) syncTrunk() {
	if r.opts.NoFetch {
		return
	}
	ts := syncLocalTrunk(r.ctx, r.trunkSHA, trunkSyncOptions{apply: true,
		optedOut: r.opts.NoFFTrunk || !r.ctx.UserConfig().FFTrunk,
		agents:   r.listAgents})
	r.opts.Journal.trunkSync(ts)
	if line := ts.Line(); line != "" {
		fmt.Fprintln(r.w, line)
	}
}

// fetchMissed fetches the own remote of a branch the run chose and its first
// fetch did not ask for: the stack is read against the trunk that fetch
// brought, so it cannot be known before it. Not expected, and one more
// fetch when it happens rather than a branch compared as last fetched
// unsaid. It refuses nothing: a branch it cannot fetch is refused at triage,
// like one the first fetch could not.
func (r *runPlan) fetchMissed() {
	if r.opts.NoFetch {
		return
	}
	var missed []string
	for _, b := range r.branches {
		if o := r.own.State(b); !o.Fetched && o.FetchFailed == "" && o.State != wtsync.OwnUnknown {
			missed = append(missed, b)
		}
	}
	if len(r.own.Asks(missed)) > 0 {
		_ = fetchOwn(r.ctx, r.own, networkTimeout, missed, false)
	}
}

// diverged is the remote commit of every branch of the run that has diverged
// from its own remote, by branch: what a plan's token covers of them.
func (r *runPlan) diverged() map[string]string {
	out := map[string]string{}
	for _, b := range r.branches {
		if o := r.own.State(b); o.State == wtsync.OwnDiverged {
			out[b] = o.Commit
		}
	}
	return out
}

// undeclaredError is a trunk with no .wt-sync.yaml: nothing a run can do, and
// in a run across repositories not a failure — that repository has not opted
// in.
type undeclaredError struct{ msg string }

func (e undeclaredError) Error() string { return e.msg }

// expectOverview recomputes the overview wt sync --json printed, against the
// trunk just fetched, and refuses the run when its token is not the one
// expected: what the run would start on is not what the overview showed.
func (r *runPlan) expectOverview() error {
	agents, err := r.listAgents()
	if err != nil {
		return err
	}
	sv, err := surveyRepo(r.ctx, r.trunkSHA, r.cfg, agents, r.own)
	if err != nil {
		return err
	}
	r.survey = &sv
	if now := sv.token(r.ctx, r.trunk, r.trunkSHA); now == nil || *now != r.opts.Expect {
		return fmt.Errorf("the overview changed since it was read (what a run on trunk %s would start on is "+
			"not what wt sync --json saw); nothing is rebased: read the overview again", r.trunk)
	}
	r.parents, r.ambiguous, r.assessed = sv.parents, sv.ambiguous, sv.assessed
	return nil
}

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
	var assessments []wtsync.Assessment
	if sv := r.survey; sv != nil {
		worktrees, assessments = sv.worktrees, sv.assessments
	} else {
		agents, err := r.listAgents()
		if err != nil {
			return nil, err
		}
		assessments = assessAll(ctx.Repo.MainRoot, r.trunkSHA, r.cfg, worktrees, agents, r.own, assessWorkers)
		r.parents, r.ambiguous, err = wtsync.Parents(ctx.Repo.MainRoot, r.trunkSHA, all)
		if err != nil {
			return nil, err
		}
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
		case onTrunk(a):
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

// allow marks an assessment whose branch has diverged from its own remote as
// one this run rebases as it stands, when it was told to. Only a named
// worktree gets here with the flag: a run with nothing named refuses it.
func (r *runPlan) allow(a wtsync.Assessment) wtsync.Assessment {
	if r.opts.AllowDiverged && a.Own.State == wtsync.OwnDiverged {
		a.Own.Allowed = true
	}
	return a
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
			p.a = wtsync.AssessOwn(r.ctx.Repo.MainRoot, r.trunkSHA, r.cfg, p.wt, agents, ownStateOf(r.own, b))
		}
		if r.opts.Force && len(p.a.Sessions) > 0 {
			p.forced = p.a.Sessions
			if p.a.Own.Blocks == wtsync.OwnBlockSession {
				// The session was all that kept the branch from its remote's
				// commit, so that commit is what the run rebases: assessed
				// again there, with nobody in the way.
				p.a = wtsync.AssessOwn(r.ctx.Repo.MainRoot, r.trunkSHA, r.cfg, p.wt, nil, ownStateOf(r.own, b))
			}
			p.a.Sessions = nil
			fmt.Fprintf(r.w, "⚠ %s: --force takes the run past %s; the files it has read will change\n",
				p.work, whoLabel(p.forced))
		}
		p.a = r.allow(p.a)
		p.verdict, p.reason = wtsync.Preflight(p.a)
		own, refused := p.a.Own, p.verdict == wtsync.RefuseRun
		r.opts.Journal.set(b, func(jp *UpParticipant) {
			jp.OwnRemoteSync = ownRemoteSyncOf(own)
			switch {
			case !refused || !own.Refuses():
			case own.FetchFailed != "":
				jp.OwnRemoteSync.SkippedReason = strp(OwnSkipFetchFailed)
			default:
				jp.OwnRemoteSync.SkippedReason = strp(own.Blocks)
			}
		})
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
		if why := sessionsChanged(p.a.Sessions, stand.Sessions); why != "" && !r.opts.Force {
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
			p.refused, p.notRun = why, m != b
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
			p.refused, p.notRun = why, true
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
	j := r.opts.Journal
	if p.refused != "" {
		fmt.Fprintf(w, "  ✗ refused: %s\n", p.refused)
		r.settle("refused", "✗ "+p.work+"  refused: "+p.refused, p.work)
		j.set(b, func(jp *UpParticipant) {
			jp.Result, jp.Reason = ResultRefused, strp(p.refused)
			if p.notRun {
				jp.Result = ResultNotRun
			}
		})
		return
	}
	if p.verdict == wtsync.SkipRun {
		fmt.Fprintf(w, "  ⏭ skipped: %s\n", p.reason)
		r.settle("skipped", "", "")
		j.set(b, func(jp *UpParticipant) { jp.Result, jp.Reason = ResultSkipped, strp(p.reason) })
		return
	}
	if p.a.OnlyFastForward() {
		r.fastForwardOnly(b, p)
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
	switch own := p.a.Own; {
	case own.State == wtsync.OwnUnknown:
		fmt.Fprintf(w, "  ⚠ %s\n", ownLine(own))
	case own.Allowed:
		fmt.Fprintf(w, "  ⚠ --allow-diverged: %s has %d commit%s this branch never had%s; rebasing it as it stands\n",
			own.Ref, own.Behind, plural(own.Behind), asLastFetched(own))
		// Only now: the flag given to a run that refused the worktree for
		// something else went over nothing.
		j.set(b, func(jp *UpParticipant) { jp.OwnRemoteSync.Allowed = true })
	case own.FastForwards():
		safety, ok := r.fastForward(b, p, "")
		if !ok {
			return
		}
		req.Safety = safety
	}
	// found is the tip the run found the branch at: the one the rebase
	// starts from, unless the run fast-forwarded it first.
	found := func(res wtsync.Result) string { return cmp.Or(p.found, res.OldTip) }
	res, rerr := trackedRebase(r.tracker, &rebaseInFlight{work: p.work, path: p.wt.Path, safety: wtsync.SafetyRef(b, r.epoch)}, func() (wtsync.Result, error) {
		return wtsync.Rebase(ctx.Repo.MainRoot, r.cfg, req, w)
	})
	p.result = &res
	if rerr != nil {
		fmt.Fprintf(w, "  ✗ failed: %v\n", rerr)
		clearHandover(w, p.wt.Path)
		r.recordFailed(b, p, rerr.Error(), found(res), res.Safety.Ref)
		r.settle("failed", fmt.Sprintf("✗ %s  failed: %v", p.work, rerr), p.work+" (failed)")
		r.refuseAbove(b, p.work+" failed")
		return
	}
	if res.Left != nil {
		herr := handOver(ctx, w, handoverInput{
			Work: p.work, Branch: b, Path: p.wt.Path, TrunkRef: r.onto, TrunkSHA: r.trunkSHA,
			Onto: req.Onto, Upstream: req.Upstream, Epoch: r.epoch, Cfg: r.cfg, Res: res, Lock: p.lock,
			Found: p.found, Tracker: r.tracker,
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
			j.setSync(b, func(jp *SyncParticipant) {
				jp.Result, jp.Reason, jp.Recovery = ResultNeedsRecovery, strp(herr.Error()), strp(way)
				jp.SafetyRef = strp(res.Safety.Ref)
			})
			r.settle("failed", "✗ "+p.work+"  left mid-rebase with no plan: "+way, p.work+" (failed)")
			r.refuseAbove(b, p.work+" failed")
			return
		}
		// The lock is left behind on purpose; dropping the handle here
		// keeps the deferred releases from removing the file.
		p.lock = nil
		j.setSync(b, func(jp *SyncParticipant) {
			jp.Result = ResultHandedOver
			jp.Reason = strp("stopped at a conflict that is yours")
			jp.Recovery = strp(wtsync.WayOut(wtsync.Way{Work: p.work, Plan: true, Rebasing: true, OwesAdd: true}))
			jp.SafetyRef, jp.UndoCommand, jp.PlanFile = strp(res.Safety.Ref), undoCommand(p.work), planFileOf(p.wt.Path)
		})
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
		r.dropFastForward(b, p)
		j.setSync(b, func(jp *SyncParticipant) {
			jp.Result, jp.Reason, jp.SafetyRef = ResultRestored, strp(restored), strp(res.Safety.Ref)
			jp.After = strp(found(res))
		})
		r.settle("restored", "✗ "+p.work+"  "+restored, p.work+" (restored)")
		r.refuseAbove(b, p.work+" was restored")
		return
	}
	line := fmt.Sprintf("  ✓ rebased %d commit%s onto %s", res.Replayed, plural(res.Replayed), ontoLabel)
	if res.SignaturesDropped > 0 {
		line += fmt.Sprintf(", %d signature%s dropped", res.SignaturesDropped, plural(res.SignaturesDropped))
	}
	fmt.Fprintln(w, line)
	head, ran, derr := completeRun(ctx, w, r.cfg, r.tracker, p.work, p.wt.Path, func() completeInput {
		// What the deferred steps compare and the undo line names is the tip
		// the run found, the commits a fast-forward brought included.
		done := res
		done.OldTip = found(res)
		return completeInput{
			Branch: b, Epoch: r.epoch, Res: done,
			Tell: append(slices.Clone(p.a.Sessions), p.forced...), TrunkName: r.trunk, Landed: p.a.Behind, Check: pathsOnce(wtsync.StopPaths(res.Stops)),
		}
	})
	p.head = head
	owed := owedSteps(ran)
	r.failures = append(r.failures, owedBy(p.work, owed)...)
	// The rebase itself stands; only this branch and what sits on it
	// lose their footing, so the rest of the run carries on.
	after, _ := ctx.Repo.ResolveRef("refs/heads/" + b)
	j.setSync(b, func(jp *SyncParticipant) {
		jp.SafetyRef, jp.UndoCommand, jp.Deferred = strp(res.Safety.Ref), undoCommand(p.work), deferredSteps(ran)
	})
	if derr != nil {
		fmt.Fprintf(w, "  ✗ failed: %v\n", derr)
		j.set(b, func(jp *UpParticipant) {
			jp.Result, jp.After = ResultRebasedStepFailed, strp(after)
			jp.FailedSteps = append(jp.FailedSteps, "finish: "+derr.Error())
		})
		r.settle("failed", fmt.Sprintf("✗ %s  failed: %v", p.work, derr), p.work+" (failed)")
		r.refuseAbove(b, p.work+" failed")
		return
	}
	r.rebased = append(r.rebased, p.work)
	if len(owed) > 0 {
		r.settle("rebased", "✗ "+p.work+"  owed: "+strings.Join(owed, ", ")+"; run it by hand, then push", "")
		j.set(b, func(jp *UpParticipant) {
			jp.Result, jp.After = ResultRebasedStepFailed, strp(after)
			jp.FailedSteps = append(jp.FailedSteps, owed...)
		})
	} else {
		r.settle("rebased", "", "")
		t := pushTarget{Work: p.work, Branch: b, Path: p.wt.Path}
		r.pushable = append(r.pushable, t)
		j.set(b, func(jp *UpParticipant) {
			jp.Result, jp.After = ResultRebased, strp(after)
			jp.PushCommand = append([]string{"git", "-C", t.Path}, pushArgs(t)...)
		})
	}
}

// fastForward moves b, which is behind its own remote, up to the remote's
// commit before its rebase. Two refs are pinned first, in one transaction:
// the safety ref at the tip the run found, so undo puts the branch back
// where it stood before both, and the fast-forward ref at the remote's
// commit, where the fast-forward leaves it, which is what lets undo take a
// fast-forwarded branch back from a rebase that was handed over, or from an
// interrupt between the two. It is not the result ref: that one says a run
// finished, and a commit the branch holds only because the run is moving it
// there is not yet anything the branch had. A fast-forward that does not go touches
// nothing: the branch is refused, and what sits on it with it. ok is false
// then. done is what the line adds when the fast-forward is all the run does.
func (r *runPlan) fastForward(b string, p *participant, done string) (safety wtsync.Safety, ok bool) {
	ctx, w, own := r.ctx, r.w, p.a.Own
	refuse := func(why string) (wtsync.Safety, bool) {
		why = "cannot fast-forward to " + own.Ref + ": " + why
		fmt.Fprintf(w, "  ✗ refused: %s\n", why)
		r.settle("refused", "✗ "+p.work+"  refused: "+why, p.work)
		r.opts.Journal.set(b, func(jp *UpParticipant) {
			jp.Result, jp.Reason = ResultRefused, strp(why)
			jp.OwnRemoteSync.SkippedReason = strp(OwnSkipFailed)
		})
		r.refuseAbove(b, p.work+" could not be fast-forwarded")
		return wtsync.Safety{}, false
	}
	tip, there := ctx.Repo.ResolveRef("refs/heads/" + b)
	if !there || tip != own.Local {
		return refuse("the branch moved since it was checked")
	}
	safety = wtsync.Safety{Branch: b, Epoch: r.epoch, Ref: wtsync.SafetyRef(b, r.epoch), Tip: tip}
	if err := wtsync.WriteRun(ctx.Repo.MainRoot, r.epoch, []wtsync.Pin{{Branch: b, Safety: tip, Forward: own.Commit}}); err != nil {
		return refuse(err.Error())
	}
	// An interrupt from here names the worktree and the ref that puts it
	// back, as one during the rebase does.
	r.tracker.set(&rebaseInFlight{work: p.work, path: p.wt.Path, safety: safety.Ref, forwarding: true})
	if err := fastForwardOwn(p.wt.Path, b, own.Commit); err != nil {
		// git moves the branch before it runs a hook, so a merge that
		// reports a failure may still have gone through: where the branch
		// is now decides, not the exit status.
		switch now, _ := ctx.Repo.ResolveRef("refs/heads/" + b); now {
		case own.Commit:
			fmt.Fprintf(w, "  note: the fast-forward went through, and git then reported: %v\n", err)
		case tip:
			r.tracker.set(nil)
			if derr := wtsync.DeleteSafety(ctx.Repo.MainRoot, safety); derr != nil {
				fmt.Fprintf(w, "  note: could not remove %s: %v\n", safety.Ref, derr)
			}
			return refuse(err.Error())
		default:
			// Neither where it was nor where it was going: the refs stay,
			// since the old tip is under one of them.
			r.tracker.set(nil)
			return refuse(fmt.Sprintf("%v; the branch is at %s, and its old tip is under %s", err, git.ShortID(now, 7), safety.Ref))
		}
	}
	p.found = tip
	fmt.Fprintf(w, "  ✓ fast-forwarded to %s  %s → %s (%d commit%s)%s%s\n", own.Ref,
		git.ShortID(tip, 7), git.ShortID(own.Commit, 7), own.Behind, plural(own.Behind), asLastFetched(own), done)
	// Where the branch stands from here, whatever the rebase comes to: a
	// rebase that is put back says so itself.
	r.opts.Journal.set(b, func(jp *UpParticipant) {
		jp.OwnRemoteSync.FastForwarded, jp.After = true, strp(own.Commit)
	})
	return safety, true
}

// fastForwardOnly is the whole of a run for a branch that is behind its own
// remote and has nothing to rebase once it is at the remote's commit: it is
// moved there, as before a rebase and with the same refs pinned, so undo
// puts it back, and that is all. No deferred step runs and nothing is
// offered to push: the branch is what its remote has. What sits on it in the
// run is rebased onto where it now is.
func (r *runPlan) fastForwardOnly(b string, p *participant) {
	w, own := r.w, p.a.Own
	why := "; already on trunk, nothing to rebase"
	if p.a.Class == wtsync.Stale {
		why = "; nothing ahead of trunk, nothing to rebase"
	}
	safety, ok := r.fastForward(b, p, why)
	if !ok {
		return
	}
	// Nothing is in flight any more: the branch is where the run leaves it.
	r.tracker.set(nil)
	p.result = &wtsync.Result{Branch: b, OldTip: p.found, NewTip: own.Commit, Safety: safety}
	p.head = own.Commit
	// The run is finished for this branch, here.
	if err := wtsync.WriteResult(r.ctx.Repo.MainRoot, b, own.Commit, r.epoch); err != nil {
		fmt.Fprintf(w, "  note: %v\n", err)
	}
	fmt.Fprintf(w, "  ↩ %s\n", wtsync.WayOut(wtsync.Way{Work: p.work, Result: true, Safety: git.ShortID(p.found, 7)}))
	tellIdle(w, append(slices.Clone(p.a.Sessions), p.forced...), wtsync.FastForwardedLine(p.work, own.Ref, own.Behind))
	r.settle("fast-forwarded", "", "")
	r.forwarded = append(r.forwarded, p.work)
	r.opts.Journal.setSync(b, func(jp *SyncParticipant) {
		jp.Result = ResultFastForwarded
		jp.SafetyRef, jp.UndoCommand = strp(safety.Ref), undoCommand(p.work)
	})
}

// dropFastForward takes back what a fast-forward left behind, once a rebase
// that was put back has taken the branch back to the tip the run found: the
// remote's commit is no longer anything the branch had, in wt's refs or in
// its reflog.
func (r *runPlan) dropFastForward(b string, p *participant) {
	if p.found == "" {
		return
	}
	if err := wtsync.Disown(r.ctx.Repo.MainRoot, b, r.epoch); err != nil {
		fmt.Fprintf(r.w, "  note: %v\n", err)
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
	case a.Own.Refuses():
		return a.Own.Refusal()
	case a.Unverified:
		return "a script strategy claims a file, and a script can only be checked by a real run"
	case a.Class == wtsync.Contested && a.Replay.Stop != nil:
		return fmt.Sprintf("it would stop at %d/%d with a conflict that is yours", a.Replay.Stop.Index, a.Replay.Stop.Total)
	}
	return "it is " + classLabel(a)
}

// recordFailed records a rebase that failed by what it left: back at the
// tip the run found, with no rebase in progress and nothing changed, is
// restored; anything else needs putting back by hand, and says how. safety
// is the ref the rebase pinned the old tip under, "" when it failed first.
func (r *runPlan) recordFailed(b string, p *participant, why, oldTip, safety string) {
	now, _ := r.ctx.Repo.ResolveRef("refs/heads/" + b)
	busy, berr := wtsync.RebaseInProgress(p.wt.Path)
	dirty, derr := repo.Dirty(p.wt.Path, true)
	head, herr := git.Run(p.wt.Path, "rev-parse", "--verify", "--quiet", "HEAD")
	restored := berr == nil && !busy && derr == nil && !dirty && herr == nil &&
		oldTip != "" && now == oldTip && head == oldTip
	if restored {
		r.dropFastForward(b, p)
	}
	r.opts.Journal.setSync(b, func(jp *SyncParticipant) {
		jp.Reason, jp.After, jp.SafetyRef = strp(why), strp(now), strp(safety)
		if restored {
			jp.Result = ResultRestored
			return
		}
		jp.Result, jp.UndoCommand = ResultNeedsRecovery, nil
		jp.Recovery = strp(wtsync.WayOut(wtsync.Way{Work: p.work, Path: p.wt.Path, Rebasing: busy,
			Safety: wtsync.SafetyRef(b, r.epoch)}))
	})
}
