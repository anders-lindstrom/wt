package commands

import (
	"fmt"
	"io"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// UndoOptions tunes SyncUndo for callers and tests.
type UndoOptions struct {
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	Now    func() time.Time
}

// SyncUndo puts back every ref the newest run touching work's branch moved:
// the run's whole set of safety refs, restored together.
func SyncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error {
	target, err := Locate(ctx, work)
	if err != nil {
		return err
	}
	if target.Branch == "" {
		return fmt.Errorf("%s has no branch", work)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	agents := opts.Agents
	if agents == nil {
		if agents, err = wtsync.ListAgents(); err != nil {
			return fmt.Errorf("cannot list agent sessions (%v); nothing undone", err)
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	// Undo can fail partway through the second (apply) phase, after some
	// branches are already back at their safety tip: those still get
	// reported, so a partial restore is visible rather than silent.
	restored, err := wtsync.Undo(ctx.Repo.MainRoot, worktrees, agents, target.Branch, now())
	for _, r := range restored {
		name := workName(ctx, r.Branch)
		if r.From == r.To {
			fmt.Fprintf(w, "%s  already at %s\n", name, short(r.To))
			continue
		}
		fmt.Fprintf(w, "%s  %s → %s  (%s)\n", name, short(r.From), short(r.To), r.Ref)
	}
	return err
}
