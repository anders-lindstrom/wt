package wtsync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Verdict is Preflight's answer.
type Verdict int

// The verdicts. Not Go/Skip/Refuse: Refuse is already the refusal
// constructor in conflict.go.
const (
	Proceed   Verdict = iota // rebase it
	SkipRun                  // nothing to do; say why
	RefuseRun                // must not be touched; say why
)

// rebaseEnv keeps every rebase step from ever prompting.
var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

// rebaseConfig pins the behaviour the loop is written against, whatever the
// user's configuration says: the merge backend (rebase-merge bookkeeping),
// no merge preservation, no autostash, no ref updating.
var rebaseConfig = []string{
	"-c", "rebase.backend=merge",
	"-c", "rebase.rebaseMerges=false",
	"-c", "rebase.autoStash=false",
	"-c", "rebase.updateRefs=false",
}

// Preflight decides from a triage assessment whether a run may start. The
// checks that make a worktree untouchable come before the class, so a dirty
// worktree is refused for its dirt whatever its class.
func Preflight(a Assessment) (Verdict, string) {
	switch {
	case a.Err != nil:
		return RefuseRun, "assessment failed: " + a.Err.Error()
	case a.Class == Detached:
		return RefuseRun, "no branch"
	case a.NoConfig:
		return RefuseRun, "no declaration on trunk"
	case a.Dirty:
		return RefuseRun, "tracked changes in the worktree"
	case a.Agent != nil:
		return RefuseRun, "an agent session is in it: " + agentLabel(a.Agent)
	}
	switch a.Class {
	case Current:
		return SkipRun, "already on trunk"
	case Stale:
		return SkipRun, "nothing ahead of trunk"
	case Divergent:
		reason := "divergent"
		if len(a.Divergent) > 0 {
			reason += ": " + a.Divergent[0]
		}
		return RefuseRun, reason
	case Contested:
		var files []string
		for _, f := range a.Files {
			if !f.Resolved {
				files = append(files, f.Path)
			}
		}
		where := ""
		if a.Replay.Stop != nil {
			where = fmt.Sprintf(" at %d/%d", a.Replay.Stop.Index, a.Replay.Stop.Total)
		}
		return RefuseRun, fmt.Sprintf("contested%s: %s; rebase by hand (resume is not built yet)", where, strings.Join(files, ", "))
	case Clean, Recipe:
		return Proceed, ""
	}
	return RefuseRun, "class unknown"
}

func agentLabel(a *Agent) string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Kind != "":
		return a.Kind
	}
	return "?"
}

// Request names one rebase.
type Request struct {
	Path     string // the worktree
	Branch   string
	Trunk    string // the ref the declaration and scripts are read from; never a parent's tip
	Onto     string // what to rebase onto: Trunk, or a stack parent's new tip
	Upstream string // "" for a plain rebase; the parent's old tip for a stack child
	Epoch    int64
}

// StopResult is one place the rebase stopped and what happened there.
type StopResult struct {
	Index, Total int
	Subject      string
	Files        []FileOutcome
}

// Result is what a rebase did.
type Result struct {
	Branch, OldTip, NewTip string
	Safety                 Safety
	Replayed               int
	Stops                  []StopResult
	Restored               bool // an unresolved stop: aborted and verified back at OldTip
	SignaturesDropped      int
}

