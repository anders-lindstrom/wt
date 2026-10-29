package retire

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/supersetws"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/schema"
)

// errRemovePlanChanged is --expect refusing a removal whose plan is not the
// one the token was made from.
var errRemovePlanChanged = errors.New("the removal plan changed since it was read (the branch, HEAD, " +
	"trunk, the checkout, its lock, sessions or an operation in it, or where it goes); " +
	"nothing was removed: read the plan again")

// What the branch stands as against trunk, for --json.
var mergeNames = map[MergeState]string{MergeUnknown: "unknown", Merged: "merged", Unmerged: "unmerged", Applied: "applied"}

// The branch outcomes, as recovery.json names them.
var outcomeNames = map[BranchOutcome]string{BranchUntouched: quarantine.BranchPlanNone,
	BranchDeleted: quarantine.BranchPlanDelete, BranchKept: quarantine.BranchPlanKeep}

// RemoveTarget names the worktree to remove: Arg as wt remove takes it, or
// Path for one already resolved (., --me-at).
type RemoveTarget struct {
	Arg  string
	Path string
}

func (t RemoveTarget) locate(ctx *kit.Context) (repo.Worktree, error) {
	if t.Path != "" {
		return worktreeRecord(ctx, t.Path), nil
	}
	return kit.Locate(ctx, t.Arg)
}

// RemoveBranchPlan is the branch checked out in the worktree, for --json.
type RemoveBranchPlan struct {
	Name        string  `json:"name"`
	Tip         *string `json:"tip"`
	Merge       string  `json:"merge"`
	Base        *string `json:"base"`
	Ahead       int     `json:"ahead"`
	PullRequest *int    `json:"pullRequest"`
	Outcome     string  `json:"outcome"`
	KeepAs      *string `json:"keepAs"`
	Reason      *string `json:"reason"`
}

// RemoveSession is an agent session working in the checkout; Kind is
// "claude" or "codex".
type RemoveSession struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
	Kind  string  `json:"kind"`
	PID   *int    `json:"pid"`
	State string  `json:"state"`
}

// RemoveLock is git's lock on the checkout.
type RemoveLock struct {
	Reason *string `json:"reason"`
	Holder string  `json:"holder"`
	PID    *int    `json:"pid"`
	Held   bool    `json:"held"`
}

// RemoveLost is a tip the removal would leave nothing holding.
type RemoveLost struct {
	Kind           string   `json:"kind"`
	OID            string   `json:"oid"`
	Count          int      `json:"count"`
	Path           *string  `json:"path"`
	RestoreCommand []string `json:"restoreCommand"`
}

// RemoveProblem is one reason the removal does not go ahead. Force is a
// problem --force goes past, ForceWith the category that does.
type RemoveProblem struct {
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	Force     bool    `json:"force"`
	ForceWith *string `json:"forceWith"`
}

// RemoveProcess is a process working in the checkout. Reap says the
// removal ends it; without --reap it is left running.
type RemoveProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Reap    bool   `json:"reap"`
}

// ReapedProcess is a process --reap ended, or tried to, and what that came
// to: one of the agents.Reap* results.
type ReapedProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Result  string `json:"result"`
}

// RemovePlanOutput is the one object wt remove --dry-run --json prints.
type RemovePlanOutput struct {
	Schema          int               `json:"schema"`
	SchemaVersion   string            `json:"schemaVersion"`
	Command         string            `json:"command"`
	Repo            *string           `json:"repo"`
	Trunk           *string           `json:"trunk"`
	TrunkSource     *string           `json:"trunkSource"`
	Token           *string           `json:"token"`
	Error           *string           `json:"error"`
	Path            *string           `json:"path"`
	AdminDir        *string           `json:"adminDir"`
	Head            *string           `json:"head"`
	Detached        bool              `json:"detached"`
	Branch          *RemoveBranchPlan `json:"branch"`
	Bases           []SweepBase       `json:"bases"`
	Dirty           bool              `json:"dirty"`
	StatusError     *string           `json:"statusError"`
	HiddenFiles     []string          `json:"hiddenFiles"`
	MovedSubmodules []string          `json:"movedSubmodules"`
	HasSubmodules   bool              `json:"hasSubmodules"`
	NestedWorktrees []string          `json:"nestedWorktrees"`
	Operation       *string           `json:"operation"`
	Sessions        []RemoveSession   `json:"sessions"`
	SessionsError   *string           `json:"sessionsError"`
	Lock            *RemoveLock       `json:"lock"`
	Unreachable     []RemoveLost      `json:"unreachable"`
	ReachError      *string           `json:"reachError"`
	Quarantine      *string           `json:"quarantine"`
	KeepSuperset    bool              `json:"keepSuperset"`
	Processes       []RemoveProcess   `json:"processes"`
	ProcessesError  *string           `json:"processesError"`
	Force           bool              `json:"force"`
	ForceWith       []string          `json:"forceWith"`
	Problems        []RemoveProblem   `json:"problems"`
}

