# wt sync resume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two things, in order. **(1)** `recipe` starts meaning what the help text already promises: the object-store simulation resolves each stop with the declared strategies, writes the resolved blobs back into the merged tree and keeps replaying, so the class reflects the *whole* replay and `Replay.Stop` names the first stop a person actually owns, wherever it is. **(2)** A contested stop is no longer aborted: `wt sync run` leaves the rebase in place with the strategy-resolved files staged, writes the plan file of spec §6, keeps the lock and the safety ref, and prints the §5 `needs you` line; `wt sync resume <work>` verifies nothing was hand-merged where a strategy owns the file and drives the same loop to the end; `wt sync undo` aborts and restores a rebase that carries a wt plan file.

**Architecture:** Part 1 changes `internal/wtsync/replay.go` only in what it does after a stop — it now asks the strategies (through the existing `resolveConflict`, in its object-store mode) and, when they all answer, writes their bytes into the conflicted tree with a scratch index (`read-tree` → `update-index --index-info` → `write-tree`) and chains the next simulated commit onto it. `triage.go` stops re-running the strategies itself and reads the outcomes the replay recorded. Part 2 splits the loop inside `Rebase` into a reusable `driver` so `Resume` can re-enter it, adds `plan.go` (the §6 markdown plus a JSON sidecar resume verifies against) and `landing.go` (the `scopes:` line), and adds `internal/commands/sync_resume.go` plus the shared handover/completion tails that `sync_run.go` and `sync_resume.go` both use.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3`, `encoding/json`, git ≥ 2.40 (Anders runs 2.55). Tests are `go test` with throwaway repositories built by `internal/wtsync/replay_test.go` (`repoWith`, `linearRepo`, `gitIn`) and `internal/commands/sync_test.go` (`syncRepo`, `gitOut`).

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` §1 (triage, the simulation, "triage is a promise"), §5 (only the after-the-fact `needs you` line), §6 (the plan file), §7 (surfaces: `resume`). The plans it follows: `docs/superpowers/plans/2026-09-09-wt-sync-foundation.md` (the simulation) and `docs/superpowers/plans/2026-09-09-wt-sync-run.md` (the run, whose Global Constraints are copied below).

## Global Constraints

Copied from the run plan, still binding:

- **Only `run`, `resume`, `undo` and `doctor --fix/--prune` write.** `wt sync` with no verb stays read-only; every `git status` it runs keeps `--no-optional-locks`.
- **A safety ref is written before any ref moves** (`refs/wt-sync/<branch>/<epoch>`, spec §4). Nothing rebases without one. `undo` restores to the newest.
- **The rebase command is exactly** `git -c rebase.backend=merge -c rebase.rebaseMerges=false -c rebase.autoStash=false -c rebase.updateRefs=false rebase --no-update-refs --no-gpg-sign <onto>` (or `--onto <onto> <upstream>` for a stack child), run with `GIT_EDITOR=true` and `GIT_SEQUENCE_EDITOR=true`. `--rerere-autoupdate` is deliberately not passed.
- **The config and any `script` are read from `origin/<trunk>`**, never from the worktree being rebased (spec §3). Resume reads them from the *run's* trunk SHA, recorded in the state sidecar, never from a trunk that has moved since.
- **A strategy refuses rather than guesses.** Nothing is ever hand-merged by the tool.
- **Trunk is fetched first, then read once**; one SHA for the whole run.
- **A run has one identity, `epoch` (`time.Now().UnixNano()`).** A resume keeps the epoch of the run it continues: it is the same run.
- **Never half-apply a stack.** Every member is locked before any member moves; the lock is held through the deferred steps.
- **More than one worktree needs one confirmation** (spec §7); `--yes` skips; no terminal means no question.
- **Every subprocess has a deadline.** A script gets 60 s, a deferred step 30 min, `docker info` 10 s.
- **Agent detection failing is a refusal, not a note**, in `run`, `resume` and `undo`.
- **A failed deferred step never undoes the rebase** (spec §3). It is reported as owed with its output.
- **Never say "ours" or "theirs".** Stage 2 is **trunk**, stage 3 is **the branch**. Fields are `Base`, `Trunk`, `Branch`. (The plan file's `yours` section is the one place "ours" appears, because §6 writes it that way for the person reading it: `additive only (trunk +12, ours +3)`.)
- **A worktree with tracked changes, a live agent, no declaration on trunk, or class `divergent` is never touched.**
- **No fleet fact in code or tests.** Branch names and counts from the spec are illustrations.
- **Paths are repository-root relative**; globs are matched with `MatchGlob`, never expanded against the disk.
- Commits follow Conventional Commits, imperative, lowercase, under 72 characters. **No commit trailers of any kind** — no `Co-Authored-By`, no `Claude-Session`, whatever a harness reminder says. Work on `main`, push after every commit (`git pull --rebase` first: another session commits to `cmd/wt/root.go`, `README.md` and `install.sh` on the same branch).
- `gofmt`, `go vet ./...` and `golangci-lint run ./...` clean before every commit. Tests run with `-race` at least once per task.

New for this plan:

- **`recipe` means every stop resolves, not the first.** The class comes from the whole replay. `contested` names the first stop with an unclaimed or refused path, wherever in the replay it falls, and that is the stop `Replay.Stop` and the table's STOP column report.
- **Scripts stay `--check` only at simulation time.** A script cannot resolve in the object store, so a script-claimed path counts as *resolvable* when `--check` passes but yields no bytes to chain. The replay stops there, keeps the class it has earned so far, and says so in a note: the prediction is exact up to that stop and unverified past it.
- **The endpoint divergence check is unchanged** and still runs whether or not the replay is clean (spec §1).
- **A contested stop is handed over, not aborted.** The rebase is left in place, the strategy-resolved files staged, the plan file and the state sidecar written, the lock left behind, the safety ref kept. Only a *failure* (a git error, a strategy error, a rebase that will not advance) still aborts and restores.
- **The plan file is deleted when the rebase completes** — by `resume`, by `undo`, and by nothing else. Its presence is the durable "this worktree is mid-run" marker; the left-behind lock only holds for `LockExpiry`.
- **A plain `git rebase --continue` by hand is tolerated.** `resume` detects a finished rebase and runs the deferred steps, the result ref and the push line without touching the rebase.
- **Resume verifies before it continues:** the plan file and the safety ref are present, no unmerged path remains, and no path a strategy resolved at that stop has a different staged blob than the one recorded. Any of those failing is a refusal that changes nothing.

---

## File Structure

```
internal/wtsync/
  simtree.go       resolvedTree: write strategy bytes into a merge-tree tree via a scratch index  (new)
  simtree_test.go                                                                                  (new)
  replay.go        Stop gains Files/Resolved; Replay gains Stops/Truncated/Why/Err;
                   SimulateRebase takes *Config, resolves each stop and keeps replaying           (modified)
  triage.go        Assess reads the replay's outcomes; Recipe/Contested from the whole replay;
                   Paused: a worktree a run left mid-rebase                                        (modified)
  landing.go       Landing, ScopeCount, LandingList: the first-parent log and its scopes (§5)      (new)
  landing_test.go                                                                                  (new)
  plan.go          PlanName/StateName, State, ReadState/WriteState/RemovePlan/HasPlan/PlanHolders,
                   PlanInput, RenderPlan: the §6 markdown                                          (new)
  plan_test.go                                                                                     (new)
  rebase.go        driver{} extracted from Rebase; Handover; Result.Left; Resume;
                   Preflight lets Contested proceed                                                (modified)
  lock.go          Lock.Keep, LeftLock, TakeOver                                                   (modified)
  undo.go          abort and restore a rebase that carries a wt plan file                          (modified)
  doctor.go        (unchanged; the plan row is built in commands, where work names exist)
internal/commands/
  sync_finish.go   handover() and completeRun(): the two tails run and resume share                (new)
  sync_run.go      hands a contested stop over instead of refusing it                              (modified)
  sync_resume.go   SyncResume                                                                      (new)
  sync_resume_test.go                                                                              (new)
  sync_undo.go     (unchanged surface; undo.go does the work)
  sync_doctor.go   the `plan` row                                                                  (modified)
  sync.go          STOP column reads the deciding stop; NOTE says how many stops                   (modified)
cmd/wt/sync.go     `resume` subcommand; help text: recipe means every stop, contested is handed over
README.md          the surfaces table gains resume
test/sync_run.bats a smoke test for the handover and resume
```

---

## Part 1 — `recipe` means every stop

### Task 1: Write a strategy's bytes back into a merged tree

**Files:**
- Create: `internal/wtsync/simtree.go`
- Create: `internal/wtsync/simtree_test.go`

**Interfaces:**
- Consumes: `gitEnv(dir, env, stdin, args...)`, `hashObject(root, data)` (both `internal/wtsync/script.go`).
- Produces: `func resolvedTree(mainRoot, tree string, resolved map[string][]byte) (string, error)` — package-private, used by Task 2.

- [ ] **Step 1: Write the failing test**

Create `internal/wtsync/simtree_test.go`:

```go
package wtsync

import (
	"strings"
	"testing"
)

func TestResolvedTreeReplacesABlob(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"}, nil, nil)
	tree := strings.TrimSpace(gitOutIn(t, dir, "rev-parse", "HEAD^{tree}"))

	out, err := resolvedTree(dir, tree, map[string][]byte{"sub/b.txt": []byte("resolved\n")})
	if err != nil {
		t.Fatal(err)
	}
	if out == tree {
		t.Fatal("expected a new tree")
	}
	if got := gitOutIn(t, dir, "cat-file", "blob", out+":sub/b.txt"); got != "resolved\n" {
		t.Fatalf("sub/b.txt = %q, want %q", got, "resolved\n")
	}
	// Everything else is carried over untouched.
	if got := gitOutIn(t, dir, "cat-file", "blob", out+":a.txt"); got != "a\n" {
		t.Fatalf("a.txt = %q, want %q", got, "a\n")
	}
}

func TestResolvedTreeKeepsTheExecutableBit(t *testing.T) {
	dir := repoWith(t, map[string]string{"s.sh": "old\n"}, nil, nil)
	gitIn(t, dir, "update-index", "--chmod=+x", "s.sh")
	gitIn(t, dir, "commit", "-q", "-m", "exec")
	tree := strings.TrimSpace(gitOutIn(t, dir, "rev-parse", "HEAD^{tree}"))

	out, err := resolvedTree(dir, tree, map[string][]byte{"s.sh": []byte("new\n")})
	if err != nil {
		t.Fatal(err)
	}
	if mode := strings.Fields(gitOutIn(t, dir, "ls-tree", out, "--", "s.sh"))[0]; mode != "100755" {
		t.Fatalf("mode = %s, want 100755", mode)
	}
}

func TestResolvedTreeRefusesAPathThatIsNotThere(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := strings.TrimSpace(gitOutIn(t, dir, "rev-parse", "HEAD^{tree}"))

	_, err := resolvedTree(dir, tree, map[string][]byte{"missing.txt": []byte("x\n")})
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("err = %v, want it to name missing.txt", err)
	}
}

func TestResolvedTreeIsANoOpForNoPaths(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := strings.TrimSpace(gitOutIn(t, dir, "rev-parse", "HEAD^{tree}"))

	out, err := resolvedTree(dir, tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != tree {
		t.Fatalf("tree = %s, want it unchanged (%s)", out, tree)
	}
}
```

`gitOutIn` may not exist in this package yet. If `grep -n "func gitOutIn" internal/wtsync/*_test.go` finds nothing, add it next to `gitIn` in `replay_test.go`:

```go
// gitOutIn runs git in dir and returns its stdout, failing the test on error.
func gitOutIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/wtsync/ -run TestResolvedTree -v`
Expected: FAIL — `undefined: resolvedTree`.

- [ ] **Step 3: Write `simtree.go`**

```go
package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// resolvedTree writes each path's resolved bytes into tree and returns the
// tree that results. It is how the simulation carries a resolved stop
// forward: merge-tree writes a tree with conflict markers in it, and this
// replaces those blobs with what the strategies answered, without an index,
// a working tree or a ref being touched.
//
// The mode is whatever the merged tree already carries for the path, so an
// executable stays executable. A path the merged tree does not carry is an
// error rather than an addition: the simulation only ever replaces a file
// git itself put there.
func resolvedTree(mainRoot, tree string, resolved map[string][]byte) (string, error) {
	if len(resolved) == 0 {
		return tree, nil
	}
	index, cleanup, err := scratchIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := gitEnv(mainRoot, env, nil, "read-tree", tree); err != nil {
		return "", fmt.Errorf("read-tree %s: %w", tree, err)
	}
	paths := make([]string, 0, len(resolved))
	for p := range resolved {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	modes, err := indexModes(mainRoot, env, paths)
	if err != nil {
		return "", err
	}
	var info strings.Builder
	for _, p := range paths {
		mode, ok := modes[p]
		if !ok {
			return "", fmt.Errorf("%s is not in the merged tree %s", p, tree)
		}
		oid, err := hashObject(mainRoot, resolved[p])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "%s %s\t%s\x00", mode, oid, p)
	}
	if _, err := gitEnv(mainRoot, env, strings.NewReader(info.String()), "update-index", "-z", "--index-info"); err != nil {
		return "", fmt.Errorf("update-index: %w", err)
	}
	out, err := gitEnv(mainRoot, env, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return out, nil
}

// indexModes reads the mode each path carries in the scratch index. Paths
// are passed after "--" and read back NUL-separated, so a name with a space
// or a quote in it survives.
func indexModes(mainRoot string, env, paths []string) (map[string]string, error) {
	args := append([]string{"ls-files", "--stage", "-z", "--"}, paths...)
	out, err := gitEnv(mainRoot, env, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("ls-files --stage: %w", err)
	}
	modes := map[string]string{}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		modes[path] = f[0]
	}
	return modes, nil
}

// scratchIndex is a private index file git creates for itself. The file must
// not exist when git first writes it: an empty file is not a valid index.
func scratchIndex() (string, func(), error) {
	dir, err := os.MkdirTemp("", "wtsync-simindex-")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/wtsync/ -run TestResolvedTree -v`
Expected: PASS, four tests.

- [ ] **Step 5: Full package, lint, commit**

```bash
gofmt -l internal/wtsync && go vet ./... && golangci-lint run ./... && go test -race ./internal/wtsync/
git add internal/wtsync/simtree.go internal/wtsync/simtree_test.go internal/wtsync/replay_test.go
git commit -m "feat(sync): write a strategy's answer back into a merged tree"
git pull --rebase && git push origin main
```

---

### Task 2: The simulation resolves each stop and keeps replaying

**Files:**
- Modify: `internal/wtsync/replay.go` (`Stop`, `Replay`, `SimulateRebase`)
- Modify: `internal/wtsync/replay_test.go`
- Modify: `internal/wtsync/triage.go:98-110` (the `SimulateRebase` call site only; classification is Task 3)

**Interfaces:**
- Consumes: `resolvedTree` (Task 1); `resolveConflict(mainRoot, onto, cfg, c, wtPath)` returning `Resolution{Outcome FileOutcome, Content []byte, InPlace bool}` (`resolve.go`); `cfg.RuleFor(path) (Rule, bool)`.
- Produces:
  - `type Stop struct { Index, Total int; Commit, Subject string; Conflicts []Conflict; Messages string; Files []FileOutcome; Resolved bool }`
  - `type Replay struct { Commits int; Stops []Stop; Stop *Stop; Truncated bool; Why string; Err error }`
  - `func SimulateRebase(mainRoot, onto, branch string, cfg *Config) (Replay, error)`

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/replay_test.go`. `ownedLineConfig` builds a declaration that claims `v.txt`'s version line, the shape `repoWith`'s fixtures already use.

```go
func ownedLineConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^[0-9]'\n    rule: max-plus-patch\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Two commits both conflict on the claimed file. Before this change the
// replay stopped at the first; now it resolves both and reports recipe.
func TestSimulateResolvesEveryStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil {
		t.Fatalf("Stop = %+v, want nil: both stops are resolved", r.Stop)
	}
	if len(r.Stops) != 2 {
		t.Fatalf("Stops = %d, want 2", len(r.Stops))
	}
	for i, s := range r.Stops {
		if !s.Resolved {
			t.Fatalf("stop %d not resolved: %+v", i+1, s.Files)
		}
	}
	if r.Err != nil {
		t.Fatalf("Err = %v", r.Err)
	}
}

