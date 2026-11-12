package synccmd

import (
	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// readLine reads the stack held on the line of branch against trunk: the
// layers named, or every local branch whose tip is on it but the backups.
func readLine(ctx *kit.Context, branch, trunkSHA string, layers []string) (wtsync.Line, error) {
	return wtsync.ReadLine(ctx.Repo.MainRoot, branch, trunkSHA, layers, ctx.UserConfig().RefSweepPatterns)
}

// stackLook is the look at the stack held on the line of the worktree look
// names: its layers, bottom first, and what holds each.
func stackLook(ctx *kit.Context, trunkSHA string, look *StackLook, listed []agents.Agent) (*syncrun.Stack, error) {
	wt, err := kit.Locate(ctx, look.Work)
	if err != nil {
		return nil, err
	}
	line, err := readLine(ctx, wt.Branch, trunkSHA, look.Layers)
	if err != nil {
		return nil, err
	}
	holders, err := syncrun.ReadHolders(ctx, line, listed)
	if err != nil {
		return nil, err
	}
	return syncrun.StackOf(line, false, holders), nil
}
