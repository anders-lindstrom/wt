package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Up is wt up: the one worktree named, the one you are in by default,
// rebased onto trunk only when it goes through without needing you — wt sync
// <work> --run --if-ready. Anything else is refused untouched and the error
// says what would stop it. Unlike wt sync it also works where trunk declares
// no .wt-sync.yaml: there nothing is declared, so only a conflict-free rebase
// goes.
func Up(ctx *Context, arg string, opts RunOptions, w io.Writer) (err error) {
	if j := opts.Journal; j != nil {
		defer setInterruptJournal(j)()
		defer func() {
			// An error before any participant was touched is the run's
			// own; one after is in the participants already.
			if err != nil && j.untouched() {
				j.fail(err)
			}
			j.Finish()
		}()
	}
	if arg == "/" || (arg == "." && repo.SamePath(ctx.Repo.Root, ctx.Repo.MainRoot)) {
		return fmt.Errorf("wt up brings a worktree onto trunk, and %s is the main checkout: "+
			"git pull there, or wt sync --run for every worktree", ctx.Repo.MainRoot)
	}
	wt, err := Locate(ctx, arg)
	if err != nil {
		return err
	}
	opts.IfReady, opts.label, opts.undeclaredOK = true, "wt up", true
	return SyncRun(ctx, []string{wt.Path}, opts, w)
}
