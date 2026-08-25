package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Remove deletes the worktree holding a piece of work, then decides what
// happens to its branch.
func Remove(ctx *Context, spec string, w io.Writer) error {
	path, err := Path(ctx, spec)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no worktree at %s", path)
	}
	return RemoveAt(ctx, path, w)
}

// RemoveAt removes the worktree at path.
//
// The branch is read from the worktree rather than rebuilt from the work name:
// once the type can vary, the name no longer determines the branch, and a
// reconstructed name may belong to an unrelated branch that this would then
// delete. Two cases decline to name a branch at all — a detached HEAD, and a
// branch that does not follow the worktree convention and so belongs to
// someone else. In both, the checkout goes and no branch is touched.
func RemoveAt(ctx *Context, path string, w io.Writer) error {
	branch := ctx.Repo.BranchAt(path)
	if branch == "" {
		fmt.Fprintf(w, "Note: %s has no branch checked out; removing the worktree only\n", path)
	} else if _, _, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix); !ok {
		fmt.Fprintf(w, "Note: %s is not a worktree branch; removing the worktree only\n", branch)
		branch = ""
	}

	fmt.Fprintf(w, "Removing worktree at %s\n", path)
	if err := ctx.Repo.RemoveWorktree(path); err != nil {
		return err
	}

	if branch == "" || !ctx.Repo.BranchExists(branch) {
		fmt.Fprintln(w, "✓ worktree removed")
		return nil
	}

	if ctx.Repo.BranchExists(ctx.Config.MainBranch) &&
		ctx.Repo.IsMerged(branch, ctx.Config.MainBranch) {
		if err := ctx.Repo.DeleteBranch(branch); err != nil {
			return err
		}
		fmt.Fprintf(w, "✓ branch %s was merged into %s and has been deleted\n",
			branch, ctx.Config.MainBranch)
		return nil
	}

	kept := naming.StripPrefix(branch, ctx.Config.TypeSuffix)
	if err := ctx.Repo.RenameBranch(branch, kept); err != nil {
		fmt.Fprintf(w, "✓ worktree removed; keeping branch %s (not merged into %s)\n",
			branch, ctx.Config.MainBranch)
		return nil
	}
	fmt.Fprintf(w, "✓ worktree removed; branch kept as %s (not merged into %s)\n",
		kept, ctx.Config.MainBranch)
	fmt.Fprintf(w, "  delete it later with: git branch -d %s\n", kept)
	return nil
}
