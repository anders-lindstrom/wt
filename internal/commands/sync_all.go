package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// SyncAll is the overview across repositories: wt sync in each one a
// selection names, a few at a time, printed one section after another. It
// changes nothing but the fetch.
func SyncAll(u *config.User, sel Selection, opts SyncOptions, w io.Writer) error {
	defer watchSignals(w, nil)()
	return AcrossRepos(u, sel, w, func(ctx *Context, w io.Writer) error { return Sync(ctx, opts, w) })
}

// SyncRunAllOptions is a run across repositories: the options every
// repository's run takes, and the one question asked for all of them.
type SyncRunAllOptions struct {
	RunOptions
	// Ask puts the question for the whole run. Nil means nobody is asked,
	// which is what a run with no terminal does in one repository too.
	Ask func(question string) (bool, error)
}

// repoRun is one repository's part of a run across many: what planning it
// found, and what it printed doing so.
type repoRun struct {
	target     RepoTarget
	ctx        *Context
	ready      []string
	notReady   []string
	idle       []string // notices for ready worktrees an idle session is in
	undeclared bool
	trunkSHA   string // the trunk the plan was made against, which the run must still find
	log        bytes.Buffer
	err        error
}

// SyncRunAll is wt sync --run across repositories. Every repository is
// fetched and planned as a run with nothing named would plan it — its ready
// worktrees, less recipe? — a few at a time; each plan is printed under its
// repository's name; one question covers them all; then each repository's
// ready worktrees are rebased, one repository after another, exactly as a run
// naming them would, onto the trunk the plan fetched. A repository whose trunk
// declares no .wt-sync.yaml has not opted in and is listed, not failed. With
// IfReady, a worktree left behind trunk fails the run.
func SyncRunAll(u *config.User, sel Selection, opts SyncRunAllOptions, w io.Writer) error {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return err
	}
	if len(set.Repos) == 0 {
		fmt.Fprintln(w, "No repositories wt manages here; wt repos says where it looked.")
		return nil
	}
	defer watchSignals(w, nil)()
	// One listing of the sessions serves every repository's plan, as one
	// `claude agents` knows them all; each run lists them again at its lock.
	agents, err := opts.agents(rebasedNothing)
	if err != nil {
		return err
	}
	plan := opts.RunOptions
	plan.Agents = agents
	runs := eachRepo(set.Repos, repoParallelism, func(t RepoTarget) *repoRun {
		return planRepoRun(t, plan)
	})

	home, _ := os.UserHomeDir()
	var undeclared, current, failures []string
	worktrees, repos := 0, 0
	for _, rr := range runs {
		switch {
		case rr.undeclared:
			undeclared = append(undeclared, rr.target.Name)
			continue
		case rr.err == nil && len(rr.ready) == 0 && len(rr.notReady) == 0:
			current = append(current, rr.target.Name)
			continue
		}
		fmt.Fprintf(w, "== %s  %s\n", rr.target.Name, abbreviateHome(rr.target.Path, home))
		_, _ = w.Write(rr.log.Bytes())
		if rr.err != nil {
			fmt.Fprintf(w, "! %v\n", rr.err)
			failures = append(failures, rr.target.Name)
		}
		for _, n := range rr.idle {
			fmt.Fprintln(w, n)
		}
		fmt.Fprintln(w)
		if opts.IfReady {
			for _, work := range rr.notReady {
				failures = append(failures, rr.target.Name+"/"+work+" (not ready)")
			}
		}
		if len(rr.ready) > 0 {
			worktrees += len(rr.ready)
			repos++
		}
	}
	if len(current) > 0 {
		fmt.Fprintf(w, "Nothing to rebase in %s: %s\n", repoCount(len(current)), strings.Join(current, ", "))
	}
	if len(undeclared) > 0 {
		fmt.Fprintf(w, "No %s on trunk, so not synced: %s\n", wtsync.ConfigFile, strings.Join(undeclared, ", "))
	}
	if repos == 0 {
		return notCompleted(failures)
	}

	question := fmt.Sprintf("Rebase %s across %s?", worktreeCount(worktrees), repoCount(repos))
	if opts.Ask != nil {
		ok, err := opts.Ask(question)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, rebasedNothing.line)
			return notCompleted(failures)
		}
	}

	// Each repository's run is the one a person naming its ready worktrees
	// would start: nothing asked again, since the question above covered it,
	// and onto the trunk the plan fetched, not a newer one it has not shown.
	// Its triage reads the sessions as they were when the question was
	// asked, and its lock lists them fresh, so a session that arrived since
	// is refused the way a run in one repository refuses it.
	one := opts.RunOptions
	one.NoFetch, one.Confirm = true, nil
	one.Agents = agents
	if one.Relist == nil {
		one.Relist = func() ([]wtsync.Agent, error) { return listAgain(opts.Agents, opts.Relist) }
	}
	for _, rr := range runs {
		if len(rr.ready) == 0 || rr.err != nil {
			continue
		}
		fmt.Fprintf(w, "== %s\n", rr.target.Name)
		if _, sha, err := trunkTip(rr.ctx); err != nil || sha != rr.trunkSHA {
			fmt.Fprintf(w, "! trunk moved since the plan (%s now); nothing rebased here, run it again\n", git.ShortID(sha, 7))
			failures = append(failures, rr.target.Name)
			continue
		}
		if err := SyncRun(rr.ctx, rr.ready, one, w); err != nil {
			fmt.Fprintf(w, "! %v\n", err)
			failures = append(failures, rr.target.Name)
		}
		fmt.Fprintln(w)
	}
	return notCompleted(failures)
}

// planRepoRun plans one repository's part of a run across many: its fetch,
// and the worktrees a run with nothing named would take.
func planRepoRun(t RepoTarget, opts RunOptions) *repoRun {
	rr := &repoRun{target: t}
	if t.Problem != "" {
		rr.err = fmt.Errorf("%s: %s", t.Path, t.Problem)
		return rr
	}
	ctx, err := Open(t.Path)
	if err != nil {
		rr.err = err
		return rr
	}
	rr.ctx = ctx
	r := &runPlan{ctx: ctx, opts: opts, w: &rr.log, tracker: &rebaseTracker{},
		trunk: ctx.Config.MainBranch, outcomes: map[string]int{}}
	if err := r.declare(); err != nil {
		var undeclared undeclaredError
		if errors.As(err, &undeclared) {
			rr.undeclared = true
			return rr
		}
		rr.err = err
		return rr
	}
	ready, err := r.selectReady()
	if err != nil {
		rr.err = err
		return rr
	}
	rr.ready, rr.notReady, rr.trunkSHA = ready, r.notReady, r.trunkSHA
	for _, work := range ready {
		wt, err := locateBranch(ctx, work)
		if err != nil {
			continue
		}
		if a := r.assessed[wt.Branch]; len(a.Sessions) > 0 {
			rr.idle = append(rr.idle, idleNotice(work, a.Sessions))
		}
	}
	return rr
}

// notCompleted is the error a run across repositories ends with, naming what
// did not finish; nil when everything did.
func notCompleted(failures []string) error {
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("not completed: %s", strings.Join(failures, ", "))
}
