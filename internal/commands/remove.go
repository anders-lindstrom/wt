package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// RemoveOptions carries the caller's confirmation policy.
//
// Confirm is a function rather than a bool because whether to ask is a question
// about the terminal, not about the repository, and terminals are the CLI
// layer's business. A nil Confirm removes without asking — which is what a
// hook, a script and `--yes` all want.
type RemoveOptions struct {
	Confirm func(Plan) (bool, error)
	// Force is what the removal goes past, category by category: an idle
	// or a busy Claude session in the worktree, a session listing that
	// failed, files git status is told not to look at, and a git worktree
	// lock whose holder is still running. Nothing forces past uncommitted
	// work, an operation in progress, or commits only the checkout's HEAD
	// holds.
	Force ForceSet
	// Agents are the Claude sessions to check the worktree against. Nil
	// asks `claude agents`; an empty slice means there are none. AgentsErr
	// is a listing the caller could not get, which refuses the way a
	// listing that fails here does. Relist lists them again right before
	// the checkout goes; nil lists them the way Agents did.
	Agents    []wtsync.Agent
	AgentsErr error
	Relist    func() ([]wtsync.Agent, error)
	// DryRun prints the plan and stops.
	DryRun bool
	// Landed answers whether a branch at tip is a pull request GitHub merged
	// into trunk, and which. `wt sweep` passes the listing it has just read;
	// nil reads the cache, which is all a plain `wt remove` may do.
	Landed func(branch, tip string) int
	// Quarantine is a new folder, absolute, to move the worktree into
	// instead of deleting it; "" deletes it.
	Quarantine string
	// Result, when set, receives what the removal did, effect by effect.
	Result *RemoveResult
	// Expect is the token wt remove --dry-run --json gave: the removal
	// refuses, touching nothing, unless the plan it makes now has that token.
	Expect string
	// Journal records the plan and the run for --json; nil records nothing.
	Journal *RemoveJournal
	// KeepSuperset leaves the worktree's Superset workspace alone: Superset
	// is not asked anything.
	KeepSuperset bool
}

// RemoveResult is what a removal did, effect by effect, as far as it got.
type RemoveResult struct {
	// Outcome is one of the Remove* outcomes.
	Outcome string
	// Worktree is what became of the checkout: one of the Worktree*
	// values. CheckoutMoved and AdminMoved say which moves a quarantine
	// made, Quarantine is its folder.
	Worktree      string
	Quarantine    string
	CheckoutMoved bool
	AdminMoved    bool
	// Branch is what the branch step came to — quarantine.BranchDeleted,
	// Renamed, Untouched, Kept or Failed — and "" when the run did not get
	// that far. BranchName and BranchTip are the branch the plan read;
	// KeepAs the name a kept branch was to get.
	Branch     string
	BranchName string
	BranchTip  string
	KeepAs     string
	// Superset is what came of deleting the worktree's Superset workspace
	// — StepDeregistered, StepNotRegistered, StepSkipped or StepFailed —
	// and "" when no worktree was removed. SupersetReason says why for a
	// skip or a failure.
	Superset       string
	SupersetReason string
	// Forced is the --force categories the removal went past.
	Forced ForceSet
	// Error is why the run refused or stopped, "" when it did neither.
	Error string
}

// What a removal came to.
const (
	// RemoveRemoved: the worktree is gone, or quarantined, and the branch
	// step did what the plan said.
	RemoveRemoved = "removed"
	// RemoveRemovedWithBranchProblem: the worktree is gone, and the branch
	// was left as it was on purpose — it moved, is no longer merged, or a
	// worktree took it.
	RemoveRemovedWithBranchProblem = "removedWithBranchProblem"
	// RemoveRefused: nothing was changed.
	RemoveRefused = "refused"
	// RemovePartial: something changed and a later step failed: the
	// second move, or the branch step.
	RemovePartial = "partial"
)

// What became of the checkout.
const (
	WorktreeRemoved     = "removed"
	WorktreeQuarantined = "quarantined"
	// WorktreeKept is a checkout left where it was.
	WorktreeKept = "kept"
	// WorktreePartlyMoved is a quarantine that moved the checkout but not
	// its admin dir.
	WorktreePartlyMoved = "partlyMoved"
)

// landed asks the question RemoveOptions.Landed answers, falling back to the
// cache: a file read, no process and no network, so this runs from a git hook.
func (o RemoveOptions) landed(ctx *Context, branch, tip string) int {
	if o.Landed != nil {
		return o.Landed(branch, tip)
	}
	return landedPR(ctx, branch, tip)
}

// BranchOutcome is what removal will do to the branch checked out in a
// worktree.
type BranchOutcome int

const (
	// BranchUntouched covers a detached HEAD, a branch already gone, and an
	// unmerged branch this tooling did not create. All keep their branch, or
	// what is left of it, and lose only the checkout.
	BranchUntouched BranchOutcome = iota
	// BranchDeleted is a branch already merged into the main branch, whoever
	// created it: merged means nothing is lost.
	BranchDeleted
	// BranchKept renames an unmerged branch out of the <type>_wt/ prefix, so
	// unfinished work survives its worktree.
	BranchKept
)

// MergeState is what is known about a branch's relation to the main branch.
// It is the fact a removal turns on, so it is read for every branch — a
// branch nobody here created still has a merge state, and a user reading the
// plan wants it whoever made the branch.
type MergeState int

const (
	// MergeUnknown is a detached HEAD, a branch already deleted, or a
	// repository with no main branch to compare against.
	MergeUnknown MergeState = iota
	// Merged is a branch the main branch already contains.
	Merged
	// Unmerged is a branch carrying commits the main branch does not have.
	Unmerged
	// Applied is a branch trunk does not contain whose every commit is on
	// trunk already under another id — rebased or cherry-picked there, as a
	// rebase merge on GitHub leaves it. Deleting it loses nothing.
	Applied
)

