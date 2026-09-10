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
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
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
	// Merge and Ahead are the branch's standing against the main branch:
	// Ahead is how many commits it carries that the main branch does not,
	// and is meaningful only when Merge is Unmerged.
	Merge MergeState
	Ahead int
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
	return RemoveAt(ctx, wt.Path, opts, w)
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
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no worktree at %s", path)
	}
	plan := planFor(ctx, path, opts)
	plan.Render(w)

	// The lock is decided before the question, because the question does not
	// change the answer: a directory somebody is working in is not removed
	// because a prompt was answered quickly.
	if plan.blockedByLock() {
		return fmt.Errorf("%s is locked and its holder is still there: %s\n"+
			"  Finish or stop it, or pass --force to break the lock",
			path, plan.LockHolder)
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
		// nothing runs.
		if fresh := planFor(ctx, path, opts); fresh != plan {
			fmt.Fprintln(w, "The worktree changed while the prompt was open. Removal would now do this:")
			fresh.Render(w)
			fmt.Fprintln(w, "Nothing was removed.")
			return fmt.Errorf("the plan changed during confirmation; run the command again")
		}
	}
	return plan.apply(ctx, w)
}

// planFor reads every fact a removal depends on, before any of them change.
func planFor(ctx *Context, path string, opts RemoveOptions) Plan {
	p := Plan{Path: path, Branch: ctx.Repo.BranchAt(path),
		MainBranch: ctx.Config.MainBranch, Force: opts.Force}
	p.readLock(ctx, opts)
	if out, err := git.Run(path, "status", "--porcelain"); err == nil && out != "" {
		p.Dirty = true
	}

	p.Merge, p.Ahead = mergeStanding(ctx, p.Branch)

	switch {
	case p.Branch == "":
		p.Reason = "detached HEAD"
	case !ctx.Repo.BranchExists(p.Branch):
		p.Reason = "already gone"
	case p.Merge == Merged:
		// Merged first, and whoever created the branch: nothing is lost, and
		// leaving it behind because wt did not make it only leaves litter.
		p.Outcome = BranchDeleted
	case p.Merge == MergeUnknown && !branchIsOurs(ctx, p.Branch):
		p.Reason = "not created by wt, and there is no " + ctx.Config.MainBranch +
			" branch here to compare it with"
	case !branchIsOurs(ctx, p.Branch):
		p.Reason = "not created by wt and not merged"
	default:
		p.Outcome = BranchKept
		p.KeepAs = naming.StripPrefix(p.Branch, ctx.Config.TypeSuffix)
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
func (p *Plan) readLock(ctx *Context, opts RemoveOptions) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return
	}
	for _, wt := range worktrees {
		if !samePath(wt.Path, p.Path) || !wt.Locked {
			continue
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
		// No pid to ask about: the sessions wt can see are the second
		// opinion, and finding one names the holder properly.
		if a := sessionIn(MigrateOptions{Agents: opts.Agents}, p.Path, io.Discard); a != nil {
			p.LockHolder = sessionLabel(a)
		}
		return
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

// mergeStanding reads where a branch stands against the main branch. The
// count comes from git rather than from "is it merged", because "not merged"
// on its own does not say whether one commit or thirty are at stake.
func mergeStanding(ctx *Context, branch string) (MergeState, int) {
	if branch == "" || !ctx.Repo.BranchExists(branch) ||
		!ctx.Repo.BranchExists(ctx.Config.MainBranch) {
		return MergeUnknown, 0
	}
	if ctx.Repo.IsMerged(branch, ctx.Config.MainBranch) {
		return Merged, 0
	}
	ahead, _ := ctx.Repo.CommitsAhead(branch, ctx.Config.MainBranch)
	return Unmerged, ahead
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
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "fatal: "))
		if line == "" || strings.HasPrefix(line, "use '") || strings.HasPrefix(line, "hint:") {
			continue
		}
		return line
	}
	return err.Error()
}