// RemoveStep is one effect of the removal, as the result reports it.
type RemoveStep struct {
	Step   string  `json:"step"`
	Result string  `json:"result"`
	Commit *string `json:"commit"`
	Reason *string `json:"reason"`
	// Pin is where the branch step moved the branch, read back; nil for
	// the other steps.
	Pin *string `json:"pin"`
}

// RemoveQuarantine is the folder a quarantine moved the worktree into, and
// how far it got, read from the folder and its recovery.json.
type RemoveQuarantine struct {
	Dir           string            `json:"dir"`
	CheckoutMoved bool              `json:"checkoutMoved"`
	AdminMoved    bool              `json:"adminMoved"`
	RecoveryFile  string            `json:"recoveryFile"`
	Steps         []quarantine.Step `json:"steps"`
}

// RemoveOutput is the one object wt remove --yes --json prints.
type RemoveOutput struct {
	Schema         int               `json:"schema"`
	SchemaVersion  string            `json:"schemaVersion"`
	Command        string            `json:"command"`
	Repo           *string           `json:"repo"`
	Trunk          *string           `json:"trunk"`
	TrunkSource    *string           `json:"trunkSource"`
	Token          *string           `json:"token"`
	Outcome        string            `json:"outcome"`
	Error          *string           `json:"error"`
	Problems       []RemoveProblem   `json:"problems"`
	Path           *string           `json:"path"`
	Branch         *string           `json:"branch"`
	Tip            *string           `json:"tip"`
	KeepAs         *string           `json:"keepAs"`
	Steps          []RemoveStep      `json:"steps"`
	Forced         []string          `json:"forced"`
	Quarantine     *RemoveQuarantine `json:"quarantine"`
	RestoreCommand []string          `json:"restoreCommand"`
	Recovery       *string           `json:"recovery"`
	RunID          *string           `json:"runId"`
	Reaped         []ReapedProcess   `json:"reaped"`
}

// removeToken names a plan: the repository, trunk and the bases' commits,
// the configuration, and everything the removal turns on — the checkout,
// its admin dir, HEAD, the branch, its tip, standing and outcome, anything
// uncommitted or hidden, submodules, nested worktrees, the operation, the
// sessions by who they are and whether each is idle or busy, the lock, what
// would be lost, the quarantine folder and the --force categories. Nil when
// the removal would refuse.
func removeToken(ctx *kit.Context, p Plan) *string { return removeTokenOf(ctx, p).Strp() }

func removeTokenOf(ctx *kit.Context, p Plan) *jsonout.Token {
	if p.refusal() != "" {
		return nil
	}
	return hashRemove(jsonout.NewToken("1:", "wt-remove"), ctx, p)
}

