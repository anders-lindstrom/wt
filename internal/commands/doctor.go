package commands

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Doctor reports configuration and worktree health, returning the number of
// problems found so the caller can set an exit code. It replaces
// setup_precheck.sh and additionally finds worktrees that are misplaced,
// unowned, or nested inside the main checkout.
func Doctor(ctx *Context, w io.Writer) (int, error) {
	problems := 0
	report := func(format string, args ...any) {
		problems++
		fmt.Fprintf(w, "  ! "+format+"\n", args...)
	}

	fmt.Fprintf(w, "Repository: %s (%s)\n", ctx.Repo.Name, ctx.Repo.MainRoot)

	fmt.Fprintln(w, "Required tools:")
	if len(ctx.Config.RequiredBins) == 0 {
		fmt.Fprintln(w, "  - none declared")
	}
	for _, bin := range ctx.Config.RequiredBins {
		if _, err := exec.LookPath(bin); err != nil {
			report("%s is declared in REQUIRED_BINS but not on PATH", bin)
		} else {
			fmt.Fprintf(w, "  ✓ %s\n", bin)
		}
	}

	if !ctx.Repo.BranchExists(ctx.Config.MainBranch) {
		report("MAIN_BRANCH %q does not exist locally", ctx.Config.MainBranch)
	}

	fmt.Fprintln(w, "Worktrees:")
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return problems, err
	}
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		// A worktree inside the main checkout pollutes the parent repo and
		// breaks tooling that walks it.
		if strings.HasPrefix(wt.Path, ctx.Repo.MainRoot+"/") {
			report("%s is inside the main checkout", wt.Path)
			continue
		}
		typ, work, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix)
		if !ok {
			fmt.Fprintf(w, "  - %s is on %q, not managed by wt\n", wt.Path, wt.Branch)
			continue
		}
		want := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
		if wt.Path != want {
			report("%s is not at its canonical path (%s); run: wt migrate %s/%s",
				wt.Path, want, typ, work)
			continue
		}
		fmt.Fprintf(w, "  ✓ %s\n", wt.Path)
	}

	if problems == 0 {
		fmt.Fprintln(w, "No problems found.")
	} else {
		fmt.Fprintf(w, "%d problem(s) found.\n", problems)
	}
	return problems, nil
}
