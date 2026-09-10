package commands

import (
	"cmp"
	"errors"
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
//
// Every refusal comes before the lock is taken over. Taking it and then
// refusing would release the lock the handover kept, so a refusal would not
// change nothing after all.
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
	// One name from here on: the one the run recorded, so every message and
	// a fresh handover say what the run said, whichever form was typed. An
	// old sidecar without one falls back to the argument, never the branch.
	st.Work = cmp.Or(st.Work, work)
	name := st.Work
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
		return fmt.Errorf("an agent session is in %s: %s; nothing is resumed", name, sessionLabel(a))
	}
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
		if err := verifySequencer(target.Path, st); err != nil {
			return err
		}
		if err := verifyHandover(target.Path, st, w); err != nil {
			return err
		}
	} else {
		if err := wtsync.VerifyFinished(target.Path, st.Branch, name, st.Onto, st.OldTip); err != nil {
			return fmt.Errorf("%s: %w; nothing is resumed", name, err)
		}
		fmt.Fprintf(w, "%s  the rebase is already finished; running what is left\n", name)
		if len(st.Resolved)+len(st.Deleted) > 0 {
			noteNotRechecked(w, fmt.Sprintf("it was finished by hand from the %d/%d the plan describes", st.Stop, st.Total), st)
		}
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lock, err := wtsync.TakeOver(gitDir, now(), st.Lock)
	if err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", name, err)
	}
	defer func() {
		if lock != nil {
			_ = lock.Release()
		}
	}()
	// The checks above ran without the lock. Another wt that ended this run
	// in between had to hold it to do so, so with the lock in hand the
	// handover is either still the one they read or gone.
	if cur, ok, err := wtsync.ReadState(gitDir); err != nil {
		return err
	} else if !ok || cur.Epoch != st.Epoch {
		return fmt.Errorf("the handover in %s changed while resume was checking it; nothing is resumed", name)
	}

	// Stacked is deliberately not set. It exists so a run does not leave a
	// branch its children are about to be rebased onto waiting on a person;
	// a resume rebases exactly one worktree, so there is no child in flight
	// to strand, and a later unclaimed stop is better answered with a fresh
	// plan file than with a refusal whose only remedy is undo.
	req := wtsync.Request{
		Path: target.Path, Branch: st.Branch, Trunk: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Work: name,
	}
	safety := wtsync.Safety{Branch: st.Branch, Epoch: st.Epoch, Ref: st.Safety, Tip: st.OldTip}
	tracker.set(&rebaseInFlight{work: name, path: target.Path, safety: st.Safety, resuming: true})
	res, rerr := wtsync.Resume(ctx.Repo.MainRoot, cfg, req, st.OldTip, safety, w)
	tracker.set(nil)
	if rerr != nil {
		return fmt.Errorf("%s: %w", name, rerr)
	}
	if res.Left != nil {
		fmt.Fprintf(w, "  stopped again at %d/%d\n", res.Left.Index, res.Left.Total)
		if err := handOver(ctx, w, handoverInput{
			Work: name, Branch: st.Branch, Path: target.Path, TrunkRef: st.TrunkRef, TrunkSHA: st.Trunk,
			Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Cfg: cfg, Res: res, Lock: lock,
		}); err != nil {
			return err
		}
		lock = nil // kept on purpose
		return fmt.Errorf("not completed: %s (needs you)", name)
	}
	fmt.Fprintf(w, "  rebased %d commit%s\n", res.Replayed, plural(res.Replayed))
	tracker.set(&rebaseInFlight{work: name, path: target.Path, rebased: true})
	_, owed, cerr := completeRun(ctx, w, cfg, completeInput{
		Work: name, Branch: st.Branch, Path: target.Path, Epoch: st.Epoch, Res: res,
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

// verifySequencer refuses a rebase in progress that is not the one the
// handover describes: one moving another ref, replaying onto another commit,
// or started from another tip — a person who aborted the run's rebase and
// started their own, perhaps after committing. Continuing it would pin its
// result under the run's epoch, and a later undo would discard their commit.
// The onto and orig-head values are resolved to commits before they are
// compared, so an abbreviated or symbolic one can neither refuse the run's
// own rebase nor pass somebody else's.
func verifySequencer(wtPath string, st wtsync.State) error {
	notOurs := func(why string) error {
		return fmt.Errorf("the rebase in progress in %s is not the one wt sync run left: %s; nothing is resumed", st.Work, why)
	}
	t, ok, err := wtsync.ReadRebaseTarget(wtPath)
	if err != nil {
		return err
	}
	if !ok {
		return notOurs("it is not a merge-backend rebase")
	}
	if t.HeadName != "refs/heads/"+st.Branch {
		return notOurs(fmt.Sprintf("it moves %s, not refs/heads/%s", t.HeadName, st.Branch))
	}
	want, err := commitOf(wtPath, st.Onto)
	if err != nil {
		return fmt.Errorf("the handover's onto %q is not a commit here: %w; nothing is resumed", st.Onto, err)
	}
	if got, err := commitOf(wtPath, t.Onto); err != nil || got != want {
		return notOurs(fmt.Sprintf("it replays onto %s, not %s", short(t.Onto), short(want)))
	}
	if got, err := commitOf(wtPath, t.OrigHead); err != nil || got != st.OldTip {
		return notOurs(fmt.Sprintf("it started from %s, not the tip the run started from (%s)", short(t.OrigHead), short(st.OldTip)))
	}
	return nil
}

func commitOf(dir, rev string) (string, error) {
	if rev == "" {
		return "", errors.New("empty")
	}
	return git.Run(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// verifyHandover refuses to continue a rebase that is not what the run left.
// None of what it finds is something the tool may repair by re-running a
// strategy over the recorded stop: that would overwrite a person's work.
//
//   - A tracked file changed but not staged makes git refuse to continue,
//     and the loop's did-not-advance guard would read that refusal as a
//     stuck rebase. In a run that means a restore; here it would mean
//     throwing away the resolution. This is refused wherever the rebase is.
//   - At the recorded stop, something still unmerged means the person is not
//     done — unless a strategy resolved it, in which case merging it is not
//     the person's to do and they are pointed at undo instead.
//   - At the recorded stop, a different blob under a strategy's name means a
//     file the brief said never to hand-merge was hand-merged.
//
// Past the recorded stop, reached by a person's own git rebase --continue,
// whatever is unmerged belongs to a stop the strategies have never seen. The
// loop gives it to them exactly as the run would have and hands over afresh
// what they do not claim; telling the person to resolve it would invite a
// hand-merge of a file a strategy owns. The recorded stop's blobs are
// committed by then, so they are not compared, and that is said out loud.
func verifyHandover(wtPath string, st wtsync.State, w io.Writer) error {
	// --untracked-files=no: an untracked file never blocks a rebase, and a
	// script may have left one.
	dirty, err := git.Run(wtPath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	var unstaged []string
	for _, line := range strings.Split(dirty, "\n") {
		// The second status column is the worktree against the index: any
		// mark there is a change git will refuse to continue over. An
		// unmerged path marks both columns and is not a change of that kind.
		if len(line) > 3 && line[1] != ' ' && !unmergedStatus(line[:2]) {
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
		noteNotRechecked(w, fmt.Sprintf("the rebase is at %d/%d, not the %d/%d the plan describes", p.Index, p.Total, st.Stop, st.Total), st)
		return nil
	}
	unmerged, err := wtsync.StagedConflicts(wtPath)
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		var owned, yours []string
		for _, c := range unmerged {
			if s := st.Strategy[c.Path]; s != "" {
				owned = append(owned, c.Path+" ("+s+")")
			} else {
				yours = append(yours, c.Path)
			}
		}
		if len(owned) > 0 {
			return fmt.Errorf("unmerged again after a strategy resolved it: %s; that is not yours to merge, wt sync undo %s and start again", strings.Join(owned, ", "), st.Work)
		}
		return fmt.Errorf("still unmerged: %s; resolve them, git add them, then resume", strings.Join(yours, ", "))
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

// unmergedStatus reports whether a porcelain v1 XY pair marks an unmerged
// path: DD, AU, UD, UA, DU, AA or UU.
func unmergedStatus(xy string) bool {
	return xy[0] == 'U' || xy[1] == 'U' || xy == "AA" || xy == "DD"
}

// noteNotRechecked says, where the recorded stop is already committed, which
// of the strategies' answers there resume can no longer compare.
func noteNotRechecked(w io.Writer, where string, st wtsync.State) {
	var ps []string
	for p := range st.Resolved {
		ps = append(ps, p)
	}
	ps = append(ps, st.Deleted...)
	sort.Strings(ps)
	line := "  note: " + where + "; what the strategies staged there is already committed and is not re-checked"
	for i, p := range ps {
		ps[i] = p + " (" + st.Strategy[p] + ")"
	}
	if len(ps) > 0 {
		line += ": " + strings.Join(ps, ", ")
	}
	fmt.Fprintln(w, line)
}
