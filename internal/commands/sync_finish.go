package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The tracker an interrupt reads is set and cleared here, and nowhere else,
// so run and resume change its answer at the same moments:
//
//   - rebasing (resuming for a resume) from just before the rebase starts.
//     A finished or failed rebase clears it as it returns. A stop being
//     handed over is still a rebase stopped in the worktree, so it stays
//     set until handOver returns, written or not: an interrupt meanwhile
//     still names the worktree and what puts it back.
//   - rebased from just before completeRun starts, its input included,
//     until it returns. The rebase is done; from there an interrupt cannot
//     abort it, only leave the deferred steps and the result ref undone.
//   - nothing in between: there is no worktree to say anything about.

// trackedRebase runs rebase with the tracker set to at for as long as the
// rebase is stopped in the worktree, as described above.
func trackedRebase(t *rebaseTracker, at *rebaseInFlight, rebase func() (wtsync.Result, error)) (wtsync.Result, error) {
	t.set(at)
	res, err := rebase()
	if err != nil || res.Left == nil {
		t.set(nil)
	}
	return res, err
}

// handoverInput is what writing a handover needs beyond the rebase's own
// result: the names a person reads, and the trunk the run was planned
// against.
type handoverInput struct {
	Work     string
	Branch   string
	Path     string
	TrunkRef string
	TrunkSHA string
	Onto     string
	Upstream string
	Epoch    int64
	Cfg      *wtsync.Config
	Res      wtsync.Result
	Lock     *wtsync.Lock
	// Earlier is the paths the run's earlier handovers stopped on.
	Earlier []string
	// Tracker is the verb's, still set to the rebase being handed over; it
	// is cleared once the handover is written or has failed.
	Tracker *rebaseTracker
}

