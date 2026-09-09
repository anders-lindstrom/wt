package wtsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Check is one thing Doctor looked at.
type Check struct {
	Name   string
	OK     bool
	Detail string
	// Fix repairs what Detail describes; nil when there is nothing to fix,
	// or fixing it is not Doctor's place (a missing declaration, a live
	// lock held by someone else).
	Fix func() error
}

// DoctorOptions tunes Doctor for callers and tests.
type DoctorOptions struct {
	Now  time.Time
	Keep time.Duration
	// Docker replaces the default `docker info` probe; nil runs it for real,
	// under a 10-second deadline.
	Docker func() error
}

// dockerDeadline bounds the default docker probe: doctor must return in a
// reasonable time even when the daemon is unreachable rather than merely
// slow to answer.
const dockerDeadline = 10 * time.Second

// Doctor checks what a run needs from a repository: a fetched trunk, a
// parseable declaration, the scripts and tools that declaration names, and
// nothing left behind by an earlier run. It never fixes anything itself;
// SyncRun does not call it either, since the checks that would block a run
// (trunk, declaration, scripts) are the same refusals a run already makes on
// its own. Doctor is the thing to run once before the first run in a
// repository and after anything changes.
func Doctor(mainRoot, trunk string, worktrees []repo.Worktree, opts DoctorOptions) ([]Check, error) {
	onto := "origin/" + trunk

	checks := []Check{trunkCheck(mainRoot, onto)}

	declCheck, cfg := declarationCheck(mainRoot, trunk, onto)
	checks = append(checks, declCheck)
	checks = append(checks, scriptsCheck(mainRoot, onto, cfg))
	checks = append(checks, rerereCheck(mainRoot))
	checks = append(checks, hooksCheck(mainRoot))
	checks = append(checks, treeFileCheck(mainRoot, onto, ".gitmodules", "submodules", "submodules are not handled by run"))
	checks = append(checks, lfsCheck(mainRoot, onto))
	if cfg != nil && len(cfg.Defer) > 0 {
		checks = append(checks, dockerCheck(opts.Docker))
	}

	safety, err := safetyCheck(mainRoot, opts)
	if err != nil {
		return nil, err
	}
	checks = append(checks, safety)

	locks, err := locksCheck(worktrees, opts.Now)
	if err != nil {
		return nil, err
	}
	checks = append(checks, locks)

	rebases, err := rebasesCheck(worktrees)
	if err != nil {
		return nil, err
	}
	checks = append(checks, rebases)

	return checks, nil
}

// rebasesCheck flags every worktree sitting mid-rebase: run refuses those,
// and one left behind by an interrupted run has to be finished or aborted
// by hand. The main checkout is not a rebase target, so it is not checked.
func rebasesCheck(worktrees []repo.Worktree) (Check, error) {
	var stuck []string
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		busy, err := RebaseInProgress(wt.Path)
		if err != nil {
			return Check{}, err
		}
		if busy {
			label := wt.Branch
			if label == "" {
				label = wt.Path
			}
			stuck = append(stuck, fmt.Sprintf("%s (git -C %s rebase --abort)", label, wt.Path))
		}
	}
	if len(stuck) == 0 {
		return Check{Name: "rebases", OK: true}, nil
	}
	return Check{Name: "rebases", OK: false, Detail: strings.Join(stuck, "; ")}, nil
}

// trunkCheck reports whether origin/<trunk> resolves in the object store.
func trunkCheck(mainRoot, onto string) Check {
	if _, err := git.Run(mainRoot, "rev-parse", "--verify", onto); err != nil {
		return Check{Name: "trunk", OK: false, Detail: "run git fetch origin"}
	}
	return Check{Name: "trunk", OK: true, Detail: onto + " resolves"}
}

// declarationCheck loads the declaration from trunk, returning it (nil on
// any error) so the scripts and docker checks can use it too.
func declarationCheck(mainRoot, trunk, onto string) (Check, *Config) {
	cfg, err := LoadFromTrunk(mainRoot, trunk)
	if err == nil {
		return Check{Name: "declaration", OK: true, Detail: ConfigFile + " on " + onto + " parses"}, cfg
	}
	if errors.Is(err, ErrNoConfig) {
		return Check{Name: "declaration", OK: false, Detail: "no " + ConfigFile + " on " + onto + ": reported only, never rebased"}, nil
	}
	return Check{Name: "declaration", OK: false, Detail: err.Error()}, nil
}

// scriptsCheck confirms every script strategy's run exists on trunk with
// mode 100755. A declaration that failed to load has nothing to check here;
// the declaration check above already reports why.
func scriptsCheck(mainRoot, onto string, cfg *Config) Check {
	if cfg == nil {
		return Check{Name: "scripts", OK: true}
	}
	var bad []string
	for _, r := range cfg.Conflicts {
		if r.Strategy != "script" {
			continue
		}
		mode, found, err := lsTreeMode(mainRoot, onto, r.Run)
		switch {
		case err != nil:
			bad = append(bad, r.Run+" ("+err.Error()+")")
		case !found:
			bad = append(bad, r.Run+" (missing)")
		case mode != "100755":
			bad = append(bad, r.Run+" (not executable)")
		}
	}
	if len(bad) > 0 {
		return Check{Name: "scripts", OK: false, Detail: strings.Join(bad, ", ")}
	}
	return Check{Name: "scripts", OK: true}
}

