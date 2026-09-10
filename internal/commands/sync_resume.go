package commands

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ResumeOptions tunes SyncResume for callers and tests.
type ResumeOptions struct {
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	Now    func() time.Time
}

// SyncResume continues the rebase a run left at a stop a person owned. It
// verifies the worktree is still what the run left, then drives the same
// loop to the end: the strategies at any later stop, the deferred steps, the
// result ref, the push line. A later stop a person owns is handed over
// again, with a fresh plan file.
//
// A rebase somebody finished themselves with `git rebase --continue` is
// tolerated: there is nothing to continue, so only what follows the rebase
// runs — after checking that it really finished rather than being aborted.
// Nothing here ever resets the worktree: a person's own resolution is in it.
func SyncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) error {
	tracker := &rebaseTracker{}
	defer watchSignals(w, tracker)()

	target, err := Locate(ctx, work)
	if err != nil {
		return err
	}
	if target.Branch == "" {
		return fmt.Errorf("%s has no branch", work)
	}
	gitDir, err := wtsync.GitDir(target.Path)
	if err != nil {
		return err
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s was not left mid-rebase by wt sync run: nothing to resume", work)
	}
	if st.Branch != target.Branch {
		return fmt.Errorf("the handover in %s is for %s, not %s; nothing is resumed", gitDir, st.Branch, target.Branch)
	}
	// The sidecar must describe the run it claims to: a safety ref built
	// from a different branch or epoch is somebody else's, and pinning the
	// wrong old tip would make undo restore to the wrong commit.
	if want := wtsync.SafetyRef(st.Branch, st.Epoch); st.Safety != want {
		return fmt.Errorf("the handover names %s, not %s; nothing is resumed", st.Safety, want)
	}
	tip, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", st.Safety)
	if err != nil {
		return fmt.Errorf("the safety ref %s is gone; nothing is resumed", st.Safety)
	}
	if tip != st.OldTip {
		return fmt.Errorf("%s pins %s but the handover says %s; nothing is resumed", st.Safety, short(tip), short(st.OldTip))
	}
	agents := opts.Agents
	if agents == nil {
		if agents, err = wtsync.ListAgents(); err != nil {
			return fmt.Errorf("cannot list agent sessions (%v); nothing is resumed", err)
		}
	}
	agentPath := target.Path
	if resolved, rerr := filepath.EvalSymlinks(target.Path); rerr == nil {
		agentPath = resolved
	}
	if a := wtsync.AgentAt(agents, agentPath); a != nil {
		return fmt.Errorf("an agent session is in %s: %s; nothing is resumed", work, sessionLabel(a))
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lock, err := wtsync.TakeOver(gitDir, now(), st.Lock)
	if err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", work, err)
	}
	defer func() {
		if lock != nil {
			_ = lock.Release()
		}
	}()
	// The run's trunk, not today's: a resume that read a newer declaration
	// would apply strategies the stopped rebase was never planned with.
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, st.Trunk)
	if err != nil {
		return err
	}
	busy, err := wtsync.RebaseInProgress(target.Path)
	if err != nil {
		return err
	}
	if busy {
		if err := verifyHandover(target.Path, st, w); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "%s  the rebase is already finished; running what is left\n", work)
	}
	// Stacked is deliberately not set. It exists so a run does not leave a
	// branch its children are about to be rebased onto waiting on a person;
	// a resume rebases exactly one worktree, so there is no child in flight
	// to strand, and a later unclaimed stop is better answered with a fresh
	// plan file than with a refusal whose only remedy is undo.
	req := wtsync.Request{
		Path: target.Path, Branch: st.Branch, Trunk: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch,
		Work: cmp.Or(st.Work, work),
	}
	safety := wtsync.Safety{Branch: st.Branch, Epoch: st.Epoch, Ref: st.Safety, Tip: st.OldTip}
	tracker.set(&rebaseInFlight{work: work, path: target.Path, safety: st.Safety})
	res, rerr := wtsync.Resume(ctx.Repo.MainRoot, cfg, req, st.OldTip, safety, w)
	tracker.set(nil)
	if rerr != nil {
		return fmt.Errorf("%s: %w", work, rerr)
	}
	if res.Left != nil {
		fmt.Fprintf(w, "  stopped again at %d/%d\n", res.Left.Index, res.Left.Total)
		if err := handOver(ctx, w, handoverInput{
			Work: work, Branch: st.Branch, Path: target.Path, TrunkRef: st.TrunkRef, TrunkSHA: st.Trunk,
			Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Cfg: cfg, Res: res, Lock: lock,
		}); err != nil {
			return err
		}
		lock = nil // kept on purpose
		return fmt.Errorf("not completed: %s (needs you)", work)
	}
	fmt.Fprintf(w, "  rebased %d commit%s\n", res.Replayed, plural(res.Replayed))
	tracker.set(&rebaseInFlight{work: work, path: target.Path, rebased: true})
	_, owed, cerr := completeRun(ctx, w, cfg, completeInput{
		Work: work, Branch: st.Branch, Path: target.Path, Epoch: st.Epoch, Res: res,
	})
	tracker.set(nil)
	if cerr != nil {
		return cerr
	}
	if len(owed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(owed, ", "))
	}
	return nil
}