// Plan is what a removal is about to do, assembled before anything is touched.
//
// It exists because the branch outcome is the surprising half of this command —
// a worktree is obviously deleted, but whether the branch is deleted, renamed
// or left alone depends on facts the user cannot see from the argument they
// typed. Printing it first turns a destructive command into one you can check.
type Plan struct {
	Path    string
	Branch  string // the branch checked out there, "" when detached
	Outcome BranchOutcome
	KeepAs  string // the name BranchKept will rename the branch to
	Reason  string // why an outcome of BranchUntouched was reached
	// Dirty is anything uncommitted in the checkout, its submodules
	// included, read so that no configuration can hide it. StatusError is
	// why it could not be read, which refuses rather than reads as clean.
	Dirty       bool
	StatusError string
	// Hidden are unedited files git status is told not to look at
	// (assume-unchanged, or skip-worktree and on disk). An edited one is
	// Dirty.
	Hidden []string
	// Moved are submodules checked out at another commit than the one
	// recorded, with nothing else changed in them.
	Moved []string
	// Nested are other worktrees whose checkouts are inside this one, and
	// would go with it.
	Nested []string
	// Operation is a git operation stopped halfway there — "rebase",
	// "merge", "cherry-pick", "revert", "bisect" — or "" for none.
	Operation string
	// Head is the commit checked out there, "" before the first commit.
	Head string
	// Unreachable is each tip whose commits nothing would hold once the
	// removal is done, counted against every ref and worktree HEAD that
	// survives it; ReachError is why that could not be worked out.
	Unreachable []Lost
	ReachError  string
	// Sessions are the Claude sessions working in the checkout, idle or
	// busy; SessionsError is why they could not be listed.
	Sessions      wtsync.Sessions
	SessionsError string
	// Merge, Ahead and Base are the branch's standing against trunk: Base is
	// the ref the answer is about (the one containing it when merged, the one
	// counted against otherwise), and Ahead is how many commits it carries
	// that Base does not, meaningful only when Merge is Unmerged.
	Merge MergeState
	Ahead int
	Base  string
	// Tip is the commit the branch was at when the plan was made. A merged
	// branch is deleted only while it is still there.
	Tip string
	// MergedPR is a pull request GitHub merged into trunk at exactly Tip,
	// which is what says the work landed when a squash or rebase merge has
	// left the branch looking unmerged to git. Sweep reads it from GitHub,
	// remove from the cache — a merge is permanent, so that cannot go stale.
	MergedPR int
	// Locked is git's own lock on the checkout, which stops it being removed
	// at all. LockReason is git's text for it, LockHolder names whoever wt
	// could work out is behind it, and LockHeld says that holder is still
	// there — a lock is held unless it is proved stale.
	Locked     bool
	LockReason string
	LockHolder string
	LockHeld   bool
	// LockPid is the process the reason named, 0 when it named none.
	LockPid int
	// Force is what the caller lets the removal go past.
	Force ForceSet
	// MainBranch is carried on the plan so rendering needs nothing but the
	// plan itself.
	MainBranch string
	// AdminDir is the worktree's own git dir, .git/worktrees/<id>, ""
	// when it cannot be read.
	AdminDir string
	// Quarantine is the folder the checkout is to be moved into instead of
	// deleted, "" to delete it; QuarantineError is why it cannot be.
	Quarantine      string
	QuarantineError string
	// KeepSuperset leaves the Superset workspace alone.
	KeepSuperset bool
	// quarantineBy names the command in recovery.json.
	quarantineBy string
	// relist lists the sessions again for the check right before the
	// checkout goes.
	relist func() ([]wtsync.Agent, error)
}

// Lost is a tip a removal would leave nothing holding.
type Lost struct {
	// Kind is LostBranch for the branch the removal deletes, LostHead for
	// the checkout's own HEAD, LostSubmodule for a submodule's commits.
	Kind  string
	OID   string
	Count int    // commits reachable from it and from nothing that survives
	Path  string // for LostSubmodule: the submodule, relative to the checkout
}

// The tips a removal can leave unreachable.
const (
	LostBranch    = "branch"
	LostHead      = "head"
	LostSubmodule = "submodule"
)

// Remove deletes the worktree named by arg, then decides what happens to its
// branch.
func Remove(ctx *Context, arg string, opts RemoveOptions, w io.Writer) error {
	wt, err := Locate(ctx, arg)
	if err != nil {
		return err
	}
	// Locate has just listed the worktrees; the plan wants the same entry.
	return removeWorktree(ctx, wt, opts, w)
}

// RemoveAt removes the worktree at path.
//
// The branch is read from the worktree rather than rebuilt from the work name:
// once the type can vary, the name no longer determines the branch, and a
// reconstructed name may belong to an unrelated branch that this would then
// delete. A detached HEAD and a branch outside the worktree convention both
// lose only the checkout; the branch, if any, is named in the plan and left
// alone.
func RemoveAt(ctx *Context, path string, opts RemoveOptions, w io.Writer) error {
	return removeWorktree(ctx, worktreeRecord(ctx, path), opts, w)
}

// worktreeRecord is git's record of the worktree at path — its lock above all.
// The path stays as the caller spelled it, because that is the path every
// message about this removal names. A path git knows nothing about, or a list
// that cannot be read, leaves a record holding only the path: no lock, which
// is what the removal would have concluded anyway.
func worktreeRecord(ctx *Context, path string) repo.Worktree {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return repo.Worktree{Path: path}
	}
	wt, ok := worktrees.ByPath(path)
	if !ok {
		return repo.Worktree{Path: path}
	}
	wt.Path = path
	return wt
}

func removeWorktree(ctx *Context, wt repo.Worktree, opts RemoveOptions, w io.Writer) error {
	res, err := removeWorktreeResult(ctx, wt, opts, w)
	if err != nil && res.Error == "" {
		res.Error = err.Error()
	}
	if opts.Result != nil {
		*opts.Result = res
	}
	return err
}

func removeWorktreeResult(ctx *Context, wt repo.Worktree, opts RemoveOptions, w io.Writer) (RemoveResult, error) {
	res := RemoveResult{Outcome: RemoveRefused, Worktree: WorktreeKept, Quarantine: opts.Quarantine}
	if _, err := os.Stat(wt.Path); err != nil {
		return res, fmt.Errorf("no worktree at %s", wt.Path)
	}
	plan := planFor(ctx, wt, opts)
	res.BranchName, res.BranchTip, res.KeepAs = plan.Branch, plan.Tip, plan.KeepAs
	plan.Render(w)
	if opts.Expect != "" || opts.Journal != nil {
		token := removeToken(ctx, plan)
		opts.Journal.planned(ctx, plan, deref(token))
		if opts.Expect != "" && deref(token) != opts.Expect {
			return res, errRemovePlanChanged
		}
	}

	// The lock is decided before the question, because the question does not
	// change the answer: a directory somebody is working in is not removed
	// because a prompt was answered quickly.
	if plan.blockedByLock() {
		hint := "pass --force=lock to break the lock"
		if f := plan.forceWould(); f != 0 {
			hint = "pass " + (plan.Force | f).Flag() + " to break the lock and remove it"
		}
		return res, fmt.Errorf("%s is locked and its holder is still there: %s\n"+
			"  Finish or stop it, or %s", wt.Path, plan.LockHolder, hint)
	}
	if why := plan.refusal(); why != "" {
		hint := ""
		if f := plan.forceWould(); f != 0 {
			hint = "\n  pass " + (plan.Force | f).Flag() + " to remove it anyway"
		}
		if _, ok := plan.lost(LostHead); ok {
			hint += "\n  keep them on a branch first: git -C " + wt.Path + " branch <name>"
		}
		return res, fmt.Errorf("%s was not removed: %s%s", wt.Path, why, hint)
	}
	if opts.DryRun {
		fmt.Fprintln(w, "Nothing was removed: --dry-run.")
		return res, nil
	}

	if opts.Confirm != nil {
		ok, err := opts.Confirm(plan)
		if err != nil {
			return res, err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was removed.")
			return res, nil
		}
		// The prompt can stay open for a while, and another session can land
		// commits meanwhile: a branch shown as merged may no longer be. The
		// delete does not re-ask that question — it carries out the plan — so
		// this re-read is what stands between a stale answer and somebody's
		// commits. The plan the user confirmed is the plan that runs, or
		// nothing runs. git's record is read again too: a lock can be taken
		// while the prompt is open, and that is a change to the plan.
		if fresh := planFor(ctx, worktreeRecord(ctx, wt.Path), opts); !fresh.same(plan) {
			if vanished, err := removedMeanwhile(ctx, plan, w); vanished {
				return res, err
			}
			fmt.Fprintln(w, "The worktree changed while the prompt was open. Removal would now do this:")
			fresh.Render(w)
			fmt.Fprintln(w, "Nothing was removed.")
			return res, fmt.Errorf("the plan changed during confirmation; run the command again")
		}
	}
	opts.Journal.applying()
	return plan.run(ctx, w)
}