// The first stop resolves; the second is a file nothing claims. The replay
// must reach the second and name it, which is the whole point of Part 1.
func TestSimulateStopsAtTheFirstUnclaimedStopWhereverItIs(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"a.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil {
		t.Fatal("Stop = nil, want the second commit")
	}
	if r.Stop.Index != 2 || r.Stop.Total != 2 {
		t.Fatalf("Stop at %d/%d, want 2/2", r.Stop.Index, r.Stop.Total)
	}
	if len(r.Stop.Files) != 1 || r.Stop.Files[0].Path != "a.txt" || r.Stop.Files[0].Resolved {
		t.Fatalf("Stop.Files = %+v, want a.txt unresolved", r.Stop.Files)
	}
	if len(r.Stops) != 2 || !r.Stops[0].Resolved {
		t.Fatalf("Stops = %+v, want the first one resolved", r.Stops)
	}
}

// With no declaration nothing is claimed, so the first stop still stops the
// replay: the behaviour every existing caller had.
func TestSimulateWithoutAConfigStopsAtTheFirst(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 1 {
		t.Fatalf("Stop = %+v, want 1/2", r.Stop)
	}
}

// A resolved stop feeds the next commit: the third commit must see the
// resolved 1.1.1, not trunk's 2.0.0 and not the branch's 1.1.0.
func TestSimulateChainsTheResolvedContent(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"b.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil {
		t.Fatalf("Stop = %+v, want nil", r.Stop)
	}
	if len(r.Stops) != 1 || r.Stops[0].Index != 1 {
		t.Fatalf("Stops = %+v, want one at 1/2", r.Stops)
	}
	if got := r.Stops[0].Files[0]; got.Strategy != "owned-line" || !got.Resolved {
		t.Fatalf("file = %+v, want owned-line resolved", got)
	}
}
```

The existing `SimulateRebase` tests in this file call it with three arguments; add `, nil` to every one of them.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/wtsync/ -run TestSimulate -v`
Expected: FAIL — too many arguments to `SimulateRebase`, and `Replay` has no field `Stops`.

- [ ] **Step 3: Change `replay.go`**

Replace the `Stop` and `Replay` types:

```go
// Stop is one commit at which a rebase stops, with the three blobs of every
// file it conflicts on and what the declared strategies answered for them.
type Stop struct {
	Index     int // 1-based position among the commits the rebase replays
	Total     int
	Commit    string
	Subject   string
	Conflicts []Conflict
	Messages  string
	// Files is one outcome per conflict, in the same order. Empty when
	// merge-tree reported a conflict with no blobs of its own.
	Files []FileOutcome
	// Resolved is true when every conflict here was answered by a strategy.
	Resolved bool
}

// Replay is the outcome of simulating a rebase commit by commit, with the
// declared strategies applied at every stop.
type Replay struct {
	Commits int
	// Stops is every stop the replay reached, in order.
	Stops []Stop
	// Stop is the first stop the strategies did not resolve — the one a
	// person owns. nil when every stop resolved, or there was none.
	Stop *Stop
	// Truncated is set when the replay could not carry past a stop it
	// resolved: a script claimed a path, and a script can only be checked
	// in the object store, never asked for bytes. Why says which.
	Truncated bool
	Why       string
	// Err collects strategy failures — a script that would not run, a
	// declaration that will not build. A file whose strategy failed is not
	// resolved, so the class fails closed; this carries the reason.
	Err error
}
```

Replace the body of `SimulateRebase`:

```go
// SimulateRebase replays branch onto `onto` inside the object store, the way
// `git rebase onto` would, applying cfg's declared strategies at every stop
// and carrying their answers forward. It reports every stop it reached and
// the first one the strategies did not resolve. No ref, index or working
// tree is touched; the only objects created are unreachable blobs, trees and
// commits. A nil cfg claims nothing, so the first stop is the last.
func SimulateRebase(mainRoot, onto, branch string, cfg *Config) (Replay, error) {
	var rep Replay
	// The same selection and order the rebase sequencer uses: right side
	// only, patch-equivalent commits dropped, merges flattened, topological.
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--reverse", "--topo-order", "--right-only", "--cherry-pick", "--no-merges", onto+"..."+branch, "--")
	if err != nil {
		return rep, err
	}
	var commits []string
	if out != "" {
		commits = strings.Split(out, "\n")
	}
	rep.Commits = len(commits)
	base, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", onto+"^{commit}", "--")
	if err != nil {
		return rep, err
	}
	for i, c := range commits {
		tree, clean, conflicts, messages, err := mergeTree(mainRoot, c+"^", base, c)
		if err != nil {
			return rep, err
		}
		if !clean {
			subject, err := gitEnv(mainRoot, nil, nil, "log", "-1", "--format=%s", c, "--")
			if err != nil {
				return rep, err
			}
			stop := Stop{
				Index: i + 1, Total: len(commits), Commit: c, Subject: subject,
				Conflicts: conflicts, Messages: messages,
			}
			// A conflict merge-tree reports only in its messages has no
			// blobs to put to a strategy, so it is nobody's but a person's.
			resolved := map[string][]byte{}
			script := ""
			stop.Resolved = len(conflicts) > 0
			for _, cf := range conflicts {
				r, rerr := resolveConflict(mainRoot, onto, cfg, cf, "")
				rep.Err = errors.Join(rep.Err, rerr)
				stop.Files = append(stop.Files, r.Outcome)
				if !r.Outcome.Resolved {
					stop.Resolved = false
					continue
				}
				if r.Outcome.Strategy == "script" {
					// --check said the script owns it, which is all a
					// script can say here: it resolves against a real
					// index in a worktree, never in the object store.
					if rule, ok := cfg.RuleFor(cf.Path); ok && script == "" {
						script = fmt.Sprintf("%s owns %s and can only be checked before a run", rule.Run, cf.Path)
					}
					continue
				}
				resolved[cf.Path] = r.Content
			}
			rep.Stops = append(rep.Stops, stop)
			if !stop.Resolved {
				last := rep.Stops[len(rep.Stops)-1]
				rep.Stop = &last
				return rep, nil
			}
			if script != "" {
				rep.Truncated = true
				rep.Why = fmt.Sprintf("replayed to stop %d/%d only: %s", stop.Index, stop.Total, script)
				return rep, nil
			}
			if tree, err = resolvedTree(mainRoot, tree, resolved); err != nil {
				return rep, err
			}
		}
		// A commit whose changes are already present replays to the same
		// tree; rebase drops it, so no simulated commit is made for it.
		// base is always a SHA computed above (verified, or a commit-tree
		// result), never a user-supplied ref, so it carries no path
		// ambiguity; no "--" here, since plain (non-`--verify`) `rev-parse`
		// echoes a trailing "--" back as a second output line, which would
		// corrupt this comparison.
		baseTree, err := gitEnv(mainRoot, nil, nil, "rev-parse", base+"^{tree}")
		if err != nil {
			return rep, err
		}
		if baseTree == tree {
			continue
		}
		base, err = gitEnv(mainRoot, simEnv, nil, "commit-tree", tree, "-p", base, "-m", "wt sync simulation")
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}
```

Add `"errors"` to the imports if it is not already there (it is, for `mergeTree`).

- [ ] **Step 4: Keep `triage.go` compiling**

In `Assess`, change only the call and the immediate use, leaving the classification for Task 3:

```go
	a.Replay, err = SimulateRebase(mainRoot, onto, wt.Branch, cfg)
	if err != nil {
		a.Err = err
		return a
	}
	a.Err = errors.Join(a.Err, a.Replay.Err)
	if a.Replay.Stop == nil {
		a.Class = Clean
	} else {
		a.Files = a.Replay.Stop.Files
		a.Class, a.Files = classifyStop(a.Files, a.Replay.Stop.Messages)
	}
```

(The old loop calling `tryStrategy` over `a.Replay.Stop.Conflicts` goes away: the replay has already asked.)

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/wtsync/ -run 'TestSimulate|TestAssess' -v`
Expected: PASS. Any existing test that asserted `recipe` on a first stop while a later stop is unclaimed now legitimately reads `contested` — update the assertion and say in the test's name what it pins.

- [ ] **Step 6: Whole tree, lint, commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/replay.go internal/wtsync/replay_test.go internal/wtsync/triage.go
git commit -m "feat(sync): replay every stop, resolving each with the strategies"
git pull --rebase && git push origin main
```