// stillMerged asks the merged question once more, in the moment before the
// branch is destroyed. It is a guard, not a decision: the plan decided this
// and nothing here can change the outcome, only stop it.
//
// A confirmed removal already re-reads the whole plan under the prompt, but
// --yes, a script and a hook go straight from the plan to the delete, and the
// delete is -D: it will not refuse on wt's behalf.
func stillMerged(ctx *Context, p Plan) error {
	merge, ahead := mergeStanding(ctx, p.Branch)
	if merge == Merged {
		return nil
	}
	found := "cannot be compared with it any more — one of the two has gone"
	if merge == Unmerged {
		found = "is " + (Plan{Ahead: ahead, MainBranch: p.MainBranch}).aheadOfMain()
	}
	return fmt.Errorf("branch %s was merged into %s when the plan was made and %s now; "+
		"nothing was deleted", p.Branch, p.MainBranch, found)
}

func branchIsOurs(ctx *Context, branch string) bool {
	_, _, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix)
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

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  path\t%s\n", p.Path)
	fmt.Fprintf(tw, "  branch\t%s\n", branch)
	fmt.Fprintf(tw, "  state\t%s\n", state)
	if p.Locked {
		fmt.Fprintf(tw, "  lock\t%s\n", p.lockLine())
	}
	_ = tw.Flush()
	fmt.Fprintln(w)

	// A held lock ends the plan: what would happen to the branch is beside
	// the point when the checkout is not going anywhere.
	if p.blockedByLock() {
		return
	}
	fmt.Fprintf(w, "  the checkout will be deleted%s\n", p.lockNote())
	switch p.Outcome {
	case BranchDeleted:
		fmt.Fprintf(w, "  the branch will be deleted (merged into %s)\n", p.MainBranch)
	case BranchKept:
		fmt.Fprintf(w, "  the branch will be kept as %q (%s)\n", p.KeepAs, p.aheadOfMain())
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
	case p.Merge == Merged:
		return "merged into " + p.MainBranch
	case p.Merge == Unmerged:
		return "not merged: " + p.aheadOfMain()
	}
	return "no " + p.MainBranch + " branch here to compare with"
}

// aheadOfMain counts the work at stake. A branch with no count read — git
// could not answer — says only that it is unmerged, rather than claiming a
// zero it does not know.
func (p Plan) aheadOfMain() string {
	switch p.Ahead {
	case 0:
		return "not merged into " + p.MainBranch
	case 1:
		return "1 commit ahead of " + p.MainBranch
	}
	return fmt.Sprintf("%d commits ahead of %s", p.Ahead, p.MainBranch)
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
		if err := ctx.Repo.DeleteBranch(p.Branch); err != nil {
			// The merge check has already passed against the main branch, so
			// this is a real failure — a locked ref, a broken repository — not
			// git second-guessing the decision. The worktree is gone by then,
			// so say so before the reason.
			fmt.Fprintln(w, "✓ worktree removed")
			return fmt.Errorf("branch %s is merged into %s, but deleting it failed: %w",
				p.Branch, ctx.Config.MainBranch, err)
		}
		fmt.Fprintf(w, "✓ worktree removed; branch %s was merged into %s and has been deleted\n",
			p.Branch, ctx.Config.MainBranch)
	case BranchKept:
		if err := ctx.Repo.RenameBranch(p.Branch, p.KeepAs); err != nil {
			fmt.Fprintf(w, "✓ worktree removed; keeping branch %s (not merged into %s)\n",
				p.Branch, ctx.Config.MainBranch)
			return nil
		}
		fmt.Fprintf(w, "✓ worktree removed; branch kept as %s (not merged into %s)\n",
			p.KeepAs, ctx.Config.MainBranch)
		fmt.Fprintf(w, "  delete it later with: git branch -d %s\n", p.KeepAs)
	default:
		fmt.Fprintln(w, "✓ worktree removed")
	}
	return nil
}