// verifyHandover refuses to continue a rebase that is not what the run left.
// Three things are checked, and none of them is something the tool may
// repair by re-running a strategy: that would overwrite a person's work.
//
//   - Something still unmerged means the person is not done.
//   - A tracked file changed but not staged makes git refuse to continue,
//     and the loop's did-not-advance guard would read that refusal as a
//     stuck rebase. In a run that means a restore; here it would mean
//     throwing away the resolution. Refusing early is the only safe answer.
//   - A different blob under a strategy's name means a file the brief said
//     never to hand-merge was hand-merged.
//
// The blob comparison only holds while the rebase is still at the stop the
// handover recorded. If somebody continued by hand to a later stop, those
// paths belong to a stop that is now history, so the comparison is skipped
// and said out loud rather than turned into a false refusal.
func verifyHandover(wtPath string, st wtsync.State, w io.Writer) error {
	unmerged, err := wtsync.StagedConflicts(wtPath)
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		var ps []string
		for _, c := range unmerged {
			ps = append(ps, c.Path)
		}
		return fmt.Errorf("still unmerged: %s; resolve them, git add them, then resume", strings.Join(ps, ", "))
	}
	// --untracked-files=no: an untracked file never blocks a rebase, and a
	// script may have left one.
	dirty, err := git.Run(wtPath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	var unstaged []string
	for _, line := range strings.Split(dirty, "\n") {
		// The second status column is the worktree against the index: any
		// mark there is a change git will refuse to continue over.
		if len(line) > 3 && line[1] != ' ' {
			unstaged = append(unstaged, strings.TrimSpace(line[3:]))
		}
	}
	if len(unstaged) > 0 {
		return fmt.Errorf("changed but not staged: %s; git add them (git refuses to continue otherwise), then resume", strings.Join(unstaged, ", "))
	}
	p, err := wtsync.RebaseProgress(wtPath)
	if err != nil {
		return err
	}
	if p.Index != st.Stop {
		fmt.Fprintf(w, "  note: the rebase is at %d/%d, not the %d/%d the plan describes; what the strategies staged there is already committed and is not re-checked\n",
			p.Index, p.Total, st.Stop, st.Total)
		return nil
	}
	var changed []string
	for path := range st.Resolved {
		cur, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path)
		if err != nil {
			return fmt.Errorf("%s is no longer staged and %s owns it; wt sync undo %s and start again", path, st.Strategy[path], st.Work)
		}
		if cur != st.Resolved[path] {
			changed = append(changed, path)
		}
	}
	for _, path := range st.Deleted {
		if _, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path); err == nil {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	var named []string
	for _, p := range changed {
		fmt.Fprintf(w, "  %s was hand-merged; %s owns it\n", p, st.Strategy[p])
		if oid := st.Resolved[p]; oid != "" {
			fmt.Fprintf(w, "    put it back: git -C %s show %s > %s && git -C %s add -- %s\n",
				wtPath, oid, filepath.Join(wtPath, p), wtPath, p)
		} else {
			fmt.Fprintf(w, "    put it back: git -C %s rm --cached -- %s && rm %s\n", wtPath, p, filepath.Join(wtPath, p))
		}
		named = append(named, p+" ("+st.Strategy[p]+")")
	}
	if len(changed) > 0 {
		return fmt.Errorf("%d file%s under \"never hand-merge here\" changed: %s; nothing is resumed",
			len(changed), plural(len(changed)), strings.Join(named, ", "))
	}
	return nil
}