// planFor reads every fact a removal depends on, before any of them change.
// wt is git's record of the worktree, which the caller has already read.
//
// The branch still comes from BranchAt rather than from wt.Branch: mid-rebase
// those two differ, and the branch the sequencer will return HEAD to is not
// the branch this checkout has.
func planFor(ctx *Context, wt repo.Worktree, opts RemoveOptions) Plan {
	p := Plan{Path: wt.Path, Branch: ctx.Repo.BranchAt(wt.Path),
		MainBranch: ctx.Config.MainBranch, Force: opts.Force.implied(), KeepSuperset: opts.KeepSuperset,
		relist: opts.relistAgents}
	p.readSessions(opts)
	p.readLock(wt)
	p.readCheckout()
	p.AdminDir, _ = repo.AdminDir(wt.Path)
	if opts.Quarantine != "" {
		p.Quarantine, p.quarantineBy = opts.Quarantine, "remove"
		p.QuarantineError = quarantineProblem(ctx, p.Quarantine, p.Path, p.AdminDir)
	}

	s := mergeStanding(ctx, p.Branch)
	p.Merge, p.Ahead, p.Base, p.Tip = s.Merge, s.Ahead, s.Base, s.Tip
	if p.Merge == Unmerged || p.Merge == MergeUnknown {
		p.MergedPR = opts.landed(ctx, p.Branch, p.Tip)
	}

	switch {
	case p.Branch == "":
		p.Reason = "detached HEAD"
	case p.Tip == "":
		// mergeStanding has just asked git for the branch's tip, and no tip is
		// a branch that is not there.
		p.Reason = "already gone"
	case p.Merge == Merged, p.Merge == Applied, p.MergedPR > 0:
		// Merged first, and whoever created the branch: nothing is lost, and
		// leaving it behind because wt did not make it only leaves litter.
		p.Outcome = BranchDeleted
	case p.Merge == MergeUnknown && !branchIsOurs(ctx, p.Branch):
		p.Reason = "not created by wt, and " + noTrunkHere(ctx.Config.MainBranch) +
			" to compare it with"
	case !branchIsOurs(ctx, p.Branch):
		p.Reason = "not created by wt and not merged"
	default:
		p.Outcome = BranchKept
		p.KeepAs = naming.StripPrefix(p.Branch, ctx.Scheme().Suffix)
	}
	p.readReach(ctx)
	return p
}

// readSessions reads the Claude sessions working in the checkout.
func (p *Plan) readSessions(opts RemoveOptions) {
	p.setSessions(sessionsIn(opts.Agents, opts.AgentsErr, p.Path))
}

func (p *Plan) setSessions(s wtsync.Sessions, err error) {
	if err != nil {
		p.SessionsError = err.Error()
		return
	}
	p.Sessions = s
}

// relistAgents is the listing for the check right before the checkout
// goes: Relist, else the listing the plan was given, else claude's.
func (o RemoveOptions) relistAgents() ([]wtsync.Agent, error) {
	switch {
	case o.Relist != nil:
		return o.Relist()
	case o.AgentsErr != nil:
		return nil, o.AgentsErr
	case o.Agents != nil:
		return o.Agents, nil
	}
	return listSessions()
}

// readCheckout reads what the files and the git dir say: anything
// uncommitted, anything hidden from status, an operation stopped halfway,
// and the commit checked out.
func (p *Plan) readCheckout() {
	st, err := repo.DirtyStrict(p.Path)
	if err != nil {
		p.StatusError = oneLine(gitSaid(err))
	} else {
		p.Dirty, p.Hidden, p.Moved = st.Dirty, st.Hidden, st.Moved
	}
	op, err := repo.OperationInProgress(p.Path)
	if err != nil && p.StatusError == "" {
		p.StatusError = "its git dir cannot be read: " + err.Error()
	}
	p.Operation = op
	head, err := repo.HeadOf(p.Path)
	if err != nil && p.StatusError == "" {
		p.StatusError = oneLine(err.Error())
	}
	p.Head = head
}

// readReach works out what the whole removal leaves unreachable: the
// branch it deletes and this checkout's HEAD, each counted against every
// ref and worktree HEAD that survives it, and the commits of submodules
// whose repositories go with the checkout. A branch kept or renamed
// survives, so a HEAD on it loses nothing. It also finds the worktrees
// nested inside this one, which would go with it.
func (p *Plan) readReach(ctx *Context) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		p.ReachError = oneLine(gitSaid(err))
		return
	}
	for _, wt := range worktrees {
		if !repo.SamePath(wt.Path, p.Path) && repo.Inside(p.Path, wt.Path, true) {
			p.Nested = append(p.Nested, wt.Path)
		}
	}
	subs, err := repo.SubmoduleLosses(p.Path)
	if err != nil {
		p.ReachError = oneLine(gitSaid(err))
		return
	}
	for _, l := range subs {
		p.Unreachable = append(p.Unreachable, Lost{Kind: LostSubmodule, OID: l.OID, Count: l.Count, Path: l.Path})
	}
	var drop []string
	if p.Outcome == BranchDeleted {
		drop = []string{"refs/heads/" + p.Branch}
	}
	keep, err := ctx.Repo.Survivors(drop, p.Path)
	if err != nil {
		p.ReachError = oneLine(gitSaid(err))
		return
	}
	count := func(kind, tip string) {
		n, err := ctx.Repo.CountLost(tip, keep)
		switch {
		case err != nil:
			p.ReachError = oneLine(gitSaid(err))
		case n > 0:
			p.Unreachable = append(p.Unreachable, Lost{Kind: kind, OID: tip, Count: n})
		}
	}
	if p.Outcome == BranchDeleted {
		count(LostBranch, p.Tip)
	}
	if p.Head != "" && (p.Outcome != BranchDeleted || p.Head != p.Tip) {
		count(LostHead, p.Head)
	}
}

// problem is one reason a removal does not go ahead.
type problem struct {
	code string // why, as sweep's --json names it: a Kept* constant
	text string
	// force is the --force category that goes past it, 0 for none: only
	// somebody's claim on the directory, never work lost with it.
	force ForceSet
}