---

### Task 3: The class and the table come from the whole replay

**Files:**
- Modify: `internal/wtsync/triage.go` (`Assessment`, `Assess`)
- Modify: `internal/wtsync/triage_test.go`
- Modify: `internal/commands/sync.go` (`stopColumn`, `noteColumn`)
- Modify: `internal/commands/sync_test.go`

**Interfaces:**
- Consumes: `Replay{Stops, Stop, Truncated, Why, Err}` (Task 2).
- Produces: `Assessment.Files` is the deciding stop's outcomes; `Assessment.Notes` carries the truncation note; `Class` is `Recipe` when every stop resolved and there was at least one, `Clean` when there was none, `Contested` when `Replay.Stop != nil`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/triage_test.go`:

```go
func TestAssessRecipeMeansEveryStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	a := Assess(dir, "main", ownedLineConfig(t), worktreeAt(dir, "feature"), nil)
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Class != Recipe {
		t.Fatalf("class = %s, want recipe", a.Class)
	}
	if len(a.Replay.Stops) != 2 {
		t.Fatalf("Stops = %d, want 2", len(a.Replay.Stops))
	}
}

func TestAssessContestedAtALaterStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"a.txt": "branch\n"}},
	)
	a := Assess(dir, "main", ownedLineConfig(t), worktreeAt(dir, "feature"), nil)
	if a.Class != Contested {
		t.Fatalf("class = %s, want contested", a.Class)
	}
	if a.Replay.Stop == nil || a.Replay.Stop.Index != 2 {
		t.Fatalf("stop = %+v, want 2/2", a.Replay.Stop)
	}
	if len(a.Files) != 1 || a.Files[0].Path != "a.txt" {
		t.Fatalf("files = %+v, want a.txt", a.Files)
	}
}
```

`worktreeAt` is whatever helper `triage_test.go` already uses to build a `repo.Worktree` for a branch — reuse it; do not invent a second one. If the existing tests build the struct inline (`repo.Worktree{Path: dir, Branch: "feature"}`), do the same inline.

Add to `internal/commands/sync_test.go` a test that the STOP column names the deciding stop:

```go
func TestSyncStopColumnNamesTheDecidingStop(t *testing.T) {
	// Build a repo whose first stop a strategy owns and whose second is
	// nobody's; the table must show the second.
	// (Follow the existing syncRepo fixture in this file for the shape.)
}
```

Write that test in full against `syncRepo`, asserting the output contains `2/2` and the unclaimed file's name with `✗`, and does not contain `1/2`.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ -run 'TestAssess|TestSyncStop' -v`
Expected: FAIL — `class = clean, want recipe` (a resolved-everything replay currently has `Stop == nil` and falls to `Clean`).

- [ ] **Step 3: Classify from the whole replay**

In `triage.go`, add to `Assessment`:

```go
	// Paused is a worktree a run left mid-rebase with a plan file in it.
	Paused bool
```

and replace the classification block in `Assess`:

```go
	a.Replay, err = SimulateRebase(mainRoot, onto, wt.Branch, cfg)
	if err != nil {
		a.Err = err
		return a
	}
	a.Err = errors.Join(a.Err, a.Replay.Err)
	switch {
	case a.Replay.Stop != nil:
		a.Files = a.Replay.Stop.Files
		a.Class, a.Files = classifyStop(a.Files, a.Replay.Stop.Messages)
	case len(a.Replay.Stops) > 0:
		// Every stop was resolved by a strategy: a run completes on its own.
		a.Class = Recipe
		a.Files = a.Replay.Stops[0].Files
	default:
		a.Class = Clean
	}
```

and after the `divergence` block (which assigns `a.Notes`), append the truncation note so it is not overwritten:

```go
	if a.Replay.Truncated {
		a.Notes = append(a.Notes, a.Replay.Why)
	}
```

- [ ] **Step 4: The table**

In `internal/commands/sync.go`, replace `stopColumn`:

```go
// stopColumn is the stop that decides the class — the first one a person
// owns, or the first of a run that resolves throughout — its subject, and
// what happens to its files: `2/12 "record every sync run" SyncWorker.java✗`.
func stopColumn(a wtsync.Assessment) string {
	stop := a.Replay.Stop
	if stop == nil {
		if len(a.Replay.Stops) == 0 {
			return "-"
		}
		stop = &a.Replay.Stops[0]
	}
	parts := []string{fmt.Sprintf("%d/%d %q", stop.Index, stop.Total, truncate(oneLine(stop.Subject), 32))}
	for _, f := range a.Files {
		mark := "✗"
		if f.Resolved {
			mark = "✓"
		}
		parts = append(parts, shortPath(f.Path)+mark)
	}
	// A recipe resolves at more than one stop often enough that hiding the
	// rest would understate the work; a contested row says the same thing
	// in NOTE, where the earlier stops are the ones already dealt with.
	if a.Replay.Stop == nil && len(a.Replay.Stops) > 1 {
		parts = append(parts, fmt.Sprintf("+%d more", len(a.Replay.Stops)-1))
	}
	return strings.Join(parts, " ")
}
```

and in `noteColumn`, before the `a.Divergent` loop:

```go
	if a.Paused {
		notes = append(notes, "left mid-rebase by wt sync run: wt sync resume")
	}
	if a.Replay.Stop != nil && len(a.Replay.Stops) > 1 {
		notes = append(notes, fmt.Sprintf("%d earlier stop%s resolved", len(a.Replay.Stops)-1, plural(len(a.Replay.Stops)-1)))
	}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/wtsync/ ./internal/commands/`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/triage.go internal/wtsync/triage_test.go internal/commands/sync.go internal/commands/sync_test.go
git commit -m "feat(sync): class the whole replay, not only its first stop"
git pull --rebase && git push origin main
```

---

## Part 2 — the handover, the plan file, and `resume`

### Task 4: What landed, and its scopes

**Files:**
- Create: `internal/wtsync/landing.go`
- Create: `internal/wtsync/landing_test.go`

**Interfaces:**
- Consumes: `gitEnv`.
- Produces:
  - `type ScopeCount struct { Scope string; Count int }`
  - `type Landing struct { Commits int; Scopes []ScopeCount }`
  - `func LandingList(mainRoot, base, trunk string) (Landing, error)`
  - `func (l Landing) ScopeLine() string` — `"pins ×6, statepush ×4, api ×2"`, `""` when nothing has a scope.

- [ ] **Step 1: Write the failing test**

Create `internal/wtsync/landing_test.go`:

```go
package wtsync

import "testing"

// A direct commit carries its own scope; a merge commit's subject has none
// ("Merge pull request #N from ..."), so its scopes come from the range it
// merged (spec §5).
func TestLandingListCountsDirectAndMergedScopes(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitOutTrimmed(t, dir, "rev-parse", "HEAD")

	// A direct commit on trunk.
	writeFileIn(t, dir, "a.txt", "a2\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): move pin quality out")

	// A side branch of two commits, merged with --no-ff.
	gitIn(t, dir, "checkout", "-q", "-b", "side")
	writeFileIn(t, dir, "b.txt", "b\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix(statepush): count endings by reason")
	writeFileIn(t, dir, "b.txt", "b2\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): pin quality again")
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "merge", "-q", "--no-ff", "-m", "Merge pull request #1 from x/side", "side")

	l, err := LandingList(dir, base, "main")
	if err != nil {
		t.Fatal(err)
	}
	if l.Commits != 2 {
		t.Fatalf("Commits = %d, want 2 first-parent commits", l.Commits)
	}
	if got := l.ScopeLine(); got != "pins ×2, statepush ×1" {
		t.Fatalf("ScopeLine = %q, want %q", got, "pins ×2, statepush ×1")
	}
}

func TestLandingListWithNoScopes(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitOutTrimmed(t, dir, "rev-parse", "HEAD")
	writeFileIn(t, dir, "a.txt", "a2\n")
	gitIn(t, dir, "commit", "-qam", "tidy up")

	l, err := LandingList(dir, base, "main")
	if err != nil {
		t.Fatal(err)
	}
	if l.Commits != 1 || l.ScopeLine() != "" {
		t.Fatalf("Landing = %+v, ScopeLine = %q", l, l.ScopeLine())
	}
}
```

Add the two small helpers next to `gitIn` in `replay_test.go` if they are not there:

```go
func gitOutTrimmed(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitOutIn(t, dir, args...))
}

func writeFileIn(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ -run TestLanding -v`
Expected: FAIL — `undefined: LandingList`.

- [ ] **Step 3: Write `landing.go`**

```go
package wtsync

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ScopeCount is one conventional-commit scope and how often it appears in
// what landed on trunk.
type ScopeCount struct {
	Scope string
	Count int
}

// Landing is what landed on trunk since the branch left it: the first-parent
// commits, and the scopes underneath them.
type Landing struct {
	Commits int
	Scopes  []ScopeCount
}

// maxScopes is how many scopes the plan file's header names. The point of
// the line is orientation, not an inventory.
const maxScopes = 5

// conventional matches a conventional-commit subject and captures its scope.
var conventional = regexp.MustCompile(`^[a-z]+(?:\(([^)]+)\))?!?:`)

// LandingList reads the first-parent log of base..trunk and counts the
// conventional-commit scopes underneath it. A direct commit carries its
// scope in its own subject; a merge commit's subject is "Merge pull request
// #N from …", which has none, so its scopes come from the range it merged,
// <sha>^1..<sha>^2 (spec §5). Everything is local and no PR body is fetched;
// the one cost is a git log per merge commit, which is why this runs when a
// plan file is written and never at triage.
func LandingList(mainRoot, base, trunk string) (Landing, error) {
	out, err := gitEnv(mainRoot, nil, nil, "log", "--first-parent", "--format=%H %P%x00%s", base+".."+trunk, "--")
	if err != nil {
		return Landing{}, fmt.Errorf("landing list: %w", err)
	}
	counts := map[string]int{}
	var l Landing
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		l.Commits++
		shas, subject, _ := strings.Cut(line, "\x00")
		fields := strings.Fields(shas)
		if len(fields) < 3 { // <sha> <parent> — one parent, a direct commit
			countScope(counts, subject)
			continue
		}
		sha := fields[0]
		merged, err := gitEnv(mainRoot, nil, nil, "log", "--format=%s", sha+"^1.."+sha+"^2", "--")
		if err != nil {
			return Landing{}, fmt.Errorf("landing list %s: %w", short(sha), err)
		}
		for _, s := range strings.Split(merged, "\n") {
			countScope(counts, s)
		}
	}
	for scope, n := range counts {
		l.Scopes = append(l.Scopes, ScopeCount{Scope: scope, Count: n})
	}
	sort.Slice(l.Scopes, func(i, j int) bool {
		if l.Scopes[i].Count != l.Scopes[j].Count {
			return l.Scopes[i].Count > l.Scopes[j].Count
		}
		return l.Scopes[i].Scope < l.Scopes[j].Scope
	})
	if len(l.Scopes) > maxScopes {
		l.Scopes = l.Scopes[:maxScopes]
	}
	return l, nil
}

func countScope(counts map[string]int, subject string) {
	m := conventional.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil || m[1] == "" {
		return
	}
	counts[m[1]]++
}

