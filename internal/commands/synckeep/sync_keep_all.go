package synckeep

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synckeep/keepstate"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/reposel"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Across repositories the keeper is still one launchd job per repository:
// each has its own plist, log and state file, which is what wt sync's
// kept-at line, wt sync doctor and wt sync keep status in that repository
// read, and a pass that fails in one repository fails nothing in another.

// KeepStartAllOptions is start across repositories: the options every job
// takes, and the one question asked for all of them.
type KeepStartAllOptions struct {
	KeepStartOptions
	DryRun bool
	Yes    bool
	// Ask puts the question for the whole start; nil means there is nobody
	// to ask, and nothing is installed without Yes.
	Ask kit.Ask
}

// keepRow is one repository's line in start, stop or status across
// repositories.
type keepRow struct {
	target reposel.Target
	ctx    *kit.Context
	job    keepstate.KeepJob
	// act says the command has something to do here; failed that the
	// repository could not be looked at or the action did not come off.
	act, failed bool
	// warn marks a row that did not fail the command but wants a look: a
	// keeper whose last pass went wrong.
	warn    bool
	verdict string
}

// keepRows opens every repository a selection names, in its order, and
// lets fn say what the command makes of each one it could open.
func keepRows(m kit.Machine, u *config.User, sel reposel.Selection, fn func(*keepRow)) ([]*keepRow, error) {
	set, err := reposel.Select(u, sel)
	if err != nil {
		return nil, err
	}
	return keepRowsOf(m, set.Repos, fn), nil
}

// keepRowsOf is keepRows for repositories already selected, one at a time.
func keepRowsOf(m kit.Machine, repos []reposel.Target, fn func(*keepRow)) []*keepRow {
	rows := make([]*keepRow, 0, len(repos))
	for _, t := range repos {
		r := &keepRow{target: t}
		rows = append(rows, r)
		if t.Problem != "" {
			r.failed, r.verdict = true, t.Problem
			continue
		}
		ctx, err := kit.Open(t.Path, io.Discard)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			continue
		}
		ctx.Machine = m
		r.ctx = ctx
		fn(r)
	}
	return rows
}

// printKeepRows prints each row as its repository's path and what came of
// it, the paths in one column.
func printKeepRows(m kit.Machine, w io.Writer, rows []*keepRow) {
	home, _ := m.HomeDir()
	width := 0
	for _, r := range rows {
		width = max(width, len(kit.AbbreviateHome(r.target.Path, home)))
	}
	for _, r := range rows {
		mark := " "
		if r.failed || r.warn {
			mark = "!"
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", mark, width, kit.AbbreviateHome(r.target.Path, home), r.verdict)
	}
}

// keepFailures is the error a command across repositories ends with, naming
// every repository that failed; nil when none did.
func keepFailures(rows []*keepRow) error {
	var failed []string
	for _, r := range rows {
		if r.failed {
			failed = append(failed, r.target.Name)
		}
	}
	return synccmd.NotCompleted(failed)
}

// keepIneligible is why a repository is not one to keep, "" when it is: a
// keeper runs wt sync, which a repository opts into with a .wt-sync.yaml on
// trunk. The error is a declaration wt cannot read, or a trunk it has not
// fetched.
func keepIneligible(ctx *kit.Context) (string, error) {
	_, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, ctx.Config.MainBranch)
	switch {
	case errors.Is(err, wtsync.ErrUndeclared):
		return fmt.Sprintf("no %s on origin/%s", wtsync.ConfigFile, ctx.Config.MainBranch), nil
	case err != nil:
		return "", err
	}
	return "", nil
}

