package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
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

func (t RemoveTarget) locate(ctx *Context) (repo.Worktree, error) {
	if t.Path != "" {
		return worktreeRecord(ctx, t.Path), nil
	}
	return Locate(ctx, t.Arg)
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

// RemoveSession is a Claude session working in the checkout.
type RemoveSession struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
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

// RemovePlanOutput is the one object wt remove --dry-run --json prints.
type RemovePlanOutput struct {
	Schema          int               `json:"schema"`
	SchemaVersion   string            `json:"schemaVersion"`
	Command         string            `json:"command"`
	Repo            *string           `json:"repo"`
	Trunk           *string           `json:"trunk"`
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
}

// removeToken names a plan: the repository, trunk and the bases' commits,
// the configuration, and everything the removal turns on — the checkout,
// its admin dir, HEAD, the branch, its tip, standing and outcome, anything
// uncommitted or hidden, submodules, nested worktrees, the operation, the
// sessions by who they are and whether each is idle or busy, the lock, what
// would be lost, the quarantine folder and the --force categories. Nil when
// the removal would refuse.
func removeToken(ctx *Context, p Plan) *string {
	if p.refusal() != "" {
		return nil
	}
	h := sha256.New()
	field := func(name string, v any) { fmt.Fprintf(h, "%s=%v\x00", name, v) }
	field("wt-remove", 1)
	field("repo", ctx.Repo.MainRoot)
	field("trunk", ctx.Config.MainBranch)
	h.Write(configFingerprint(ctx))
	bases, err := trunkBases(ctx)
	field("basesError", err != nil)
	for _, b := range bases {
		field("base", b.Name+" "+b.Tip)
	}
	field("path", p.Path)
	field("adminDir", p.AdminDir)
	field("head", p.Head)
	field("branch", p.Branch)
	field("tip", p.Tip)
	field("outcome", outcomeNames[p.Outcome])
	field("keepAs", p.KeepAs)
	if p.KeepAs != "" {
		// A name taken meanwhile turns the rename into a failure.
		taken, _ := ctx.Repo.ResolveRef("refs/heads/" + p.KeepAs)
		field("keepAsTaken", taken)
	}
	field("reason", p.Reason)
	field("merge", mergeNames[p.Merge])
	field("ahead", p.Ahead)
	field("base", p.Base)
	field("mergedPR", p.MergedPR)
	field("dirty", p.Dirty)
	field("statusError", p.StatusError)
	field("hidden", strings.Join(p.Hidden, "\x01"))
	field("moved", strings.Join(p.Moved, "\x01"))
	field("nested", strings.Join(p.Nested, "\x01"))
	field("operation", p.Operation)
	var ids []string
	for _, a := range p.Sessions {
		ids = append(ids, a.ID+" "+sessionState(a))
	}
	slices.Sort(ids)
	field("sessions", strings.Join(ids, "\x01"))
	field("sessionsError", p.SessionsError)
	field("lock", fmt.Sprint(p.Locked, p.LockHeld, p.LockPid, p.LockReason))
	for _, l := range p.Unreachable {
		field("lost", fmt.Sprint(l.Kind, l.OID, l.Count, l.Path))
	}
	field("reachError", p.ReachError)
	field("quarantine", p.Quarantine)
	field("force", strings.Join(p.Force.Names(), ","))
	field("superset", supersetEnabled(ctx))
	field("keepSuperset", p.KeepSuperset)
	token := "1:" + hex.EncodeToString(h.Sum(nil))[:32]
	return &token
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
			rp.ForceWith = strp(pr.force.Names()[0])
		}
		out = append(out, rp)
	}
	return out
}

// sessionState is a session's state as --json names it.
func sessionState(a wtsync.Agent) string {
	if a.Idle() {
		return "idle"
	}
	return "busy"
}

// restoreBranch is the command that puts a deleted branch back at tip.
func restoreBranch(ctx *Context, branch, tip string) []string {
	return []string{"git", "-C", ctx.Repo.MainRoot, "branch", branch, tip}
}