// problems is every reason this removal would not go ahead, --force or not,
// in the order they are named.
func (p Plan) problems() []problem {
	var out []problem
	add := func(code, text string, force ForceSet) {
		out = append(out, problem{code: code, text: text, force: force})
	}
	if p.SessionsError != "" {
		add(KeptSessionsUnknown, "cannot list agent sessions ("+p.SessionsError+")", ForceSessionsUnknown)
	}
	// Idle and busy sessions are forced apart, so each is a problem of
	// its own.
	var idle, busy wtsync.Sessions
	for _, a := range p.Sessions {
		if a.Idle() {
			idle = append(idle, a)
		} else {
			busy = append(busy, a)
		}
	}
	if len(idle) > 0 {
		add(KeptSession, whoLabel(idle)+" in it", ForceIdleSessions)
	}
	if len(busy) > 0 {
		add(KeptSession, whoLabel(busy)+" in it", ForceBusySessions)
	}
	if p.Locked && p.LockHeld {
		add(KeptLockHeld, heldLock(p), ForceLock)
	}
	if p.StatusError != "" {
		add(KeptStatusUnknown, "cannot read its status ("+p.StatusError+")", 0)
	}
	if p.Dirty {
		add(KeptDirty, "dirty", 0)
	}
	for _, sm := range p.Moved {
		add(KeptDirty, "submodule "+sm+" is not at the commit recorded for it "+
			"(git submodule update, or commit the new one)", 0)
	}
	if len(p.Nested) > 0 {
		add(KeptNestedWorktree, "the worktree "+someOf(p.Nested, 3)+" is inside it", 0)
	}
	if len(p.Hidden) > 0 {
		add(KeptHiddenChanges, "git status is told not to look at "+someOf(p.Hidden, 3), ForceHiddenFiles)
	}
	if p.Operation != "" {
		add(KeptOperation, "a "+p.Operation+" is in progress", 0)
	}
	for _, l := range p.Unreachable {
		switch l.Kind {
		case LostHead:
			add(KeptHeadUnreachable, fmt.Sprintf("%s on its HEAD %s would be on no branch",
				commitCount(l.Count), git.ShortID(l.OID, 12)), 0)
		case LostSubmodule:
			add(KeptSubmoduleUnreachable, fmt.Sprintf("submodule %s has %s nothing outside this worktree holds (%s)",
				l.Path, commitCount(l.Count), git.ShortID(l.OID, 12)), 0)
		}
	}
	if p.ReachError != "" {
		add(KeptReachUnknown, "cannot tell what the removal would leave unreachable ("+p.ReachError+")", 0)
	}
	if p.QuarantineError != "" {
		add(ProblemQuarantineUnusable, "it cannot be quarantined: "+p.QuarantineError, 0)
	}
	return out
}

// ProblemQuarantineUnusable is a quarantine folder that cannot be used: it
// exists, is on another volume, or is inside the repository. Sweep checks its
// folder before it plans, so this never reaches its --json.
const ProblemQuarantineUnusable = "quarantineUnusable"

// quarantineProblem is why the checkout at path and its admin dir cannot be
// moved into dir, "" when they can.
func quarantineProblem(ctx *Context, dir, path, admin string) string {
	if admin == "" {
		return "its git dir under .git/worktrees cannot be read"
	}
	if err := quarantineOutside(ctx, dir); err != nil {
		return err.Error()
	}
	if err := quarantine.Check(dir, path, admin); err != nil {
		return err.Error()
	}
	return ""
}

// quarantineOutside refuses a quarantine folder inside any checkout of the
// repository or inside its git dir: another removal would take it with a
// checkout, and git's own files are git's.
func quarantineOutside(ctx *Context, dir string) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return fmt.Errorf("cannot list the worktrees to check where %s is: %w", dir, err)
	}
	roots := []string{}
	for _, wt := range worktrees {
		roots = append(roots, wt.Path)
	}
	if common, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		roots = append(roots, common)
	}
	// dir does not exist yet: its parent, symlinks resolved, says where it is.
	where := dir
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		where = filepath.Join(parent, filepath.Base(dir))
	}
	for _, root := range roots {
		if repo.Inside(root, dir, false) || repo.Inside(root, where, false) || insideResolved(root, where) {
			return fmt.Errorf("%s is inside %s: a quarantine goes outside every checkout and the git dir", dir, root)
		}
	}
	return nil
}

func insideResolved(root, p string) bool {
	r, err := filepath.EvalSymlinks(root)
	return err == nil && repo.Inside(r, p, false)
}

// blocking is the problems that stop this removal: all of them, less the
// ones whose --force category was given.
func (p Plan) blocking() []problem {
	var out []problem
	for _, pr := range p.problems() {
		if !p.Force.Has(pr.force) {
			out = append(out, pr)
		}
	}
	return out
}

// forced is the --force categories this removal goes past.
func (p Plan) forced() ForceSet {
	var f ForceSet
	for _, pr := range p.problems() {
		if p.Force.Has(pr.force) {
			f |= pr.force
		}
	}
	return f
}

// refusal is why this removal will not go ahead, "" when it will.
func (p Plan) refusal() string {
	var why []string
	for _, pr := range p.blocking() {
		why = append(why, pr.text)
	}
	return strings.Join(why, ", ")
}

// forceWould is the --force categories that are all that stands between
// this plan and the removal, 0 when a problem no force goes past is there
// too, or none is.
func (p Plan) forceWould() ForceSet {
	var f ForceSet
	for _, pr := range p.blocking() {
		if pr.force == 0 {
			return 0
		}
		f |= pr.force
	}
	return f
}

// same reports two readings of a worktree as the same plan. Sessions count
// by who they are, not by what they are doing. A deleted branch's lost
// commits are what the removal reports, not a reason to refuse it: they
// change when a sweep removes another worktree holding the same tip. The
// losses that refuse are compared.
func (p Plan) same(q Plan) bool {
	p.Sessions, q.Sessions = identities(p.Sessions), identities(q.Sessions)
	p.relist, q.relist = nil, nil
	p.Unreachable, q.Unreachable = refusingLosses(p.Unreachable), refusingLosses(q.Unreachable)
	return reflect.DeepEqual(p, q)
}

func refusingLosses(lost []Lost) []Lost {
	var out []Lost
	for _, l := range lost {
		if l.Kind != LostBranch {
			out = append(out, l)
		}
	}
	return out
}

func identities(s wtsync.Sessions) wtsync.Sessions {
	var out wtsync.Sessions
	for _, a := range s {
		out = append(out, wtsync.Agent{ID: a.ID, Name: a.Name, Cwd: a.Cwd, PID: a.PID})
	}
	return out
}

