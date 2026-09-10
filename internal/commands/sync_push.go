package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
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

// pushArgs is the push for one finished branch. The lease is the
// remote-tracking ref, and --force-if-includes also refuses when a fetch
// moved that ref to commits the branch never had. A branch with no upstream
// gets origin/<branch>.
func pushArgs(t pushTarget) []string {
	args := []string{"push", "--force-with-lease", "--force-if-includes"}
	if _, err := git.Run(t.Path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", t.Branch+"@{upstream}"); err != nil {
		args = append(args, "-u")
	}
	return append(args, "origin", t.Branch)
}

// offerPush ends a run or a resume: it pushes the branches that finished, or
// prints the command for each when told not to, when the answer is no, or
// when there is nobody to ask. It returns the pushes that failed, named the
// way the not-completed error names them.
func offerPush(w io.Writer, mode PushMode, confirm func(works []string) (bool, error), targets []pushTarget) ([]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	fmt.Fprintln(w)
	push := mode == PushAlways
	if mode == PushAsk && confirm != nil {
		works := make([]string, len(targets))
		for i, t := range targets {
			works[i] = t.Work
		}
		ok, err := confirm(works)
		if err != nil {
			return nil, err
		}
		push = ok
	}
	if !push {
		for _, t := range targets {
			fmt.Fprintf(w, "push: git -C %s %s\n", t.Path, strings.Join(pushArgs(t), " "))
		}
		return nil, nil
	}
	var failed []string
	for _, t := range targets {
		from := "new on origin"
		if before, err := git.Run(t.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+t.Branch); err == nil {
			from = short(before)
		}
		if _, err := git.RunTimeout(t.Path, fetchTimeout, pushArgs(t)...); err != nil {
			fmt.Fprintf(w, "  ✗ push of %s failed: %s\n", t.Branch, pushReason(err))
			failed = append(failed, t.Work+" (push failed)")
			continue
		}
		to, err := git.Run(t.Path, "rev-parse", "--verify", t.Branch)
		if err != nil {
			return failed, err
		}
		fmt.Fprintf(w, "  ✓ pushed %s  %s → %s\n", t.Branch, from, short(to))
	}
	return failed, nil
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