// hashRemove hashes everything a removal turns on.
func hashRemove(t *jsonout.Token, ctx *kit.Context, p Plan) *jsonout.Token {
	t.Str("repo", ctx.Repo.MainRoot).Str("trunk", ctx.Config.MainBranch).
		Str("trunkSource", string(ctx.Config.TrunkSource)).Config(kit.ConfigFingerprint(ctx))
	bases, err := ctx.Repo.TrunkBases(ctx.Config.MainBranch)
	t.Bool("basesError", err != nil)
	for _, b := range bases {
		t.Str("bases.Name", b.Name).Str("bases.Tip", b.Tip)
	}
	t.Str("Path", p.Path).Str("AdminDir", p.AdminDir).Str("Head", p.Head).Str("Branch", p.Branch).Str("Tip", p.Tip).
		Str("Outcome", outcomeNames[p.Outcome]).Str("KeepAs", p.KeepAs)
	if p.KeepAs != "" {
		// A name taken meanwhile turns the rename into a failure.
		taken, _, err := ctx.Repo.ResolveRef("refs/heads/" + p.KeepAs)
		t.Str("keepAsTaken", taken).Bool("keepAsTakenError", err != nil)
	}
	t.Str("Reason", p.Reason).Str("Merge", mergeNames[p.Merge]).Int("Ahead", int64(p.Ahead)).Str("Base", p.Base).
		Int("MergedPR", int64(p.MergedPR)).Bool("Dirty", p.Dirty).Str("StatusError", p.StatusError).
		List("Hidden", p.Hidden...).List("Moved", p.Moved...).List("Nested", p.Nested...).Str("Operation", p.Operation)
	var ids []string
	for _, a := range p.Sessions {
		ids = append(ids, a.ID+" "+sessionState(a))
	}
	slices.Sort(ids)
	t.List("Sessions", ids...).Str("SessionsError", p.SessionsError).
		Bool("Locked", p.Locked).Bool("LockHeld", p.LockHeld).Int("LockPid", int64(p.LockPid)).Str("LockReason", p.LockReason)
	for _, l := range p.Unreachable {
		t.Str("Unreachable.Kind", l.Kind).Str("Unreachable.OID", l.OID).Int("Unreachable.Count", int64(l.Count)).
			Str("Unreachable.Path", l.Path)
	}
	t.Str("ReachError", p.ReachError).Str("Quarantine", p.Quarantine).List("Force", p.Force.Names()...).
		Bool("superset", supersetws.Enabled(ctx)).Bool("KeepSuperset", p.KeepSuperset)
	// The processes it ends, when it ends any: a tool that read the plan
	// agreed to those. Without --reap they are left alone and do not count.
	t.Bool("Reap", p.Reap)
	if p.Reap {
		for _, pr := range p.Processes {
			t.Int("Processes.PID", int64(pr.PID)).Str("Processes.Command", pr.Command())
		}
	}
	return t
}

// problemsOf is every problem with the plan, --force or not.
func problemsOf(p Plan) []RemoveProblem {
	return removeProblems(p.problems())
}

// blockingOf is the problems that refuse this removal.
func blockingOf(p Plan) []RemoveProblem {
	return removeProblems(p.blocking())
}

func removeProblems(problems []problem) []RemoveProblem {
	out := []RemoveProblem{}
	for _, pr := range problems {
		rp := RemoveProblem{Code: pr.code, Message: pr.text, Force: pr.force != 0}
		if pr.force != 0 {
			rp.ForceWith = jsonout.Strp(pr.force.Names()[0])
		}
		out = append(out, rp)
	}
	return out
}

// sessionState is a session's state as --json names it.
func sessionState(a agents.Agent) string {
	if a.Idle() {
		return "idle"
	}
	return "busy"
}

// restoreBranch is the command that puts a deleted branch back at tip.
func restoreBranch(ctx *kit.Context, branch, tip string) []string {
	return []string{"git", "-C", ctx.Repo.MainRoot, "branch", branch, tip}
}