// lsTreeMode returns the mode `git ls-tree` reports for path at ref, and
// whether the path exists there at all. err is set only when ref itself
// could not be read: ls-tree exits 0 with empty output when ref resolves
// fine but path is simply not in it, so that case reports found=false with
// a nil error rather than being folded into "missing".
func lsTreeMode(mainRoot, onto, path string) (mode string, found bool, err error) {
	out, err := git.Run(mainRoot, "ls-tree", onto, "--", path)
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", false, nil
	}
	return fields[0], true, nil
}

// rerereCheck reports whether rerere.enabled is on. A run never passes
// --rerere-autoupdate and never consults a recorded resolution: it decides
// every stop from the conflict's three stages and the declaration. Turning
// rerere on is still worth it for the rebases a run refuses and hands back,
// which is why the failing case keeps a Fix.
func rerereCheck(mainRoot string) Check {
	out, err := git.Run(mainRoot, "config", "--get", "rerere.enabled")
	on := err == nil && out == "true"
	if on {
		return Check{Name: "rerere", OK: true, Detail: "on; a run recomputes every stop from its three stages and does not pass --rerere-autoupdate, so recorded resolutions never decide a stop"}
	}
	return Check{
		Name:   "rerere",
		OK:     false,
		Detail: "off; a run does not use rerere; enabling it records the resolutions of the rebases you do by hand (the ones run refuses)",
		Fix: func() error {
			_, err := git.Run(mainRoot, "config", "rerere.enabled", "true")
			return err
		},
	}
}

// hooksCheck reports any active pre-rebase or post-rewrite hook, which a
// rebase would run unasked. commit-msg and pre-commit are noted, not
// blocking: the deferred commit must satisfy them, and a failure there
// leaves the step owed with its output unstaged in the worktree, never
// undoing the rebase — the run's business, not doctor's to refuse over.
func hooksCheck(mainRoot string) Check {
	dir, err := hooksDir(mainRoot)
	if err != nil {
		return Check{Name: "hooks", OK: false, Detail: err.Error()}
	}
	var blocking, noted []string
	for _, name := range []string{"pre-rebase", "post-rewrite"} {
		if activeHook(dir, name) {
			blocking = append(blocking, name)
		}
	}
	for _, name := range []string{"commit-msg", "pre-commit"} {
		if activeHook(dir, name) {
			noted = append(noted, name)
		}
	}
	var parts []string
	if len(blocking) > 0 {
		parts = append(parts, "active: "+strings.Join(blocking, ", "))
	} else {
		parts = append(parts, "no active pre-rebase or post-rewrite hook")
	}
	if len(noted) > 0 {
		parts = append(parts, "note: "+strings.Join(noted, ", ")+" active; the deferred commit must satisfy them")
	}
	return Check{Name: "hooks", OK: len(blocking) == 0, Detail: strings.Join(parts, "; ")}
}

// hooksDir resolves the hooks directory a rebase in mainRoot would run:
// core.hooksPath when set (relative to mainRoot when it is a relative
// value), else the git dir's own hooks directory. Linked worktrees share
// the main repository's hooks, so this is always resolved against mainRoot.
func hooksDir(mainRoot string) (string, error) {
	if p, err := git.Run(mainRoot, "config", "--get", "core.hooksPath"); err == nil && p != "" {
		if filepath.IsAbs(p) {
			return p, nil
		}
		return filepath.Join(mainRoot, p), nil
	}
	rel, err := git.Run(mainRoot, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) {
		return rel, nil
	}
	return filepath.Join(mainRoot, rel), nil
}

