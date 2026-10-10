package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// PushMode is what a run or a resume does with the branches it finished.
type PushMode int

const (
	// PushAsk asks once through the caller's confirm, and prints the push
	// command for each branch when there is nobody to ask.
	PushAsk PushMode = iota
	// PushAlways pushes without asking.
	PushAlways
	// PushNever prints the push command for each branch and asks nothing.
	PushNever
)

// pushTarget is a worktree whose rebase finished with nothing owed: the only
// kind a run offers to push.
type pushTarget struct {
	Work, Branch, Path string
}

// pushOf is where t's branch pushes, read now: after the rebase, and after
// anything a question recorded.
func pushOf(ctx *Context, t pushTarget) wtsync.Push {
	return wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, ctx.Config.MainBranch).Push(t.Branch)
}

// notPushed is why a branch has nowhere to push, with the command that
// settles it when one does.
func notPushed(to wtsync.Push) string {
	if to.Undecided != nil {
		return to.Undecided.Hint()
	}
	return to.Why
}

// pushedByHand is what a run adds under a branch it rebased and left
// unpushed for want of a destination: the next wt up finds it on trunk and
// pushes nothing.
const pushedByHand = "    that command also prints the push to run: the branch is rebased, and no later wt up pushes it"

// pushLine is what a branch left unpushed gets: the command that pushes it,
// or why there is none.
func pushLine(t pushTarget, to wtsync.Push) string {
	if to.Remote == "" {
		return fmt.Sprintf("  ✗ %s not pushed: %s", t.Branch, notPushed(to))
	}
	return fmt.Sprintf("push: git -C %s %s", t.Path, strings.Join(to.Args(), " "))
}

// pushJSON is the same for --json: the push as an argv, or the reason and
// the command that settles it.
func pushJSON(t pushTarget, to wtsync.Push) (command []string, noPush *string, fix []string) {
	if to.Remote != "" {
		return append([]string{"git", "-C", t.Path}, to.Args()...), nil, nil
	}
	if to.Undecided != nil {
		return nil, strp(to.Undecided.Reason()), to.Undecided.Fix()
	}
	return nil, strp(to.Why), nil
}

// offerPush ends a run or a resume: it pushes the branches that finished, or
// prints the command for each when told not to, when the answer is no, or
// when there is nobody to ask. A branch with nowhere to push is named with
// why, and is a failure only when a push was asked for; one whose
// destination is a choice is put to the person first, and the answer
// recorded. It returns the works it pushed, and the pushes that did not come
// off, named the way the not-completed error names them.
func offerPush(ctx *Context, w io.Writer, mode PushMode, ask pushOptions, targets []pushTarget) (pushed, failed []string, err error) {
	if len(targets) == 0 {
		return nil, nil, nil
	}
	fmt.Fprintln(w)
	asking := mode == PushAsk && ask.ConfirmPush != nil
	type ready struct {
		pushTarget
		to wtsync.Push
	}
	var going []ready
	for _, t := range targets {
		to := pushOf(ctx, t)
		if u := to.Undecided; u != nil && asking && ask.ChoosePush != nil {
			answer, err := ask.ChoosePush(PushChoice{Branch: u.Branch, Upstream: u.Upstream.String(), Own: u.Own.String()})
			if err != nil {
				return nil, nil, err
			}
			for _, choice := range []repo.PushTo{u.Upstream, u.Own} {
				if answer != choice.String() {
					continue
				}
				if err := ctx.Repo.SetPushTo(t.Branch, choice); err != nil {
					return nil, nil, err
				}
				fmt.Fprintf(w, "  recorded: %s pushes to %s\n", t.Branch, choice)
				to = pushOf(ctx, t)
				break
			}
		}
		if to.Remote == "" {
			fmt.Fprintln(w, pushLine(t, to))
			if to.Undecided != nil {
				fmt.Fprintln(w, pushedByHand)
			}
			if mode == PushAlways {
				failed = append(failed, t.Work+" (not pushed: nowhere to push)")
			}
			continue
		}
		going = append(going, ready{t, to})
	}
	push := mode == PushAlways
	if asking && len(going) > 0 {
		works := make([]string, len(going))
		for i, g := range going {
			if works[i] = g.Work; g.to.Renamed() {
				works[i] += " to " + g.to.Ref()
			}
		}
		ok, err := ask.ConfirmPush(works)
		if err != nil {
			return nil, nil, err
		}
		push = ok
	}
	if !push {
		for _, g := range going {
			fmt.Fprintln(w, pushLine(g.pushTarget, g.to))
		}
		return nil, failed, nil
	}
	for _, g := range going {
		t, to := g.pushTarget, g.to
		where := ""
		if to.Renamed() {
			// Said before the push, not after: nobody should learn from the
			// result that it went to a branch of another name.
			where = " to " + to.Ref()
			fmt.Fprintf(w, "  pushing %s to %s (%s)\n", t.Branch, to.Ref(), to.Rule)
		}
		from := "new on " + to.Remote
		if before, err := git.Run(t.Path, "rev-parse", "--verify", "--quiet", to.Tracking); err == nil {
			from = git.ShortID(before, 7)
		}
		if _, err := git.RunTimeout(t.Path, networkTimeout, to.Args()...); err != nil {
			fmt.Fprintf(w, "  ✗ push of %s%s failed: %s\n", t.Branch, where, pushReason(err))
			failed = append(failed, t.Work+" (push failed)")
			continue
		}
		tip, err := git.Run(t.Path, "rev-parse", "--verify", t.Branch)
		if err != nil {
			// The push went through; only reading the tip to report it did
			// not. Returning here would take the run's whole report with it
			// — every worktree that did not finish would go unnamed — so
			// this is named like any other push that did not come off and
			// the loop carries on.
			fmt.Fprintf(w, "  ✗ pushed %s%s, but its new tip could not be read: %s\n", t.Branch, where, pushReason(err))
			failed = append(failed, t.Work+" (pushed, tip unread)")
			continue
		}
		fmt.Fprintf(w, "  ✓ pushed %s%s  %s → %s\n", t.Branch, where, from, git.ShortID(tip, 7))
		pushed = append(pushed, t.Work)
	}
	return pushed, failed, nil
}

// pushReason is the line of git's refusal that says why — the "!" line
// naming the ref and the reason — or its first line when there is none.
func pushReason(err error) string {
	lines := strings.Split(err.Error(), "\n")
	for _, l := range lines {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "! ") {
			return l
		}
	}
	return strings.TrimSpace(lines[0])
}
