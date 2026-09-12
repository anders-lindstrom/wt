package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// UndoOptions tunes SyncUndo for callers and tests. Undo asks only when idle
// sessions are in a checkout it would put back.
type UndoOptions struct {
	// Force undoes a branch that has moved since the run, pinning a fresh
	// safety ref at the tip it discards first.
	Force bool
	verbOptions
}

// SyncUndo puts back every ref the newest run touching work's branch moved:
// the run's whole set of safety refs, restored together.
func SyncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error {
	// Undo never rebases, so an interrupt has only the locks to release.
	defer watchSignals(w, nil)()

	target, err := locateBranch(ctx, work)
	if err != nil {
		return err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	agents, err := opts.agents(undidNothing)
	if err != nil {
		return err
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
	var told []idle
	anyIdle := false
	for _, b := range branches {
		path, ok := paths[b]
		if !ok {
			continue
		}
		sessions := wtsync.SessionsAt(agents, path)
		if len(sessions.Busy()) > 0 {
			return fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", b, sessions.Label(sessionLabel))
		}
		// Every checkout is listed for the re-check, not only the ones a
		// session is in now: one arriving in an empty checkout while the
		// question waits is exactly what the second listing is for.
		told = append(told, idle{label: b, path: path, sessions: sessions})
		if len(sessions) > 0 {
			anyIdle = true
			fmt.Fprintln(w, idleNotice(workName(ctx, b), sessions))
		}
	}
	if anyIdle {
		ok, fresh, err := askIdle(w, opts.verbOptions, []string{name}, told, agents, undidNothing)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		agents = fresh
	}
	// Undo can fail partway through the second (apply) phase, after some
	// branches are already back at their safety tip: those still get
	// reported, so a partial restore is visible rather than silent.
	restored, err := wtsync.Undo(ctx.Repo.MainRoot, worktrees, agents, target.Branch, opts.now(), opts.Force)
	for _, r := range restored {
		rowWork := workName(ctx, r.Branch)
		fmt.Fprintln(w, restoredLine(rowWork, r))
		switch {
		case r.From == r.To && !r.Aborted:
			// Nothing moved under anybody.
		case r.NotRewound:
			tellIdle(w, sessionsOf(told, r.Branch), wtsync.UndoneLine(rowWork, git.ShortID(r.From, 7), false))
		default:
			tellIdle(w, sessionsOf(told, r.Branch), wtsync.UndoneLine(rowWork, git.ShortID(r.To, 7), true))
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
		return fmt.Sprintf("%s  aborted the rebase; not rewound: still at %s, not %s  (%s)", name, git.ShortID(r.From, 7), git.ShortID(r.To, 7), r.Ref)
	case r.From != r.To:
		aborted := ""
		if r.Aborted {
			aborted = "aborted the rebase; "
		}
		return fmt.Sprintf("%s  %s%s → %s  (%s)", name, aborted, git.ShortID(r.From, 7), git.ShortID(r.To, 7), r.Ref)
	case r.Aborted:
		return fmt.Sprintf("%s  aborted the rebase; back at %s", name, git.ShortID(r.To, 7))
	default:
		return fmt.Sprintf("%s  already at %s", name, git.ShortID(r.To, 7))
	}
}