func removePlanOutput(ctx *Context, p Plan, token string) RemovePlanOutput {
	o := emptyRemovePlan()
	o.Repo, o.Trunk, o.Token = strp(ctx.Repo.MainRoot), strp(ctx.Config.MainBranch), strp(token)
	o.Path, o.AdminDir, o.Head, o.Detached = strp(p.Path), strp(p.AdminDir), strp(p.Head), p.Branch == ""
	if p.Branch != "" {
		b := &RemoveBranchPlan{Name: p.Branch, Tip: strp(p.Tip), Merge: mergeNames[p.Merge], Base: strp(p.Base),
			Ahead: p.Ahead, Outcome: outcomeNames[p.Outcome], KeepAs: strp(p.KeepAs)}
		if p.MergedPR > 0 {
			n := p.MergedPR
			b.PullRequest = &n
		}
		if p.Outcome == BranchUntouched {
			b.Reason = strp(p.Reason)
		}
		o.Branch = b
	}
	if bases, err := trunkBases(ctx); err == nil {
		for _, b := range bases {
			o.Bases = append(o.Bases, SweepBase(b))
		}
	}
	o.Dirty, o.StatusError = p.Dirty, strp(p.StatusError)
	o.HiddenFiles = append(o.HiddenFiles, p.Hidden...)
	o.MovedSubmodules = append(o.MovedSubmodules, p.Moved...)
	o.NestedWorktrees = append(o.NestedWorktrees, p.Nested...)
	if _, err := os.Lstat(filepath.Join(p.Path, ".gitmodules")); err == nil {
		o.HasSubmodules = true
	}
	o.Operation = strp(operationName(p.Operation))
	for _, a := range p.Sessions {
		s := RemoveSession{ID: a.ID, Name: strp(a.Name), State: sessionState(a)}
		if a.PID > 0 {
			pid := a.PID
			s.PID = &pid
		}
		o.Sessions = append(o.Sessions, s)
	}
	o.SessionsError = strp(p.SessionsError)
	if p.Locked {
		l := &RemoveLock{Reason: strp(p.LockReason), Holder: p.LockHolder, Held: p.LockHeld}
		if p.LockPid > 0 {
			pid := p.LockPid
			l.PID = &pid
		}
		o.Lock = l
	}
	for _, l := range p.Unreachable {
		lost := RemoveLost{Kind: l.Kind, OID: l.OID, Count: l.Count, Path: strp(l.Path)}
		if l.Kind == LostBranch {
			lost.RestoreCommand = restoreBranch(ctx, p.Branch, l.OID)
		}
		o.Unreachable = append(o.Unreachable, lost)
	}
	o.ReachError, o.Quarantine = strp(p.ReachError), strp(p.Quarantine)
	// force keeps its v1 meaning — every problem with force true is gone
	// past — which only bare --force promises.
	o.Force, o.ForceWith = p.Force == ForceAll, p.Force.Names()
	o.KeepSuperset = p.KeepSuperset
	o.Problems = problemsOf(p)
	return o
}

func emptyRemovePlan() RemovePlanOutput {
	return RemovePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("remove-plan"), Command: "remove",
		Bases: []SweepBase{}, HiddenFiles: []string{}, MovedSubmodules: []string{}, NestedWorktrees: []string{},
		Sessions: []RemoveSession{}, Unreachable: []RemoveLost{}, ForceWith: []string{}, Problems: []RemoveProblem{}}
}

