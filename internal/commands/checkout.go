package commands

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

var unsafeInWorkName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// WorkNameFromBranch derives a directory-safe work name from a branch name.
// A worktree branch loses its type prefix — fix_wt/login-crash becomes
// login-crash rather than fix_wt-login-crash — and anything else has unsafe
// runs collapsed to a single dash.
func WorkNameFromBranch(branch, suffix string) string {
	work := naming.StripPrefix(branch, suffix)
	work = unsafeInWorkName.ReplaceAllString(work, "-")
	return strings.Trim(work, "-")
}

// Checkout puts a worktree on an existing branch, for reviewing a pull request
// or picking up work that already has a branch. It never creates a branch: if
// the branch is not there, that is a mistake worth reporting rather than
// quietly inventing one.
func Checkout(ctx *Context, branch, work string, opts NewOptions, w io.Writer) (string, error) {
	if branch == "" {
		return "", fmt.Errorf("no branch given")
	}
	if !ctx.Repo.BranchExists(branch) {
		return "", fmt.Errorf("branch %s does not exist; use `wt new` to create one", branch)
	}
	if work == "" {
		work = WorkNameFromBranch(branch, ctx.Config.TypeSuffix)
		if work == "" {
			return "", fmt.Errorf("could not derive a work name from branch %q", branch)
		}
		if work != branch {
			fmt.Fprintf(w, "Using work name %q for branch %s\n", work, branch)
		}
	}

	path := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name,
		ctx.Config.DefaultType, work, ctx.Config.TypeSuffix)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}

	fmt.Fprintf(w, "Checking out %s at %s\n", branch, path)
	if err := ctx.Repo.AddExistingWorktree(path, branch); err != nil {
		return "", err
	}
	if opts.NoSetup {
		return path, nil
	}
	if err := Setup(ctx, path, SetupOptions{
		Source:    ctx.Repo.MainRoot,
		SkipBuild: opts.SkipBuild,
	}, w); err != nil {
		return path, err
	}
	return path, nil
}
