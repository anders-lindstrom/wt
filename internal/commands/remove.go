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
	// BranchUntouched covers a detached HEAD and a branch this tooling did not
	// create. Both keep their branch and lose only the checkout.
	BranchUntouched BranchOutcome = iota
	// BranchDeleted is a branch already merged into the main branch.
	BranchDeleted
	// BranchKept renames an unmerged branch out of the <type>_wt/ prefix, so
	// unfinished work survives its worktree.
	BranchKept
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
		// commits meanwhile: a branch shown as merged may no longer be, and
		// `git branch -d` would still delete it if its upstream has the new
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

	switch {
	case p.Branch == "":
		p.Reason = "detached HEAD"
	case !branchIsOurs(ctx, p.Branch):
		p.Reason = "not created by wt"
	case !ctx.Repo.BranchExists(p.Branch):
		p.Reason = "already gone"
	case ctx.Repo.BranchExists(ctx.Config.MainBranch) &&
		ctx.Repo.IsMerged(p.Branch, ctx.Config.MainBranch):
		p.Outcome = BranchDeleted
	default:
		p.Outcome = BranchKept
		p.KeepAs = naming.StripPrefix(p.Branch, ctx.Config.TypeSuffix)
	}
	return p
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
	branch := "(none)"
	if p.Branch != "" {
		branch = p.Branch
	}
	if p.Reason != "" {
		branch += " — " + p.Reason
	} else if p.Outcome == BranchDeleted {
		branch += " — merged into " + p.MainBranch
	} else {
		branch += " — not merged into " + p.MainBranch
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
		fmt.Fprintf(w, "  the branch will be deleted\n")
	case BranchKept:
		fmt.Fprintf(w, "  the branch will be kept as %q\n", p.KeepAs)
	default:
		fmt.Fprintln(w, "  no branch will be touched")
	}
	fmt.Fprintln(w)
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
			return err
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
