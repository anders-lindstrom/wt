package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
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
	// Force breaks a git worktree lock whose holder is still running. A lock
	// is somebody's claim on the directory, so nothing else overrides it.
	Force bool
	// Agents are the sessions to check against when a lock names no pid.
	// Nil asks `claude agents`; an empty slice means there are none. It is
	// consulted only for a locked worktree, which is rare — every other
	// removal costs nothing.
	Agents []wtsync.Agent
	// Landed answers whether a branch at tip is a pull request GitHub merged
	// into trunk, and which. `wt sweep` passes the listing it has just read;
	// nil reads the cache, which is all a plain `wt remove` may do.
	Landed func(branch, tip string) int
}

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
	Dirty   bool   // the checkout has uncommitted changes
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
	// Force is the caller's willingness to break a held lock.
	Force bool
	// MainBranch is carried on the plan so rendering needs nothing but the
	// plan itself.
	MainBranch string
}

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
	if _, err := os.Stat(wt.Path); err != nil {
		return fmt.Errorf("no worktree at %s", wt.Path)
	}
	plan := planFor(ctx, wt, opts)
	plan.Render(w)

	// The lock is decided before the question, because the question does not
	// change the answer: a directory somebody is working in is not removed
	// because a prompt was answered quickly.
	if plan.blockedByLock() {
		return fmt.Errorf("%s is locked and its holder is still there: %s\n"+
			"  Finish or stop it, or pass --force to break the lock",
			wt.Path, plan.LockHolder)
	}

	if opts.Confirm != nil {
		ok, err := opts.Confirm(plan)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was removed.")
			return nil
		}
		// The prompt can stay open for a while, and another session can land
		// commits meanwhile: a branch shown as merged may no longer be. The
		// delete does not re-ask that question — it carries out the plan — so
		// this re-read is what stands between a stale answer and somebody's
		// commits. The plan the user confirmed is the plan that runs, or
		// nothing runs. git's record is read again too: a lock can be taken
		// while the prompt is open, and that is a change to the plan.
		if fresh := planFor(ctx, worktreeRecord(ctx, wt.Path), opts); fresh != plan {
			fmt.Fprintln(w, "The worktree changed while the prompt was open. Removal would now do this:")
			fresh.Render(w)
			fmt.Fprintln(w, "Nothing was removed.")
			return fmt.Errorf("the plan changed during confirmation; run the command again")
		}
	}
	return plan.apply(ctx, w)
}

