package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// SweepAllOptions is a sweep across repositories: the one-repository options
// every repository is swept with, and the one question asked for all of them.
type SweepAllOptions struct {
	SweepOptions
	// Ask puts the question for the whole run, and nil means there is nobody
	// to ask: the plans are printed and nothing is swept, as with one
	// repository.
	Ask func(question string) (bool, error)
}

// repoSweep is one repository's part of a sweep across many.
type repoSweep struct {
	target RepoTarget
	ctx    *Context
	plan   SweepPlan
	log    bytes.Buffer // what fetching and planning printed, shown with its plan
	err    error
}

// quiet is a plan with nothing to show: nothing goes, and nothing is kept
// that would want a word.
func (s *repoSweep) quiet() bool {
	p := s.plan
	return s.err == nil && p.Empty() && len(p.CheckedOut) == 0 && len(p.Gone) == 0
}

// SweepAll sweeps every repository a selection names: fetch and plan each, a
// few at a time; print every plan with something in it, one section per
// repository; ask once for the lot; then carry out each repository's plan the
// way a sweep of it alone would, re-reading it first. A repository that
// cannot be swept is reported and the rest go ahead.
func SweepAll(u *config.User, sel Selection, opts SweepAllOptions, w io.Writer) error {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return err
	}
	if len(set.Repos) == 0 {
		fmt.Fprintln(w, "No repositories wt manages here; wt repos says where it looked.")
		return nil
	}
	defer watchSignals(w, nil)()
	// The sessions are listed once for every repository's plan: `claude
	// agents` knows them all, and twenty listings would say the same thing
	// twenty times. Before anything goes, each repository lists them again.
	agents, listErr := opts.listAgents()
	one := opts.SweepOptions
	one.Agents, one.Relist = agents, nil
	if agents == nil {
		one.Agents = []wtsync.Agent{}
	}

	sweeps := eachRepo(set.Repos, repoParallelism, func(t RepoTarget) *repoSweep {
		s := &repoSweep{target: t}
		s.ctx, s.plan, s.err = planRepoSweep(t, one, listErr, &s.log)
		return s
	})

	home, _ := os.UserHomeDir()
	var quiet, failed []string
	branches, worktrees, repos := 0, 0, 0
	for _, s := range sweeps {
		if s.quiet() {
			quiet = append(quiet, s.target.Name)
			continue
		}
		fmt.Fprintf(w, "== %s  %s\n", s.target.Name, abbreviateHome(s.target.Path, home))
		if s.err != nil {
			fmt.Fprintf(w, "! %v\n\n", s.err)
			failed = append(failed, s.target.Name)
			continue
		}
		_, _ = w.Write(s.log.Bytes())
		s.plan.Render(w, opts.Width)
		if !s.plan.Empty() {
			branches += len(s.plan.Delete)
			worktrees += len(s.plan.Remove)
			repos++
		}
	}
	if len(quiet) > 0 {
		fmt.Fprintf(w, "Nothing to sweep in %s: %s\n", repoCount(len(quiet)), strings.Join(quiet, ", "))
	}
	if repos == 0 {
		return failures(failed, len(sweeps))
	}

	what := sweepCounts(branches, worktrees) + " across " + repoCount(repos)
	switch {
	case opts.Yes:
	case opts.Ask != nil:
		ok, err := opts.Ask("Sweep " + what + "?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was swept.")
			return failures(failed, len(sweeps))
		}
	default:
		fmt.Fprintf(w, "Nothing was swept: there is no terminal to ask. Pass --yes to sweep %s.\n", what)
		return failures(failed, len(sweeps))
	}

	// Each repository's apply lists the sessions again itself, immediately
	// before it removes anything, as a sweep of it alone does: a session that
	// arrived while the question was open, or while the repositories before
	// it were swept, keeps its worktree.
	for _, s := range sweeps {
		if s.err != nil || s.plan.Empty() {
			continue
		}
		fmt.Fprintf(w, "== %s\n", s.target.Name)
		if err := s.plan.apply(s.ctx, opts.SweepOptions, w); err != nil {
			fmt.Fprintf(w, "! %v\n", err)
			failed = append(failed, s.target.Name)
		}
	}
	return failures(failed, len(sweeps))
}

// planRepoSweep is one repository's sweep up to the question: the checks a
// sweep of it alone makes, its fetch, and its plan. What the fetch prints goes
// to log, to be shown with the plan.
func planRepoSweep(t RepoTarget, opts SweepOptions, listErr error, log io.Writer) (*Context, SweepPlan, error) {
	if t.Problem != "" {
		return nil, SweepPlan{}, fmt.Errorf("%s: %s", t.Path, t.Problem)
	}
	ctx, err := Open(t.Path)
	if err != nil {
		return nil, SweepPlan{}, err
	}
	if err := sweepGuard(ctx); err != nil {
		return nil, SweepPlan{}, err
	}
	if err := sweepFetch(ctx, opts.NoFetch, log); err != nil {
		return nil, SweepPlan{}, err
	}
	bases, err := trunkBases(ctx)
	if err != nil {
		return nil, SweepPlan{}, err
	}
	list := func() ([]wtsync.Agent, error) { return opts.Agents, listErr }
	plan, err := planSweep(ctx, bases, list, opts.pullRequests(ctx))
	return ctx, plan, err
}

// sweepCounts is "3 branches and 2 worktrees", leaving out a zero.
func sweepCounts(branches, worktrees int) string {
	var parts []string
	if branches > 0 {
		parts = append(parts, branchCount(branches))
	}
	if worktrees > 0 {
		parts = append(parts, worktreeCount(worktrees))
	}
	return strings.Join(parts, " and ")
}

// failures is the error a run across repositories ends with when any of them
// could not be swept, naming them; nil when every one could.
func failures(failed []string, of int) error {
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %s could not be swept in full: %s", len(failed), repoCount(of), strings.Join(failed, ", "))
}
