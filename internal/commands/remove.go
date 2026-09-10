package commands

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// RemoveOptions carries the caller's confirmation policy.
//
// Confirm is a function rather than a bool because whether to ask is a question
// about the terminal, not about the repository, and terminals are the CLI
// layer's business. A nil Confirm removes without asking — which is what a
// hook, a script and `--yes` all want.
type RemoveOptions struct {
	Confirm func(Plan) (bool, error)
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
	plan := planFor(ctx, path)
	plan.Render(w)

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
		if fresh := planFor(ctx, path); fresh != plan {
			fmt.Fprintln(w, "The worktree changed while the prompt was open. Removal would now do this:")
			fresh.Render(w)
			fmt.Fprintln(w, "Nothing was removed.")
			return fmt.Errorf("the plan changed during confirmation; run the command again")
		}
	}
	return plan.apply(ctx, w)
}

// planFor reads every fact a removal depends on, before any of them change.
func planFor(ctx *Context, path string) Plan {
	p := Plan{Path: path, Branch: ctx.Repo.BranchAt(path), MainBranch: ctx.Config.MainBranch}
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
	_ = tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  the checkout will be deleted")
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
	if err := ctx.Repo.RemoveWorktree(p.Path); err != nil {
		return err
	}
	switch p.Outcome {
	case BranchDeleted:
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