func removePlanOutput(ctx *kit.Context, p Plan, token string) RemovePlanOutput {
	o := emptyRemovePlan()
	o.Repo, o.Trunk, o.TrunkSource, o.Token = jsonout.Strp(ctx.Repo.MainRoot), jsonout.Strp(ctx.Config.MainBranch), ctx.TrunkSource(), jsonout.Strp(token)
	o.Path, o.AdminDir, o.Head, o.Detached = jsonout.Strp(p.Path), jsonout.Strp(p.AdminDir), jsonout.Strp(p.Head), p.Branch == ""
	if p.Branch != "" {
		b := &RemoveBranchPlan{Name: p.Branch, Tip: jsonout.Strp(p.Tip), Merge: mergeNames[p.Merge], Base: jsonout.Strp(p.Base),
			Ahead: p.Ahead, Outcome: outcomeNames[p.Outcome], KeepAs: jsonout.Strp(p.KeepAs)}
		if p.MergedPR > 0 {
			n := p.MergedPR
			b.PullRequest = &n
		}
		if p.Outcome == BranchUntouched {
			b.Reason = jsonout.Strp(p.Reason)
		}
		o.Branch = b
	}
	if bases, err := ctx.Repo.TrunkBases(ctx.Config.MainBranch); err == nil {
		for _, b := range bases {
			o.Bases = append(o.Bases, SweepBase(b))
		}
	}
	o.Dirty, o.StatusError = p.Dirty, jsonout.Strp(p.StatusError)
	o.HiddenFiles = append(o.HiddenFiles, p.Hidden...)
	o.MovedSubmodules = append(o.MovedSubmodules, p.Moved...)
	o.NestedWorktrees = append(o.NestedWorktrees, p.Nested...)
	if _, err := os.Lstat(filepath.Join(p.Path, ".gitmodules")); err == nil {
		o.HasSubmodules = true
	}
	o.Operation = jsonout.Strp(operationName(p.Operation))
	for _, a := range p.Sessions {
		s := RemoveSession{ID: a.ID, Name: jsonout.Strp(a.Name), Kind: a.Program(), State: sessionState(a)}
		if a.PID > 0 {
			pid := a.PID
			s.PID = &pid
		}
		o.Sessions = append(o.Sessions, s)
	}
	o.SessionsError = jsonout.Strp(p.SessionsError)
	if p.Locked {
		l := &RemoveLock{Reason: jsonout.Strp(p.LockReason), Holder: p.LockHolder, Held: p.LockHeld}
		if p.LockPid > 0 {
			pid := p.LockPid
			l.PID = &pid
		}
		o.Lock = l
	}
	for _, l := range p.Unreachable {
		lost := RemoveLost{Kind: l.Kind, OID: l.OID, Count: l.Count, Path: jsonout.Strp(l.Path)}
		if l.Kind == LostBranch {
			lost.RestoreCommand = restoreBranch(ctx, p.Branch, l.OID)
		}
		o.Unreachable = append(o.Unreachable, lost)
	}
	o.ReachError, o.Quarantine = jsonout.Strp(p.ReachError), jsonout.Strp(p.Quarantine)
	// force keeps its v1 meaning — every problem with force true is gone
	// past — which only bare --force promises.
	o.Force, o.ForceWith = p.Force == kit.ForceAll, p.Force.Names()
	o.KeepSuperset = p.KeepSuperset
	o.Processes, o.ProcessesError = removeProcesses(p.Processes, p.Reap), jsonout.Strp(p.ProcessesError)
	o.Problems = problemsOf(p)
	return o
}

// removeProcesses is processes as --json lists them.
func removeProcesses(procs []agents.Process, reap bool) []RemoveProcess {
	out := []RemoveProcess{}
	for _, p := range procs {
		out = append(out, RemoveProcess{PID: p.PID, Command: p.Command(), Reap: reap})
	}
	return out
}

// reapedProcesses is what --reap did, as --json reports it.
func reapedProcesses(reaped []agents.Reaped) []ReapedProcess {
	out := []ReapedProcess{}
	for _, r := range reaped {
		out = append(out, ReapedProcess{PID: r.PID, Command: r.Command(), Result: r.Result})
	}
	return out
}

func emptyRemovePlan() RemovePlanOutput {
	return RemovePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("remove-plan"), Command: "remove",
		Bases: []SweepBase{}, HiddenFiles: []string{}, MovedSubmodules: []string{}, NestedWorktrees: []string{},
		Sessions: []RemoveSession{}, Unreachable: []RemoveLost{}, Processes: []RemoveProcess{}, ForceWith: []string{},
		Problems: []RemoveProblem{}}
}

