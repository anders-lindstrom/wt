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
	// Yes, Confirm and Relist are RunOptions' own. Undo asks only when idle
	// sessions are in a checkout it would put back; a nil Confirm never asks.
	Yes     bool
	Confirm func(works []string) (bool, error)
	Relist  func() ([]wtsync.Agent, error)
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
	name := workName(ctx, target.Branch)
	branches, err := wtsync.UndoBranches(ctx.Repo.MainRoot, target.Branch)
	if err != nil {
		return err
	}
	paths := map[string]string{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			paths[wt.Branch] = wt.Path
		}
	}
	// Every session is named and asked about here, before wtsync.Undo takes
	// a single lock: taking over a handover's lock and then hearing no would
	// release it.
	told := map[string]wtsync.Sessions{}
	for _, b := range branches {
		path, ok := paths[b]
		if !ok {
			continue
		}
		sessions := wtsync.SessionsAt(agents, path)
		if len(sessions.Busy()) > 0 {
			return fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", b, sessions.Label(sessionLabel))
		}
		if len(sessions) > 0 {
			told[b] = sessions
			fmt.Fprintln(w, idleNotice(workName(ctx, b), sessions))
		}
	}
	if len(told) > 0 && opts.Confirm != nil && !opts.Yes {
		ok, err := opts.Confirm([]string{name})
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "nothing undone")
			return nil
		}
		if agents, err = listAgain(opts.Agents, opts.Relist); err != nil {
			return fmt.Errorf("cannot list agent sessions again (%v); nothing undone", err)
		}
		for _, b := range branches {
			if path, ok := paths[b]; ok {
				if why := sessionsChanged(told[b], wtsync.SessionsAt(agents, path)); why != "" {
					return fmt.Errorf("%s: %s; nothing undone", b, why)
				}
			}
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
		rowWork := workName(ctx, r.Branch)
		fmt.Fprintln(w, restoredLine(rowWork, r))
		switch {
		case r.From == r.To && !r.Aborted:
			// Nothing moved under anybody.
		case r.NotRewound:
			tellIdle(w, told[r.Branch], wtsync.UndoneLine(rowWork, short(r.From), false))
		default:
			tellIdle(w, told[r.Branch], wtsync.UndoneLine(rowWork, short(r.To), true))
		}
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