// StartAll is wt sync keep start across repositories: a job for
// every repository a selection names that declares wt sync and has none yet,
// each exactly as start in that repository installs it. A repository with a
// job is left as it is, whatever its interval, so a second start installs
// only what the first did not. The plan is printed first and one question
// covers it.
func StartAll(m kit.Machine, u *config.User, sel reposel.Selection, opts KeepStartAllOptions, w io.Writer) error {
	s, err := newKeepStarter(m, opts.KeepStartOptions)
	if err != nil {
		return err
	}
	var todo []*keepRow
	return kit.Bulk[*keepRow]{
		Plan: func(repos []reposel.Target) ([]*keepRow, error) {
			return keepRowsOf(m, repos, func(r *keepRow) {
				job, err := s.job(r.ctx)
				if err != nil {
					r.failed, r.verdict = true, err.Error()
					return
				}
				r.job = job
				installed, present := keepJobPresent(job)
				switch {
				case installed:
					k, err := keepstate.Keeper(r.ctx, false)
					if err != nil {
						r.failed, r.verdict = true, err.Error()
						return
					}
					r.verdict = "already kept, every " + keepstate.FmtEvery(k.St.IntervalOrDefault()) + "; left as it is"
					return
				case present:
					r.failed, r.verdict = true, "loaded but its plist is gone; wt sync keep stop, then start"
					return
				}
				why, err := keepIneligible(r.ctx)
				switch {
				case err != nil:
					r.failed, r.verdict = true, err.Error()
				case why != "":
					r.verdict = "skipped: " + why
				default:
					r.act, r.verdict = true, "to install, every "+keepstate.FmtEvery(s.every)
				}
			}), nil
		},
		Report: func(w io.Writer, rows []*keepRow) string {
			printKeepRows(m, w, rows)
			for _, r := range rows {
				if r.act {
					todo = append(todo, r)
				}
			}
			if len(todo) == 0 {
				fmt.Fprintln(w, "Nothing to install.")
				return ""
			}
			fmt.Fprintln(w, keepAuthLine(todo[0].job))
			return fmt.Sprintf("a keeper in %s", kit.Count(len(todo), "repository", "repositories"))
		},
		Decide: func(w io.Writer, what string) bool {
			return kit.Decision{DryRun: opts.DryRun, Yes: opts.Yes, Ask: opts.Ask}.Decide(w, "install", "installed", what)
		},
		Apply: func(w io.Writer, _ []*keepRow) {
			for _, r := range todo {
				next, err := s.install(r.ctx, r.job)
				if err != nil {
					r.failed, r.verdict = true, err.Error()
					continue
				}
				r.verdict = "installed, every " + keepstate.FmtEvery(s.every) + ", first at " + keepstate.KeepClock(next, s.now())
			}
			fmt.Fprintln(w)
			printKeepRows(m, w, todo)
			fmt.Fprintln(w, "Each keeper logs to .git/wt-sync-keep.log in its main checkout; wt sync keep status --all")
		},
		End: keepFailures,
	}.Run(u, sel, w)
}

// StopAll is wt sync keep stop across repositories: every job a
// selection's repositories have is booted out and its plist removed, with
// no question, since start puts it back. A repository with no job is listed
// and left alone; one that no longer declares wt sync is stopped all the
// same.
func StopAll(m kit.Machine, u *config.User, sel reposel.Selection, dryRun bool, w io.Writer) error {
	if !keepstate.HasLaunchd(m) {
		return ErrNoLaunchd
	}
	rows, err := keepRows(m, u, sel, func(r *keepRow) {
		job, err := keepstate.KeepJobFor(r.ctx)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			return
		}
		installed, present := keepJobPresent(job)
		switch {
		case !present:
			r.verdict = "no keeper"
		case dryRun:
			r.act, r.verdict = true, "to stop"
		default:
			r.act = true
			if err := stopKeepJob(job, installed); err != nil {
				r.failed, r.verdict = true, err.Error()
			} else if installed {
				r.verdict = "stopped; removed " + job.PlistPath
			} else {
				r.verdict = "stopped; its plist was already gone"
			}
		}
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, kit.NoRepos)
		return nil
	}
	printKeepRows(m, w, rows)
	stopped := 0
	for _, r := range rows {
		if r.act && !r.failed {
			stopped++
		}
	}
	switch {
	case dryRun:
		fmt.Fprintln(w, "Nothing was stopped: --dry-run.")
	case stopped == 0:
		fmt.Fprintln(w, "Nothing to stop.")
	default:
		fmt.Fprintf(w, "Stopped a keeper in %s; the logs stay.\n", kit.Count(stopped, "repository", "repositories"))
	}
	return keepFailures(rows)
}

// StatusAll is wt sync keep status across repositories, one line
// each: what wt sync doctor's keeper row says, or why the repository is not
// one to keep.
func StatusAll(m kit.Machine, u *config.User, sel reposel.Selection, now time.Time, w io.Writer) error {
	rows, err := keepRows(m, u, sel, func(r *keepRow) {
		k, err := keepstate.Keeper(r.ctx, true)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			return
		}
		if !k.Installed && !k.Loaded && !k.Kept {
			if why, err := keepIneligible(r.ctx); err == nil && why != "" {
				r.verdict = "not kept: " + why
				return
			}
		}
		c := k.Check(now)
		r.warn, r.verdict = !c.OK, c.Detail
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, kit.NoRepos)
		return nil
	}
	printKeepRows(m, w, rows)
	return keepFailures(rows)
}
