package commands

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// fetchTimeout bounds the one call in a run that reaches the network. It is
// shorter than the default git deadline: a fetch that has not finished by
// now is not going to, and every worktree in the run is waiting on it.
const fetchTimeout = 5 * time.Minute

// RunOptions tunes SyncRun for callers and tests.
type RunOptions struct {
	NoFetch bool
	Yes     bool
	// Confirm is asked once before rebasing more than one worktree. A nil
	// Confirm never asks: a script or a hook with no terminal is not a
	// person who can answer.
	Confirm func(works []string) (bool, error)
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	Now    func() time.Time
}

type participant struct {
	wt      repo.Worktree
	work    string
	a       wtsync.Assessment
	verdict wtsync.Verdict
	reason  string
	lock    *wtsync.Lock
	result  *wtsync.Result
	head    string // HEAD after the rebase and the deferred steps; what a child rebases onto
}

// SyncRun rebases the named worktrees (and the stacks they belong to) onto
// origin/<trunk>: safety ref, strategies at each stop, deferred steps once
// at the end. Anything refused, restored, failed or owed is reported and
// makes the returned error non-nil, so a script sees it.
func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error {
	tracker := &rebaseTracker{}
	defer watchSignals(w, tracker)()

	trunk := ctx.Config.MainBranch
	onto := "origin/" + trunk
	if opts.NoFetch {
		fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	} else {
		if _, err := git.RunTimeout(ctx.Repo.MainRoot, fetchTimeout, "fetch", "--quiet", "origin", trunk); err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		fmt.Fprintf(w, "fetched %s\n", onto)
	}
	// One SHA for the whole run: the declaration, the scripts and every
	// rebase target are the same trunk, whatever someone else fetches
	// underneath us mid-run.
	trunkSHA, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", onto)
	if err != nil {
		return fmt.Errorf("%s is not known here; run git fetch origin", onto)
	}
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, trunkSHA)
	if errors.Is(err, wtsync.ErrNoConfig) {
		return fmt.Errorf("%s declares no %s on %s: nothing is rebased", ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	if err != nil {
		return err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if !wt.IsMain && wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	var named []string
	for _, arg := range works {
		wt, err := Locate(ctx, arg)
		if err != nil {
			return err
		}
		if wt.Branch == "" {
			return fmt.Errorf("%s has no branch", arg)
		}
		named = append(named, wt.Branch)
	}
	parents, ambiguous, err := wtsync.Parents(ctx.Repo.MainRoot, worktrees)
	if err != nil {
		return err
	}
	for _, b := range named {
		if anc, ok := ambiguous[b]; ok {
			return fmt.Errorf("%s merges %s; that shape is not handled", workName(ctx, b), strings.Join(anc, " and "))
		}
	}
	seen := map[string]bool{}
	var branches []string
	for _, b := range named {
		var added []string
		for _, m := range wtsync.Members(parents, b) {
			if seen[m] {
				continue
			}
			seen[m] = true
			branches = append(branches, m)
			if m != b {
				added = append(added, workName(ctx, m))
			}
		}
		if len(added) > 0 {
			fmt.Fprintf(w, "%s is a stack with %s: rebasing all of them\n", workName(ctx, b), strings.Join(added, ", "))
		}
	}
	branches = wtsync.Order(parents, branches)

	agents := opts.Agents
	if agents == nil {
		if agents, err = wtsync.ListAgents(); err != nil {
			return fmt.Errorf("cannot list agent sessions (%v); nothing is rebased", err)
		}
	}
	parts := map[string]*participant{}
	for _, b := range branches {
		p := &participant{wt: byBranch[b], work: workName(ctx, b)}
		p.a = wtsync.Assess(ctx.Repo.MainRoot, trunkSHA, cfg, p.wt, agents)
		p.verdict, p.reason = wtsync.Preflight(p.a)
		parts[b] = p
	}
	// A member refused before anything has moved poisons its whole stack:
	// never half-apply (spec §4).
	poisoned := map[string]string{}
	poison := func(b, why string) {
		for _, m := range wtsync.Members(parents, b) {
			if poisoned[m] == "" {
				poisoned[m] = why
			}
		}
	}
	// A rebase that failed or was restored is different: the branch is back
	// where it was and its parent and siblings are untouched, so only what
	// sits on top of it loses its base.
	poisonAbove := func(b, why string) {
		for _, m := range wtsync.Descendants(parents, b) {
			if poisoned[m] == "" {
				poisoned[m] = why
			}
		}
	}
	for _, b := range branches {
		if parts[b].verdict == wtsync.RefuseRun {
			poison(b, parts[b].work+": "+parts[b].reason)
		}
	}
	var going []string
	for _, b := range branches {
		if parts[b].verdict == wtsync.Proceed && poisoned[b] == "" {
			going = append(going, parts[b].work)
		}
	}
	if len(going) > 1 && opts.Confirm != nil && !opts.Yes {
		fmt.Fprintf(w, "about to rebase: %s\n", strings.Join(going, ", "))
		ok, err := opts.Confirm(going)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "nothing rebased")
			return nil
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	epoch := now().UnixNano()

	// Lock every member of every proceeding stack before touching any, and
	// re-check what triage saw: the lock is what makes the check hold.
	release := func(b string) {
		if p := parts[b]; p != nil && p.lock != nil {
			_ = p.lock.Release()
			p.lock = nil
		}
	}
	defer func() {
		for _, b := range branches {
			release(b)
		}
	}()
	for _, b := range branches {
		p := parts[b]
		if poisoned[b] != "" || p.verdict != wtsync.Proceed {
			continue
		}
		gitDir, err := wtsync.GitDir(p.wt.Path)
		if err != nil {
			return err
		}
		lock, err := wtsync.Acquire(gitDir, now())
		if err != nil {
			poison(b, p.work+": locked: "+err.Error())
			continue
		}
		p.lock = lock
		// A check that cannot be run is not a passed check: this fails
		// closed, the way the restore check in wtsync does. It says so,
		// rather than reporting the state it never managed to read.
		busy, err := wtsync.RebaseInProgress(p.wt.Path)
		if err != nil {
			poison(b, p.work+": changed since triage: could not check: "+err.Error())
			continue
		}
		if busy {
			why := p.work + ": changed since triage: a rebase is in progress"
			if has, herr := wtsync.HasPlan(gitDir); herr == nil && has {
				why = p.work + ": left mid-rebase by an earlier run: wt sync resume " + p.work
			}
			poison(b, why)
			continue
		}
		status, err := git.Run(p.wt.Path, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			poison(b, p.work+": changed since triage: could not check: "+err.Error())
			continue
		}
		if status != "" {
			poison(b, p.work+": changed since triage: tracked changes")
			continue
		}
	}
	for _, b := range branches {
		if poisoned[b] != "" {
			release(b)
		}
	}

	var failures []string
	for _, b := range branches {
		p := parts[b]
		fmt.Fprintf(w, "%s  %s  %d behind, %d ahead\n", p.work, b, p.a.Behind, p.a.Ahead)
		if why, ok := poisoned[b]; ok {
			fmt.Fprintf(w, "  refused: %s\n", why)
			failures = append(failures, p.work)
			continue
		}
		if p.verdict == wtsync.SkipRun {
			fmt.Fprintf(w, "  skipped: %s\n", p.reason)
			continue
		}
		req := wtsync.Request{Path: p.wt.Path, Branch: b, Trunk: trunkSHA, Onto: trunkSHA, Epoch: epoch, Work: p.work}
		req.Stacked = len(wtsync.Descendants(parents, b)) > 0
		ontoLabel := onto
		if parent, ok := parents[b]; ok {
			if pp := parts[parent]; pp != nil && pp.result != nil && !pp.result.Restored && pp.head != "" {
				req.Onto, req.Upstream, ontoLabel = pp.head, pp.result.OldTip, pp.work
			}
		}
		if p.a.Class == wtsync.Contested && p.a.Replay.Stop != nil {
			what := "the run stops there and writes a plan"
			if req.Stacked {
				what = "the run stops there and puts the branch back: a stack parent cannot be left waiting"
			}
			fmt.Fprintf(w, "  contested at %d/%d: %s\n", p.a.Replay.Stop.Index, p.a.Replay.Stop.Total, what)
		}
		tracker.set(&rebaseInFlight{work: p.work, path: p.wt.Path, safety: wtsync.SafetyRef(b, epoch)})
		res, rerr := wtsync.Rebase(ctx.Repo.MainRoot, cfg, req, w)
		tracker.set(nil)
		p.result = &res
		if res.Safety.Ref != "" {
			fmt.Fprintf(w, "  safety %s = %s\n", res.Safety.Ref, short(res.OldTip))
		}
		if rerr != nil {
			fmt.Fprintf(w, "  failed: %v\n", rerr)
			clearHandover(w, p.wt.Path)
			failures = append(failures, p.work+" (failed)")
			poisonAbove(b, p.work+" failed")
			release(b)
			continue
		}
		if res.Left != nil {
			if err := handOver(ctx, w, handoverInput{
				Work: p.work, Branch: b, Path: p.wt.Path, TrunkRef: onto, TrunkSHA: trunkSHA,
				Onto: req.Onto, Upstream: req.Upstream, Epoch: epoch, Cfg: cfg, Res: res, Lock: p.lock,
			}); err != nil {
				fmt.Fprintf(w, "  failed: %v\n", err)
				// The rebase is still in the worktree and there is now no
				// plan file to explain it, so say the two things a person
				// cannot see for themselves. Any half-written brief goes:
				// the sidecar is the marker and it was never written, so
				// nothing acts on what is left, and a stale markdown would
				// describe a stop that is not this one.
				clearHandover(w, p.wt.Path)
				fmt.Fprintf(w, "  %s is left mid-rebase with no plan: finish it by hand, or wt sync undo %s puts it back\n", p.work, p.work)
				failures = append(failures, p.work+" (failed)")
				release(b)
				poisonAbove(b, p.work+" failed")
				continue
			}
			// The lock is left behind on purpose; dropping the handle here
			// keeps the deferred release from removing the file.
			p.lock = nil
			failures = append(failures, p.work+" (needs you)")
			poisonAbove(b, p.work+" is waiting for you")
			continue
		}
		if res.Restored {
			last := res.Stops[len(res.Stops)-1]
			var files []string
			for _, f := range last.Files {
				if !f.Resolved {
					files = append(files, f.Path)
				}
			}
			fmt.Fprintf(w, "  restored: %s at %d/%d not resolved; rebase by hand\n", strings.Join(files, ", "), last.Index, last.Total)
			clearHandover(w, p.wt.Path)
			failures = append(failures, p.work+" (restored)")
			poisonAbove(b, p.work+" was restored")
			release(b)
			continue
		}
		line := fmt.Sprintf("  rebased %d commit%s onto %s", res.Replayed, plural(res.Replayed), ontoLabel)
		if res.SignaturesDropped > 0 {
			line += fmt.Sprintf(", %d signature%s dropped", res.SignaturesDropped, plural(res.SignaturesDropped))
		}
		fmt.Fprintln(w, line)
		// The rebase is done; from here an interrupt cannot abort it, only
		// leave the deferred steps and the result ref undone.
		tracker.set(&rebaseInFlight{work: p.work, path: p.wt.Path, rebased: true})
		head, owed, derr := completeRun(ctx, w, cfg, completeInput{
			Work: p.work, Branch: b, Path: p.wt.Path, Epoch: epoch, Res: res,
		})
		tracker.set(nil)
		p.head = head
		failures = append(failures, owed...)
		// The rebase itself stands; only this branch and what sits on it
		// lose their footing, so the rest of the run carries on.
		if derr != nil {
			fmt.Fprintf(w, "  failed: %v\n", derr)
			failures = append(failures, p.work+" (failed)")
			poisonAbove(b, p.work+" failed")
			release(b)
			continue
		}
		release(b)
	}
	if len(failures) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(failures, ", "))
	}
	return nil
}

func printDeferred(w io.Writer, d wtsync.DeferredResult) {
	switch {
	case !d.Ran:
		fmt.Fprintf(w, "  defer %s  skipped: %s\n", d.Step.Run, d.Why)
	case d.Err != nil:
		fmt.Fprintf(w, "  defer %s  %s  OWED: %v\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Err)
		for _, l := range lastLines(d.Output, 20) {
			fmt.Fprintf(w, "    %s\n", l)
		}
	case d.Commit != "":
		fmt.Fprintf(w, "  defer %s  %s  committed %d file%s (+%d −%d) as %s\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Files, plural(d.Files), d.Insertions, d.Deletions, d.Commit)
	default:
		fmt.Fprintf(w, "  defer %s  %s\n", d.Step.Run, d.Elapsed.Round(time.Second))
	}
}

func lastLines(s string, n int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