// someOf names up to n of a list, and how many more there are.
func someOf(list []string, n int) string {
	if len(list) <= n {
		return strings.Join(list, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(list[:n], ", "), len(list)-n)
}

func commitCount(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// pidInReason finds the pid a lock reason names, if it names one. Claude Code
// writes "(pid 9253 start ...)"; nothing else is assumed.
var pidInReason = regexp.MustCompile(`\bpid (\d+)\b`)

// readLock fills in what git's worktree lock means for this removal: whether
// there is one, who is behind it, and whether they are still there.
//
// A lock is held unless it can be shown stale. Somebody took it on purpose,
// and the cost of being wrong runs one way: refusing costs a flag, breaking a
// live session's ground costs its work.
func (p *Plan) readLock(wt repo.Worktree) {
	if !wt.Locked {
		return
	}
	p.Locked, p.LockReason, p.LockHeld = true, wt.LockReason, true
	p.LockHolder = wt.LockReason
	if p.LockHolder == "" {
		p.LockHolder = "a lock with no reason given"
	}
	if m := pidInReason.FindStringSubmatch(wt.LockReason); m != nil {
		pid, _ := strconv.Atoi(m[1])
		p.LockPid = pid
		p.LockHeld = pidAlive(pid)
		return
	}
	// No pid to ask about: the sessions wt can see are the second opinion, and
	// finding one names the holder properly.
	if a := p.Sessions.Lead(); a != nil {
		p.LockHolder = sessionLabel(a)
	}
}

// pidAlive reports whether a process is still there. Signal 0 checks for
// existence without touching it; a process owned by somebody else answers
// "permission denied", which is still an answer that it exists.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// blockedByLock reports the one state a removal will not talk itself out of.
func (p Plan) blockedByLock() bool {
	return p.Locked && p.LockHeld && !p.Force.Has(ForceLock)
}

// standing is where a branch stands against trunk, as mergeStanding read it.
type standing struct {
	Merge MergeState
	Ahead int
	Base  string
	Tip   string
}

// mergeStanding reads where a branch stands against trunk. Merged means its
// tip is reachable from origin/<trunk> as last fetched or from the local
// trunk, the bases wt sweep uses: a pull request merged on GitHub counts even
// while the main checkout's trunk is behind. Applied is the next best answer:
// no base contains the tip, but one of them has every commit's change. Nothing
// is fetched, because remove runs from hooks. The count comes from git rather
// than from "is it merged", because "not merged" on its own does not say
// whether one commit or thirty are at stake; it is taken against the first
// base, origin/<trunk> when there is one.
func mergeStanding(ctx *Context, branch string) standing {
	s := standing{Base: ctx.Config.MainBranch}
	if branch == "" {
		return s
	}
	tip, ok := ctx.Repo.ResolveRef("refs/heads/" + branch)
	if !ok {
		return s
	}
	s.Tip = tip
	bases, err := trunkBases(ctx)
	if err != nil {
		return s
	}
	s.Merge, s.Base = Unmerged, bases[0].Name
	for i, b := range bases {
		n, ok := ctx.Repo.CommitsAhead(tip, b.Tip)
		if ok && n == 0 {
			s.Merge, s.Ahead, s.Base = Merged, 0, b.Name
			return s
		}
		if i == 0 {
			s.Ahead = n
		}
	}
	for _, b := range bases {
		if ctx.Repo.Applied(tip, b.Tip) {
			// The count the plan prints is against the base it names.
			s.Merge, s.Base = Applied, b.Name
			s.Ahead, _ = ctx.Repo.CommitsAhead(tip, b.Tip)
			break
		}
	}
	return s
}

// appliedTo is the first base holding every commit of tip under another id,
// "" when none does.
func appliedTo(ctx *Context, tip string, bases []TrunkBase) string {
	for _, b := range bases {
		if ctx.Repo.Applied(tip, b.Tip) {
			return b.Name
		}
	}
	return ""
}

// removedMeanwhile handles a plan that changed under the prompt because
// something else is removing the worktree: a Claude session ending, an editor
// closing its workspace, another wt. Telling the user to run the command again
// would send them after a checkout that is no longer there, so this waits
// briefly for the deletion to finish and then says what is left. vanished is
// false when the worktree is still there to be removed.
func removedMeanwhile(ctx *Context, plan Plan, w io.Writer) (vanished bool, err error) {
	if !worktreeGone(ctx, plan.Path) {
		if plan.Dirty || !repo.Vanishing(plan.Path) {
			return false, nil
		}
		for deadline := time.Now().Add(vanishWait); !worktreeGone(ctx, plan.Path) && time.Now().Before(deadline); {
			time.Sleep(vanishWait / 20)
		}
	}
	if !worktreeGone(ctx, plan.Path) {
		fmt.Fprintln(w, "Files are disappearing from the worktree: something else is removing it.")
		fmt.Fprintln(w, "wt removed nothing.")
		return true, fmt.Errorf("%s is being removed by something else; wt list shows when it is done", plan.Path)
	}
	fmt.Fprintln(w, "Something else removed the worktree while the prompt was open; wt removed nothing.")
	if plan.Branch == "" {
		return true, nil
	}
	now, ok := ctx.Repo.ResolveRef("refs/heads/" + plan.Branch)
	switch {
	case !ok:
		fmt.Fprintf(w, "  branch %s is gone too\n", plan.Branch)
	case now == plan.Tip && plan.Outcome == BranchKept:
		fmt.Fprintf(w, "  branch %s is still here (%s); wt sweep lists it\n", plan.Branch, aheadOf(plan.Ahead, plan.Base))
	default:
		fmt.Fprintf(w, "  branch %s is still here; wt sweep deletes it once merged\n", plan.Branch)
	}
	return true, nil
}

// vanishWait is how long a removal waits for a worktree somebody else is
// deleting to be gone. A var so tests need not sit through it.
var vanishWait = 3 * time.Second

// worktreeGone is a checkout with no directory left, or one git no longer
// lists.
func worktreeGone(ctx *Context, path string) bool {
	if _, err := os.Stat(path); err != nil {
		return true
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return false
	}
	_, ok := worktrees.ByPath(path)
	return !ok
}

// noTrunkHere says there is nothing to compare a branch with.
func noTrunkHere(trunk string) string {
	return "neither origin/" + trunk + " nor " + trunk + " is here"
}

// removalFailed turns git's answer into wt's. git's own advice for a locked
// worktree is to run `remove -f -f`, which is not a command anyone here
// should type: the flag on this command is the one that means that.
func removalFailed(path string, err error) error {
	said := gitSaid(err)
	if strings.Contains(strings.ToLower(said), "lock") {
		return fmt.Errorf("git refused to remove %s: %s; try wt remove --force", path, said)
	}
	return fmt.Errorf("git refused to remove %s: %s", path, said)
}

// gitSaid reduces a git failure to the sentence worth showing: its first real
// line, without the "fatal:" and without git's advice to run something else.
func gitSaid(err error) string {
	return git.Reason(err, "use '", "hint:")
}

// stillMerged asks the merged question once more, in the moment before the
// branch is destroyed. It is a guard, not a decision: the plan decided this
// and nothing here can change the outcome, only stop it.
//
// A confirmed removal already re-reads the whole plan under the prompt, but
// --yes, a script and a hook go straight from the plan to the delete, and the
// delete does not ask whether the branch is merged: it only refuses a branch
// that moved.
func stillMerged(ctx *Context, p Plan) error {
	// git cannot answer this for a squash- or rebase-merged branch, which
	// looks unmerged for ever. In its place: MergedPR is only set for a pull
	// request merged into trunk whose head commit is this tip, DeleteBranchAt
	// refuses a branch that has moved off it, and sweep re-reads the pull
	// request state before it acts.
	if p.MergedPR > 0 {
		return nil
	}
	now := mergeStanding(ctx, p.Branch)
	if now.Merge == Merged || now.Merge == Applied {
		return nil
	}
	found := "cannot be compared with it any more — one of the two has gone"
	if now.Merge == Unmerged {
		found = "is " + aheadOf(now.Ahead, now.Base)
	}
	return fmt.Errorf("branch %s was merged into %s when the plan was made and %s now; "+
		"nothing was deleted", p.Branch, p.Base, found)
}

func branchIsOurs(ctx *Context, branch string) bool {
	_, _, ok := ctx.Scheme().Parse(branch)
	return ok
}

// Render writes the plan as the two things a user needs to check: which
// worktree this is, and what it will cost.
func (p Plan) Render(w io.Writer) {
	state := "clean"
	switch {
	case p.StatusError != "":
		state = "unknown: cannot read its status (" + p.StatusError + ")"
	case p.Dirty:
		state = "uncommitted changes"
	case len(p.Moved) > 0:
		state = "submodule " + someOf(p.Moved, 3) + " not at its recorded commit"
	}
	branch := "(none) — detached HEAD"
	if p.Branch != "" {
		branch = p.Branch + " — " + p.standing()
	}

	rows := [][]string{{"  path", p.Path}, {"  branch", branch}, {"  state", state}}
	if len(p.Hidden) > 0 {
		rows = append(rows, []string{"  hidden", "git status is told not to look at " + someOf(p.Hidden, 3)})
	}
	if p.Operation != "" {
		rows = append(rows, []string{"  operation", "a " + p.Operation + " is in progress"})
	}
	switch {
	case p.SessionsError != "":
		rows = append(rows, []string{"  sessions", "cannot list agent sessions (" + p.SessionsError + ")"})
	case len(p.Sessions) > 0:
		rows = append(rows, []string{"  sessions", whoLabel(p.Sessions) + " working in it"})
	}
	if p.Locked {
		rows = append(rows, []string{"  lock", p.lockLine()})
	}
	_ = printTable(w, rows)
	fmt.Fprintln(w)

	// A held lock ends the plan: what would happen to the branch is beside
	// the point when the checkout is not going anywhere. So does anything
	// else that refuses it, which the error names.
	if p.blockedByLock() {
		return
	}
	if p.refusal() != "" {
		fmt.Fprintln(w, "  nothing will be removed")
		fmt.Fprintln(w)
		return
	}
	for _, pr := range p.problems() {
		// A held lock has its own words on the line below.
		if pr.code != KeptLockHeld {
			fmt.Fprintf(w, "  ! --force: going past %s\n", pr.text)
		}
	}
	if p.Quarantine != "" {
		fmt.Fprintf(w, "  the checkout will be moved to %s%s\n", p.Quarantine, p.lockNote())
	} else {
		fmt.Fprintf(w, "  the checkout will be deleted%s\n", p.lockNote())
	}
	switch p.Outcome {
	case BranchDeleted:
		if p.MergedPR > 0 {
			fmt.Fprintf(w, "  the branch will be deleted (#%d merged on GitHub)\n", p.MergedPR)
			break
		}
		if p.Merge == Applied {
			fmt.Fprintf(w, "  the branch will be deleted (every commit is on %s)\n", p.Base)
			break
		}
		fmt.Fprintf(w, "  the branch will be deleted (merged into %s)\n", p.Base)
	case BranchKept:
		fmt.Fprintf(w, "  the branch will be kept as %q (%s)\n", p.KeepAs, aheadOf(p.Ahead, p.Base))
	default:
		fmt.Fprintf(w, "  no branch will be touched: %s\n", p.Reason)
	}
	if l, ok := p.lost(LostBranch); ok {
		fmt.Fprintf(w, "  its %s will then be on no branch: %s brings them back\n",
			commitCount(l.Count), p.restoreHint(l))
	}
	fmt.Fprintln(w)
}

// lost is the tip of that kind the removal leaves unreachable, if any.
func (p Plan) lost(kind string) (Lost, bool) {
	for _, l := range p.Unreachable {
		if l.Kind == kind {
			return l, true
		}
	}
	return Lost{}, false
}

// restoreHint is the command that puts a deleted branch's commits back.
func (p Plan) restoreHint(l Lost) string {
	return "git branch " + p.Branch + " " + git.ShortID(l.OID, 12)
}

// lockLine is the lock as the plan shows it: git's own reason, and what wt
// worked out about whoever is behind it.
func (p Plan) lockLine() string {
	switch {
	case !p.LockHeld && p.LockPid > 0:
		return fmt.Sprintf("stale: %s — pid %d is gone", p.LockReason, p.LockPid)
	case !p.LockHeld:
		return fmt.Sprintf("stale: %s", p.LockHolder)
	case p.LockHolder != p.LockReason && p.LockReason != "":
		// A session found by wt rather than named in the reason.
		return fmt.Sprintf("%s — %s is working in it", p.LockReason, p.LockHolder)
	}
	return p.LockHolder + " — still there"
}

// lockNote says what will happen to the lock, on the line about the checkout
// it is holding.
func (p Plan) lockNote() string {
	switch {
	case !p.Locked:
		return ""
	case p.LockHeld:
		return ", breaking the lock above"
	}
	return ", releasing its stale lock first"
}

// standing is the branch's merge state in words, which the plan states for
// every branch: it is the fact that decides what removal costs.
func (p Plan) standing() string {
	switch {
	case p.Branch == "":
		return "detached HEAD"
	case p.Reason == "already gone":
		return "already gone"
	case p.MergedPR > 0 && p.Merge != Merged:
		// git still counts the original commits, so state both, and which
		// one the removal believes.
		return fmt.Sprintf("#%d merged on GitHub, squashed or rebased, so git still counts it %s",
			p.MergedPR, aheadOf(p.Ahead, p.Base))
	case p.Merge == Merged:
		return "merged into " + p.Base
	case p.Merge == Applied:
		return fmt.Sprintf("every commit is on %s under a new id, rebased or cherry-picked, so git still counts it %s",
			p.Base, aheadOf(p.Ahead, p.Base))
	case p.Merge == Unmerged:
		return "not merged: " + aheadOf(p.Ahead, p.Base)
	}
	return noTrunkHere(p.MainBranch) + " to compare with"
}

// aheadOf counts the work at stake: n commits base does not have. A branch
// with no count read — git could not answer — says only that it is unmerged,
// rather than claiming a zero it does not know.
func aheadOf(n int, base string) string {
	switch n {
	case 0:
		return "not merged into " + base
	case 1:
		return "1 commit ahead of " + base
	}
	return fmt.Sprintf("%d commits ahead of %s", n, base)
}

// apply carries out the plan. Every decision was already made in planFor, so
// nothing here re-reads state that the removal itself has changed — except
// the checkout, read once more right before it goes.
func (p Plan) apply(ctx *Context, w io.Writer) error {
	_, err := p.run(ctx, w)
	return err
}

// afterFinalCheck runs between the last read of the checkout and the first
// change. Tests set it to write a file a real editor would write there.
var afterFinalCheck func()

// run is apply, reporting what it did effect by effect. The checkout goes
// first, deleted or quarantined, and then the branch step, so a removal
// that fails leaves its branch where it was. Once the checkout is gone from
// its path, its Superset workspace goes too; it is looked up before, while
// the path still resolves. KeepSuperset asks Superset nothing.
func (p Plan) run(ctx *Context, w io.Writer) (RemoveResult, error) {
	workspaces := keptSupersetWorkspaces(ctx)
	if !p.KeepSuperset {
		workspaces = findSupersetWorkspaces(ctx, p.Path)
	}
	res, err := p.remove(ctx, w)
	if res.Worktree == WorktreeRemoved || res.Worktree == WorktreeQuarantined {
		res.Superset, res.SupersetReason = workspaces.deregister(ctx, p.Path, w)
	}
	return res, err
}

// remove is run without Superset.
func (p Plan) remove(ctx *Context, w io.Writer) (RemoveResult, error) {
	res := RemoveResult{Outcome: RemoveRefused, Worktree: WorktreeKept, Quarantine: p.Quarantine,
		BranchName: p.Branch, BranchTip: p.Tip, KeepAs: p.KeepAs}
	now, why := p.recheck()
	if why != "" {
		hint := ""
		if f := now.forceWould(); f != 0 {
			hint = "\n  pass " + (p.Force | f).Flag() + " to remove it anyway"
		}
		return res, fmt.Errorf("%s was not removed: %s%s", p.Path, why, hint)
	}
	res.Forced = p.forced() | now.forced()
	if afterFinalCheck != nil {
		afterFinalCheck()
	}
	var rec *quarantine.Record
	if p.Quarantine != "" {
		var err error
		if rec, err = p.quarantine(ctx, w, &res); err != nil {
			return res, err
		}
	} else {
		if err := p.releaseLock(ctx, w); err != nil {
			return res, err
		}
		if err := ctx.Repo.RemoveWorktree(p.Path); err != nil {
			return res, removalFailed(p.Path, err)
		}
		res.Worktree = WorktreeRemoved
	}
	res.Outcome = RemoveRemoved

	removed := "worktree removed"
	if rec != nil {
		removed = "worktree moved to " + p.Quarantine
	}
	result, msg, err := p.branchStep(ctx)
	res.Branch = result
	if rec != nil {
		if jerr := rec.RecordBranch(result, err); jerr != nil {
			res.Outcome = RemovePartial
			return res, jerr
		}
	}
	restore := ""
	if rec != nil {
		restore = fmt.Sprintf("  wt restore %s puts it back\n", p.Quarantine)
	}
	switch result {
	case quarantine.BranchFailed:
		res.Outcome = RemovePartial
		fmt.Fprintf(w, "! partly done: %s, but the branch step failed\n%s", removed, restore)
		return res, err
	case quarantine.BranchKept:
		res.Outcome = RemoveRemovedWithBranchProblem
		fmt.Fprintf(w, "✓ %s\n%s", removed, restore)
		return res, err
	}
	if msg == "" {
		fmt.Fprintf(w, "✓ %s\n", removed)
	} else {
		fmt.Fprintf(w, "✓ %s; %s\n", removed, msg)
	}
	if p.Outcome == BranchKept {
		fmt.Fprintf(w, "  delete it later with: git branch -d %s\n", p.KeepAs)
	}
	fmt.Fprintf(w, "%s", restore)
	if l, ok := p.lost(LostBranch); ok && p.Outcome == BranchDeleted {
		fmt.Fprintf(w, "  %s restores its commits\n", p.restoreHint(l))
	}
	return res, nil
}

// releaseLock takes git's lock off the checkout, a stale one or one --force
// breaks, and says so.
func (p Plan) releaseLock(ctx *Context, w io.Writer) error {
	if !p.Locked {
		return nil
	}
	if err := ctx.Repo.UnlockWorktree(p.Path); err != nil {
		return fmt.Errorf("could not release the lock on %s: %s", p.Path, gitSaid(err))
	}
	if p.LockHeld {
		fmt.Fprintf(w, "! broke the lock on the checkout: %s\n", p.LockHolder)
	} else {
		fmt.Fprintf(w, "- released a stale lock: %s\n", p.LockHolder)
	}
	return nil
}

// branchStep carries out the plan's branch outcome once the checkout is
// gone. result is one of quarantine's Branch* values: BranchKept is a step
// turned down on purpose, BranchFailed one git failed; err says why for
// both. msg is the clause the success line ends with.
func (p Plan) branchStep(ctx *Context) (result, msg string, err error) {
	switch p.Outcome {
	case BranchDeleted:
		if err := stillMerged(ctx, p); err != nil {
			return quarantine.BranchKept, "", err
		}
		// Only at the tip the plan showed, so a commit that lands after the
		// plan is never deleted with the branch. DeleteBranchAt refuses a
		// branch another worktree is using, which update-ref would not.
		if err := ctx.Repo.DeleteBranchAt(p.Branch, p.Tip); err != nil {
			var inUse *repo.BranchInUseError
			switch {
			case errors.As(err, &inUse):
				return quarantine.BranchKept, "", fmt.Errorf("branch %s is merged into %s but was kept: %s is using it",
					p.Branch, p.Base, inUse.Path)
			case errors.Is(err, repo.ErrWorktreesUnknown):
				return quarantine.BranchKept, "", fmt.Errorf("branch %s is merged into %s but was kept: %w", p.Branch, p.Base, err)
			}
			if now, ok := ctx.Repo.ResolveRef("refs/heads/" + p.Branch); ok && now != p.Tip {
				return quarantine.BranchKept, "", fmt.Errorf("branch %s was kept: it moved after the plan was made", p.Branch)
			}
			return quarantine.BranchFailed, "", fmt.Errorf("branch %s is merged into %s, but deleting it failed: %s",
				p.Branch, p.Base, gitSaid(err))
		}
		switch {
		case p.MergedPR > 0:
			msg = fmt.Sprintf("branch %s was merged as #%d and has been deleted", p.Branch, p.MergedPR)
		case p.Merge == Applied:
			msg = fmt.Sprintf("every commit of %s is on %s, so the branch has been deleted", p.Branch, p.Base)
		default:
			msg = fmt.Sprintf("branch %s was merged into %s and has been deleted", p.Branch, p.Base)
		}
		return quarantine.BranchDeleted, msg, nil
	case BranchKept:
		if err := ctx.Repo.RenameBranch(p.Branch, p.KeepAs); err != nil {
			return quarantine.BranchFailed, "", fmt.Errorf("branch %s is kept under that name (%s): renaming it to %s failed: %s",
				p.Branch, aheadOf(p.Ahead, p.Base), p.KeepAs, gitSaid(err))
		}
		return quarantine.BranchRenamed, fmt.Sprintf("branch kept as %s (%s)", p.KeepAs, aheadOf(p.Ahead, p.Base)), nil
	}
	return quarantine.BranchUntouched, "", nil
}

// quarantine moves the checkout and its admin dir into p.Quarantine, each
// step written to recovery.json before and after it runs: the folder and
// the journal first, then git's lock with the quarantine's reason, so no
// prune takes the registration while the checkout is away, then the
// checkout, then the admin dir, whose move is the unregistration. Nothing is
// deleted. It returns the journal for the branch step.
func (p Plan) quarantine(ctx *Context, w io.Writer, res *RemoveResult) (*quarantine.Record, error) {
	dir := p.Quarantine
	admin, err := repo.AdminDir(p.Path)
	if err != nil {
		return nil, fmt.Errorf("%s was not removed: its git dir cannot be read: %w", p.Path, err)
	}
	if err := quarantine.Check(dir, p.Path, admin); err != nil {
		return nil, fmt.Errorf("%s was not removed: %w", p.Path, err)
	}
	checkoutID, err := quarantine.Identify(p.Path)
	if err != nil {
		return nil, err
	}
	adminID, err := quarantine.Identify(admin)
	if err != nil {
		return nil, err
	}
	gitFile, err := os.ReadFile(filepath.Join(p.Path, ".git"))
	if err != nil {
		return nil, fmt.Errorf("%s was not removed: %w", p.Path, err)
	}
	rec := quarantine.Record{Command: p.quarantineBy, Repo: ctx.Repo.MainRoot, GitFile: string(gitFile),
		CommonDir: filepath.Dir(filepath.Dir(admin)), WorktreeID: filepath.Base(admin),
		Checkout: quarantine.Place{Path: p.Path, Quarantined: filepath.Join(dir, "checkout"),
			Device: checkoutID.Device, Inode: checkoutID.Inode},
		Admin: quarantine.Place{Path: admin, Quarantined: filepath.Join(dir, "admin"),
			Device: adminID.Device, Inode: adminID.Inode},
		Head: strp(p.Head)}
	if p.Branch != "" && p.Tip != "" {
		plan := quarantine.BranchPlanNone
		switch p.Outcome {
		case BranchDeleted:
			plan = quarantine.BranchPlanDelete
		case BranchKept:
			plan = quarantine.BranchPlanKeep
		}
		rec.Branch = &quarantine.Branch{Name: p.Branch, Tip: p.Tip, Plan: plan, KeepAs: strp(p.KeepAs)}
	}
	r, err := quarantine.Begin(dir, rec)
	if err != nil {
		return nil, fmt.Errorf("%s was not removed: %w", p.Path, err)
	}

	if err := r.Set(quarantine.StepLock, quarantine.Running, nil); err != nil {
		return nil, err
	}
	if err := p.releaseLock(ctx, w); err != nil {
		_ = r.Set(quarantine.StepLock, quarantine.Failed, err)
		return nil, err
	}
	if err := ctx.Repo.LockWorktree(p.Path, quarantine.LockReason(dir)); err != nil {
		err = fmt.Errorf("%s was not removed: git would not lock it: %s", p.Path, gitSaid(err))
		_ = r.Set(quarantine.StepLock, quarantine.Failed, err)
		return nil, err
	}
	// The lock is a file in the admin dir: on disk before it is journalled.
	if err := quarantine.SyncDir(admin); err != nil {
		_ = r.Set(quarantine.StepLock, quarantine.Failed, err)
		return nil, err
	}
	if err := r.Set(quarantine.StepLock, quarantine.Done, nil); err != nil {
		return nil, err
	}

	// Once the branch is deleted and the admin dir is out of the
	// repository, nothing else may hold its commits: gc would take them.
	if err := r.Set(quarantine.StepPin, quarantine.Running, nil); err != nil {
		return nil, err
	}
	if err := r.Pin(ctx.Repo); err != nil {
		_ = r.Set(quarantine.StepPin, quarantine.Failed, err)
		return nil, fmt.Errorf("%s was not removed: %w", p.Path, err)
	}
	if err := r.Set(quarantine.StepPin, quarantine.Done, nil); err != nil {
		return nil, err
	}

	if err := r.Set(quarantine.StepMoveCheckout, quarantine.Running, nil); err != nil {
		return nil, err
	}
	if err := quarantine.Move(p.Path, r.Checkout.Quarantined); err != nil {
		_ = r.Set(quarantine.StepMoveCheckout, quarantine.Failed, err)
		if quarantine.Locate(r.Checkout) == quarantine.AtOriginal {
			// Nothing moved: the lock and the pins are all there is to take
			// back. The record stays; a restore finds nothing more to do.
			_ = ctx.Repo.UnlockWorktree(p.Path)
			_ = r.Unpin(ctx.Repo)
			return nil, fmt.Errorf("%s was not removed: %w", p.Path, err)
		}
		// It moved and the rest failed: the lock stays, so no prune takes
		// the registration of a checkout that is not at its path.
		res.CheckoutMoved, res.Worktree, res.Outcome = true, WorktreePartlyMoved, RemovePartial
		fmt.Fprintf(w, "! partly done: the checkout is in %s, its git dir is still registered\n"+
			"  wt restore %s puts it back\n", dir, dir)
		return nil, fmt.Errorf("%s was moved, and then: %w", p.Path, err)
	}
	res.CheckoutMoved, res.Worktree = true, WorktreePartlyMoved
	if err := r.Set(quarantine.StepMoveCheckout, quarantine.Done, nil); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}

	if err := r.Set(quarantine.StepMoveAdmin, quarantine.Running, nil); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}
	if err := quarantine.Move(admin, r.Admin.Quarantined); err != nil {
		_ = r.Set(quarantine.StepMoveAdmin, quarantine.Failed, err)
		res.Outcome = RemovePartial
		if quarantine.Locate(r.Admin) != quarantine.AtOriginal {
			res.AdminMoved, res.Worktree = true, WorktreeQuarantined
			fmt.Fprintf(w, "! partly done: the worktree is in %s\n  wt restore %s puts it back\n", dir, dir)
			return nil, fmt.Errorf("%s was moved, and then: %w", p.Path, err)
		}
		fmt.Fprintf(w, "! partly done: the checkout is in %s, its git dir is still registered\n"+
			"  wt restore %s puts it back\n", dir, dir)
		return nil, fmt.Errorf("%s was moved, but its git dir was not: %w", p.Path, err)
	}
	res.AdminMoved, res.Worktree = true, WorktreeQuarantined
	if err := r.Set(quarantine.StepMoveAdmin, quarantine.Done, nil); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}

	// The id is free now, and a new worktree may take it: git in the
	// quarantine must work on its own admin dir, never on that one's.
	if err := r.Set(quarantine.StepRelink, quarantine.Running, nil); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}
	if err := r.Relink(); err != nil {
		_ = r.Set(quarantine.StepRelink, quarantine.Failed, err)
		res.Outcome = RemovePartial
		fmt.Fprintf(w, "! partly done: the worktree is in %s\n  wt restore %s puts it back\n", dir, dir)
		return nil, fmt.Errorf("%s was moved, and then: %w", p.Path, err)
	}
	if err := r.Set(quarantine.StepRelink, quarantine.Done, nil); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}
	if err := r.BeginBranch(ctx.Repo); err != nil {
		res.Outcome = RemovePartial
		return nil, err
	}
	return r, nil
}

// recheck reads the checkout and the sessions once more, immediately
// before it is deleted or moved: a plan can sit under a prompt for minutes,
// and a file written or a commit made since is work it never saw, a session
// that turned busy or arrived one it never weighed. A session refuses unless
// --force names its state now. The sessions are listed first, because that
// can take seconds, and the checkout is read last, as close to the delete as
// it gets. It returns that reading, and why is "" when it can still go.
func (p Plan) recheck() (now Plan, why string) {
	now = Plan{Path: p.Path, Force: p.Force}
	relist := p.relist
	if relist == nil {
		relist = listSessions
	}
	agents, err := relist()
	if agents == nil {
		agents = []wtsync.Agent{}
	}
	now.setSessions(sessionsIn(agents, err, p.Path))
	now.readCheckout()
	if now.Head != p.Head && now.StatusError == "" {
		return now, "its HEAD moved after the plan was made"
	}
	if why := now.refusal(); why != "" {
		return now, "it changed since the plan was made: " + why
	}
	return now, ""
}
