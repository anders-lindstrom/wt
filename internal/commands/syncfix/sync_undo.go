package syncfix

import (
	"errors"
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
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
	Journal *synccmd.RunJournal
	syncrun.VerbOptions
}

// SyncUndo puts back every ref the newest run touching work's branch moved:
// the run's whole set of safety refs, restored together.
func SyncUndo(ctx *kit.Context, work string, opts UndoOptions, w io.Writer) error {
	return synccmd.Journaled(opts.Journal, func() error { return syncUndo(ctx, work, opts, w) })
}

func syncUndo(ctx *kit.Context, work string, opts UndoOptions, w io.Writer) error {
	// Undo never rebases, so an interrupt has only the locks to release.
	defer kit.WatchSignals(w, nil)()

	target, err := kit.LocateBranch(ctx, work)
	if err != nil {
		return err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	listed, err := opts.SessionsNow(ctx, syncrun.UndidNothing)
	if err != nil {
		return err
	}
	name := kit.WorktreeName(ctx, target.Branch, target.Path)
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
	var told []syncrun.Idle
	anyIdle := false
	for _, b := range branches {
		path, ok := paths[b]
		if !ok {
			continue
		}
		sessions := agents.SessionsAt(listed, path)
		if len(sessions.Busy()) > 0 {
			return fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", b, sessions.Label(kit.SessionLabel))
		}
		// Every checkout is listed for the re-check, not only the ones a
		// session is in now: one arriving in an empty checkout while the
		// question waits is exactly what the second listing is for.
		told = append(told, syncrun.Idle{Label: b, Path: path, Sessions: sessions})
		if len(sessions) > 0 {
			anyIdle = true
			fmt.Fprintln(w, syncrun.IdleNotice(kit.BranchWorkName(ctx, b), sessions))
		}
	}
	if anyIdle {
		ok, fresh, err := syncrun.AskIdle(ctx, w, opts.VerbOptions, []string{name}, told, listed, syncrun.UndidNothing)
		if err != nil {
			return err
		}
		if !ok {
			opts.Journal.FailWith(errors.New("not confirmed: " + syncrun.UndidNothing.Line))
			return nil
		}
		listed = fresh
	}
	// Every branch joins before anything is put back, so a signal between
	// one and the next can tell which it caught moved: its tip, or the
	// rebase it was waiting in, is not what it was.
	if j := opts.Journal; j != nil {
		j.Repo(ctx.Repo.MainRoot)
		rebasing := map[string]bool{}
		for _, b := range branches {
			// Unreadable is recorded as none, which OnSignal reads as moved.
			tip, _, _ := ctx.Repo.ResolveRef("refs/heads/" + b)
			j.Join(kit.WorktreeName(ctx, b, paths[b]), b, paths[b], tip, nil)
			if path, ok := paths[b]; ok {
				rebasing[b], _ = wtsync.RebaseInProgress(path)
			}
		}
		j.OnSignal(func(p *synccmd.SyncParticipant) bool {
			tip, _, err := ctx.Repo.ResolveRef("refs/heads/" + p.Branch)
			busy := false
			if p.Path != "" {
				busy, _ = wtsync.RebaseInProgress(p.Path)
			}
			return err != nil || kit.Strp(tip) == nil || p.Before == nil || tip != *p.Before || busy != rebasing[p.Branch]
		})
	}
	// Undo can fail partway through the second (apply) phase, after some
	// branches are already back at their safety tip: those still get
	// reported, so a partial restore is visible rather than silent.
	restored, err := wtsync.Undo(ctx.Repo.MainRoot, worktrees, listed, target.Branch, opts.Clock(), opts.Force)
	if err != nil && len(restored) > 0 {
		// Stopped partway: what it put back is reported below, and why it
		// stopped goes with it.
		opts.Journal.FailWith(err)
	}
	for _, r := range restored {
		rowWork := kit.WorktreeName(ctx, r.Branch, paths[r.Branch])
		fmt.Fprintln(w, restoredLine(rowWork, r))
		recordUndone(opts.Journal, rowWork, r)
		switch {
		case r.From == r.To && !r.Aborted:
			// Nothing moved under anybody.
		case r.NotRewound:
			syncrun.TellIdle(w, syncrun.SessionsOf(told, r.Branch), wtsync.UndoneLine(rowWork, git.ShortID(r.From, 7), false))
		default:
			syncrun.TellIdle(w, syncrun.SessionsOf(told, r.Branch), wtsync.UndoneLine(rowWork, git.ShortID(r.To, 7), true))
		}
	}
	return err
}

// recordUndone records what undo did to one branch: put back, found there
// already, or aborted and left where it was when the undo stopped.
func recordUndone(j *synccmd.RunJournal, work string, r wtsync.Restored) {
	if j == nil {
		return
	}
	if !j.Has(r.Branch) {
		j.Join(work, r.Branch, r.Path, r.From, nil)
	}
	j.SetSync(r.Branch, func(p *synccmd.SyncParticipant) {
		p.Before, p.SafetyRef, p.After = kit.Strp(r.From), kit.Strp(r.Ref), kit.Strp(r.To)
		switch {
		case r.NotRewound:
			p.Result, p.After = synccmd.ResultNeedsRecovery, kit.Strp(r.From)
			p.Reason = kit.Strp("aborted the rebase, but not rewound to " + r.To)
			p.Recovery = kit.Strp("the old tip is under " + r.Ref + "; wt sync undo --force " + work + " rewinds it there")
		case r.From == r.To && !r.Aborted:
			p.Result, p.Reason = synccmd.ResultSkipped, kit.Strp("already at its old tip")
		default:
			p.Result = synccmd.ResultUndone
		}
		if r.Kept != "" {
			// A forced undo pinned what it discarded as a run of its own,
			// which undo takes back in turn.
			p.Reason = kit.Strp(fmt.Sprintf("%s kept under %s", r.KeptTip, r.Kept))
			p.UndoCommand = synccmd.UndoCommand(work)
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
