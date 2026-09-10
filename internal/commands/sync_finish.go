package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

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
}

// handOver leaves the worktree mid-rebase for a person: the §6 brief, the
// sidecar resume and undo verify against, the lock left behind, and the §5
// line. The brief is written first and the sidecar second, both atomically:
// the sidecar is the marker, so a crash between them leaves a brief nothing
// acts on rather than a marker with no brief. The caller must not release
// the lock afterwards — Keep has already taken it off this process's books.
func handOver(ctx *Context, w io.Writer, in handoverInput) error {
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
	st := wtsync.State{
		Branch: in.Branch, Work: in.Work, Trunk: in.TrunkSHA, TrunkRef: in.TrunkRef,
		Onto: in.Onto, Upstream: in.Upstream, Epoch: in.Epoch,
		Safety: in.Res.Safety.Ref, OldTip: in.Res.OldTip,
		Stop: in.Res.Left.Index, Total: in.Res.Left.Total,
		Resolved: in.Res.Left.Staged, Strategy: map[string]string{},
		Deleted: in.Res.Left.Deleted, Left: in.Res.Left.Left,
	}
	for _, f := range in.Res.Left.Files {
		if f.Resolved {
			st.Strategy[f.Path] = f.Strategy
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
	fmt.Fprintf(w, "  plan %s\n", wtsync.PlanPath(gitDir))
	fmt.Fprintf(w, "  %s\n", wtsync.NeedsYouLine(in.Work, in.Res.Left.Left))
	return nil
}

// completeInput is what the tail after a finished rebase needs.
type completeInput struct {
	Work, Branch, Path string
	Epoch              int64
	Res                wtsync.Result
}

// completeRun is what run and resume both do once a rebase has finished: the
// deferred steps, the result ref that says where the run left the branch,
// the handover removed, and the push line. owed names the deferred steps
// that failed — the rebase stands regardless (spec §3).
func completeRun(ctx *Context, w io.Writer, cfg *wtsync.Config, in completeInput) (head string, owed []string, err error) {
	// w, not nil: RunDeferred announces each step as it starts, so a long
	// one is not silence until printDeferred reports the result.
	results, err := wtsync.RunDeferred(in.Path, cfg.Defer, in.Res.OldTip, in.Res.NewTip, w)
	if err != nil {
		return "", nil, err
	}
	for _, d := range results {
		printDeferred(w, d)
		if d.Err != nil {
			owed = append(owed, in.Work+" (owed: "+d.Step.Run+")")
		}
	}
	if head, err = git.Run(in.Path, "rev-parse", "HEAD"); err != nil {
		return "", owed, err
	}
	if err = wtsync.WriteResult(ctx.Repo.MainRoot, in.Branch, head, in.Epoch); err != nil {
		return head, owed, err
	}
	gitDir, err := wtsync.GitDir(in.Path)
	if err != nil {
		return head, owed, err
	}
	if err := wtsync.RemovePlan(gitDir); err != nil {
		return head, owed, err
	}
	fmt.Fprintf(w, "  push: git -C %s push --force-with-lease\n", in.Path)
	return head, owed, nil
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