// Rebase rebases one worktree, applying the declared strategies at each
// stop. An unresolved stop aborts and restores to the safety ref and is a
// normal outcome (Restored); an error is a git failure, after the same
// restore, or a restore that could not be verified.
func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error) {
	res := Result{Branch: req.Branch}
	git := func(args ...string) (string, error) {
		if log != nil && os.Getenv("WT_SYNC_TRACE") != "" {
			fmt.Fprintf(log, "  $ git %s\n", strings.Join(args, " "))
		}
		return gitEnv(req.Path, rebaseEnv, nil, args...)
	}
	old, err := git("rev-parse", "--verify", req.Branch)
	if err != nil {
		return res, err
	}
	res.OldTip = old
	if res.Safety, err = WriteSafety(mainRoot, req.Branch, old, req.Epoch); err != nil {
		return res, err
	}
	base := req.Upstream
	if base == "" {
		base = req.Onto
	}
	if res.SignaturesDropped, err = signedCount(req.Path, base, old); err != nil {
		return res, err
	}

	restore := func() error {
		_, _ = git("rebase", "--abort")
		if busy, _ := RebaseInProgress(req.Path); busy {
			_, _ = git("rebase", "--quit")
			// --quit drops the sequencer's state and nothing else: HEAD is
			// left detached where the rebase stopped, the conflict is still
			// in the index, and the branch ref never moved. So reset first,
			// unconditionally, while HEAD is still detached (the guarded
			// reset below compares HEAD with the branch's own tip, which
			// re-attaching first would make equal and skip), and only then
			// put HEAD back on the branch.
			if _, err := git("reset", "--hard", res.Safety.Ref); err != nil {
				return fmt.Errorf("not restored: reset failed: %w; the old tip is %s", err, res.Safety.Ref)
			}
			_, _ = git("symbolic-ref", "HEAD", "refs/heads/"+req.Branch)
		}
		if head, _ := git("rev-parse", "HEAD"); head != old {
			if _, err := git("reset", "--hard", res.Safety.Ref); err != nil {
				return fmt.Errorf("not restored: reset failed: %w; the old tip is %s", err, res.Safety.Ref)
			}
		}
		if busy, _ := RebaseInProgress(req.Path); busy {
			return fmt.Errorf("not restored: a rebase is still in progress; the old tip is %s", res.Safety.Ref)
		}
		if ref, _ := git("symbolic-ref", "--quiet", "HEAD"); ref != "refs/heads/"+req.Branch {
			return fmt.Errorf("not restored: HEAD is %q, not %s; the old tip is %s", ref, req.Branch, res.Safety.Ref)
		}
		if head, _ := git("rev-parse", "HEAD"); head != old {
			return fmt.Errorf("not restored: HEAD is %s, not %s; the old tip is %s", short(head), short(old), res.Safety.Ref)
		}
		// A status that cannot be read is not a clean status: this check
		// fails closed, since its whole job is to prove the worktree is
		// untouched.
		status, serr := git("--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if serr != nil {
			return fmt.Errorf("not restored: status could not be read: %w; the old tip is %s", serr, res.Safety.Ref)
		}
		if status != "" {
			return fmt.Errorf("not restored: tracked changes remain; the old tip is %s", res.Safety.Ref)
		}
		return nil
	}
	fail := func(err error) (Result, error) {
		return res, errors.Join(err, restore())
	}

	args := append(append([]string{}, rebaseConfig...), "rebase", "--no-update-refs", "--no-gpg-sign")
	if req.Upstream != "" {
		args = append(args, "--onto", req.Onto, req.Upstream)
	} else {
		args = append(args, req.Onto)
	}
	_, err = git(args...)
	lastIndex, lastUnmerged := -1, ""
	stops, limit := 0, 0
	for err != nil {
		busy, perr := RebaseInProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		if !busy {
			return fail(fmt.Errorf("rebase: %w", err))
		}
		p, perr := RebaseProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		conflicts, cerr := StagedConflicts(req.Path)
		if cerr != nil {
			return fail(cerr)
		}
		// The guard below compares one stop with the one before it, which an
		// alternating unmerged set would walk straight past; this cap bounds
		// the loop whatever the sequencer does. Two stops per commit plus
		// slack, or a flat 64 when the sequencer reports no total.
		stops++
		if limit == 0 {
			if limit = 2*p.Total + 2; p.Total == 0 {
				limit = 64
			}
		}
		if stops > limit {
			return fail(fmt.Errorf("rebase did not converge after %d stops", limit))
		}
		unmerged := pathsOf(conflicts)
		if p.Index == lastIndex && (len(conflicts) == 0 || unmerged == lastUnmerged) {
			return fail(fmt.Errorf("rebase did not advance at %d/%d: %w", p.Index, p.Total, err))
		}
		lastIndex, lastUnmerged = p.Index, unmerged
		stop := StopResult{Index: p.Index, Total: p.Total, Subject: p.Subject}
		unresolved := false
		for _, c := range conflicts {
			r, rerr := resolveConflict(mainRoot, req.Trunk, cfg, c, req.Path)
			if rerr != nil {
				stop.Files = append(stop.Files, r.Outcome)
				logStop(log, stop)
				res.Stops = append(res.Stops, stop)
				return fail(rerr)
			}
			if r.Outcome.Resolved && !r.InPlace {
				if aerr := Apply(req.Path, c, r.Content); aerr != nil {
					return fail(aerr)
				}
			}
			if !r.Outcome.Resolved {
				unresolved = true
			}
			stop.Files = append(stop.Files, r.Outcome)
		}
		if len(conflicts) == 0 {
			// Stopped with nothing unmerged: rerere or a hook did it all, or
			// the sequencer stopped for a reason we do not handle. One
			// --continue is the honest move; the guard above catches a
			// second stop in the same place.
			stop.Files = append(stop.Files, FileOutcome{Path: messagesPath, Note: "stopped with nothing unmerged"})
		}
		logStop(log, stop)
		res.Stops = append(res.Stops, stop)
		if unresolved {
			res.Restored = true
			return res, restore()
		}
		_, err = git("rebase", "--continue")
	}
	// The command reported success, so the sequencer must be finished. If it
	// is not, the rebase did not complete and the worktree goes back.
	busy, perr := RebaseInProgress(req.Path)
	if perr != nil {
		return fail(perr)
	}
	if busy {
		return fail(errors.New("rebase reported success but is still in progress"))
	}
	// Past this point the rewrite happened and is what the branch now is.
	// Failing to read it back is a reporting failure, not a reason to throw
	// the work away, so nothing below restores.
	unread := func(err error) (Result, error) {
		return res, fmt.Errorf("rebased but could not read the result: %w; the old tip is %s", err, res.Safety.Ref)
	}
	if res.NewTip, err = git("rev-parse", "HEAD"); err != nil {
		return unread(err)
	}
	count, err := git("rev-list", "--count", req.Onto+"..HEAD")
	if err != nil {
		return unread(err)
	}
	if res.Replayed, err = strconv.Atoi(count); err != nil {
		return unread(err)
	}
	return res, nil
}

func pathsOf(cs []Conflict) string {
	var ps []string
	for _, c := range cs {
		ps = append(ps, c.Path)
	}
	sort.Strings(ps)
	return strings.Join(ps, "\x00")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func logStop(log io.Writer, s StopResult) {
	if log == nil {
		return
	}
	parts := []string{fmt.Sprintf("  stop %d/%d %q", s.Index, s.Total, s.Subject)}
	for _, f := range s.Files {
		mark, note := "✗", f.Note
		if f.Resolved {
			mark, note = "✓", f.Strategy
		}
		parts = append(parts, f.Path+mark+" "+note)
	}
	fmt.Fprintln(log, strings.Join(parts, "  "))
}

// signedCount counts the commits in base..tip that carry a signature, which a
// rewrite drops (spec §4).
func signedCount(wtPath, base, tip string) (int, error) {
	out, err := gitEnv(wtPath, nil, nil, "log", "--format=%G?", base+".."+tip)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if l != "" && l != "N" {
			n++
		}
	}
	return n, nil
}
