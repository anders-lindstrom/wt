package wtsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DeferredResult is what one deferred step did.
type DeferredResult struct {
	Step                         Deferred
	Ran                          bool
	Why                          string
	Output                       string
	Err                          error
	Commit                       string
	Files, Insertions, Deletions int
	Elapsed                      time.Duration
}

// ChangedPaths lists the paths that differ between two tips: what the rebase
// changed, conflicts or not (spec §3).
func ChangedPaths(wtPath, oldTip, newTip string) ([]string, error) {
	out, err := gitEnv(wtPath, nil, nil, "diff", "--name-only", "-z", oldTip, newTip)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

var shortstatRE = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// DeferTimeout bounds one deferred step.
const DeferTimeout = 30 * time.Minute

// RunDeferred runs the steps whose paths the rebase touched, in order, and
// commits a step's output when it changes tracked files and the step
// declares a message. A failed step is owed, never undone, and stops the
// steps after it: its half-written files must not be swept into a later
// step's commit. A commit git or a hook refuses is a step failure too, not
// a returned error: the rebase stands and the output stays in the worktree.
// The error return is only for reading what the rebase changed, which
// happens before any step runs.
func RunDeferred(wtPath string, steps []Deferred, oldTip, newTip string, log io.Writer) ([]DeferredResult, error) {
	return runDeferredWithTimeout(wtPath, steps, oldTip, newTip, log, DeferTimeout)
}

func runDeferredWithTimeout(wtPath string, steps []Deferred, oldTip, newTip string, log io.Writer, timeout time.Duration) ([]DeferredResult, error) {
	changed, err := ChangedPaths(wtPath, oldTip, newTip)
	if err != nil {
		return nil, err
	}
	var results []DeferredResult
	failed := false
	for _, step := range steps {
		r := DeferredResult{Step: step}
		switch {
		case failed:
			r.Why = "not run: an earlier step failed"
			results = append(results, r)
			continue
		case len(step.Paths) > 0 && !anyMatches(step.Paths, changed):
			r.Why = "no listed path changed"
			results = append(results, r)
			continue
		}
		r.Ran = true
		if log != nil {
			fmt.Fprintf(log, "  ▸ %s\n", step.Run)
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "sh", "-c", step.Run)
		cmd.Dir = wtPath
		cmd.Env = withEnv(rebaseEnv...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		runErr := runScript(cmd)
		cancel()
		r.Elapsed = time.Since(start)
		r.Output = strings.TrimSpace(out.String())
		// Check the deadline before ErrWaitDelay: a step that finished
		// cleanly but left a child detached in its own process group (so our
		// group kill can't reach it) still holding stdout/stderr open past
		// the wait delay comes back as exec.ErrWaitDelay, which looks like
		// success — but by the time Wait returns, ctx may already be past
		// its deadline. The deadline must win over that appearance of
		// success (script.go's order), or the step is reported as a clean
		// success despite running past its deadline.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.Err = fmt.Errorf("timed out after %s", timeout)
		} else {
			if errors.Is(runErr, exec.ErrWaitDelay) {
				// The step itself exited zero; only a background child it
				// left running kept stdio open past the wait delay. That is
				// not a failure of the step.
				runErr = nil
			}
			if runErr != nil {
				var exit *exec.ExitError
				if errors.As(runErr, &exit) {
					r.Err = fmt.Errorf("exit %d", exit.ExitCode())
				} else {
					r.Err = runErr
				}
			}
		}
		if r.Err != nil {
			failed = true
			results = append(results, r)
			continue
		}
		status, serr := gitEnv(wtPath, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		switch {
		case serr != nil:
			r.Err = fmt.Errorf("status could not be read: %w", serr)
			failed = true
		case status == "":
		case step.Commit == "":
			r.Err = errors.New("left tracked changes but declares no commit")
			failed = true
		default:
			if cerr := commitOutput(wtPath, step.Commit); cerr != nil {
				// A hook refused, most likely. Unstage so the step's
				// output is left in the worktree exactly as it was
				// written: the step is owed, and the rebase stands.
				_, _ = gitEnv(wtPath, rebaseEnv, nil, "reset", "-q")
				r.Err = fmt.Errorf("commit failed: %w", cerr)
				failed = true
				break
			}
			if merr := commitStat(wtPath, &r); merr != nil {
				r.Err = merr
				failed = true
			}
		}
		results = append(results, r)
	}
	return results, nil
}

// commitOutput stages what the step changed and commits it. Hooks run: a
// repository's pre-commit and commit-msg apply to a deferred commit like any
// other, and one that refuses makes the step owed rather than failing the run.
func commitOutput(wtPath, message string) error {
	if _, err := gitEnv(wtPath, rebaseEnv, nil, "add", "-u"); err != nil {
		return err
	}
	_, err := gitEnv(wtPath, rebaseEnv, nil, "commit", "--no-gpg-sign", "-q", "-m", message)
	return err
}

// commitStat fills in the commit's short id and its shortstat.
func commitStat(wtPath string, r *DeferredResult) error {
	sha, err := gitEnv(wtPath, nil, nil, "rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	r.Commit = sha
	stat, err := gitEnv(wtPath, nil, nil, "diff", "--shortstat", "HEAD~1", "HEAD")
	if err != nil {
		return err
	}
	if m := shortstatRE.FindStringSubmatch(stat); m != nil {
		r.Files, _ = strconv.Atoi(m[1])
		r.Insertions, _ = strconv.Atoi(m[2])
		r.Deletions, _ = strconv.Atoi(m[3])
	}
	return nil
}

func anyMatches(patterns, paths []string) bool {
	for _, p := range paths {
		if matchesAny(patterns, p) {
			return true
		}
	}
	return false
}
