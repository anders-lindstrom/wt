package synccmd

import (
	"fmt"
	"io"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/repo"
)

// Up is wt up: the one worktree named, the one you are in by default,
// rebased onto trunk only when it goes through without needing you — wt sync
// rebase <work> --if-ready. Anything else is refused untouched and the error
// says what would stop it. Unlike wt sync rebase it also works where trunk
// declares no .wt-sync.yaml: there nothing is declared, so only a
// conflict-free rebase goes.
func Up(ctx *kit.Context, arg string, opts RunOptions, w io.Writer) error {
	return syncrun.Journaled(opts.Journal, func() error { return up(ctx, arg, opts, w) })
}

func up(ctx *kit.Context, arg string, opts RunOptions, w io.Writer) error {
	if arg == "/" || (arg == "." && repo.SamePath(ctx.Repo.Root, ctx.Repo.MainRoot)) {
		return fmt.Errorf("wt up brings a worktree onto trunk, and %s is the main checkout: "+
			"git pull there, or wt sync --rebase for every worktree", ctx.Repo.MainRoot)
	}
	wt, err := kit.Locate(ctx, arg)
	if err != nil {
		return err
	}
	if opts.Onto != "" && opts.Expect != "" {
		work := kit.WorktreeName(ctx, wt.Branch, wt.Path)
		return fmt.Errorf("wt up --expect takes the token wt status --json gives, which knows no --onto: take it from "+
			"wt sync %s --onto %s --json, and run wt sync rebase %s --onto %s --expect <token>", work, opts.Onto, work, opts.Onto)
	}
	opts.IfReady, opts.label, opts.undeclaredOK, opts.planExpect = true, "wt up", true, true
	_, err = RunSync(ctx, []string{wt.Path}, opts, w)
	return err
}