// handOver leaves the worktree mid-rebase for a person: the §6 brief, the
// sidecar resume and undo verify against, the lock left behind, and the §5
// line. The brief is written first and the sidecar second, both atomically:
// the sidecar is the marker, so a crash between them leaves a brief nothing
// acts on rather than a marker with no brief. Keep is called only once the
// sidecar exists, and a kept lock ignores Release, so a caller that releases
// it — or a defer it did not write — cannot delete the file the next run has
// to respect.
func handOver(ctx *Context, w io.Writer, in handoverInput) error {
	// The rebase is stopped in the worktree until this returns, however it
	// returns, and the tracker says so for exactly that long.
	defer in.Tracker.set(nil)
	if in.Res.Left == nil {
		return fmt.Errorf("%s: nothing to hand over", in.Work)
	}
	gitDir, err := wtsync.GitDir(in.Path)
	if err != nil {
		return err
	}
	base, err := git.Run(ctx.Repo.MainRoot, "merge-base", in.TrunkSHA, in.Res.OldTip)
	if err != nil {
		return fmt.Errorf("merge-base: %w", err)
	}
	landing, err := wtsync.LandingList(ctx.Repo.MainRoot, base, in.TrunkSHA)
	if err != nil {
		return err
	}
	plan, err := wtsync.RenderPlan(wtsync.PlanInput{
		MainRoot: ctx.Repo.MainRoot, Work: in.Work, Branch: in.Branch, TrunkRef: in.TrunkRef,
		Trunk: in.TrunkSHA, Base: base, Landing: landing, Handover: *in.Res.Left, Config: in.Cfg,
	})
	if err != nil {
		return err
	}
	if err := wtsync.WritePlanFile(gitDir, plan); err != nil {
		return err
	}
	// Where the rebase is: detached, at the last pick before the stop. A
	// resume that stops again hands over through here too, so it is fresh
	// at every stop.
	head, err := git.Run(in.Path, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	st := wtsync.State{
		Branch: in.Branch, Work: in.Work, Trunk: in.TrunkSHA, TrunkRef: in.TrunkRef,
		Onto: in.Onto, Upstream: in.Upstream, Epoch: in.Epoch,
		Safety: in.Res.Safety.Ref, OldTip: in.Res.OldTip, Head: head,
		Stop: in.Res.Left.Index, Total: in.Res.Left.Total,
		Resolved: in.Res.Left.Staged, Strategy: map[string]string{}, Lifted: map[string]string{},
		Deleted: in.Res.Left.Deleted, Left: in.Res.Left.Left,
		Stopped: pathsOnce(in.Earlier, wtsync.StopPaths(in.Res.Stops)),
	}
	for _, f := range in.Res.Left.Files {
		if f.Resolved {
			st.Strategy[f.Path] = f.Strategy
		}
		if f.Lifted && f.Resolved {
			st.Lifted[f.Path] = f.Note
		}
	}
	if in.Lock != nil {
		st.Lock = wtsync.LeftLock{PID: in.Lock.PID, Started: in.Lock.Started.Unix()}
	}
	if err := wtsync.WriteState(gitDir, st); err != nil {
		return err
	}
	// Only now: until the sidecar exists nothing can take this lock over.
	if in.Lock != nil {
		in.Lock.Keep()
	}
	fmt.Fprintf(w, "  ⚠ %s\n", wtsync.NeedsYouLine(in.Work, in.Res.Left.Left))
	fmt.Fprintf(w, "    plan %s\n", wtsync.PlanPath(gitDir))
	return nil
}

// completeInput is what the tail after a finished rebase needs beyond the
// worktree itself.
type completeInput struct {
	Branch string
	Epoch  int64
	Res    wtsync.Result
	// Tell are the idle sessions in the worktree. With any, the finish ends
	// with the line to relay to them: Landed trunk commits on TrunkName, and
	// the Check files the rebase stopped on.
	Tell wtsync.Sessions
	// TrunkName is trunk as a person says it, "main" rather than a commit.
	// Every other Trunk a sync verb passes around is a SHA.
	TrunkName string
	Landed    int
	Check     []string
}

// completeRun is what run and resume both do once a rebase has finished: the
// deferred steps, the result ref that says where the run left the branch,
// the handover removed, and the undo line. owed is the deferred steps that
// failed, by name — the rebase stands regardless (spec §3), and whose they
// are is the caller's to say. The push is the caller's too, once every
// worktree is done.
//
// The worktree is work at path; t is the verb's tracker, and build is asked
// for the rest of the input only once t says rebased, so nothing the finish
// needs is prepared outside the span an interrupt reports as rebased.
func completeRun(ctx *Context, w io.Writer, cfg *wtsync.Config, t *rebaseTracker, work, path string, build func() completeInput) (head string, owed []string, err error) {
	// The rebase is done; from here an interrupt cannot abort it, only
	// leave the deferred steps and the result ref undone. Cleared last, so
	// the relay line below is still printed under it.
	t.set(&rebaseInFlight{work: work, path: path, rebased: true})
	defer t.set(nil)
	in := build()
	// Only ever called once a rebase has finished, so the files moved under
	// the idle sessions however this returns.
	defer tellIdle(w, in.Tell, wtsync.RebasedLine(work, in.TrunkName, in.Landed, in.Check))
	// w, not nil: RunDeferred announces each step as it starts, so a long
	// one is not silence until printDeferred reports the result.
	results, err := wtsync.RunDeferred(path, cfg.Defer, in.Res.OldTip, in.Res.NewTip, w)
	if err != nil {
		return "", nil, err
	}
	for _, d := range results {
		printDeferred(w, d)
		if d.Err != nil {
			owed = append(owed, d.Step.Run)
		}
	}
	if head, err = git.Run(path, "rev-parse", "HEAD"); err != nil {
		return "", owed, err
	}
	if err = wtsync.WriteResult(ctx.Repo.MainRoot, in.Branch, head, in.Epoch); err != nil {
		return head, owed, err
	}
	gitDir, err := wtsync.GitDir(path)
	if err != nil {
		return head, owed, err
	}
	if err := wtsync.RemovePlan(gitDir); err != nil {
		return head, owed, err
	}
	fmt.Fprintf(w, "  ↩ %s\n", wtsync.WayOut(wtsync.Way{Work: work, Result: true, Safety: git.ShortID(in.Res.OldTip, 7)}))
	return head, owed, nil
}

// owedBy names a finish's owed steps the way the closing error names them.
func owedBy(work string, steps []string) []string {
	named := make([]string, 0, len(steps))
	for _, s := range steps {
		named = append(named, work+" (owed: "+s+")")
	}
	return named
}

// clearHandover removes a handover after a rebase was put back rather than
// finished. A marker left behind would report the worktree as waiting on
// somebody who has nothing to do.
func clearHandover(w io.Writer, path string) {
	gitDir, err := wtsync.GitDir(path)
	if err == nil {
		err = wtsync.RemovePlan(gitDir)
	}
	if err != nil {
		fmt.Fprintf(w, "  note: could not remove the plan file: %v\n", err)
	}
}
