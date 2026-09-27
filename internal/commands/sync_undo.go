package commands

import (
	"errors"
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
	// Journal records what undo put back, for --json; nil records nothing.
	Journal *RunJournal
	verbOptions
}

// SyncUndo puts back every ref the newest run touching work's branch moved:
// the run's whole set of safety refs, restored together.
func SyncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error {
	return journaled(opts.Journal, func() error { return syncUndo(ctx, work, opts, w) })
}

func syncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error {
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
	name := worktreeName(ctx, target.Branch, target.Path)
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
			opts.Journal.fail(errors.New("not confirmed: " + undidNothing.line))
			return nil
		}
		agents = fresh
	}
	// Every branch joins before anything is put back, so a signal between
	// one and the next can tell which it caught moved: its tip, or the
	// rebase it was waiting in, is not what it was.
	if j := opts.Journal; j != nil {
		j.repo(ctx.Repo.MainRoot)
		rebasing := map[string]bool{}
		for _, b := range branches {
			tip, _ := ctx.Repo.ResolveRef("refs/heads/" + b)
			j.join(worktreeName(ctx, b, paths[b]), b, paths[b], tip)
			if path, ok := paths[b]; ok {
				rebasing[b], _ = wtsync.RebaseInProgress(path)
			}
		}
		j.onSignal(func(p *SyncParticipant) bool {
			tip, _ := ctx.Repo.ResolveRef("refs/heads/" + p.Branch)
			busy := false
			if p.Path != "" {
				busy, _ = wtsync.RebaseInProgress(p.Path)
			}
			return strp(tip) == nil || p.Before == nil || tip != *p.Before || busy != rebasing[p.Branch]
		})
	}
	// Undo can fail partway through the second (apply) phase, after some
	// branches are already back at their safety tip: those still get
	// reported, so a partial restore is visible rather than silent.
	restored, err := wtsync.Undo(ctx.Repo.MainRoot, worktrees, agents, target.Branch, opts.now(), opts.Force)
	if err != nil && len(restored) > 0 {
		// Stopped partway: what it put back is reported below, and why it
		// stopped goes with it.
		opts.Journal.fail(err)
	}
	for _, r := range restored {
		rowWork := worktreeName(ctx, r.Branch, paths[r.Branch])
		fmt.Fprintln(w, restoredLine(rowWork, r))
		recordUndone(opts.Journal, rowWork, r)
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

// recordUndone records what undo did to one branch: put back, found there
// already, or aborted and left where it was when the undo stopped.
func recordUndone(j *RunJournal, work string, r wtsync.Restored) {
	if j == nil {
		return
	}
	if !j.has(r.Branch) {
		j.join(work, r.Branch, r.Path, r.From)
	}
	j.setSync(r.Branch, func(p *SyncParticipant) {
		p.Before, p.SafetyRef, p.After = strp(r.From), strp(r.Ref), strp(r.To)
		switch {
		case r.NotRewound:
			p.Result, p.After = ResultNeedsRecovery, strp(r.From)
			p.Reason = strp("aborted the rebase, but not rewound to " + r.To)
			p.Recovery = strp("the old tip is under " + r.Ref + "; wt sync undo --force " + work + " rewinds it there")
		case r.From == r.To && !r.Aborted:
			p.Result, p.Reason = ResultSkipped, strp("already at its old tip")
		default:
			p.Result = ResultUndone
		}
		if r.Kept != "" {
			// A forced undo pinned what it discarded as a run of its own,
			// which undo takes back in turn.
			p.Reason = strp(fmt.Sprintf("%s kept under %s", r.KeptTip, r.Kept))
			p.UndoCommand = undoCommand(work)
		}
	})
}

// restoredLine is what undo says it did to one branch.
func restoredLine(name string, r wtsync.Restored) string {
	// A forced undo of a handover that moved both aborts and rewinds:
	// the rewind is the part a person must see.
	var line string
	switch {
	case r.NotRewound:
		line = fmt.Sprintf("%s  aborted the rebase; not rewound: still at %s, not %s  (%s)", name, git.ShortID(r.From, 7), git.ShortID(r.To, 7), r.Ref)
	case r.From != r.To:
		aborted := ""
		if r.Aborted {
			aborted = "aborted the rebase; "
		}
		line = fmt.Sprintf("%s  %s%s → %s  (%s)", name, aborted, git.ShortID(r.From, 7), git.ShortID(r.To, 7), r.Ref)
	case r.Aborted:
		line = fmt.Sprintf("%s  aborted the rebase; back at %s", name, git.ShortID(r.To, 7))
	default:
		line = fmt.Sprintf("%s  already at %s", name, git.ShortID(r.To, 7))
	}
	// What a forced undo discarded is under its own safety ref, and the
	// row is the one place that says where: for an aborted handover the
	// branch never moved, so nothing above names the commit it kept.
	if r.Kept != "" {
		line += fmt.Sprintf("; %s kept under %s, %s", git.ShortID(r.KeptTip, 7), r.Kept, wtsync.WayOut(wtsync.Way{Work: name, Result: true}))
	}
	return line
}
