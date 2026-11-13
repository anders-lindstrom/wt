package synccmd

import (
	"errors"

	"github.com/anlindstrom/wt/internal/agents"
	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// readLine reads the stack held on the line of branch against trunk: the
// layers named, or every local branch whose tip is on it but the backups.
func readLine(ctx *kit.Context, branch, trunkSHA string, layers []string) (wtsync.Line, error) {
	return wtsync.ReadLine(ctx.Repo.MainRoot, branch, trunkSHA, layers, ctx.UserConfig().RefSweepPatterns)
}

// stackLookAt is the look at the stack held on the line of the worktree
// look names, read from base (trunk, or where a rebase onto target starts):
// its layers, bottom first, and what holds each. A target on the line is
// refused.
func stackLookAt(ctx *kit.Context, base, trunkSHA string, target *OntoTarget, look *StackLook, listed []agents.Agent) (*syncrun.Stack, error) {
	wt, err := kit.Locate(ctx, look.Work)
	if err != nil {
		return nil, err
	}
	line, err := readLine(ctx, wt.Branch, base, look.Layers)
	if err != nil {
		return nil, err
	}
	if target != nil {
		if why := target.onLine(ctx, line, trunkSHA, kit.WorktreeName(ctx, wt.Branch, wt.Path)); why != "" {
			return nil, errors.New(why)
		}
	}
	holders, err := syncrun.ReadHolders(ctx, line, listed)
	if err != nil {
		return nil, err
	}
	return syncrun.StackOf(line, false, holders), nil
}