// ScopeLine renders the header's scopes: "pins ×6, statepush ×4, api ×2".
func (l Landing) ScopeLine() string {
	var parts []string
	for _, s := range l.Scopes {
		parts = append(parts, fmt.Sprintf("%s ×%d", s.Scope, s.Count))
	}
	return strings.Join(parts, ", ")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/wtsync/ -run TestLanding -v`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./internal/wtsync/
git add internal/wtsync/landing.go internal/wtsync/landing_test.go internal/wtsync/replay_test.go
git commit -m "feat(sync): count what landed on trunk and its scopes"
git pull --rebase && git push origin main
```

---

### Task 5: The plan file and the state sidecar

**Files:**
- Create: `internal/wtsync/plan.go`
- Create: `internal/wtsync/plan_test.go`

**Interfaces:**
- Consumes: `Landing`/`ScopeLine` (Task 4); `Conflict`, `FileOutcome`, `Config`, `Rule`, `Deferred`; `gitEnv`, `hashObject`.
- Produces:
  - `const PlanName = "wt-sync-plan.md"`, `const StateName = "wt-sync-state.json"`
  - `func PlanPath(gitDir string) string`, `func StatePath(gitDir string) string`
  - `type LeftLock struct { PID int; Started int64 }`
  - `type State struct { … }` (below)
  - `func WriteState(gitDir string, s State) error`, `func ReadState(gitDir string) (State, bool, error)`
  - `func HasPlan(gitDir string) (bool, error)`, `func RemovePlan(gitDir string) error`
  - `func PlanHolders(worktrees []repo.Worktree) ([]repo.Worktree, error)`
  - `type PlanInput struct { … }`, `func RenderPlan(in PlanInput) (string, error)`
  - `func NeedsYouLine(work string, left []string) string`
- The `Handover` type this consumes is defined in Task 6; write Task 5 against the declaration given here and let Task 6 add it to `rebase.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/wtsync/plan_test.go`:

```go
package wtsync

import (
	"strings"
	"testing"
)

func planConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`conflicts:
  - paths: [v.txt]
    strategy: owned-line
    line: '^[0-9]'
    rule: max-plus-patch
  - paths: ["etc/*.json"]
    strategy: openapi
defer:
  - run: ./gradlew generateOpenApi
    paths: ["etc/*.json"]
    commit: "chore(api): regenerate"
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRenderPlanHasEverySection(t *testing.T) {
	dir := repoWith(t, map[string]string{"v.txt": "1.0.0\n", "a.txt": "a\n"}, nil, nil)
	base := gitOutTrimmed(t, dir, "rev-parse", "HEAD")
	writeFileIn(t, dir, "a.txt", "trunk\nextra\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): trunk moves a.txt")

	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "state_stats", Branch: "feat_wt/state_stats", TrunkRef: "origin/main",
		Base: base, Trunk: "main",
		Landing: Landing{Commits: 90, Scopes: []ScopeCount{{"pins", 6}, {"statepush", 4}}},
		Config:  planConfig(t),
		Handover: Handover{
			Index: 2, Total: 12, Subject: "record every sync run",
			Conflicts: []Conflict{
				{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("2.0.0\n"), Branch: []byte("1.1.0\n")},
				{Path: "a.txt", Base: []byte("a\n"), Trunk: []byte("trunk\nextra\n"), Branch: []byte("branch\n")},
			},
			Files: []FileOutcome{
				{Path: "v.txt", Strategy: "owned-line", Resolved: true},
				{Path: "a.txt", Note: "unclaimed"},
			},
			Staged: map[string]string{"v.txt": "deadbeef"},
			Left:   []string{"a.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# rebase state_stats onto origin/main",
		"90 landed. scopes: pins ×6, statepush ×4",
		"## already resolved — do not re-open",
		"v.txt",
		"owned-line",
		"## yours — 1 file",
		"a.txt",
		"trunk: feat(pins): trunk moves a.txt",
		"## never hand-merge here",
		"etc/*.json",
		"openapi",
		"the deferred `./gradlew generateOpenApi` owns it",
		"## deferred, runs when the rebase completes",
		"./gradlew generateOpenApi",
		"wt sync resume state_stats",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stop 2/12") == false {
		t.Fatalf("plan does not say where the rebase stopped:\n%s", out)
	}
}

// Both sides only added lines: the plan says so, because that is a
// five-second read rather than a merge (spec §6).
func TestRenderPlanMarksAnAdditiveConflict(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitOutTrimmed(t, dir, "rev-parse", "HEAD")
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Config: planConfig(t),
		Handover: Handover{
			Index: 1, Total: 1,
			Conflicts: []Conflict{{Path: "a.txt", Base: []byte("a\n"), Trunk: []byte("a\nt1\nt2\n"), Branch: []byte("a\nb1\n")}},
			Files:     []FileOutcome{{Path: "a.txt", Note: "unclaimed"}},
			Left:      []string{"a.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "additive only (trunk +2, ours +1)") {
		t.Fatalf("plan does not mark the additive conflict:\n%s", out)
	}
}

func TestStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadState(dir); err != nil || ok {
		t.Fatalf("ReadState on an empty dir = %v, %v", ok, err)
	}
	want := State{
		Branch: "feat_wt/w", Work: "w", Trunk: "abc", TrunkRef: "origin/main", Onto: "abc",
		Epoch: 42, Safety: "refs/wt-sync/feat_wt/w/42", OldTip: "def", Stop: 2, Total: 12,
		Resolved: map[string]string{"v.txt": "cafe"}, Strategy: map[string]string{"v.txt": "owned-line"},
		Left:     []string{"a.txt"}, Lock: LeftLock{PID: 7, Started: 99},
	}
	if err := WriteState(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadState(dir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if got.Epoch != want.Epoch || got.Resolved["v.txt"] != "cafe" || got.Lock.PID != 7 {
		t.Fatalf("State = %+v, want %+v", got, want)
	}
}

func TestNeedsYouLine(t *testing.T) {
	got := NeedsYouLine("state_stats", []string{"src/SyncWorker.java", "a", "b", "c"})
	want := "wt: state_stats needs you. 4 left after resolvers: SyncWorker.java +3 · wt sync resume state_stats"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ -run 'TestRenderPlan|TestState|TestNeedsYou' -v`
Expected: FAIL — `undefined: RenderPlan`.

- [ ] **Step 3: Write `plan.go`**

```go
package wtsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// PlanName is the file a run leaves in a worktree's own git dir when it
// stops at a conflict a person owns (spec §6). Its presence is what makes a
// mid-rebase worktree wt's rather than somebody's own: resume continues one,
// undo aborts one, and doctor reports one.
const PlanName = "wt-sync-plan.md"

// StateName is the machine-readable half of the same handover: what resume
// needs in order to prove the worktree is still what the run left, and to
// continue the run rather than start a new one.
const StateName = "wt-sync-state.json"

// PlanPath is where the plan file lives for a worktree's git dir.
func PlanPath(gitDir string) string { return filepath.Join(gitDir, PlanName) }

// StatePath is where the sidecar lives for a worktree's git dir.
func StatePath(gitDir string) string { return filepath.Join(gitDir, StateName) }

// LeftLock is the lock a run left behind when it handed a stop over, so the
// resume that continues that run can take it over and nothing else can.
type LeftLock struct {
	PID     int   `json:"pid"`
	Started int64 `json:"started"`
}

// State is everything resume needs. The trunk SHA is the run's, not
// whatever origin/<trunk> means later: a resume that read a newer
// declaration would apply strategies the stopped rebase was never planned
// with.
type State struct {
	Branch   string            `json:"branch"`
	Work     string            `json:"work"`
	Trunk    string            `json:"trunk"`
	TrunkRef string            `json:"trunk_ref"`
	Onto     string            `json:"onto"`
	Upstream string            `json:"upstream"`
	Epoch    int64             `json:"epoch"`
	Safety   string            `json:"safety"`
	OldTip   string            `json:"old_tip"`
	Stop     int               `json:"stop"`
	Total    int               `json:"total"`
	Resolved map[string]string `json:"resolved"`
	Strategy map[string]string `json:"strategy"`
	Left     []string          `json:"left"`
	Lock     LeftLock          `json:"lock"`
}

// WriteState writes the sidecar.
func WriteState(gitDir string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(StatePath(gitDir), append(data, '\n'), 0o644)
}

// ReadState reads the sidecar; ok is false when there is none.
func ReadState(gitDir string) (State, bool, error) {
	data, err := os.ReadFile(StatePath(gitDir))
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, fmt.Errorf("%s: %w", StatePath(gitDir), err)
	}
	return s, true, nil
}

// HasPlan reports whether a run left a plan file in this git dir.
func HasPlan(gitDir string) (bool, error) {
	_, err := os.Stat(PlanPath(gitDir))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// RemovePlan deletes both halves of a handover. A rebase that completes has
// nothing left to hand over, so the files go: a stale plan would make the
// next triage report a worktree as waiting on somebody forever.
func RemovePlan(gitDir string) error {
	for _, p := range []string{PlanPath(gitDir), StatePath(gitDir)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// PlanHolders lists the worktrees holding a plan file. The main checkout is
// never a rebase target, so it is not looked at.
func PlanHolders(worktrees []repo.Worktree) ([]repo.Worktree, error) {
	var out []repo.Worktree
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		has, err := HasPlan(gitDir)
		if err != nil {
			return nil, err
		}
		if has {
			out = append(out, wt)
		}
	}
	return out, nil
}

// NeedsYouLine is the after-the-fact protocol line of spec §5. Generated
// here and relayed verbatim; never composed by hand.
func NeedsYouLine(work string, left []string) string {
	files := "-"
	if len(left) > 0 {
		files = path.Base(left[0])
		if n := len(left) - 1; n > 0 {
			files += " +" + strconv.Itoa(n)
		}
	}
	return fmt.Sprintf("wt: %s needs you. %d left after resolvers: %s · wt sync resume %s",
		work, len(left), files, work)
}

// PlanInput is everything the plan file is rendered from.
type PlanInput struct {
	MainRoot string
	Work     string
	Branch   string
	TrunkRef string // origin/<trunk>, for the title
	Trunk    string // the trunk SHA, for the per-file log
	Base     string // the merge base, for the per-file log
	Landing  Landing
	Handover Handover
	Config   *Config
}

// RenderPlan writes the brief of spec §6: what landed, what the strategies
// already did and must not be re-opened, what is left for a person with the
// trunk-side reason for each file, where hand-merging is forbidden outright,
// and what runs once the rebase completes. Every section removes a specific
// expense; none of it costs a model anything to produce.
func RenderPlan(in PlanInput) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# rebase %s onto %s\n", in.Work, in.TrunkRef)
	scopes := in.Landing.ScopeLine()
	if scopes == "" {
		scopes = "none named"
	}
	fmt.Fprintf(&b, "%d landed. scopes: %s\n", in.Landing.Commits, scopes)
	fmt.Fprintf(&b, "stopped at stop %d/%d", in.Handover.Index, in.Handover.Total)
	if in.Handover.Subject != "" {
		fmt.Fprintf(&b, ", replaying %q", oneLinePlan(in.Handover.Subject))
	}
	b.WriteString("\n")

	conflicts := map[string]Conflict{}
	for _, c := range in.Handover.Conflicts {
		conflicts[c.Path] = c
	}

	var resolved, left []FileOutcome
	for _, f := range in.Handover.Files {
		if f.Resolved {
			resolved = append(resolved, f)
		} else {
			left = append(left, f)
		}
	}

	if len(resolved) > 0 {
		b.WriteString("\n## already resolved — do not re-open\n")
		for _, f := range resolved {
			fmt.Fprintf(&b, "%-40s %s\n", f.Path, f.Strategy)
		}
	}

	fmt.Fprintf(&b, "\n## yours — %d file%s\n", len(left), pluralPlan(len(left)))
	for _, f := range left {
		note := f.Note
		if c, ok := conflicts[f.Path]; ok && f.Note == "unclaimed" {
			note = shapeOf(in.MainRoot, c)
		}
		fmt.Fprintf(&b, "%-40s %s\n", f.Path, note)
		subject, err := trunkSubject(in.MainRoot, in.Base, in.Trunk, f.Path)
		if err != nil {
			return "", err
		}
		if subject != "" {
			fmt.Fprintf(&b, "    trunk: %s\n", oneLinePlan(subject))
		}
	}

	if in.Config != nil {
		var owned []string
		for _, r := range in.Config.Conflicts {
			for _, p := range r.Paths {
				owned = append(owned, fmt.Sprintf("%-40s ->  %s", p, r.Strategy))
			}
		}
		for _, d := range in.Config.Defer {
			for _, p := range d.Paths {
				owned = append(owned, fmt.Sprintf("%-40s ->  the deferred `%s` owns it", p, d.Run))
			}
		}
		sort.Strings(owned)
		if len(owned) > 0 {
			b.WriteString("\n## never hand-merge here\n")
			for _, l := range owned {
				b.WriteString(l + "\n")
			}
		}
		if len(in.Config.Defer) > 0 {
			b.WriteString("\n## deferred, runs when the rebase completes\n")
			for _, d := range in.Config.Defer {
				b.WriteString(d.Run + "\n")
			}
		}
	}

	fmt.Fprintf(&b, "\nresolve what is yours, `git add` it, then: wt sync resume %s\n", in.Work)
	fmt.Fprintf(&b, "or put everything back: wt sync undo %s\n", in.Work)
	return b.String(), nil
}

// shapeOf describes one unresolved conflict the way §6 does: both sides only
// added lines (a five-second read), or they overlap. A blob git will not
// diff as text is reported as overlapping, which is the cautious answer.
func shapeOf(mainRoot string, c Conflict) string {
	tAdd, tDel, terr := blobDiff(mainRoot, c.Base, c.Trunk)
	bAdd, bDel, berr := blobDiff(mainRoot, c.Base, c.Branch)
	if terr != nil || berr != nil || tDel != 0 || bDel != 0 {
		return "same hunk both sides"
	}
	return fmt.Sprintf("additive only (trunk +%d, ours +%d)", tAdd, bAdd)
}

// blobDiff counts the lines added and removed between two blobs, using git's
// own numstat over objects it already holds.
func blobDiff(mainRoot string, from, to []byte) (added, removed int, err error) {
	a, err := hashObject(mainRoot, from)
	if err != nil {
		return 0, 0, err
	}
	b, err := hashObject(mainRoot, to)
	if err != nil {
		return 0, 0, err
	}
	out, err := gitEnv(mainRoot, nil, nil, "diff", "--numstat", a, b)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return 0, 0, nil
	}
	added, aerr := strconv.Atoi(f[0])
	removed, derr := strconv.Atoi(f[1])
	if aerr != nil || derr != nil { // "-\t-\t" for a binary blob
		return 0, 0, fmt.Errorf("not a textual diff")
	}
	return added, removed, nil
}

// trunkSubject is why trunk changed this file: the newest subject in
// base..trunk touching it (spec §6). Empty when trunk did not touch it.
func trunkSubject(mainRoot, base, trunk, path string) (string, error) {
	out, err := gitEnv(mainRoot, nil, nil, "log", "-1", "--format=%s", base+".."+trunk, "--", path)
	if err != nil {
		return "", fmt.Errorf("trunk subject for %s: %w", path, err)
	}
	return out, nil
}

func oneLinePlan(s string) string { return strings.Join(strings.Fields(s), " ") }

func pluralPlan(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/wtsync/ -run 'TestRenderPlan|TestState|TestNeedsYou' -v`
Expected: FAIL until Task 6 declares `Handover`. Declare it in `rebase.go` now (the type only — Task 6 fills in the code that builds it):

```go
// Handover is a stop the run left for a person: where the rebase is, the
// three blobs of every conflict there, what the strategies answered, the
// blob each resolved path was staged with (so resume can prove it was not
// hand-merged), and the paths a person owns.
type Handover struct {
	Index, Total int
	Commit       string
	Subject      string
	Conflicts    []Conflict
	Files        []FileOutcome
	Staged       map[string]string
	Left         []string
}
```

Then rerun; expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./internal/wtsync/
git add internal/wtsync/plan.go internal/wtsync/plan_test.go internal/wtsync/rebase.go
git commit -m "feat(sync): write the plan file and the state a resume needs"
git pull --rebase && git push origin main
```

---

### Task 6: The rebase leaves a contested stop in place, and resume re-enters the loop

**Files:**
- Modify: `internal/wtsync/rebase.go` (extract `driver`; `Result.Left`; `Resume`; `Preflight`)
- Modify: `internal/wtsync/rebase_test.go`
- Modify: `internal/wtsync/lock.go` (`Lock.Keep`, `TakeOver`)
- Modify: `internal/wtsync/lock_test.go`

**Interfaces:**
- Consumes: `StagedConflicts`, `RebaseProgress`, `RebaseInProgress`, `Apply` (`stage.go`); `resolveConflict` (`resolve.go`); `WriteSafety`, `Safety` (`safety.go`); `Handover` (Task 5).
- Produces:
  - `Result` gains `Left *Handover`; `StopResult` gains `Commit string`.
  - `func Resume(mainRoot string, cfg *Config, req Request, old string, safety Safety, log io.Writer) (Result, error)`
  - `func (l *Lock) Keep()`
  - `func TakeOver(gitDir string, now time.Time, prev LeftLock) (*Lock, error)`
  - `Preflight` returns `Proceed, ""` for `Contested`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/rebase_test.go`. Reuse the fixture style already there (`repoWith` plus a worktree, or whatever `rebase_test.go` builds — read it and follow it exactly).

```go
// A stop nothing claims is left in place, not aborted: the rebase is still
// in progress, the strategy's answer for the claimed file is staged, and the
// handover names what is left.
func TestRebaseLeavesAContestedStopInPlace(t *testing.T) {
	// trunk changes v.txt and a.txt; the branch changes both in one commit.
	// v.txt is claimed by owned-line, a.txt by nobody.
	dir, wt := rebaseFixture(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n", "a.txt": "branch\n"}},
	)
	res, err := Rebase(dir, ownedLineConfig(t), Request{
		Path: wt, Branch: "feature", Trunk: "main", Onto: "main", Epoch: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil {
		t.Fatalf("Left = nil, want a handover; Restored = %v", res.Restored)
	}
	if res.Restored {
		t.Fatal("Restored = true, want the rebase left in place")
	}
	if got := res.Left.Left; len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("Left.Left = %v, want [a.txt]", got)
	}
	if res.Left.Staged["v.txt"] == "" {
		t.Fatal("Staged has no oid for v.txt")
	}
	busy, err := RebaseInProgress(wt)
	if err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want true", busy, err)
	}
	// v.txt is staged with what the strategy wrote; a.txt is still unmerged.
	unmerged, err := StagedConflicts(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmerged) != 1 || unmerged[0].Path != "a.txt" {
		t.Fatalf("unmerged = %+v, want only a.txt", unmerged)
	}
}

// Resume drives the same loop: with the unclaimed file resolved by hand and
// staged, the rebase finishes and the branch moves.
func TestResumeFinishesTheRebase(t *testing.T) {
	dir, wt := rebaseFixture(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n", "a.txt": "branch\n"}},
	)
	req := Request{Path: wt, Branch: "feature", Trunk: "main", Onto: "main", Epoch: 1}
	res, err := Rebase(dir, ownedLineConfig(t), req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	writeFileIn(t, wt, "a.txt", "resolved by hand\n")
	gitIn(t, wt, "add", "a.txt")

	out, err := Resume(dir, ownedLineConfig(t), req, res.OldTip, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left != nil {
		t.Fatalf("Left = %+v, want nil", out.Left)
	}
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatal("still mid-rebase")
	}
	if out.NewTip == "" || out.NewTip == out.OldTip {
		t.Fatalf("NewTip = %q, OldTip = %q", out.NewTip, out.OldTip)
	}
}

// Somebody ran git rebase --continue themselves and it finished: resume
// accepts that and reports the finished rebase rather than failing.
func TestResumeToleratesAFinishedRebase(t *testing.T) {
	dir, wt := rebaseFixture(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n", "a.txt": "branch\n"}},
	)
	req := Request{Path: wt, Branch: "feature", Trunk: "main", Onto: "main", Epoch: 1}
	res, err := Rebase(dir, ownedLineConfig(t), req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	writeFileIn(t, wt, "a.txt", "by hand\n")
	gitIn(t, wt, "add", "a.txt")
	gitInEnv(t, wt, []string{"GIT_EDITOR=true"}, "rebase", "--continue")

	out, err := Resume(dir, ownedLineConfig(t), req, res.OldTip, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left != nil || out.NewTip == "" {
		t.Fatalf("Resume = %+v", out)
	}
}

func TestPreflightLetsContestedProceed(t *testing.T) {
	v, why := Preflight(Assessment{Class: Contested, Files: []FileOutcome{{Path: "a.txt"}}})
	if v != Proceed {
		t.Fatalf("verdict = %v (%s), want Proceed", v, why)
	}
}
```

`rebaseFixture(t, trunkEdits, branchEdits) (mainRoot, wtPath string)` and `gitInEnv` may already exist in `rebase_test.go` under other names — read that file and reuse whatever it has for building a repository with a linked worktree on `feature`; only add helpers that are genuinely missing.

Add to `internal/wtsync/lock_test.go`:

```go
func TestTakeOverReclaimsTheRunsOwnLock(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	l.Keep() // the run left it behind on purpose

	got, err := TakeOver(dir, now, LeftLock{PID: l.PID, Started: l.Started.Unix()})
	if err != nil {
		t.Fatalf("TakeOver = %v, want the lock", err)
	}
	if got == nil {
		t.Fatal("TakeOver returned no lock")
	}
	if err := got.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTakeOverRespectsSomebodyElsesLock(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()

	if _, err := TakeOver(dir, now, LeftLock{PID: l.PID + 1, Started: l.Started.Unix()}); err == nil {
		t.Fatal("TakeOver took a lock that is not the run's")
	}
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ -run 'TestRebaseLeaves|TestResume|TestPreflightLets|TestTakeOver' -v`
Expected: FAIL — `undefined: Resume`, `res.Left undefined`, `undefined: TakeOver`.

- [ ] **Step 3: Extract the driver in `rebase.go`**

Add to the types:

```go
// StopResult is one place the rebase stopped and what happened there.
type StopResult struct {
	Index, Total int
	Commit       string
	Subject      string
	Files        []FileOutcome
}
```

and to `Result`:

```go
	// Left is the stop the run handed over to a person: the rebase is still
	// in progress in the worktree, with every strategy's answer staged.
	// Nil when the rebase finished or was restored.
	Left *Handover
```

Replace `Rebase`'s body with a `driver`. The loop, the restore, `fail` and the trailing read-back are moved verbatim except where noted:

```go
// driver is one rebase in flight: Rebase starts one and drives it, Resume
// re-enters the same loop for one a previous run left stopped.
type driver struct {
	mainRoot string
	cfg      *Config
	req      Request
	log      io.Writer
	old      string
	res      Result
}

func (d *driver) git(args ...string) (string, error) {
	if d.log != nil && os.Getenv("WT_SYNC_TRACE") != "" {
		fmt.Fprintf(d.log, "  $ git %s\n", strings.Join(args, " "))
	}
	return gitEnv(d.req.Path, rebaseEnv, nil, args...)
}

// restore is the existing restore closure, verbatim, with `git` → d.git,
// `res` → d.res, `old` → d.old and `req` → d.req.
func (d *driver) restore() error { /* moved unchanged */ }

func (d *driver) fail(err error) (Result, error) { return d.res, errors.Join(err, d.restore()) }

// Rebase rebases one worktree, applying the declared strategies at each
// stop. A stop no strategy resolves is left in place with what the
// strategies did resolve already staged, and reported as a handover
// (Result.Left): the worktree stays mid-rebase for a person, and
// `wt sync resume` or `wt sync undo` is what moves next. An error is a git
// failure, after a restore to the safety ref, or a restore that could not be
// verified.
func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error) {
	d := &driver{mainRoot: mainRoot, cfg: cfg, req: req, log: log, res: Result{Branch: req.Branch}}
	old, err := d.git("rev-parse", "--verify", req.Branch)
	if err != nil {
		return d.res, err
	}
	d.old, d.res.OldTip = old, old
	if d.res.Safety, err = WriteSafety(mainRoot, req.Branch, old, req.Epoch); err != nil {
		return d.res, err
	}
	base := req.Upstream
	if base == "" {
		base = req.Onto
	}
	if d.res.SignaturesDropped, err = signedCount(req.Path, base, old); err != nil {
		return d.res, err
	}
	args := append(append([]string{}, rebaseConfig...), "rebase", "--no-update-refs", "--no-gpg-sign")
	if req.Upstream != "" {
		args = append(args, "--onto", req.Onto, req.Upstream)
	} else {
		args = append(args, req.Onto)
	}
	_, err = d.git(args...)
	return d.drive(err)
}

// Resume drives a rebase a previous run left stopped. The caller has already
// verified the worktree against the run's state file: the plan is present,
// the safety ref resolves, nothing is unmerged, and no path a strategy
// resolved was hand-merged. A rebase that is no longer in progress — someone
// ran `git rebase --continue` themselves and it finished — is accepted as
// done, and only what comes after the rebase is left to do.
func Resume(mainRoot string, cfg *Config, req Request, old string, safety Safety, log io.Writer) (Result, error) {
	d := &driver{mainRoot: mainRoot, cfg: cfg, req: req, log: log, old: old,
		res: Result{Branch: req.Branch, OldTip: old, Safety: safety}}
	base := req.Upstream
	if base == "" {
		base = req.Onto
	}
	var err error
	if d.res.SignaturesDropped, err = signedCount(req.Path, base, old); err != nil {
		return d.res, err
	}
	busy, err := RebaseInProgress(req.Path)
	if err != nil {
		return d.res, err
	}
	if !busy {
		return d.finish()
	}
	_, cerr := d.git("rebase", "--continue")
	return d.drive(cerr)
}

// drive runs the stop-resolve-continue loop until the rebase finishes, fails
// or reaches a stop a person owns. err is what the command that got the
// rebase moving returned: nil means it is already finished.
func (d *driver) drive(err error) (Result, error) {
	lastIndex, lastUnmerged := -1, ""
	stops, limit := 0, 0
	for err != nil {
		/* the existing loop body, verbatim, with these two changes:

		   - stop is built with its Commit:
		       stop := StopResult{Index: p.Index, Total: p.Total, Commit: p.Commit, Subject: p.Subject}
		   - the unresolved branch hands over instead of restoring:
		       if unresolved {
		           h, herr := d.handover(stop, conflicts)
		           if herr != nil {
		               return d.fail(herr)
		           }
		           d.res.Left = h
		           return d.res, nil
		       }
		*/
	}
	busy, perr := RebaseInProgress(d.req.Path)
	if perr != nil {
		return d.fail(perr)
	}
	if busy {
		return d.fail(errors.New("rebase reported success but is still in progress"))
	}
	return d.finish()
}

// finish reads back what the completed rebase produced. Past this point the
// rewrite happened and is what the branch now is: failing to read it back is
// a reporting failure, not a reason to throw the work away, so nothing here
// restores.
func (d *driver) finish() (Result, error) {
	unread := func(err error) (Result, error) {
		return d.res, fmt.Errorf("rebased but could not read the result: %w; the old tip is %s", err, d.res.Safety.Ref)
	}
	var err error
	if d.res.NewTip, err = d.git("rev-parse", "HEAD"); err != nil {
		return unread(err)
	}
	count, err := d.git("rev-list", "--count", d.req.Onto+"..HEAD")
	if err != nil {
		return unread(err)
	}
	if d.res.Replayed, err = strconv.Atoi(count); err != nil {
		return unread(err)
	}
	return d.res, nil
}

// handover records the stop the run is leaving: what a person owns, and the
// blob each strategy staged, which is what resume compares against to prove
// nothing was hand-merged where a strategy owns the file.
func (d *driver) handover(stop StopResult, conflicts []Conflict) (*Handover, error) {
	h := &Handover{
		Index: stop.Index, Total: stop.Total, Commit: stop.Commit, Subject: stop.Subject,
		Conflicts: conflicts, Files: stop.Files, Staged: map[string]string{},
	}
	for _, f := range stop.Files {
		if !f.Resolved {
			h.Left = append(h.Left, f.Path)
			continue
		}
		oid, err := d.git("rev-parse", "--verify", ":0:"+f.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: reading what %s staged: %w", f.Path, f.Strategy, err)
		}
		h.Staged[f.Path] = oid
	}
	return h, nil
}
```

`Result.Restored` keeps its meaning: a *failure* still aborts and restores. Only the unresolved-stop path changed.

In `Preflight`, replace the `Contested` case:

```go
	case Contested:
		// A contested stop is handed over rather than refused: the run
		// rebases up to it, resolves what it can, and leaves the rest with
		// a plan file. Where it stops is printed by the caller.
		return Proceed, ""
```

- [ ] **Step 4: `Lock.Keep` and `TakeOver` in `lock.go`**

```go
// Keep detaches the lock from this process without removing the file: a run
// that leaves a worktree mid-rebase leaves its lock behind, so a second run
// does not start in a worktree somebody has to finish first. It is not a
// forever lock — LockExpiry still frees it — and the plan file, not this, is
// the durable marker that a run is waiting.
func (l *Lock) Keep() {
	held.Lock()
	delete(held.locks, l.Path)
	held.Unlock()
}

// TakeOver acquires the lock, first removing the one a run left behind when
// it handed a stop over. Only that exact lock is displaced: prev is what the
// run recorded in its state file, and anything else — a live run, a lock
// with a different pid or start time — is respected the way Acquire
// respects it.
func TakeOver(gitDir string, now time.Time, prev LeftLock) (*Lock, error) {
	cur, ok, err := ReadLock(gitDir)
	if err != nil {
		return nil, err
	}
	if ok && prev.PID != 0 && cur.PID == prev.PID && cur.Started.Unix() == prev.Started {
		if err := os.Remove(cur.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return Acquire(gitDir, now)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/wtsync/`
Expected: PASS. Existing tests that asserted `Restored` on an unclaimed stop now assert `Left != nil` — update them, and keep at least one test that a genuine git failure still restores.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/rebase.go internal/wtsync/rebase_test.go internal/wtsync/lock.go internal/wtsync/lock_test.go
git commit -m "feat(sync): hand a contested stop over instead of aborting"
git pull --rebase && git push origin main
```

---

### Task 7: `wt sync run` writes the plan and prints the "needs you" line

**Files:**
- Create: `internal/commands/sync_finish.go`
- Modify: `internal/commands/sync_run.go`
- Modify: `internal/commands/sync_run_test.go`

**Interfaces:**
- Consumes: `wtsync.Result.Left`, `wtsync.Handover` (Task 6); `wtsync.RenderPlan`, `wtsync.State`, `wtsync.WriteState`, `wtsync.PlanPath`, `wtsync.RemovePlan`, `wtsync.NeedsYouLine`, `wtsync.LandingList` (Tasks 4–5); `wtsync.Lock.Keep` (Task 6).
- Produces: `handOver(ctx, w, handoverInput) error` and `completeRun(ctx, w, cfg, completeInput) (head string, owed []string, err error)`, both used by Task 8.

- [ ] **Step 1: Write the failing test**

Add to `internal/commands/sync_run_test.go`, following the fixture the file already uses:

```go
// A contested stop is no longer refused: the run rebases up to it, stages
// what the strategies resolved, leaves the rebase in place and writes the
// plan file and the state sidecar.
func TestSyncRunHandsAContestedStopOver(t *testing.T) {
	// Build a repo whose branch conflicts on a claimed file and an
	// unclaimed one, with a worktree on that branch.
	// (Use the same helper the other tests in this file use.)
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"w"}, RunOptions{NoFetch: true, Yes: true, Agents: []wtsync.Agent{}}, &out)
	if err == nil {
		t.Fatal("err = nil, want the run reported as not completed")
	}
	if !strings.Contains(out.String(), "needs you") {
		t.Fatalf("output has no needs-you line:\n%s", out.String())
	}
	gitDir, err := wtsync.GitDir(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatalf("no plan file: %v", err)
	}
	for _, want := range []string{"## already resolved", "## yours", "wt sync resume"} {
		if !strings.Contains(string(plan), want) {
			t.Fatalf("plan is missing %q:\n%s", want, plan)
		}
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if st.Epoch == 0 || st.Safety == "" || len(st.Resolved) == 0 {
		t.Fatalf("state = %+v", st)
	}
	// The lock stays behind so nothing else starts in this worktree.
	if _, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; want the lock kept", ok, err)
	}
	if busy, err := wtsync.RebaseInProgress(wtPath); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left in place", busy, err)
	}
}
```

Write it in full against the file's existing fixture helper; do not invent a new one.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/commands/ -run TestSyncRunHands -v`
Expected: FAIL — the run refuses with "contested … rebase by hand" and writes no plan.

- [ ] **Step 3: Write `sync_finish.go`**

```go
package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// handoverInput is what writing a plan file needs beyond the rebase's own
// result: the names a person reads, and the trunk the run was planned
// against.
type handoverInput struct {
	Work     string
	Branch   string
	Path     string
	TrunkRef string
	TrunkSHA string
	Onto     string
	Upstream string
	Epoch    int64
	Cfg      *wtsync.Config
	Res      wtsync.Result
	Lock     *wtsync.Lock
}

// handOver leaves the worktree mid-rebase for a person: the §6 plan file,
// the state sidecar resume verifies against, the lock left behind, and the
// §5 line. The caller must not release the lock afterwards — Keep has
// already taken it off this process's books.
func handOver(ctx *Context, w io.Writer, in handoverInput) error {
	if in.Res.Left == nil {
		return fmt.Errorf("%s: nothing to hand over", in.Work)
	}
	gitDir, err := wtsync.GitDir(in.Path)
	if err != nil {
		return err
	}
	base, err := git.Run(ctx.Repo.MainRoot, "merge-base", in.TrunkSHA, in.Res.OldTip)
	if err != nil {
		return fmt.Errorf("merge-base: %w", err)
	}
	landing, err := wtsync.LandingList(ctx.Repo.MainRoot, base, in.TrunkSHA)
	if err != nil {
		return err
	}
	plan, err := wtsync.RenderPlan(wtsync.PlanInput{
		MainRoot: ctx.Repo.MainRoot, Work: in.Work, Branch: in.Branch, TrunkRef: in.TrunkRef,
		Trunk: in.TrunkSHA, Base: base, Landing: landing, Handover: *in.Res.Left, Config: in.Cfg,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(wtsync.PlanPath(gitDir), []byte(plan), 0o644); err != nil {
		return err
	}
	st := wtsync.State{
		Branch: in.Branch, Work: in.Work, Trunk: in.TrunkSHA, TrunkRef: in.TrunkRef,
		Onto: in.Onto, Upstream: in.Upstream, Epoch: in.Epoch,
		Safety: in.Res.Safety.Ref, OldTip: in.Res.OldTip,
		Stop: in.Res.Left.Index, Total: in.Res.Left.Total,
		Resolved: in.Res.Left.Staged, Strategy: map[string]string{}, Left: in.Res.Left.Left,
	}
	for _, f := range in.Res.Left.Files {
		if f.Resolved {
			st.Strategy[f.Path] = f.Strategy
		}
	}
	if in.Lock != nil {
		st.Lock = wtsync.LeftLock{PID: in.Lock.PID, Started: in.Lock.Started.Unix()}
		in.Lock.Keep()
	}
	if err := wtsync.WriteState(gitDir, st); err != nil {
		return err
	}
	fmt.Fprintf(w, "  plan %s\n", wtsync.PlanPath(gitDir))
	fmt.Fprintf(w, "  %s\n", wtsync.NeedsYouLine(in.Work, in.Res.Left.Left))
	return nil
}

// completeInput is what the tail after a finished rebase needs.
type completeInput struct {
	Work, Branch, Path string
	Epoch              int64
	Res                wtsync.Result
}

// completeRun is what run and resume both do once a rebase has finished: the
// deferred steps, the result ref that says where the run left the branch,
// the plan file removed, and the push line. owed names the deferred steps
// that failed — the rebase stands regardless (spec §3).
func completeRun(ctx *Context, w io.Writer, cfg *wtsync.Config, in completeInput) (head string, owed []string, err error) {
	// w, not nil: RunDeferred announces each step as it starts, so a long
	// one is not silence until printDeferred reports the result.
	results, err := wtsync.RunDeferred(in.Path, cfg.Defer, in.Res.OldTip, in.Res.NewTip, w)
	if err != nil {
		return "", nil, err
	}
	for _, d := range results {
		printDeferred(w, d)
		if d.Err != nil {
			owed = append(owed, in.Work+" (owed: "+d.Step.Run+")")
		}
	}
	if head, err = git.Run(in.Path, "rev-parse", "HEAD"); err != nil {
		return "", owed, err
	}
	if err = wtsync.WriteResult(ctx.Repo.MainRoot, in.Branch, head, in.Epoch); err != nil {
		return head, owed, err
	}
	gitDir, err := wtsync.GitDir(in.Path)
	if err != nil {
		return head, owed, err
	}
	// The rebase completed: there is nothing left to hand over, and a stale
	// plan would report this worktree as waiting on somebody forever.
	if err := wtsync.RemovePlan(gitDir); err != nil {
		return head, owed, err
	}
	fmt.Fprintf(w, "  push: git -C %s push --force-with-lease\n", in.Path)
	return head, owed, nil
}

var _ = strings.TrimSpace // keep the import honest if the file needs it
```

(Drop that last line if `strings` is used, which it will be once the file is finished; do not leave a no-op declaration in the tree.)

- [ ] **Step 4: Rework the run's per-branch block in `sync_run.go`**

Before the `wtsync.Rebase` call, say what is coming:

```go
		if p.a.Class == wtsync.Contested && p.a.Replay.Stop != nil {
			fmt.Fprintf(w, "  contested at %d/%d: the run stops there and writes a plan\n",
				p.a.Replay.Stop.Index, p.a.Replay.Stop.Total)
		}
```

Replace the `if res.Restored { … }` block's neighbourhood so a handover is its own outcome (the `Restored` block stays, for a rebase a failure put back):

```go
		if res.Left != nil {
			if err := handOver(ctx, w, handoverInput{
				Work: p.work, Branch: b, Path: p.wt.Path, TrunkRef: onto, TrunkSHA: trunkSHA,
				Onto: req.Onto, Upstream: req.Upstream, Epoch: epoch, Cfg: cfg, Res: res, Lock: p.lock,
			}); err != nil {
				fmt.Fprintf(w, "  failed: %v\n", err)
				failures = append(failures, p.work+" (failed)")
				release(b)
				poisonAbove(b, p.work+" failed")
				continue
			}
			// The lock is left behind on purpose; dropping the handle here
			// keeps the deferred release from removing the file.
			p.lock = nil
			failures = append(failures, p.work+" (needs you)")
			poisonAbove(b, p.work+" is waiting for you")
			continue
		}
```

Replace the deferred-steps tail with `completeRun`:

```go
		tracker.set(&rebaseInFlight{work: p.work, path: p.wt.Path, rebased: true})
		head, owed, derr := completeRun(ctx, w, cfg, completeInput{
			Work: p.work, Branch: b, Path: p.wt.Path, Epoch: epoch, Res: res,
		})
		tracker.set(nil)
		p.head = head
		failures = append(failures, owed...)
		if derr != nil {
			fmt.Fprintf(w, "  failed: %v\n", derr)
			failures = append(failures, p.work+" (failed)")
			poisonAbove(b, p.work+" failed")
			release(b)
			continue
		}
		release(b)
```

Name resume in the mid-rebase poison so the message says what to do:

```go
		if busy {
			why := p.work + ": changed since triage: a rebase is in progress"
			if has, herr := wtsync.HasPlan(gitDir); herr == nil && has {
				why = p.work + ": left mid-rebase by an earlier run: wt sync resume " + p.work
			}
			poison(b, why)
			continue
		}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/commands/`
Expected: PASS. The existing test asserting that `run` refuses a contested worktree now asserts the handover instead — rewrite it rather than deleting it.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/commands/sync_finish.go internal/commands/sync_run.go internal/commands/sync_run_test.go
git commit -m "feat(sync): run leaves a contested stop with a plan file"
git pull --rebase && git push origin main
```

---

### Task 8: `wt sync resume <work>`

**Files:**
- Create: `internal/commands/sync_resume.go`
- Create: `internal/commands/sync_resume_test.go`
- Modify: `cmd/wt/sync.go` (the `resume` subcommand)

**Interfaces:**
- Consumes: `handOver`, `completeRun` (Task 7); `wtsync.Resume`, `wtsync.TakeOver` (Task 6); `wtsync.ReadState`, `wtsync.State` (Task 5); `Locate`, `workName`, `watchSignals`, `rebaseTracker`, `rebaseInFlight`.
- Produces: `type ResumeOptions struct { Agents []wtsync.Agent; Now func() time.Time }` and `func SyncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/commands/sync_resume_test.go`. Reuse the fixture from `sync_run_test.go`:

```go
package commands

// Resume drives a handed-over rebase to the end once the person has done
// their part.
func TestSyncResumeFinishesAHandedOverRebase(t *testing.T) {
	// run first, until it hands over; then resolve the unclaimed file by
	// hand and git add it; then resume.
	// Assert: the plan file and the state sidecar are gone, the branch tip
	// moved, the result ref exists, the push line was printed, and the
	// worktree is not mid-rebase.
}

// A file the strategies own must not be hand-merged: resume refuses and
// changes nothing.
func TestSyncResumeRefusesAHandMergedOwnedFile(t *testing.T) {
	// After the handover, overwrite the strategy-resolved file and git add
	// it. Assert: SyncResume returns an error naming the file and the
	// strategy, the rebase is still in progress, and the plan file is still
	// there.
}

// Unmerged paths left in the index mean the person is not done.
func TestSyncResumeRefusesWhileSomethingIsUnmerged() {
	// After the handover, resume without staging anything. Assert: the
	// error names the unmerged path and says to git add it.
}

func TestSyncResumeWithoutAPlan(t *testing.T) {
	// A worktree no run touched. Assert: "was not left mid-rebase by wt
	// sync run".
}
```

Write all four in full, with the assertions above spelled out as `if !strings.Contains(...)` checks against the returned error and the captured output, using the same fixture helper `sync_run_test.go` uses.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/commands/ -run TestSyncResume -v`
Expected: FAIL — `undefined: SyncResume`.

- [ ] **Step 3: Write `sync_resume.go`**

```go
package commands

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ResumeOptions tunes SyncResume for callers and tests.
type ResumeOptions struct {
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	Now    func() time.Time
}

// SyncResume continues the rebase a run left at a stop a person owned. It
// verifies the worktree is still what the run left — the plan file and the
// safety ref are there, nothing is unmerged, and no file a strategy resolved
// was hand-merged — then drives the same loop to the end: the strategies at
// any later stop, the deferred steps, the result ref, the push line. A later
// stop a person owns is handed over again, with a fresh plan file.
//
// A rebase somebody finished themselves with `git rebase --continue` is
// tolerated: there is nothing to continue, so only what follows the rebase
// runs.
func SyncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) error {
	tracker := &rebaseTracker{}
	defer watchSignals(w, tracker)()

	target, err := Locate(ctx, work)
	if err != nil {
		return err
	}
	if target.Branch == "" {
		return fmt.Errorf("%s has no branch", work)
	}
	gitDir, err := wtsync.GitDir(target.Path)
	if err != nil {
		return err
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s was not left mid-rebase by wt sync run: nothing to resume", work)
	}
	if st.Branch != target.Branch {
		return fmt.Errorf("the plan in %s is for %s, not %s; nothing is resumed", gitDir, st.Branch, target.Branch)
	}
	if _, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", st.Safety); err != nil {
		return fmt.Errorf("the safety ref %s is gone; nothing is resumed", st.Safety)
	}
	agents := opts.Agents
	if agents == nil {
		if agents, err = wtsync.ListAgents(); err != nil {
			return fmt.Errorf("cannot list agent sessions (%v); nothing is resumed", err)
		}
	}
	agentPath := target.Path
	if resolved, rerr := filepath.EvalSymlinks(target.Path); rerr == nil {
		agentPath = resolved
	}
	if a := wtsync.AgentAt(agents, agentPath); a != nil {
		return fmt.Errorf("an agent session is in %s; nothing is resumed", work)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lock, err := wtsync.TakeOver(gitDir, now(), st.Lock)
	if err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", work, err)
	}
	defer func() {
		if lock != nil {
			_ = lock.Release()
		}
	}()
	// The run's trunk, not today's: a resume that read a newer declaration
	// would apply strategies the stopped rebase was never planned with.
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, st.Trunk)
	if err != nil {
		return err
	}
	busy, err := wtsync.RebaseInProgress(target.Path)
	if err != nil {
		return err
	}
	if busy {
		if err := verifyHandover(target.Path, st, w); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "%s  the rebase is already finished; running what is left\n", work)
	}
	req := wtsync.Request{
		Path: target.Path, Branch: st.Branch, Trunk: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch,
	}
	safety := wtsync.Safety{Branch: st.Branch, Epoch: st.Epoch, Ref: st.Safety, Tip: st.OldTip}
	tracker.set(&rebaseInFlight{work: work, path: target.Path, safety: st.Safety})
	res, rerr := wtsync.Resume(ctx.Repo.MainRoot, cfg, req, st.OldTip, safety, w)
	tracker.set(nil)
	if rerr != nil {
		return fmt.Errorf("%s: %w", work, rerr)
	}
	if res.Left != nil {
		fmt.Fprintf(w, "  stopped again at %d/%d\n", res.Left.Index, res.Left.Total)
		if err := handOver(ctx, w, handoverInput{
			Work: work, Branch: st.Branch, Path: target.Path, TrunkRef: st.TrunkRef, TrunkSHA: st.Trunk,
			Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Cfg: cfg, Res: res, Lock: lock,
		}); err != nil {
			return err
		}
		lock = nil // kept on purpose
		return fmt.Errorf("not completed: %s (needs you)", work)
	}
	fmt.Fprintf(w, "  rebased %d commit%s\n", res.Replayed, plural(res.Replayed))
	tracker.set(&rebaseInFlight{work: work, path: target.Path, rebased: true})
	_, owed, cerr := completeRun(ctx, w, cfg, completeInput{
		Work: work, Branch: st.Branch, Path: target.Path, Epoch: st.Epoch, Res: res,
	})
	tracker.set(nil)
	if cerr != nil {
		return cerr
	}
	if len(owed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(owed, ", "))
	}
	return nil
}

// verifyHandover refuses to continue a rebase that is not what the run left:
// something still unmerged means the person is not done, and a changed blob
// under a strategy's name means a file the plan said never to hand-merge was
// hand-merged. Neither is a failure the tool may paper over by re-running
// the strategy: it would overwrite a person's work.
func verifyHandover(wtPath string, st wtsync.State, w io.Writer) error {
	unmerged, err := wtsync.StagedConflicts(wtPath)
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		var ps []string
		for _, c := range unmerged {
			ps = append(ps, c.Path)
		}
		return fmt.Errorf("still unmerged: %s; resolve them and git add them, then resume", strings.Join(ps, ", "))
	}
	var changed []string
	for path := range st.Resolved {
		cur, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path)
		if err != nil {
			return fmt.Errorf("%s is no longer staged and %s owns it; wt sync undo and start again", path, st.Strategy[path])
		}
		if cur != st.Resolved[path] {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	for _, p := range changed {
		fmt.Fprintf(w, "  %s was hand-merged; %s owns it\n", p, st.Strategy[p])
		fmt.Fprintf(w, "    put it back: git -C %s cat-file blob %s > %s && git -C %s add -- %s\n",
			wtPath, st.Resolved[p], p, wtPath, p)
	}
	if len(changed) > 0 {
		return fmt.Errorf("%d file%s under \"never hand-merge here\" changed; nothing is resumed",
			len(changed), plural(len(changed)))
	}
	return nil
}
```

- [ ] **Step 4: Wire the subcommand in `cmd/wt/sync.go`**

```go
	resume := &cobra.Command{
		Use:   "resume <work>",
		Short: "Continue the rebase a run left at a conflict that was yours",
		Long: "Pick up the rebase wt sync run left in this worktree: the plan file in\n" +
			".git/worktrees/<name>/wt-sync-plan.md says what landed, what the declared\n" +
			"strategies already resolved and must not be re-opened, and what is yours.\n" +
			"Resolve those, git add them, then run this.\n\n" +
			"Before continuing it checks that nothing is still unmerged and that no\n" +
			"file a strategy resolved was hand-merged; either one is a refusal that\n" +
			"changes nothing. Then it drives the rest of the rebase — the strategies\n" +
			"at any later stop — runs the deferred steps, pins the result ref and\n" +
			"prints the push command. A later conflict that is yours is handed over\n" +
			"again with a fresh plan file.\n\n" +
			"A rebase you finished yourself with git rebase --continue is fine: this\n" +
			"notices and runs only what comes after it. wt sync undo <work> aborts a\n" +
			"handed-over rebase and puts the branch back instead.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncResume(ctx, args[0], commands.ResumeOptions{}, cmd.OutOrStdout())
		},
	}
	sync.AddCommand(resume)
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/commands/ ./cmd/...`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/commands/sync_resume.go internal/commands/sync_resume_test.go cmd/wt/sync.go
git commit -m "feat(sync): wt sync resume continues a handed-over rebase"
git pull --rebase && git push origin main
```

---

### Task 9: `undo` aborts a handed-over rebase; `doctor` and the table report one

**Files:**
- Modify: `internal/wtsync/undo.go`
- Modify: `internal/wtsync/undo_test.go`
- Modify: `internal/wtsync/triage.go` (`Assess`: `Paused`)
- Modify: `internal/commands/sync_doctor.go` (the `plan` row)
- Modify: `internal/commands/sync_doctor_test.go`
- Modify: `internal/commands/sync_undo.go` (print the abort)

**Interfaces:**
- Consumes: `wtsync.HasPlan`, `wtsync.RemovePlan`, `wtsync.PlanHolders` (Task 5).
- Produces: `Restored` gains `Aborted bool`; `Assessment.Paused` is set (declared in Task 3).

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/undo_test.go`:

```go
// A rebase this tool left, with its plan file, is aborted and restored;
// undo is the way back from a handover.
func TestUndoAbortsAHandedOverRebase(t *testing.T) {
	// Build the handover with Rebase (as TestRebaseLeavesAContestedStopInPlace
	// does), then call Undo. Assert: no rebase in progress, the branch is at
	// the safety tip, the plan file and the sidecar are gone, and the
	// Restored row has Aborted set.
}

// A rebase somebody else started by hand carries no plan file and is still
// refused: undo does not abort work it did not start.
func TestUndoStillRefusesAForeignRebase(t *testing.T) {
	// Start a conflicting rebase in the worktree by hand, then Undo.
	// Assert: an error saying it is mid-rebase, and the rebase still in
	// progress afterwards.
}
```

Write both in full.

Add to `internal/commands/sync_doctor_test.go`:

```go
func TestSyncDoctorReportsAWorktreeWaitingOnAPerson(t *testing.T) {
	// Write a plan file into a worktree's git dir, run SyncDoctor, assert
	// the output has a "plan" row naming the work and "wt sync resume".
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ -run 'TestUndoAborts|TestUndoStill|TestSyncDoctorReportsAWork' -v`
Expected: FAIL.

- [ ] **Step 3: `undo.go`**

Add `Aborted bool` to `Restored`. In the check loop, replace the mid-rebase refusal and skip the dirt check for a worktree that is about to be aborted (a stopped rebase always has staged changes):

```go
	var aborting []repo.Worktree
	…
		busy, err := RebaseInProgress(wt.Path)
		if err != nil {
			return nil, err
		}
		if busy {
			has, herr := HasPlan(gitDir)
			if herr != nil {
				return nil, herr
			}
			if !has {
				return nil, fmt.Errorf("%s is mid-rebase; nothing undone", s.Branch)
			}
			// A rebase this tool left, with its plan file: aborting it is
			// exactly what undo is for. Its staged conflicts are not dirt.
			aborting = append(aborting, wt)
			continue
		}
		out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return nil, err
		}
		if out != "" {
			return nil, fmt.Errorf("%s has tracked changes; nothing undone", s.Branch)
		}
```

Then, after that loop and before the tips are read:

```go
	// The aborts come first: until a rebase is aborted the branch ref is
	// still where the run found it and the worktree is mid-flight, so
	// neither the moved-since check nor the reset below means anything.
	aborted := map[string]bool{}
	for _, wt := range aborting {
		if _, err := gitEnv(wt.Path, rebaseEnv, nil, "rebase", "--abort"); err != nil {
			return nil, fmt.Errorf("%s: rebase --abort: %w; nothing undone", wt.Branch, err)
		}
		if busy, err := RebaseInProgress(wt.Path); err != nil {
			return nil, err
		} else if busy {
			return nil, fmt.Errorf("%s is still mid-rebase after the abort; nothing undone", wt.Branch)
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		if err := RemovePlan(gitDir); err != nil {
			return nil, err
		}
		aborted[wt.Branch] = true
	}
```

and in the apply loop, `r := Restored{…, Aborted: aborted[s.Branch]}`.

In `internal/commands/sync_undo.go`, print it:

```go
		if r.Aborted && r.From == r.To {
			fmt.Fprintf(w, "%s  aborted the rebase; back at %s\n", name, short(r.To))
			continue
		}
```
placed before the existing `r.From == r.To` branch.

- [ ] **Step 4: `Paused` in `Assess`**

In `triage.go`, after the `Detached` return and before the status call:

```go
	gitDir, err := GitDir(wt.Path)
	if err != nil {
		a.Err = fmt.Errorf("git dir: %w", err)
		return a
	}
	if a.Paused, err = HasPlan(gitDir); err != nil {
		a.Err = err
		return a
	}
```

and immediately after `a.Behind, a.Ahead = behind, ahead`:

```go
	if a.Paused {
		// A run stopped here and is waiting on a person. The worktree holds
		// a half-finished rebase, so simulating another one would describe
		// a state nobody is in.
		a.Class = Contested
		return a
	}
```

- [ ] **Step 5: The `plan` row in `sync_doctor.go`**

After `checks, err := wtsync.Doctor(...)`, before the table is printed:

```go
	// The plan row is built here rather than in wtsync: it names the resume
	// command, and only this package knows a worktree's work name.
	holders, err := wtsync.PlanHolders(worktrees)
	if err != nil {
		return err
	}
	plan := wtsync.Check{Name: "plan", OK: len(holders) == 0, Detail: "no worktree is waiting on a person"}
	if len(holders) > 0 {
		var lines []string
		for _, wt := range holders {
			name := workName(ctx, wt.Branch)
			lines = append(lines, name+": wt sync resume "+name)
		}
		plan.Detail = strings.Join(lines, "; ")
	}
	checks = append(checks, plan)
```

`plan` is advisory: it is not in the blocking list, and it has no `Fix`.

- [ ] **Step 6: Run the tests**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/undo.go internal/wtsync/undo_test.go internal/wtsync/triage.go internal/commands/sync_doctor.go internal/commands/sync_doctor_test.go internal/commands/sync_undo.go
git commit -m "feat(sync): undo aborts a handed-over rebase; doctor reports one"
git pull --rebase && git push origin main
```

---

### Task 10: Help, README, the bats smoke test, and the handoff

**Files:**
- Modify: `cmd/wt/sync.go` (the bare `sync` long help, and `run`'s)
- Modify: `README.md` (the surfaces table)
- Modify: `test/sync_run.bats`
- Modify: `/Users/anderslindstrom/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md`

- [ ] **Step 1: The bare `wt sync` help**

Replace the two lines that are now wrong:

```
			"  recipe     every conflict at every stop is claimed by a strategy in\n" +
			"             .wt-sync.yaml; a run completes on its own\n" +
			"  contested  a conflict somewhere in the replay is nobody's; a run stops\n" +
			"             there, stages what the strategies did resolve, and writes a\n" +
			"             plan file — finish it and wt sync resume <work>\n" +
```

and the flow block:

```
			"  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps\n" +
			"  finish  wt sync resume <work>   continue after you resolved what was yours\n" +
			"          push with --force-with-lease; wt sync undo <work> puts every ref back\n" +
			"          wt sync doctor          what a run needs, and --fix / --prune\n" +
```

Delete the line "A contested worktree is refused by run until resume exists: rebase it by hand." In `run`'s long help, replace "class contested (rebase those by hand; resume is not built yet)" with a sentence saying a contested stop is left in place with a plan file and `wt sync resume`.

- [ ] **Step 2: README**

In the surfaces table add `wt sync resume <work>` between `run` and `undo`, described as "continue the rebase a run left at a conflict that was yours", and correct the `recipe` description to "every conflict at every stop is claimed by a strategy".

- [ ] **Step 3: The bats smoke test**

Add to `test/sync_run.bats`, following the shape of the tests already there:

```bash
@test "sync run hands a contested stop over and resume finishes it" {
  # a repo with a claimed file and an unclaimed one both in conflict
  run wt sync run w --no-fetch --yes
  [ "$status" -ne 0 ]
  [[ "$output" == *"needs you"* ]]
  [ -f "$(git -C "$WT" rev-parse --absolute-git-dir)/wt-sync-plan.md" ]

  echo resolved > "$WT/a.txt"
  git -C "$WT" add a.txt
  run wt sync resume w
  [ "$status" -eq 0 ]
  [ ! -f "$(git -C "$WT" rev-parse --absolute-git-dir)/wt-sync-plan.md" ]
}
```

Write it in full against the fixture the file already builds.

- [ ] **Step 4: Full check and install**

```bash
make check
./install.sh
```

- [ ] **Step 5: The handoff**

Append to `/Users/anderslindstrom/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md` a section `## What landed for resume (2026-09-09)`: the commit list, that `recipe` now means every stop, the handover contract, `wt sync resume`, what `undo` does with a handed-over rebase, the `plan` doctor row, and the residuals.

- [ ] **Step 6: Commit**

```bash
git add cmd/wt/sync.go README.md test/sync_run.bats
git commit -m "docs(sync): state the handover and resume in the help"
git pull --rebase && git push origin main
```

---

## Self-review

**Spec coverage:**

- **§1, the simulation and "triage is a promise":** Tasks 1–3. The replay now resolves and chains, so `clean` is still exact, `recipe` means the strategies carry the whole replay, and `contested` carries the first stop a person owns wherever it is. The endpoint `divergent` check is untouched and still runs whether or not the replay is clean. The one place the promise is weaker than before is a script-claimed path, which is stated in the class's note rather than hidden — Global Constraints, and Task 2's `Truncated`.
- **§5, the after-the-fact line:** `NeedsYouLine` in Task 5, printed by `handOver` in Task 7. The ask protocol, `wait`/`nak`/`ack` and the queue stay out of scope.
- **§6, the plan file:** Task 5 renders every section the spec names — the header with `scopes:`, "already resolved — do not re-open", "yours" with the trunk-side subject per file and the `additive only` hint, "never hand-merge here", and the deferred steps. The `rr-cache` line of §6's example is **not** produced: `--rerere-autoupdate` is deliberately not passed (run plan ruling), so the tool never knows a resolution came from rerere. Cost if that matters later: a person is not told a conflict was seen before on another branch.
- **§7, `resume`:** Task 8, plus the doctor row and the table note in Task 9.

**Rulings made in this plan, and what each costs if wrong:**

1. **A script-claimed path truncates the replay rather than continuing it.** A `--check` pass proves ownership but yields no bytes, and inventing bytes would be the tool guessing. Cost if wrong: a `recipe` worktree whose declaration uses `script` can still stop at a later commit during a real run — which, after Part 2, means a plan file rather than an abort. Neither live repository declares a `script` today.
2. **`recipe` shows the first stop in the STOP column with `+N more`; `contested` shows the deciding stop and says how many earlier ones resolved in NOTE.** Cost if wrong: cosmetic.
3. **The lock is left behind on a handover and expires normally (30 minutes); the plan file is the durable marker.** `TakeOver` displaces only the exact lock the state file recorded. Cost if wrong: after 30 minutes another run could take the lock in a worktree left mid-rebase — and would then refuse it for being mid-rebase, which is the same answer.
4. **Resume verifies the staged blobs only while a rebase is in progress.** Once someone has finished the rebase by hand those paths are committed, not staged, and later commits may legitimately have changed them. Cost if wrong: a hand-merge of an owned file made *after* a by-hand `git rebase --continue` is not caught; the deferred step (the regeneration) is still the check that follows.
5. **A resume keeps the run's epoch and the run's trunk SHA.** It is the same run: one safety ref, one result ref, one declaration. Cost if wrong: a resume days later replays later commits against a declaration older than trunk's — which is the conservative direction, since the rebase in progress already targets that trunk.
6. **`undo` aborts a mid-rebase worktree only when it carries a wt plan file.** Cost if wrong: a rebase started by hand in a worktree that also has a stale plan file would be aborted; `RemovePlan` on every completion is what keeps that file from going stale.
7. **A handed-over worktree is reported `contested` by the read-only table with a note, without re-simulating.** Cost if wrong: the STOP column is empty for such a row; the plan file has the detail.

**Placeholder scan:** the tests sketched in Tasks 7–9 and Task 8's four cases are marked "write in full" with their assertions stated explicitly; every implementation step carries real code. No TBDs.

**Type consistency:** `Stop{Index, Total, Commit, Subject, Conflicts, Messages, Files, Resolved}` (Task 2) is what Task 3 classifies from and what `stopColumn` reads. `Replay{Commits, Stops, Stop, Truncated, Why, Err}` is used identically in Tasks 2, 3 and 9. `Handover{Index, Total, Commit, Subject, Conflicts, Files, Staged, Left}` is declared in Task 5, built in Task 6, consumed by `RenderPlan` (Task 5) and `handOver` (Task 7). `State` fields (Task 5) are written by `handOver` (Task 7) and read by `SyncResume` and `verifyHandover` (Task 8). `Result.Left` (Task 6) is what Tasks 7 and 8 branch on; `Restored` keeps its old meaning. `LeftLock{PID, Started}` (Task 5) is what `TakeOver` (Task 6) compares. `Check{Name, OK, Detail, Fix}` is unchanged. `handoverInput`/`completeInput` (Task 7) are used by both Task 7 and Task 8.