// RemovePlanJSON writes wt remove --dry-run --json: the plan, its problems
// and the token that holds a later removal to it. It changes nothing; the
// plan as wt prints it goes to progress. A plan the removal would refuse, or
// none at all, is still one object, with error set, and the error is
// returned too.
func RemovePlanJSON(ctx *kit.Context, t RemoveTarget, opts RemoveOptions, out, progress io.Writer) error {
	j := newRemoveJournal(nil, ctx)
	opts.DryRun, opts.Confirm, opts.Journal, opts.Expect, opts.Result = true, nil, j, "", nil
	wt, err := t.locate(ctx)
	if err == nil {
		err = removeWorktree(ctx, wt, opts, progress)
	}
	o := emptyRemovePlan()
	if j.Res.plan != nil {
		o = removePlanOutput(ctx, *j.Res.plan, j.Res.token)
	} else {
		o.Repo, o.Trunk, o.TrunkSource = jsonout.Strp(ctx.Repo.MainRoot), jsonout.Strp(ctx.Config.MainBranch), ctx.TrunkSource()
	}
	if err != nil {
		o.Error, o.Token = jsonout.Strp(err.Error()), nil
	}
	if werr := jsonout.WriteJSON(out, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RemoveFailedJSON writes the one object for a removal that could not even
// open the repository: the plan's with dryRun, the result's otherwise.
func RemoveFailedJSON(out io.Writer, dryRun bool, err error) {
	if dryRun {
		o := emptyRemovePlan()
		o.Error = jsonout.Strp(err.Error())
		_ = jsonout.WriteJSON(out, o)
		return
	}
	newRemoveJournal(out, nil).finish(RemoveResult{}, err)
}

// RemoveJSON is wt remove --yes --json: the removal, and one object on out
// saying what it did, effect by effect — when it returns, or when a signal
// ends it. The plan as wt prints it and the progress go to progress.
func RemoveJSON(ctx *kit.Context, t RemoveTarget, opts RemoveOptions, out, progress io.Writer) error {
	j := newRemoveJournal(out, ctx)
	defer kit.SetInterruptJournal(j)()
	defer kit.WatchSignals(progress, nil)()
	var res RemoveResult
	opts.Confirm, opts.DryRun, opts.Journal, opts.Result = nil, false, j, &res
	wt, err := t.locate(ctx)
	if err == nil {
		err = removeWorktree(ctx, wt, opts, progress)
	}
	j.finish(res, err)
	return err
}

// RemoveJournal holds what a removal planned and how far it got, so that
// one object is written at the end — by the removal when it returns, or by
// the signal handler when it does not. The object is written once. A nil
// journal records nothing.
type RemoveJournal struct {
	*jsonout.Journal[removeRecord]
}

// removeRecord is what a removal's journal holds: the plan and its token,
// how far the removal got, and what it returned.
type removeRecord struct {
	ctx     *kit.Context
	plan    *Plan
	token   string
	started bool
	runID   string
	res     *RemoveResult
	err     error
}

// newRemoveJournal is a journal that writes its object to out; nil out
// writes nothing, for the plan. The object is read back from the disk
// outside the lock: a signal parks the git a returning run is reading
// with, and the handler must still be able to write.
func newRemoveJournal(out io.Writer, ctx *kit.Context) *RemoveJournal {
	return &RemoveJournal{jsonout.NewJournal(out, removeRecord{ctx: ctx}, jsonout.JournalRules[removeRecord]{
		Snapshot: func(r removeRecord, signalled bool, recovery string) any {
			var plan *Plan
			if r.plan != nil {
				p := *r.plan
				plan = &p
			}
			res, err, runID := r.res, r.err, r.runID
			switch {
			case signalled:
				// What is true of the worktree and the branch now.
				res, err = nil, nil
			case res != nil:
				// What the removal returned is what is left: a run that
				// pinned nothing was dropped.
				runID = res.RunID
			}
			return removeOutput(r.ctx, plan, r.token, runID, r.started, res, err, recovery)
		}})}
}

// planned records the plan the removal made, and its token.
func (j *RemoveJournal) planned(ctx *kit.Context, p Plan, token string) {
	if j == nil {
		return
	}
	j.Edit(func(r *removeRecord) { r.ctx, r.plan, r.token = ctx, &p, token })
}

// applying marks the point after which the removal changes things.
func (j *RemoveJournal) applying() {
	if j == nil {
		return
	}
	j.Edit(func(r *removeRecord) { r.started = true })
}

// ran records the run the branch is about to be moved into.
func (j *RemoveJournal) ran(runID string) {
	if j == nil {
		return
	}
	j.Edit(func(r *removeRecord) { r.runID = runID })
}

// finish writes the object for a removal that returned.
func (j *RemoveJournal) finish(res RemoveResult, err error) {
	if j == nil {
		return
	}
	j.Edit(func(r *removeRecord) { r.res, r.err = &res, err })
	j.Finish()
}

// Interrupted writes the object for a removal a signal ended, from what is
// true of the worktree and the branch now.
func (j *RemoveJournal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

// removeOutput is the result object. res is what the removal returned, nil
// for one a signal ended; where the worktree and the quarantine are is read
// from the disk either way, and the branch too for an interrupted run.
func removeOutput(ctx *kit.Context, plan *Plan, token, runID string, started bool, res *RemoveResult, err error,
	recovery string) RemoveOutput {
	o := RemoveOutput{Schema: 1, SchemaVersion: schema.VersionOf("remove"), Command: "remove",
		Token: jsonout.Strp(token), Problems: []RemoveProblem{}, Forced: []string{}, Recovery: jsonout.Strp(recovery),
		Reaped: []ReapedProcess{}}
	if res != nil {
		o.Reaped = reapedProcesses(res.Reaped)
	}
	if err != nil {
		o.Error = jsonout.Strp(err.Error())
	}
	if ctx != nil {
		o.Repo, o.Trunk, o.TrunkSource = jsonout.Strp(ctx.Repo.MainRoot), jsonout.Strp(ctx.Config.MainBranch), ctx.TrunkSource()
	}
	notRun := func(name, commit string) RemoveStep {
		return RemoveStep{Step: name, Result: jsonout.StepNotRun, Commit: jsonout.Strp(commit)}
	}
	if plan == nil || !started {
		o.Outcome = RemoveRefused
		if res == nil {
			o.Outcome = jsonout.OutcomeInterrupted
		}
		tip := ""
		if plan != nil {
			o.Path, o.Branch, o.Tip, o.KeepAs = jsonout.Strp(plan.Path), jsonout.Strp(plan.Branch), jsonout.Strp(plan.Tip), jsonout.Strp(plan.KeepAs)
			tip = plan.Tip
			if res != nil {
				o.Problems = blockingOf(*plan)
			}
		}
		if errors.Is(err, errRemovePlanChanged) {
			o.Problems = append(o.Problems, RemoveProblem{Code: jsonout.ProblemPlanChanged, Message: err.Error()})
		}
		o.Steps = []RemoveStep{notRun(jsonout.StepWorktree, ""), notRun(jsonout.StepBranch, tip), notRun(jsonout.StepSuperset, "")}
		return o
	}

	o.Path, o.Branch, o.Tip, o.KeepAs = jsonout.Strp(plan.Path), jsonout.Strp(plan.Branch), jsonout.Strp(plan.Tip), jsonout.Strp(plan.KeepAs)
	worktree := WorktreeKept
	if dir := plan.Quarantine; dir != "" {
		q := &RemoveQuarantine{Dir: dir, RecoveryFile: filepath.Join(dir, quarantine.FileName), Steps: []quarantine.Step{}}
		// A move is its directory, by identity, in the quarantine: not
		// whatever else is at that name. The record is written after the
		// last check, so from there on a quarantine has changed something —
		// a lock, pins — that wt restore takes back, moved or not.
		if r, lerr := quarantine.Load(dir); lerr == nil {
			q.Steps = append(q.Steps, r.Steps...)
			q.CheckoutMoved = quarantine.Locate(r.Checkout) == quarantine.InQuarantine
			q.AdminMoved = quarantine.Locate(r.Admin) == quarantine.InQuarantine
			o.Quarantine = q
			o.RestoreCommand = []string{"wt", "restore", dir}
			if res != nil && res.Outcome == RemoveRefused {
				res.Outcome = RemovePartial
			}
		}
		switch {
		case q.CheckoutMoved && q.AdminMoved:
			worktree = WorktreeQuarantined
		case q.CheckoutMoved:
			worktree = WorktreePartlyMoved
		}
	} else if worktreeGone(ctx, plan.Path) {
		worktree = WorktreeRemoved
	}
	gone := worktree == WorktreeRemoved || worktree == WorktreeQuarantined

	// A branch git cannot read is not called gone, but its pin is still
	// looked for: if it is gone, the pin is where its tip is held.
	branchGone, branchUnread := false, false
	if plan.Branch != "" && plan.Tip != "" {
		_, ok, err := ctx.Repo.ResolveRef("refs/heads/" + plan.Branch)
		branchGone, branchUnread = !ok && err == nil, err != nil
	}
	o.RunID = jsonout.Strp(runID)
	pin := ""
	if branchGone || branchUnread {
		pin = refpins.PinHolding(ctx.Repo.MainRoot, runID, plan.Branch, plan.Tip)
	}
	switch {
	case o.RestoreCommand != nil:
	case pin != "":
		o.RestoreCommand = refpins.RestoreFromRun(runID, plan.Branch)
	case branchGone:
		o.RestoreCommand = restoreBranch(ctx, plan.Branch, plan.Tip)
	}

	wstep := RemoveStep{Step: jsonout.StepWorktree, Result: worktree}
	bstep := RemoveStep{Step: jsonout.StepBranch, Commit: jsonout.Strp(plan.Tip), Pin: jsonout.Strp(pin)}
	sstep := RemoveStep{Step: jsonout.StepSuperset, Result: jsonout.StepNotRun}
	if res != nil {
		o.Outcome = res.Outcome
		if o.Outcome == "" {
			o.Outcome = RemoveRefused
		}
		if !gone && err != nil {
			wstep.Reason = jsonout.Strp(kit.OneLine(err.Error()))
		}
		bstep.Result = res.Branch
		if bstep.Result == "" {
			bstep.Result = jsonout.StepNotRun
		} else if (res.Branch == quarantine.BranchKept || res.Branch == quarantine.BranchFailed) && err != nil {
			bstep.Reason = jsonout.Strp(kit.OneLine(err.Error()))
		}
		if res.Superset != "" {
			sstep.Result, sstep.Reason = res.Superset, jsonout.Strp(res.SupersetReason)
		}
		o.Forced = res.Forced.Names()
	} else {
		o.Outcome = jsonout.OutcomeInterrupted
		if worktree == WorktreeKept {
			wstep.Result = jsonout.StepInterrupted
		}
		bstep.Result = interruptedBranch(ctx, *plan, gone, branchGone)
		// Superset follows the branch step whatever it came to, so once
		// the worktree is gone it may have begun.
		if gone {
			sstep.Result = jsonout.StepInterrupted
		}
	}
	o.Steps = []RemoveStep{wstep, bstep, sstep}
	return o
}

// operationName is an operation as --json names it: a sequencer left
// without its HEAD marker is "sequencer".
func operationName(op string) string {
	if op == "cherry-pick or revert" {
		return "sequencer"
	}
	return op
}

// interruptedBranch is what became of the branch in a removal a signal
// ended, read from the repository: not run while the worktree is still
// there, the branch's state once it is gone, interrupted when that state
// does not say the step finished.
func interruptedBranch(ctx *kit.Context, p Plan, worktreeGone, branchGone bool) string {
	switch {
	case !worktreeGone:
		return jsonout.StepNotRun
	case p.Outcome == BranchUntouched:
		return quarantine.BranchUntouched
	case p.Outcome == BranchDeleted && branchGone:
		return quarantine.BranchDeleted
	case p.Outcome == BranchKept && branchGone:
		// A name git cannot read leaves the step interrupted, which claims nothing.
		if _, ok, _ := ctx.Repo.ResolveRef("refs/heads/" + p.KeepAs); ok {
			return quarantine.BranchRenamed
		}
	}
	return jsonout.StepInterrupted
}
