package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/anders-lindstrom/wt/internal/config"
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
	Ask func(question string) (bool, error)
}

// keepRow is one repository's line in start, stop or status across
// repositories.
type keepRow struct {
	target RepoTarget
	ctx    *Context
	job    keepJob
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
func keepRows(u *config.User, sel Selection, fn func(*keepRow)) ([]*keepRow, error) {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return nil, err
	}
	rows := make([]*keepRow, 0, len(set.Repos))
	for _, t := range set.Repos {
		r := &keepRow{target: t}
		rows = append(rows, r)
		if t.Problem != "" {
			r.failed, r.verdict = true, t.Problem
			continue
		}
		ctx, err := Open(t.Path)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			continue
		}
		r.ctx = ctx
		fn(r)
	}
	return rows, nil
}

// printKeepRows prints each row as its repository's path and what came of
// it, the paths in one column.
func printKeepRows(w io.Writer, rows []*keepRow) {
	home, _ := os.UserHomeDir()
	width := 0
	for _, r := range rows {
		width = max(width, len(abbreviateHome(r.target.Path, home)))
	}
	for _, r := range rows {
		mark := " "
		if r.failed || r.warn {
			mark = "!"
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", mark, width, abbreviateHome(r.target.Path, home), r.verdict)
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
	return notCompleted(failed)
}

// keepIneligible is why a repository is not one to keep, "" when it is: a
// keeper runs wt sync, which a repository opts into with a .wt-sync.yaml on
// trunk. The error is a declaration wt cannot read, or a trunk it has not
// fetched.
func keepIneligible(ctx *Context) (string, error) {
	_, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, ctx.Config.MainBranch)
	switch {
	case errors.Is(err, wtsync.ErrNoConfig):
		return fmt.Sprintf("no %s on origin/%s", wtsync.ConfigFile, ctx.Config.MainBranch), nil
	case err != nil:
		return "", err
	}
	return "", nil
}

// SyncKeepStartAll is wt sync keep start across repositories: a job for
// every repository a selection names that declares wt sync and has none yet,
// each exactly as start in that repository installs it. A repository with a
// job is left as it is, whatever its interval, so a second start installs
// only what the first did not. The plan is printed first and one question
// covers it.
func SyncKeepStartAll(u *config.User, sel Selection, opts KeepStartAllOptions, w io.Writer) error {
	s, err := newKeepStarter(opts.KeepStartOptions)
	if err != nil {
		return err
	}
	rows, err := keepRows(u, sel, func(r *keepRow) {
		job, err := s.job(r.ctx)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			return
		}
		r.job = job
		installed, present := keepJobPresent(job)
		switch {
		case installed:
			k, err := keeper(r.ctx, false)
			if err != nil {
				r.failed, r.verdict = true, err.Error()
				return
			}
			r.verdict = "already kept, every " + fmtEvery(k.st.interval()) + "; left as it is"
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
			r.act, r.verdict = true, "to install, every "+fmtEvery(s.every)
		}
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No repositories wt manages here; wt repos says where it looked.")
		return nil
	}
	printKeepRows(w, rows)
	var todo []*keepRow
	for _, r := range rows {
		if r.act {
			todo = append(todo, r)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(w, "Nothing to install.")
		return keepFailures(rows)
	}
	fmt.Fprintln(w, keepAuthLine(todo[0].job))
	what := fmt.Sprintf("a keeper in %s", repoCount(len(todo)))
	switch {
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was installed: --dry-run.")
		return keepFailures(rows)
	case opts.Yes:
	case opts.Ask != nil:
		ok, err := opts.Ask("Install " + what + "?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was installed.")
			return keepFailures(rows)
		}
	default:
		fmt.Fprintf(w, "Nothing was installed: there is no terminal to ask. Pass --yes to install %s.\n", what)
		return keepFailures(rows)
	}
	for _, r := range todo {
		next, err := s.install(r.ctx, r.job)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			continue
		}
		r.verdict = "installed, every " + fmtEvery(s.every) + ", first at " + keepClock(next, s.now())
	}
	fmt.Fprintln(w)
	printKeepRows(w, todo)
	fmt.Fprintln(w, "Each keeper logs to .git/wt-sync-keep.log in its main checkout; wt sync keep status --all")
	return keepFailures(rows)
}

// SyncKeepStopAll is wt sync keep stop across repositories: every job a
// selection's repositories have is booted out and its plist removed, with
// no question, since start puts it back. A repository with no job is listed
// and left alone; one that no longer declares wt sync is stopped all the
// same.
func SyncKeepStopAll(u *config.User, sel Selection, dryRun bool, w io.Writer) error {
	if keepGOOS != "darwin" {
		return ErrNoLaunchd
	}
	rows, err := keepRows(u, sel, func(r *keepRow) {
		job, err := keepJobFor(r.ctx)
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
		fmt.Fprintln(w, "No repositories wt manages here; wt repos says where it looked.")
		return nil
	}
	printKeepRows(w, rows)
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
		fmt.Fprintf(w, "Stopped a keeper in %s; the logs stay.\n", repoCount(stopped))
	}
	return keepFailures(rows)
}

// SyncKeepStatusAll is wt sync keep status across repositories, one line
// each: what wt sync doctor's keeper row says, or why the repository is not
// one to keep.
func SyncKeepStatusAll(u *config.User, sel Selection, now time.Time, w io.Writer) error {
	rows, err := keepRows(u, sel, func(r *keepRow) {
		k, err := keeper(r.ctx, true)
		if err != nil {
			r.failed, r.verdict = true, err.Error()
			return
		}
		if !k.installed && !k.loaded && !k.kept {
			if why, err := keepIneligible(r.ctx); err == nil && why != "" {
				r.verdict = "not kept: " + why
				return
			}
		}
		c := k.check(now)
		r.warn, r.verdict = !c.OK, c.Detail
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No repositories wt manages here; wt repos says where it looked.")
		return nil
	}
	printKeepRows(w, rows)
	return keepFailures(rows)
}