// activeHook reports whether dir/name is an executable file. Sample hooks
// ship as name.sample, so checking the exact name already excludes them.
func activeHook(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// refResolves reports whether ref names something in the object store.
// `git cat-file -e <ref>:<path>` exits non-zero both when path is missing
// from a good ref and when ref itself is bad (128 either way on the git
// version this was tested against, not the 1-vs-other split the naive
// reading suggests, and distinguishable only by parsing the stderr text) so
// ref is checked on its own first instead.
func refResolves(mainRoot, ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = mainRoot
	return cmd.Run() == nil
}

// treeFileCheck fails when path exists on trunk, for checks (like
// submodules) that are pass/fail on presence alone. onto not resolving is
// reported as its own failure rather than as "path absent".
func treeFileCheck(mainRoot, onto, path, name, detail string) Check {
	if !refResolves(mainRoot, onto) {
		return Check{Name: name, OK: false, Detail: onto + " does not resolve; run git fetch origin"}
	}
	cmd := exec.Command("git", "cat-file", "-e", onto+":"+path)
	cmd.Dir = mainRoot
	if cmd.Run() == nil {
		return Check{Name: name, OK: false, Detail: detail}
	}
	// onto is known good, so any failure here means path is absent from it.
	return Check{Name: name, OK: true}
}

// lfsCheck flags any .gitattributes on trunk (root or nested) that declares
// an LFS filter: run does not handle LFS-tracked content. onto not
// resolving is reported as its own failure rather than as "no match".
func lfsCheck(mainRoot, onto string) Check {
	if !refResolves(mainRoot, onto) {
		return Check{Name: "lfs", OK: false, Detail: onto + " does not resolve; run git fetch origin"}
	}
	cmd := exec.Command("git", "grep", "-l", "-e", "filter=lfs", onto, "--", ".gitattributes", "**/.gitattributes")
	cmd.Dir = mainRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errText := strings.TrimSpace(stderr.String()); err != nil && errText != "" {
		// onto is known good, so output on stderr here is a real failure (a
		// bad pathspec, say), not silence-because-nothing-matched.
		return Check{Name: "lfs", OK: false, Detail: errText}
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		// err != nil with empty stdout and empty stderr is grep -l's "no
		// match"; err == nil never comes with empty stdout for -l.
		return Check{Name: "lfs", OK: true}
	}
	var files []string
	for _, line := range strings.Split(text, "\n") {
		_, path, ok := strings.Cut(line, ":")
		if !ok {
			path = line
		}
		files = append(files, path)
	}
	return Check{Name: "lfs", OK: false, Detail: "LFS paths are not handled by run: " + strings.Join(files, ", ")}
}

// dockerCheck runs probe (or `docker info` under a deadline, by default) and
// reports whether it succeeded. Only called when the declaration has
// deferred steps: nothing else in run touches Docker.
func dockerCheck(probe func() error) Check {
	if probe == nil {
		probe = func() error {
			ctx, cancel := context.WithTimeout(context.Background(), dockerDeadline)
			defer cancel()
			cmd := exec.CommandContext(ctx, "docker", "info")
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			return cmd.Run()
		}
	}
	if err := probe(); err != nil {
		return Check{Name: "docker", OK: false, Detail: "deferred steps that need Docker will be owed: " + err.Error()}
	}
	return Check{Name: "docker", OK: true}
}

// safetyCheck flags every safety ref Prunable would delete: they pin old
// history a run has already superseded and block garbage collection.
func safetyCheck(mainRoot string, opts DoctorOptions) (Check, error) {
	all, err := ListSafety(mainRoot)
	if err != nil {
		return Check{}, err
	}
	prunable := Prunable(all, opts.Now, opts.Keep)
	if len(prunable) == 0 {
		return Check{Name: "safety-refs", OK: true}, nil
	}
	var refs []string
	for _, s := range prunable {
		refs = append(refs, s.Ref)
	}
	fix := func() error {
		for _, s := range prunable {
			if err := DeleteSafety(mainRoot, s); err != nil {
				return err
			}
		}
		return nil
	}
	suffix := "s"
	if len(prunable) == 1 {
		suffix = ""
	}
	detail := fmt.Sprintf("%d ref%s pin old history (%s)", len(prunable), suffix, strings.Join(refs, ", "))
	return Check{Name: "safety-refs", OK: false, Detail: detail, Fix: fix}, nil
}

// locksCheck flags every wt-sync.lock found in any worktree's git dir: an
// expired one (older than LockExpiry) gets a Fix that removes it; a live one
// is only named, since it belongs to a run that may still be in progress.
func locksCheck(worktrees []repo.Worktree, now time.Time) (Check, error) {
	type held struct {
		label   string
		gitDir  string
		lock    *Lock
		expired bool
	}
	var found []held
	for _, wt := range worktrees {
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return Check{}, err
		}
		lock, ok, err := ReadLock(gitDir)
		if err != nil {
			return Check{}, err
		}
		if !ok {
			continue
		}
		label := wt.Branch
		if label == "" {
			label = wt.Path
		}
		found = append(found, held{label: label, gitDir: gitDir, lock: lock, expired: now.Sub(lock.Started) >= LockExpiry})
	}
	if len(found) == 0 {
		return Check{Name: "locks", OK: true}, nil
	}
	var parts []string
	var expired []held
	for _, h := range found {
		state := "live"
		if h.expired {
			state = "expired"
			expired = append(expired, h)
		}
		parts = append(parts, fmt.Sprintf("%s (%s, pid %d since %s)", h.label, state, h.lock.PID, h.lock.Started.Format(time.RFC3339)))
	}
	var fix func() error
	if len(expired) > 0 {
		fix = func() error {
			for _, h := range expired {
				if err := os.Remove(filepath.Join(h.gitDir, LockName)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			return nil
		}
	}
	return Check{Name: "locks", OK: false, Detail: strings.Join(parts, "; "), Fix: fix}, nil
}