// planFor reads every fact a removal depends on, before any of them change.
// wt is git's record of the worktree, which the caller has already read.
//
// The branch still comes from BranchAt rather than from wt.Branch: mid-rebase
// those two differ, and the branch the sequencer will return HEAD to is not
// the branch this checkout has.
func planFor(ctx *Context, wt repo.Worktree, opts RemoveOptions) Plan {
	p := Plan{Path: wt.Path, Branch: ctx.Repo.BranchAt(wt.Path),
		MainBranch: ctx.Config.MainBranch, Force: opts.Force}
	p.readLock(wt, opts)
	if dirty, err := repo.Dirty(wt.Path, false); err == nil && dirty {
		p.Dirty = true
	}

	s := mergeStanding(ctx, p.Branch)
	p.Merge, p.Ahead, p.Base, p.Tip = s.Merge, s.Ahead, s.Base, s.Tip
	if p.Merge != Merged {
		p.MergedPR = opts.landed(ctx, p.Branch, p.Tip)
	}

	switch {
	case p.Branch == "":
		p.Reason = "detached HEAD"
	case p.Tip == "":
		// mergeStanding has just asked git for the branch's tip, and no tip is
		// a branch that is not there.
		p.Reason = "already gone"
	case p.Merge == Merged, p.MergedPR > 0:
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
	return p
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
func (p *Plan) readLock(wt repo.Worktree, opts RemoveOptions) {
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
	if a := sessionIn(MigrateOptions{Agents: opts.Agents}, p.Path, io.Discard); a != nil {
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
	return p.Locked && p.LockHeld && !p.Force
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
// while the main checkout's trunk is behind. Nothing is fetched, because
// remove runs from hooks. The count comes from git rather than from "is it
// merged", because "not merged" on its own does not say whether one commit or
// thirty are at stake; it is taken against the first base, origin/<trunk>
// when there is one.
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
	return s
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
	if now.Merge == Merged {
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
	if p.Dirty {
		state = "uncommitted changes"
	}
	branch := "(none) — detached HEAD"
	if p.Branch != "" {
		branch = p.Branch + " — " + p.standing()
	}

	rows := [][]string{{"  path", p.Path}, {"  branch", branch}, {"  state", state}}
	if p.Locked {
		rows = append(rows, []string{"  lock", p.lockLine()})
	}
	_ = printTable(w, rows)
	fmt.Fprintln(w)

	// A held lock ends the plan: what would happen to the branch is beside
	// the point when the checkout is not going anywhere.
	if p.blockedByLock() {
		return
	}
	fmt.Fprintf(w, "  the checkout will be deleted%s\n", p.lockNote())
	switch p.Outcome {
	case BranchDeleted:
		if p.MergedPR > 0 {
			fmt.Fprintf(w, "  the branch will be deleted (#%d merged on GitHub)\n", p.MergedPR)
			break
		}
		fmt.Fprintf(w, "  the branch will be deleted (merged into %s)\n", p.Base)
	case BranchKept:
		fmt.Fprintf(w, "  the branch will be kept as %q (%s)\n", p.KeepAs, aheadOf(p.Ahead, p.Base))
	default:
		fmt.Fprintf(w, "  no branch will be touched: %s\n", p.Reason)
	}
	fmt.Fprintln(w)
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
// nothing here re-reads state that the removal itself has changed.
func (p Plan) apply(ctx *Context, w io.Writer) error {
	if p.Locked {
		if err := ctx.Repo.UnlockWorktree(p.Path); err != nil {
			return fmt.Errorf("could not release the lock on %s: %s", p.Path, gitSaid(err))
		}
		if p.LockHeld {
			fmt.Fprintf(w, "! broke the lock on the checkout: %s\n", p.LockHolder)
		} else {
			fmt.Fprintf(w, "- released a stale lock: %s\n", p.LockHolder)
		}
	}
	if err := ctx.Repo.RemoveWorktree(p.Path); err != nil {
		return removalFailed(p.Path, err)
	}
	switch p.Outcome {
	case BranchDeleted:
		if err := stillMerged(ctx, p); err != nil {
			fmt.Fprintln(w, "✓ worktree removed")
			return err
		}
		// Only at the tip the plan showed, so a commit that lands after the
		// plan is never deleted with the branch. DeleteBranchAt refuses a
		// branch another worktree is using, which update-ref would not.
		if err := ctx.Repo.DeleteBranchAt(p.Branch, p.Tip); err != nil {
			fmt.Fprintln(w, "✓ worktree removed")
			var inUse *repo.BranchInUseError
			switch {
			case errors.As(err, &inUse):
				return fmt.Errorf("branch %s is merged into %s but was kept: %s is using it",
					p.Branch, p.Base, inUse.Path)
			case errors.Is(err, repo.ErrWorktreesUnknown):
				return fmt.Errorf("branch %s is merged into %s but was kept: %w", p.Branch, p.Base, err)
			}
			if now, ok := ctx.Repo.ResolveRef("refs/heads/" + p.Branch); ok && now != p.Tip {
				return fmt.Errorf("branch %s was kept: it moved after the plan was made", p.Branch)
			}
			return fmt.Errorf("branch %s is merged into %s, but deleting it failed: %s",
				p.Branch, p.Base, gitSaid(err))
		}
		if p.MergedPR > 0 {
			fmt.Fprintf(w, "✓ worktree removed; branch %s was merged as #%d and has been deleted\n",
				p.Branch, p.MergedPR)
			break
		}
		fmt.Fprintf(w, "✓ worktree removed; branch %s was merged into %s and has been deleted\n",
			p.Branch, p.Base)
	case BranchKept:
		if err := ctx.Repo.RenameBranch(p.Branch, p.KeepAs); err != nil {
			fmt.Fprintf(w, "✓ worktree removed; keeping branch %s (%s)\n",
				p.Branch, aheadOf(p.Ahead, p.Base))
			return nil
		}
		fmt.Fprintf(w, "✓ worktree removed; branch kept as %s (%s)\n",
			p.KeepAs, aheadOf(p.Ahead, p.Base))
		fmt.Fprintf(w, "  delete it later with: git branch -d %s\n", p.KeepAs)
	default:
		fmt.Fprintln(w, "✓ worktree removed")
	}
	return nil
}