// RemovePlanJSON writes wt remove --dry-run --json: the plan, its problems
// and the token that holds a later removal to it. It changes nothing; the
// plan as wt prints it goes to progress. A plan the removal would refuse, or
// none at all, is still one object, with error set, and the error is
// returned too.
func RemovePlanJSON(ctx *Context, t RemoveTarget, opts RemoveOptions, out, progress io.Writer) error {
	j := &RemoveJournal{}
	opts.DryRun, opts.Confirm, opts.Journal, opts.Expect, opts.Result = true, nil, j, "", nil
	wt, err := t.locate(ctx)
	if err == nil {
		err = removeWorktree(ctx, wt, opts, progress)
	}
	o := emptyRemovePlan()
	if j.plan != nil {
		o = removePlanOutput(ctx, *j.plan, j.token)
	} else {
		o.Repo, o.Trunk = strp(ctx.Repo.MainRoot), strp(ctx.Config.MainBranch)
	}
	if err != nil {
		o.Error, o.Token = strp(err.Error()), nil
	}
	if werr := writeJSON(out, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RemoveFailedJSON writes the one object for a removal that could not even
// open the repository: the plan's with dryRun, the result's otherwise.
func RemoveFailedJSON(out io.Writer, dryRun bool, err error) {
	if dryRun {
		o := emptyRemovePlan()
		o.Error = strp(err.Error())
		_ = writeJSON(out, o)
		return
	}
	(&RemoveJournal{out: out}).finish(RemoveResult{}, err)
}

// RemoveJSON is wt remove --yes --json: the removal, and one object on out
// saying what it did, effect by effect — when it returns, or when a signal
// ends it. The plan as wt prints it and the progress go to progress.
func RemoveJSON(ctx *Context, t RemoveTarget, opts RemoveOptions, out, progress io.Writer) error {
	j := &RemoveJournal{out: out, ctx: ctx}
	defer setInterruptJournal(j)()
	defer watchSignals(progress, nil)()
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
	mu      sync.Mutex
	once    sync.Once
	out     io.Writer
	ctx     *Context
	plan    *Plan
	token   string
	started bool
}

// planned records the plan the removal made, and its token.
func (j *RemoveJournal) planned(ctx *Context, p Plan, token string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ctx, j.plan, j.token = ctx, &p, token
}

// applying marks the point after which the removal changes things.
func (j *RemoveJournal) applying() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.started = true
}

// finish writes the object for a removal that returned.
func (j *RemoveJournal) finish(res RemoveResult, err error) {
	j.write(&res, err, "")
}

// interrupted writes the object for a removal a signal ended, from what is
// true of the worktree and the branch now.
func (j *RemoveJournal) interrupted(_ string, recovery string) {
	j.write(nil, nil, recovery)
}

func (j *RemoveJournal) write(res *RemoveResult, err error, recovery string) {
	if j == nil || j.out == nil {
		return
	}
	j.mu.Lock()
	ctx, started, token := j.ctx, j.started, j.token
	var plan *Plan
	if j.plan != nil {
		p := *j.plan
		plan = &p
	}
	j.mu.Unlock()
	// The object is read outside once: a signal parks the git a returning
	// run is reading with, and the handler must still be able to write.
	o := removeOutput(ctx, plan, token, started, res, err, recovery)
	j.once.Do(func() { _ = writeJSON(j.out, o) })
}

// removeOutput is the result object. res is what the removal returned, nil
// for one a signal ended; where the worktree and the quarantine are is read
// from the disk either way, and the branch too for an interrupted run.
func removeOutput(ctx *Context, plan *Plan, token string, started bool, res *RemoveResult, err error,
	recovery string) RemoveOutput {
	o := RemoveOutput{Schema: 1, SchemaVersion: schema.VersionOf("remove"), Command: "remove",
		Token: strp(token), Problems: []RemoveProblem{}, Forced: []string{}, Recovery: strp(recovery)}
	if err != nil {
		o.Error = strp(err.Error())
	}
	if ctx != nil {
		o.Repo, o.Trunk = strp(ctx.Repo.MainRoot), strp(ctx.Config.MainBranch)
	}
	notRun := func(name, commit string) RemoveStep {
		return RemoveStep{Step: name, Result: StepNotRun, Commit: strp(commit)}
	}
	if plan == nil || !started {
		o.Outcome = RemoveRefused
		if res == nil {
			o.Outcome = OutcomeInterrupted
		}
		tip := ""
		if plan != nil {
			o.Path, o.Branch, o.Tip, o.KeepAs = strp(plan.Path), strp(plan.Branch), strp(plan.Tip), strp(plan.KeepAs)
			tip = plan.Tip
			if res != nil {
				o.Problems = blockingOf(*plan)
			}
		}
		if errors.Is(err, errRemovePlanChanged) {
			o.Problems = append(o.Problems, RemoveProblem{Code: ProblemPlanChanged, Message: err.Error()})
		}
		o.Steps = []RemoveStep{notRun(StepWorktree, ""), notRun(StepBranch, tip), notRun(StepSuperset, "")}
		return o
	}

	o.Path, o.Branch, o.Tip, o.KeepAs = strp(plan.Path), strp(plan.Branch), strp(plan.Tip), strp(plan.KeepAs)
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

	branchGone := false
	if plan.Branch != "" && plan.Tip != "" {
		_, ok := ctx.Repo.ResolveRef("refs/heads/" + plan.Branch)
		branchGone = !ok
	}
	if branchGone && o.RestoreCommand == nil {
		o.RestoreCommand = restoreBranch(ctx, plan.Branch, plan.Tip)
	}

	wstep := RemoveStep{Step: StepWorktree, Result: worktree}
	bstep := RemoveStep{Step: StepBranch, Commit: strp(plan.Tip)}
	sstep := RemoveStep{Step: StepSuperset, Result: StepNotRun}
	if res != nil {
		o.Outcome = res.Outcome
		if o.Outcome == "" {
			o.Outcome = RemoveRefused
		}
		if !gone && err != nil {
			wstep.Reason = strp(oneLine(err.Error()))
		}
		bstep.Result = res.Branch
		if bstep.Result == "" {
			bstep.Result = StepNotRun
		} else if (res.Branch == quarantine.BranchKept || res.Branch == quarantine.BranchFailed) && err != nil {
			bstep.Reason = strp(oneLine(err.Error()))
		}
		if res.Superset != "" {
			sstep.Result, sstep.Reason = res.Superset, strp(res.SupersetReason)
		}
		o.Forced = res.Forced.Names()
	} else {
		o.Outcome = OutcomeInterrupted
		if worktree == WorktreeKept {
			wstep.Result = StepInterrupted
		}
		bstep.Result = interruptedBranch(ctx, *plan, gone, branchGone)
		// Superset follows the branch step whatever it came to, so once
		// the worktree is gone it may have begun.
		if gone {
			sstep.Result = StepInterrupted
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
func interruptedBranch(ctx *Context, p Plan, worktreeGone, branchGone bool) string {
	switch {
	case !worktreeGone:
		return StepNotRun
	case p.Outcome == BranchUntouched:
		return quarantine.BranchUntouched
	case p.Outcome == BranchDeleted && branchGone:
		return quarantine.BranchDeleted
	case p.Outcome == BranchKept && branchGone:
		if _, ok := ctx.Repo.ResolveRef("refs/heads/" + p.KeepAs); ok {
			return quarantine.BranchRenamed
		}
	}
	return StepInterrupted
}
