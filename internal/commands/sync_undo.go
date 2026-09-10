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
	// Force undoes a branch that has moved since the run, pinning a fresh
	// safety ref at the tip it discards first.
	Force bool
}

// SyncUndo puts back every ref the newest run touching work's branch moved:
// the run's whole set of safety refs, restored together.
func SyncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error {
	// Undo never rebases, so an interrupt has only the locks to release.
	defer watchSignals(w, nil)()

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
		if agents, err = wtsync.ListOtherAgents(); err != nil {
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
	restored, err := wtsync.Undo(ctx.Repo.MainRoot, worktrees, agents, target.Branch, now(), opts.Force)
	for _, r := range restored {
		fmt.Fprintln(w, restoredLine(workName(ctx, r.Branch), r))
	}
	return err
}

// restoredLine is what undo says it did to one branch.
func restoredLine(name string, r wtsync.Restored) string {
	// A forced undo of a handover that moved both aborts and rewinds:
	// the rewind is the part a person must see.
	switch {
	case r.NotRewound:
		return fmt.Sprintf("%s  aborted the rebase; not rewound: still at %s, not %s  (%s)", name, short(r.From), short(r.To), r.Ref)
	case r.From != r.To:
		aborted := ""
		if r.Aborted {
			aborted = "aborted the rebase; "
		}
		return fmt.Sprintf("%s  %s%s → %s  (%s)", name, aborted, short(r.From), short(r.To), r.Ref)
	case r.Aborted:
		return fmt.Sprintf("%s  aborted the rebase; back at %s", name, short(r.To))
	default:
		return fmt.Sprintf("%s  already at %s", name, short(r.To))
	}
}
