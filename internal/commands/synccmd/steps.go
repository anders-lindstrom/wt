package synccmd

import (
	"fmt"
	"io"
	"sort"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// stepsDue is what of the declaration's steps a run of wt may run, known
// before it starts; nothing for a worktree a run would not rebase. A stack
// child's commits are counted from its parent's tip, as its rebase replays
// them. An error reading it counts as nothing due: the overview is advice,
// and a run reads it again and refuses on an error.
func stepsDue(onto string, cfg *wtsync.Config, parents map[string]string, wt repo.Worktree, a wtsync.Assessment) wtsync.Due {
	if cfg == nil || wt.Branch == "" || a.OnlyFastForward() {
		return wtsync.Due{}
	}
	if v, _ := wtsync.Preflight(a); v != wtsync.Proceed {
		return wtsync.Due{}
	}
	base := onto
	if parent, ok := parents[wt.Branch]; ok {
		base = "refs/heads/" + parent
	}
	due, err := wtsync.StepsDue(wt.Path, cfg, base, rebasedTip(wt.Branch, a), onto)
	if err != nil {
		return wtsync.Due{}
	}
	return due
}

// rebasedTip is what a run rebases of branch: its own remote's commit when
// it is behind that remote and the run fast-forwards it there first, so the
// commits only the remote has are counted too; the branch otherwise.
func rebasedTip(branch string, a wtsync.Assessment) string {
	if a.Own.FastForwards() && a.Own.Commit != "" {
		return a.Own.Commit
	}
	return branch
}

// parentsOf is the stack parents of every worktree, nil when they cannot be
// read: then every branch's commits count from trunk.
func parentsOf(ctx *kit.Context, onto string) map[string]string {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil
	}
	parents, _, err := wtsync.Parents(ctx.Repo.MainRoot, onto, all)
	if err != nil {
		return nil
	}
	return parents
}

// checksLine is the overview's line for a worktree whose run checks commits.
func checksLine(n int) string {
	return "checks " + kit.Count(n, "commit") + " with the each_commit steps"
}

// printChecks lists the commits a run would check, for the detail view.
func printChecks(w io.Writer, due wtsync.Due) {
	if len(due.CheckCommits) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s, once each is replayed\n", checksLine(len(due.CheckCommits)))
	for _, c := range due.CheckCommits {
		fmt.Fprintf(w, "  %s  %s\n", git.ShortID(c.SHA, 7), kit.OneLine(c.Subject))
	}
	for _, s := range due.Checks {
		fmt.Fprintf(w, "  ▸ %s\n", s.Run)
	}
}

// eachCommitSteps is cfg's each_commit steps for --json.
func eachCommitSteps(cfg *wtsync.Config) []DeclaredStep {
	steps := []DeclaredStep{}
	if cfg == nil {
		return steps
	}
	for _, s := range cfg.EachCommit {
		steps = append(steps, DeclaredStep{Step: s.Run, Paths: append([]string{}, s.Paths...), Needs: append([]string{}, s.Needs...)})
	}
	return steps
}

// checkCommits is due's commits for --json.
func checkCommits(due wtsync.Due) []CheckCommit {
	out := []CheckCommit{}
	for _, c := range due.CheckCommits {
		out = append(out, CheckCommit{Commit: c.SHA, Subject: c.Subject})
	}
	return out
}

// needProbes is every need cfg declares, probed when a step of some due
// would run with it.
func needProbes(cfg *wtsync.Config, dues []wtsync.Due, prober *syncrun.Prober) []NeedProbe {
	out := []NeedProbe{}
	var names []string
	for n := range wtsync.KnownNeeds {
		if cfg.DeclaresNeed(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		np := NeedProbe{Need: n}
		for _, d := range dues {
			for _, dn := range d.Needs() {
				if dn == n {
					np.Probed = true
				}
			}
		}
		if np.Probed {
			if err := prober.Probe(n); err != nil {
				np.Error = jsonout.Strp(kit.OneLine(err.Error()))
			} else {
				np.OK = true
			}
		}
		out = append(out, np)
	}
	return out
}

// printNeeds says which probed needs did not answer, for the overview.
func printNeeds(w io.Writer, probes []NeedProbe) {
	for _, p := range probes {
		if p.Probed && !p.OK {
			fmt.Fprintf(w, "\n⚠ %s did not answer: %s; a run refuses the worktrees whose steps need it, or owes those steps with --allow-owed\n", p.Need, *p.Error)
		}
	}
}
