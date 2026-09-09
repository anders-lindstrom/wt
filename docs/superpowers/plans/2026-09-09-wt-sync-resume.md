# wt sync resume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two things, in order. **(1)** `recipe` starts meaning what the help text already promises: the object-store simulation resolves each stop with the declared strategies, writes the resolved blobs back into the merged tree and keeps replaying, so the class reflects the *whole* replay and `Replay.Stop` names the first stop a person actually owns, wherever it is. **(2)** A contested stop is no longer aborted: `wt sync run` leaves the rebase in place with the strategy-resolved files staged, writes the plan file of spec §6, keeps the lock and the safety ref, and prints the §5 `needs you` line; `wt sync resume <work>` verifies nothing was hand-merged where a strategy owns the file and drives the same loop to the end; `wt sync undo` aborts and restores a rebase that carries a wt handover.

**Architecture:** Part 1 changes `internal/wtsync/replay.go` only in what it does after a stop — it asks the strategies (through the existing `resolveConflict` in its object-store mode) and, when they all answer, writes their bytes into the conflicted tree with a scratch index (`read-tree` → `update-index --index-info` → `write-tree`) and chains the next simulated commit onto it. `triage.go` stops re-running the strategies itself and reads the outcomes the replay recorded. Part 2 splits the loop inside `Rebase` into a reusable `driver` so `Resume` can re-enter it, adds `plan.go` (the §6 markdown plus a JSON sidecar that is the authoritative handover marker) and `landing.go` (the `scopes:` line), and adds `internal/commands/sync_resume.go`, plus the shared handover and completion tails that `sync_run.go` and `sync_resume.go` both use.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3`, `encoding/json`, git ≥ 2.40 (Anders runs 2.55). Tests are `go test` with throwaway repositories built by the helpers that already exist: `gitIn` and `gitCmd` (`internal/wtsync/config_test.go`), `repoWith` and `linearRepo` (`replay_test.go`), `runRepo` and `trunkReq` (`rebase_test.go`), `featureWorktree` (`triage_test.go`), `syncRepo` and `gitOut` (`internal/commands/sync_test.go`), `repoWithWorktree` (`internal/commands/list_test.go`).

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` §1 (triage, the simulation, "triage is a promise"), §5 (only the after-the-fact `needs you` line), §6 (the plan file), §7 (surfaces: `resume`). The plans it follows: `docs/superpowers/plans/2026-09-09-wt-sync-foundation.md` (the simulation) and `docs/superpowers/plans/2026-09-09-wt-sync-run.md` (the run, whose Global Constraints are copied below).

## Global Constraints

Copied from the run plan, still binding:

- **Only `run`, `resume`, `undo` and `doctor --fix/--prune` write.** `wt sync` with no verb stays read-only; every `git status` it runs keeps `--no-optional-locks`.
- **A safety ref is written before any ref moves** (`refs/wt-sync/<branch>/<epoch>`, spec §4). Nothing rebases without one. `undo` restores to the newest.
- **The rebase command is** `git -c rebase.backend=merge -c rebase.rebaseMerges=false -c rebase.autoStash=false -c rebase.updateRefs=false -c rerere.autoupdate=false rebase --no-update-refs --no-gpg-sign <onto>` (or `--onto <onto> <upstream>` for a stack child), run with `GIT_EDITOR=true` and `GIT_SEQUENCE_EDITOR=true`. The `rerere.autoupdate=false` is **new in this plan**: the run plan believed that not passing `--rerere-autoupdate` was enough, and it is not — a user with `rerere.autoupdate=true` gets cached resolutions staged, which removes them from `ls-files -u` before any strategy sees them and before the handover can protect them.
- **The config and any `script` are read from `origin/<trunk>`**, never from the worktree being rebased (spec §3). Resume reads them from the *run's* trunk SHA, recorded in the sidecar, never from a trunk that has moved since.
- **A strategy refuses rather than guesses.** Nothing is ever hand-merged by the tool.
- **Trunk is fetched first, then read once**; one SHA for the whole run.
- **A run has one identity, `epoch` (`time.Now().UnixNano()`).** A resume keeps the epoch of the run it continues: it is the same run.
- **Never half-apply a stack.** Every member is locked before any member moves; the lock is held through the deferred steps.
- **More than one worktree needs one confirmation** (spec §7); `--yes` skips; no terminal means no question.
- **Every subprocess has a deadline.** A script gets 60 s, a deferred step 30 min, `docker info` 10 s, a git 10 min. Task 2 closes the gap the run plan left open here.
- **Agent detection failing is a refusal, not a note**, in `run`, `resume` and `undo`.
- **A failed deferred step never undoes the rebase** (spec §3). It is reported as owed with its output.
- **Never say "ours" or "theirs".** Stage 2 is **trunk**, stage 3 is **the branch**. Fields are `Base`, `Trunk`, `Branch`. (The plan file's `yours` section is the one place "ours" appears, because §6 writes it that way for the person reading it: `additive only (trunk +12, ours +3)`.)
- **A worktree with tracked changes, a live agent, no declaration on trunk, or class `divergent` is never touched.**
- **No fleet fact in code or tests.** Branch names and counts from the spec are illustrations.
- **Paths are repository-root relative**; globs are matched with `MatchGlob`, never expanded against the disk.
- Commits follow Conventional Commits, imperative, lowercase, under 72 characters. **No commit trailers of any kind** — no `Co-Authored-By`, no `Claude-Session`, whatever a harness reminder says. Work on `main`, push after every commit (`git pull --rebase` first: another session commits to `cmd/wt/root.go`, `README.md` and `install.sh` on the same branch).
- `gofmt`, `go vet ./...` and `golangci-lint run ./...` clean before every commit. Tests run with `-race` at least once per task.

New for this plan:

- **A worktree stopped mid-rebase reports the branch it is on.** `git worktree list --porcelain` says `detached` for it, because a merge-backend rebase detaches HEAD (verified 2026-09-09). Everything in `wt sync` keys on the branch — `Locate`, `Assess`, `Parents`, `Undo` — so without this a handed-over worktree cannot be named, resumed, undone or seen as part of a stack. `repo.Worktrees` reads the sequencer's own `head-name` and fills `Branch`, with `Rebasing` marking why (Task 3).
- **`recipe` means every stop resolves, not the first.** The class comes from the whole replay. `contested` names the first stop with an unclaimed or refused path, wherever in the replay it falls, and that is the stop `Replay.Stop` and the table's STOP column report.
- **Scripts stay `--check` only at simulation time, and a replay that cannot be carried past one is marked `recipe?`, never plain `recipe`.** A script cannot resolve in the object store, so a script-claimed path counts as *resolvable* when `--check` passes but yields no bytes to chain. Printing plain `recipe` there would keep exactly the false promise Part 1 exists to remove.
- **The endpoint divergence check is unchanged** and still runs whether or not the replay is clean (spec §1).
- **A contested stop is handed over, not aborted** — unless the branch has descendants in the same run, where the stop is restored instead (never half-apply a stack). Only a *failure* (a git error, a strategy error, a rebase that will not advance) still aborts and restores.
- **A resume never restores.** A run's restore throws away only the tool's own work; a resume's would throw away a person's. Any failure during a resume leaves the worktree exactly as it is and points at `wt sync undo`.
- **The sidecar, not the markdown, is the handover marker.** Both are written temp-then-rename so a crash cannot leave half a handover; `HasPlan` reads the sidecar.
- **The handover is deleted whenever the run ends** — by `resume` completing, by `undo`, and by a `run` whose restore succeeded. A stale marker would report a worktree as waiting on somebody forever.
- **A plain `git rebase --continue` by hand is tolerated,** but a finished rebase is verified before it is believed: HEAD on the branch, `onto` an ancestor of HEAD, HEAD not still at the old tip. An aborted or reset rebase is refused, not certified as this run's result.
- **Resume verifies before it continues:** the sidecar and the safety ref are present and agree with each other, the live sequencer is the rebase the sidecar describes, no unmerged path remains, no tracked file is left unstaged (git refuses to continue on that, and the loop's non-advance guard would otherwise turn it into a restore), and no path a strategy resolved at this stop has a different staged blob than the one recorded.

---

## File Structure

```
internal/repo/repo.go        Worktree.Rebasing; a detached mid-rebase worktree reports head-name  (modified)
internal/repo/repo_test.go                                                                        (modified)
internal/wtsync/
  simtree.go       resolvedTree: write strategy bytes into a merge-tree tree via a scratch index  (new)
  simtree_test.go                                                                                  (new)
  script.go        gitEnvAllow: gitEnv that tolerates one exit code, so mergeTree and catFileRaw
                   get the deadline and the process group every other subprocess has              (modified)
  replay.go        Stop gains Files/Resolved; Replay gains Stops/Truncated/Why/Err; SimulateRebase
                   takes *Config, resolves each stop and keeps replaying; blobs kept only for the
                   deciding stop; mergeTree and catFileRaw go through gitEnvAllow                 (modified)
  triage.go        Assess reads the replay's outcomes; Recipe/Contested from the whole replay;
                   Unverified; Paused                                                              (modified)
  landing.go       Landing, ScopeCount, LandingList: the first-parent log and its scopes (§5)      (new)
  landing_test.go                                                                                  (new)
  plan.go          PlanName/StateName, State, ReadState/WriteState/RemovePlan/HasPlan/PlanHolders,
                   PlanInput, RenderPlan, NeedsYouLine                                             (new)
  plan_test.go                                                                                     (new)
  rebase.go        driver{} extracted from Rebase; Handover; Result.Left; Resume; Preflight lets
                   Contested proceed and refuses Paused; rerere.autoupdate=false                   (modified)
  lock.go          Lock.Keep, TakeOver                                                             (modified)
  undo.go          abort and restore a rebase that carries a handover, after every refusal check   (modified)
internal/commands/
  sync_finish.go   handOver() and completeRun(): the two tails run and resume share                (new)
  sync_run.go      hands a contested stop over instead of refusing it                              (modified)
  sync_resume.go   SyncResume, verifyHandover                                                      (new)
  sync_resume_test.go                                                                              (new)
  sync_doctor.go   the `plan` row; the `rebases` row points at resume                              (modified)
  sync.go          STOP column reads the deciding stop; `recipe?`; NOTE says how many stops        (modified)
cmd/wt/sync.go     `resume` subcommand; help text corrected throughout
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
- Consumes: `gitEnv(dir, env, stdin, args...)` and `hashObject(root, data)` (`internal/wtsync/script.go`); `gitIn(t, dir, args...) string` (`config_test.go`) — it already returns trimmed stdout, so no new output helper is needed; `repoWith` (`replay_test.go`).
- Produces: `func resolvedTree(mainRoot, tree string, resolved map[string][]byte) (string, error)` — package-private, used by Task 4.

Verified 2026-09-09 against git 2.55, so the implementer does not have to: `git merge-tree --write-tree` on a conflict exits 1 and still writes a usable tree containing every conflicted path **at its own name with the markers inside**; `read-tree` → `ls-files --stage -z` → `update-index -z --index-info` (the stage-0 `<mode> <oid>\t<path>\0` form) → `write-tree` against `GIT_INDEX_FILE` round-trips, replacing one blob and carrying every other entry through unchanged.

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
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, map[string][]byte{"sub/b.txt": []byte("resolved\n")})
	if err != nil {
		t.Fatal(err)
	}
	if out == tree {
		t.Fatal("expected a new tree")
	}
	if got := gitIn(t, dir, "cat-file", "-p", out+":sub/b.txt"); got != "resolved" {
		t.Fatalf("sub/b.txt = %q, want %q", got, "resolved")
	}
	if got := gitIn(t, dir, "cat-file", "-p", out+":a.txt"); got != "a" {
		t.Fatalf("a.txt = %q, want %q", got, "a")
	}
}

func TestResolvedTreeKeepsTheExecutableBit(t *testing.T) {
	dir := repoWith(t, map[string]string{"s.sh": "old\n"}, nil, nil)
	gitIn(t, dir, "update-index", "--chmod=+x", "s.sh")
	gitIn(t, dir, "commit", "-q", "-m", "exec")
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, map[string][]byte{"s.sh": []byte("new\n")})
	if err != nil {
		t.Fatal(err)
	}
	if mode := strings.Fields(gitIn(t, dir, "ls-tree", out, "--", "s.sh"))[0]; mode != "100755" {
		t.Fatalf("mode = %s, want 100755", mode)
	}
}

func TestResolvedTreeRefusesAPathThatIsNotThere(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	_, err := resolvedTree(dir, tree, map[string][]byte{"missing.txt": []byte("x\n")})
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("err = %v, want it to name missing.txt", err)
	}
}

func TestResolvedTreeIsANoOpForNoPaths(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != tree {
		t.Fatalf("tree = %s, want it unchanged (%s)", out, tree)
	}
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
//
// One divergence, stated because it cannot be closed here: a real rebase
// stages a strategy's answer with `git add`, which runs the repository's
// clean filters and end-of-line normalisation; this hashes the bytes as
// they are. A repository with a filter that rewrites resolver output would
// feed the next commit something the simulation did not model.
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

// indexModes reads the mode each path carries in the scratch index. Paths go
// after "--" and come back NUL-separated, so a name with a space or a quote
// in it survives.
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
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./internal/wtsync/
git add internal/wtsync/simtree.go internal/wtsync/simtree_test.go
git commit -m "feat(sync): write a strategy's answer back into a merged tree"
git pull --rebase && git push origin main
```

---

### Task 2: Every git the replay runs gets the deadline it was promised

**Files:**
- Modify: `internal/wtsync/script.go` (add `gitEnvAllow`)
- Modify: `internal/wtsync/replay.go` (`mergeTree`, `catFileRaw`)
- Modify: `internal/wtsync/script_test.go` or `replay_test.go` (one test)

The run plan's Global Constraints say every subprocess has a deadline. `gitEnv` provides one (`GitTimeout`, 10 minutes) and a process group `KillRunning` can kill on an interrupt. `mergeTree` and `catFileRaw` use plain `exec.Command` and have neither. Task 4 makes the replay call both once per commit instead of once per branch, which is the moment to fix it.

**Interfaces:**
- Produces: `func gitEnvAllow(dir string, env []string, stdin io.Reader, allow int, args ...string) (string, int, error)` — like `gitEnv`, but an exit status equal to `allow` is returned as `(stdout, allow, nil)` instead of an error. `gitEnv` becomes a thin wrapper that allows nothing.

- [ ] **Step 1: Write the failing test**

Add to `internal/wtsync/script_test.go`:

```go
func TestGitEnvAllowReturnsTheAllowedExitStatus(t *testing.T) {
	// main and feature have diverged, so neither is the other's ancestor.
	dir := repoWith(t,
		map[string]string{"a.txt": "a\n"},
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})

	// The allowed status is an answer, not a failure: this is the whole
	// point of the function, so it is what the test must exercise.
	out, code, err := gitEnvAllow(dir, nil, nil, 1, "merge-base", "--is-ancestor", "main", "feature")
	if err != nil || code != 1 || out != "" {
		t.Fatalf("diverged: %q, %d, %v; want code 1 and no error", out, code, err)
	}
	// A true answer still exits 0.
	if _, code, err := gitEnvAllow(dir, nil, nil, 1, "merge-base", "--is-ancestor", "main", "main"); err != nil || code != 0 {
		t.Fatalf("same commit: %d, %v; want code 0", code, err)
	}
	// Any other status is still an error (cat-file exits 128 here).
	if _, _, err := gitEnvAllow(dir, nil, nil, 1, "cat-file", "-p", "notacommit"); err == nil {
		t.Fatal("an unexpected exit status must still be an error")
	}
	// allow < 0 tolerates nothing: that is gitEnv's contract.
	if _, _, err := gitEnvAllow(dir, nil, nil, -1, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("allow -1 must not tolerate exit 1")
	}
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ -run TestGitEnvAllow -v`
Expected: FAIL — `undefined: gitEnvAllow`.

- [ ] **Step 3: Implement**

In `script.go`, rename the body of `gitEnv` into `gitEnvAllow` and keep `gitEnv` as a wrapper:

```go
// gitEnvAllow runs git in dir with extra environment and optional stdin,
// returning trimmed stdout and the exit status. A status equal to allow is
// an answer, not a failure: merge-base --is-ancestor and merge-tree both
// use one. Every other non-zero status is an error. allow < 0 allows none.
func gitEnvAllow(dir string, env []string, stdin io.Reader, allow int, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = withEnv(append([]string{"GIT_TERMINAL_PROMPT=0"}, env...)...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := runScript(cmd)
	out := strings.TrimRight(stdout.String(), "\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", 0, fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), GitTimeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if allow >= 0 && errors.As(err, &exit) && exit.ExitCode() == allow {
			return out, allow, nil
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", 0, fmt.Errorf("%s (%w)", msg, err)
		}
		return "", 0, err
	}
	return out, 0, nil
}

// gitEnv runs git and treats every non-zero status as a failure.
func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	out, _, err := gitEnvAllow(dir, env, stdin, -1, args...)
	return out, err
}
```

`gitEnvAllow` trims trailing newlines like `gitEnv` always has, which `mergeTree` must not rely on: it re-reads its own NUL records. Have `mergeTree` ask for the untrimmed bytes by keeping its own small reader — or simpler, since `-z` output ends in a NUL rather than a newline, trimming newlines is harmless. Verify with the existing `TestSimulate…` tests, which cover merge-tree output parsing.

In `replay.go`, replace `mergeTree`'s `exec.Command` block with:

```go
	out, code, err := gitEnvAllow(mainRoot, nil, nil, 1, args...)
	if err != nil {
		return "", false, nil, "", fmt.Errorf("git merge-tree: %w", err)
	}
	records := strings.Split(out, "\x00")
	tree = strings.TrimSpace(records[0])
	if code == 0 {
		return tree, true, nil, "", nil
	}
```

and the rest of the function unchanged, and replace `catFileRaw`:

```go
// catFileRaw reads a blob. Trailing newlines are preserved: gitEnv trims
// them, so this goes through the same deadline and process group by asking
// for the raw bytes with a stdout redirect git itself does not touch.
func catFileRaw(mainRoot, oid string) ([]byte, error) {
	out, err := gitEnvRaw(mainRoot, "cat-file", "blob", oid)
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w", oid, err)
	}
	return out, nil
}
```

and add `gitEnvRaw` next to `gitEnvAllow` in `script.go`, returning `stdout.Bytes()` untrimmed and allowing nothing. The two must NOT be copies of each other: put the context deadline, the `runScript` call, the deadline-exceeded check and the stderr formatting in one unexported core (`runGit(dir, env, stdin, args...) (stdout []byte, stderrMsg string, err error)` or similar), and let `gitEnvAllow` and `gitEnvRaw` add only their own trimming and exit-status handling on top. A blob's trailing newline is content: trimming it would corrupt every strategy's input, so this is not optional.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/wtsync/`
Expected: PASS — in particular every existing `merge3`, `openapi` and `replay` test, which are what prove the blob bytes survived.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/script.go internal/wtsync/script_test.go internal/wtsync/replay.go
git commit -m "fix(sync): give merge-tree and cat-file the deadline they lacked"
git pull --rebase && git push origin main
```

---

### Task 3: A worktree stopped mid-rebase reports the branch it is on

**Files:**
- Modify: `internal/repo/repo.go` (`Worktree`, `Worktrees`)
- Modify: `internal/repo/repo_test.go`

A merge-backend rebase detaches HEAD, so `git worktree list --porcelain` prints `detached` and no `branch` line for a worktree a run has handed over (verified 2026-09-09). Every part of `wt sync` keys on the branch: `Locate` matches a work name against `wt.Branch` and returns false when it is empty, `Assess` returns `Detached` before anything else, `Parents` skips detached worktrees, and `Undo.byBranch` skips them too. Without this task the whole of Part 2 is unreachable: a handed-over worktree cannot be named, resumed, undone, or seen as part of a stack, and `wt list` calls it `(detached)`.

The sequencer records the branch itself, in `<gitdir>/rebase-merge/head-name` (or `rebase-apply/head-name`). Reading it needs no subprocess.

**Interfaces:**
- Produces: `repo.Worktree` gains `Rebasing bool`. For a worktree git reports as detached *because a rebase is in progress*, `Branch` is filled from `head-name`, `Detached` is set to `false`, and `Rebasing` is `true`. A worktree detached for any other reason is untouched.

- [ ] **Step 1: Write the failing test**

Add to `internal/repo/repo_test.go` (follow whatever fixture that file already uses to build a repo with a worktree; if it has none, build one with `git init`, a commit, `git worktree add`):

```go
func TestWorktreesNameTheBranchOfAStoppedRebase(t *testing.T) {
	// main + a linked worktree on feature, with a conflict between them.
	// Start the rebase in the worktree and let it stop.
	// …fixture…

	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	var wt Worktree
	for _, w := range list {
		if !w.IsMain {
			wt = w
		}
	}
	if wt.Branch != "feature" {
		t.Fatalf("Branch = %q, want feature: git says detached during a rebase", wt.Branch)
	}
	if wt.Detached {
		t.Fatal("Detached = true: a worktree whose branch we can name is not detached")
	}
	if !wt.Rebasing {
		t.Fatal("Rebasing = false, want true")
	}
}

func TestWorktreesLeaveAGenuinelyDetachedWorktreeAlone(t *testing.T) {
	// A worktree checked out at a bare SHA, no rebase.
	// Assert Branch == "", Detached == true, Rebasing == false.
}

// A rebase started from an already-detached HEAD writes the literal
// "detached HEAD" into head-name. There is no branch to recover, so the
// worktree must stay detached rather than acquire a branch by that name.
func TestWorktreesIgnoreADetachedHeadRebase(t *testing.T) {
	// git worktree add --detach, commit on the detached HEAD, start a
	// conflicting rebase, let it stop.
	// Assert Branch == "", Detached == true, Rebasing == false.
}
```

Write all three in full.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/repo/ -run TestWorktrees -v`
Expected: FAIL — `Branch = "", want feature`.

- [ ] **Step 3: Implement**

Add the field:

```go
	// Rebasing is set when git reports the worktree as detached only
	// because a rebase is in progress. Branch then names the branch the
	// sequencer will put HEAD back on, read from its own head-name.
	Rebasing bool
```

and, in `Worktrees`, after `flush()` has built the list:

```go
	for i := range list {
		if !list[i].Detached || list[i].Branch != "" {
			continue
		}
		if branch := rebaseHeadName(list[i].Path); branch != "" {
			list[i].Branch, list[i].Detached, list[i].Rebasing = branch, false, true
		}
	}
```

with:

```go
// rebaseHeadName reads the branch a stopped rebase will return HEAD to, from
// the sequencer's own bookkeeping. A rebase detaches HEAD, so git reports
// the worktree as detached while it runs; head-name is how git itself
// remembers where it came from. Empty when no rebase is in progress or the
// bookkeeping cannot be read.
func rebaseHeadName(wtPath string) string {
	dir, err := gitDirOf(wtPath)
	if err != nil {
		return ""
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		b, err := os.ReadFile(filepath.Join(dir, name, "head-name"))
		if err != nil {
			continue
		}
		// A rebase started from an already-detached HEAD records the
		// literal "detached HEAD" here, not a ref: there is no branch to
		// name, and treating that string as one would invent a branch
		// called "detached HEAD" (verified against git 2.55).
		head := strings.TrimSpace(string(b))
		if !strings.HasPrefix(head, "refs/heads/") {
			continue
		}
		return strings.TrimPrefix(head, "refs/heads/")
	}
	return ""
}

// gitDirOf resolves a checkout's git dir without a subprocess: a linked
// worktree's .git is a file holding "gitdir: <path>", the main checkout's is
// the directory itself.
func gitDirOf(wtPath string) (string, error) {
	p := filepath.Join(wtPath, ".git")
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return p, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if dir == "" {
		return "", fmt.Errorf("%s names no git dir", p)
	}
	return dir, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./...`
Expected: PASS. `wt list` now prints the branch for a worktree mid-rebase instead of `(detached)`, which is the honest answer; if a test asserted the old wording, fix the assertion.

- [ ] **Step 5: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/repo/repo.go internal/repo/repo_test.go
git commit -m "fix(repo): name the branch of a worktree stopped mid-rebase"
git pull --rebase && git push origin main
```

---

### Task 4: The simulation resolves each stop, keeps replaying, and the table says so

**Files:**
- Modify: `internal/wtsync/replay.go` (`Stop`, `Replay`, `SimulateRebase`)
- Modify: `internal/wtsync/replay_test.go`
- Modify: `internal/wtsync/triage.go` (`Assessment`, `Assess`)
- Modify: `internal/wtsync/triage_test.go`
- Modify: `internal/commands/sync.go` (`stopColumn`, `classColumn`, `noteColumn`)
- Modify: `internal/commands/sync_test.go`

The classification and the table change in the same task as the replay: a commit that changed only the replay would leave `TestAssessClassifiesCleanRecipeAndContested` and the command table test failing, because a fully resolved replay has `Stop == nil` and the old code calls that `Clean`.

**Interfaces:**
- Consumes: `resolvedTree` (Task 1); `resolveConflict(mainRoot, onto, cfg, c, wtPath) (Resolution, error)` with `Resolution{Outcome FileOutcome, Content []byte, InPlace bool}` (`resolve.go`); `cfg.RuleFor(path) (Rule, bool)`.
- Produces:
  - `type Stop struct { Index, Total int; Commit, Subject string; Conflicts []Conflict; Messages string; Files []FileOutcome; Resolved bool }` — `Conflicts` carries the three blobs **only for the stop that stops the replay**; a resolved stop keeps its outcomes and drops its bytes.
  - `type Replay struct { Commits int; Stops []Stop; Stop *Stop; Truncated bool; Why string; Err error }`
  - `func SimulateRebase(mainRoot, onto, branch string, cfg *Config) (Replay, error)`
  - `Assessment` gains `Unverified bool` (the replay was truncated) and `Paused bool` (a run left a handover here; set in Task 10).

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/replay_test.go`:

```go
func ownedLineConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"))
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
		if len(s.Conflicts) != 0 {
			t.Fatalf("stop %d kept its blobs; only the deciding stop may", i+1)
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
	if len(r.Stop.Conflicts) != 1 || len(r.Stop.Conflicts[0].Trunk) == 0 {
		t.Fatalf("the deciding stop must keep its blobs: %+v", r.Stop.Conflicts)
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

// The resolved bytes must be what the NEXT commit replays against. Trunk
// is 2.0.0, so stop 1 resolves max-plus-patch(branch 1.1.0, trunk 2.0.0) to
// 2.0.1. The second branch commit touches v.txt again AND a file nothing
// claims, so the replay stops there and keeps that stop's blobs — and the
// trunk side of its v.txt conflict is the proof: 2.0.1 means the resolved
// blob was carried forward, 2.0.0 means it was not. (Unchained, the second
// resolution would come out 2.0.1 instead of 2.0.2, so asserting on the
// blob is both simpler and stricter than asserting on the version.)
func TestSimulateChainsTheResolvedContent(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.1.1\n", "a.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 2 {
		t.Fatalf("Stop = %+v, want the second commit", r.Stop)
	}
	var got string
	for _, c := range r.Stop.Conflicts {
		if c.Path == "v.txt" {
			got = string(c.Trunk)
		}
	}
	if got != "2.0.1\n" {
		t.Fatalf("v.txt trunk side at stop 2 = %q, want %q: the resolved blob was not chained", got, "2.0.1\n")
	}
}
```

Add to `internal/wtsync/triage_test.go`:

```go
func TestAssessRecipeMeansEveryStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	a := Assess(dir, "main", ownedLineConfig(t), featureWorktree(t, dir), nil)
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
	a := Assess(dir, "main", ownedLineConfig(t), featureWorktree(t, dir), nil)
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

And to `internal/commands/sync_test.go` a test that the STOP column names the deciding stop: extend `syncRepo`'s bump worktree with a second commit that conflicts on an unclaimed file, then assert the output contains `2/2`, the unclaimed file with `✗`, and `1 earlier stop resolved`, and does not contain `1/2`. Write it in full.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ -run 'TestSimulate|TestAssess|TestSync' -v`
Expected: FAIL — too many arguments to `SimulateRebase`; `Replay` has no field `Stops`.

- [ ] **Step 3: Change `replay.go`**

Replace the `Stop` and `Replay` types:

```go
// Stop is one commit at which a rebase stops, with what the declared
// strategies answered for every file it conflicts on.
type Stop struct {
	Index int // 1-based position among the commits the rebase replays
	Total int
	Commit  string
	Subject string
	// Conflicts carries the three blobs of each conflicted file. It is kept
	// only for the stop that stops the replay: a long branch can stop
	// dozens of times on a megabyte file, and holding every stop's blobs
	// would make one assessment cost hundreds of megabytes for bytes
	// nothing reads again.
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

Replace `SimulateRebase`:

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
			resolved := map[string][]byte{}
			script := ""
			// A conflict merge-tree reports only in its messages has no
			// blobs to put to a strategy, so it is nobody's but a person's.
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
			if !stop.Resolved {
				rep.Stops = append(rep.Stops, stop)
				last := rep.Stops[len(rep.Stops)-1]
				rep.Stop = &last
				return rep, nil
			}
			// Past here the stop is resolved and nothing reads its bytes
			// again: keep the outcomes, drop the blobs.
			stop.Conflicts = nil
			rep.Stops = append(rep.Stops, stop)
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

- [ ] **Step 4: Classify from the whole replay in `triage.go`**

Add to `Assessment`:

```go
	// Unverified means the replay could not be carried to the end: a script
	// claims a path, and a script can only be checked before a run. The
	// class is what the replay earned up to that point.
	Unverified bool
	// Paused is a worktree a run left mid-rebase with a handover in it.
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
	a.Unverified = a.Replay.Truncated
	switch {
	case a.Replay.Stop != nil:
		a.Files = a.Replay.Stop.Files
		a.Class, a.Files = classifyStop(a.Files, a.Replay.Stop.Messages)
	case len(a.Replay.Stops) > 0:
		// Every stop reached was resolved by a strategy.
		a.Class = Recipe
		a.Files = a.Replay.Stops[0].Files
	default:
		a.Class = Clean
	}
```

and, after the `divergence` block that assigns `a.Notes`, append rather than overwrite:

```go
	if a.Replay.Truncated {
		a.Notes = append(a.Notes, a.Replay.Why)
	}
```

- [ ] **Step 5: The table in `internal/commands/sync.go`**

The CLASS cell gets its own function, so an unverified replay never prints a bare `recipe`:

```go
// classColumn is the class, with a question mark when the replay could not
// be carried to the end. `recipe?` is not `recipe`: a script owns a path,
// and all the simulation could ask it was whether it claims the file.
func classColumn(a wtsync.Assessment) string {
	if a.Unverified {
		return a.Class.String() + "?"
	}
	return a.Class.String()
}
```

used in the `Fprintf` in place of `a.Class`. Replace `stopColumn`:

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
	if n := len(a.Replay.Stops) - 1; a.Replay.Stop != nil && n > 0 {
		notes = append(notes, fmt.Sprintf("%d earlier stop%s resolved", n, plural(n)))
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./...`
Expected: PASS. Existing assertions that a first-stop-resolving branch is `recipe` while a later stop is unclaimed now legitimately read `contested`; update them and name in the test what they pin.

- [ ] **Step 7: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/replay.go internal/wtsync/replay_test.go internal/wtsync/triage.go internal/wtsync/triage_test.go internal/commands/sync.go internal/commands/sync_test.go
git commit -m "feat(sync): class the whole replay, not only its first stop"
git pull --rebase && git push origin main
```

---

## Part 2 — the handover, the plan file, and `resume`

### Task 5: What landed, and its scopes

**Files:**
- Create: `internal/wtsync/landing.go`
- Create: `internal/wtsync/landing_test.go`

**Interfaces:**
- Consumes: `gitEnv`; `gitIn` (already returns trimmed stdout).
- Produces: `type ScopeCount struct { Scope string; Count int }`; `type Landing struct { Commits int; Scopes []ScopeCount }`; `func LandingList(mainRoot, base, trunk string) (Landing, error)`; `func (l Landing) ScopeLine() string` → `"pins ×6, statepush ×4"`, `""` when nothing carries a scope.

Verified 2026-09-09: `git log --first-parent --format='%H %P%x00%s'` prints all parents on the left of the NUL and the subject on the right, and `git log --format=%s <sha>^1..<sha>^2` lists the merged range's own subjects.

- [ ] **Step 1: Write the failing test**

Create `internal/wtsync/landing_test.go`:

```go
package wtsync

import (
	"os"
	"path/filepath"
	"testing"
)

// A direct commit carries its own scope; a merge commit's subject has none
// ("Merge pull request #N from …"), so its scopes come from the range it
// merged (spec §5).
func TestLandingListCountsDirectAndMergedScopes(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("a.txt", "a2\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): move pin quality out")

	gitIn(t, dir, "checkout", "-q", "-b", "side")
	write("c.txt", "c\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix(statepush): count endings by reason")
	write("c.txt", "c2\n")
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
	base := gitIn(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
// #N from …", which has none, so its scopes come from the ranges it merged —
// <sha>^1..<sha>^N for every parent after the first, so an octopus merge is
// not read as if it had two sides (spec §5). Everything is local and no PR
// body is fetched; the one cost is a git log per merge commit, which is why
// this runs when a handover is written and never at triage.
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
		if len(fields) < 3 { // <sha> <parent>, or a root commit: not a merge
			countScope(counts, subject)
			continue
		}
		sha := fields[0]
		for n := 2; n <= len(fields)-1; n++ {
			rng := fmt.Sprintf("%s^1..%s^%d", sha, sha, n)
			merged, err := gitEnv(mainRoot, nil, nil, "log", "--format=%s", rng, "--")
			if err != nil {
				return Landing{}, fmt.Errorf("landing list %s: %w", short(sha), err)
			}
			for _, s := range strings.Split(merged, "\n") {
				countScope(counts, s)
			}
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
git add internal/wtsync/landing.go internal/wtsync/landing_test.go
git commit -m "feat(sync): count what landed on trunk and its scopes"
git pull --rebase && git push origin main
```

---

### Task 6: The plan file and the state sidecar

**Files:**
- Create: `internal/wtsync/plan.go`
- Create: `internal/wtsync/plan_test.go`
- Modify: `internal/wtsync/rebase.go` (declare `Handover` only; Task 7 builds it)

**Interfaces:**
- Consumes: `Landing`/`ScopeLine` (Task 5); `Conflict`, `FileOutcome`, `Config`, `Rule`, `Deferred`; `gitEnv`, `hashObject`; `repo.Worktree`.
- Produces:
  - `const PlanName = "wt-sync-plan.md"`, `const StateName = "wt-sync-state.json"`
  - `func PlanPath(gitDir string) string`, `func StatePath(gitDir string) string`
  - `type LeftLock struct { PID int; Started int64 }`
  - `type State struct { … }` (below), `func WriteState`, `func ReadState`, `func WritePlanFile`
  - `func HasPlan(gitDir string) (bool, error)` — reads the **sidecar**, which is the marker; `func RemovePlan(gitDir string) error`; `func PlanHolders(worktrees []repo.Worktree) ([]repo.Worktree, error)`
  - `type PlanInput struct { … }`, `func RenderPlan(in PlanInput) (string, error)`, `func NeedsYouLine(work string, left []string) string`
  - `type Handover struct { Index, Total int; Commit, Subject string; Conflicts []Conflict; Files []FileOutcome; Staged map[string]string; Deleted []string; Left []string }`

Two shapes matter here and both came out of review:

- **The sidecar is the marker, not the markdown.** Both are written to a temp file and renamed into place, so a crash cannot leave a half-written handover; `HasPlan` reads the sidecar because that is what `resume` and `undo` act on.
- **A path can be resolved by deletion.** `Script.ResolveInWorktree` accepts any outcome with no unmerged entry left, and staging a deletion satisfies that. `Staged` therefore holds only paths that still exist; `Deleted` holds the rest, and resume checks those are still absent.

- [ ] **Step 1: Write the failing test**

Create `internal/wtsync/plan_test.go`:

```go
package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func planConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`conflicts:
  - paths: [v.txt]
    strategy: owned-line
    line: '^\d'
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
	base := gitIn(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("trunk\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		"stopped at stop 2/12",
		"## already resolved — do not re-open",
		"owned-line",
		"## yours — 1 file",
		"trunk: feat(pins): trunk moves a.txt",
		"## never hand-merge here",
		"etc/*.json",
		"openapi",
		"the deferred `./gradlew generateOpenApi` owns it",
		"## deferred, runs when the rebase completes",
		"wt sync resume state_stats",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan is missing %q:\n%s", want, out)
		}
	}
}

// A file the strategies own but refused is a person's after all. It must not
// appear under "never hand-merge here", where its own declaration would
// otherwise put it — the plan would then say both "resolve this" and "never
// resolve this".
func TestRenderPlanDoesNotForbidWhatItAsksFor(t *testing.T) {
	dir := repoWith(t, map[string]string{"v.txt": "1.0.0\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Config: planConfig(t),
		Handover: Handover{
			Index: 1, Total: 1,
			Conflicts: []Conflict{{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("x\n"), Branch: []byte("y\n")}},
			Files:     []FileOutcome{{Path: "v.txt", Strategy: "owned-line", Note: "both sides changed a line it does not own"}},
			Left:      []string{"v.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := out[strings.Index(out, "## never hand-merge here"):]
	if strings.Contains(forbidden, "v.txt") {
		t.Fatalf("a refused owned file is listed as never-hand-merge:\n%s", out)
	}
	if !strings.Contains(out, "owned-line refused it") {
		t.Fatalf("the yours entry does not say the strategy refused it:\n%s", out)
	}
}

// Both sides only added lines: the plan says so, because that is a
// five-second read rather than a merge (spec §6).
func TestRenderPlanMarksAnAdditiveConflict(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
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

func TestStateRoundTripsAndIsTheMarker(t *testing.T) {
	dir := t.TempDir()
	if has, err := HasPlan(dir); err != nil || has {
		t.Fatalf("HasPlan on an empty dir = %v, %v", has, err)
	}
	want := State{
		Branch: "feat_wt/w", Work: "w", Trunk: "abc", TrunkRef: "origin/main", Onto: "abc",
		Epoch: 42, Safety: "refs/wt-sync/feat_wt/w/42", OldTip: "def", Stop: 2, Total: 12,
		Resolved: map[string]string{"v.txt": "cafe"}, Strategy: map[string]string{"v.txt": "owned-line"},
		Deleted:  []string{"gone.txt"}, Left: []string{"a.txt"}, Lock: LeftLock{PID: 7, Started: 99},
	}
	if err := WriteState(dir, want); err != nil {
		t.Fatal(err)
	}
	has, err := HasPlan(dir)
	if err != nil || !has {
		t.Fatalf("HasPlan after WriteState = %v, %v; the sidecar is the marker", has, err)
	}
	got, ok, err := ReadState(dir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if got.Epoch != want.Epoch || got.Resolved["v.txt"] != "cafe" || got.Lock.PID != 7 || len(got.Deleted) != 1 {
		t.Fatalf("State = %+v, want %+v", got, want)
	}
	if err := RemovePlan(dir); err != nil {
		t.Fatal(err)
	}
	if has, _ := HasPlan(dir); has {
		t.Fatal("RemovePlan left the marker")
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
Expected: FAIL — `undefined: RenderPlan`, `undefined: Handover`.

- [ ] **Step 3: Declare `Handover` in `rebase.go`**

```go
// Handover is a stop the run left for a person: where the rebase is, the
// three blobs of every conflict there, what the strategies answered, the
// blob each resolved path was staged with, the paths a strategy resolved by
// deleting, and the paths a person owns. Staged and Deleted are what resume
// compares against to prove nothing was hand-merged where a strategy owns
// the file.
type Handover struct {
	Index, Total int
	Commit       string
	Subject      string
	Conflicts    []Conflict
	Files        []FileOutcome
	Staged       map[string]string
	Deleted      []string
	Left         []string
}
```

- [ ] **Step 4: Write `plan.go`**

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

// PlanName is the brief a run leaves in a worktree's own git dir when it
// stops at a conflict a person owns (spec §6).
const PlanName = "wt-sync-plan.md"

// StateName is the machine-readable half of the same handover, and the
// marker the tool acts on: what resume needs in order to prove the worktree
// is still what the run left, and to continue that run rather than start a
// new one. The markdown is for a person; this is for the tool, so a person
// deleting the markdown does not make a handed-over rebase unrecoverable.
const StateName = "wt-sync-state.json"

// PlanPath is where the brief lives for a worktree's git dir.
func PlanPath(gitDir string) string { return filepath.Join(gitDir, PlanName) }

// StatePath is where the sidecar lives for a worktree's git dir.
func StatePath(gitDir string) string { return filepath.Join(gitDir, StateName) }

// LeftLock is the lock a run left behind when it handed a stop over, so the
// resume or undo that continues that run can take it over and nothing else
// can.
type LeftLock struct {
	PID     int   `json:"pid"`
	Started int64 `json:"started"`
}

// State is everything resume and undo need. The trunk SHA is the run's, not
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
	Deleted  []string          `json:"deleted"`
	Left     []string          `json:"left"`
	Lock     LeftLock          `json:"lock"`
}

// writeAtomic writes data to a temp file in the same directory and renames
// it into place, so a crash or a full disk cannot leave half a handover.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// WritePlanFile writes the human brief.
func WritePlanFile(gitDir, plan string) error {
	return writeAtomic(PlanPath(gitDir), []byte(plan))
}

// WriteState writes the sidecar. Write the brief first: this is the marker,
// and a marker present without its brief is worse than the reverse.
func WriteState(gitDir string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(StatePath(gitDir), append(data, '\n'))
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

// HasPlan reports whether a run left a handover in this git dir.
func HasPlan(gitDir string) (bool, error) {
	_, err := os.Stat(StatePath(gitDir))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// RemovePlan deletes both halves of a handover, and any temp file a crashed
// write left. A run that ends — completed, undone, or restored — has nothing
// left to hand over, and a stale marker would report the worktree as waiting
// on somebody forever.
func RemovePlan(gitDir string) error {
	for _, p := range []string{PlanPath(gitDir), StatePath(gitDir), PlanPath(gitDir) + ".tmp", StatePath(gitDir) + ".tmp"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// PlanHolders lists the worktrees holding a handover. The main checkout is
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

// PlanInput is everything the brief is rendered from.
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
		switch {
		case f.Strategy != "":
			// A file a declaration claims, whose strategy refused it: it is
			// a person's after all, and saying which strategy refused and
			// why is the whole reason they can trust that.
			note = fmt.Sprintf("%s refused it: %s", f.Strategy, f.Note)
		case f.Note == "unclaimed":
			if c, ok := conflicts[f.Path]; ok {
				note = shapeOf(in.MainRoot, c)
			}
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
		// A glob that matches a file this stop handed over is not listed:
		// telling a person both to resolve a file and never to touch it is
		// worse than saying nothing.
		asked := map[string]bool{}
		for _, f := range left {
			asked[f.Path] = true
		}
		claims := func(pattern string) bool {
			for p := range asked {
				if MatchGlob(pattern, p) {
					return true
				}
			}
			return false
		}
		var owned []string
		for _, r := range in.Config.Conflicts {
			for _, p := range r.Paths {
				if !claims(p) {
					owned = append(owned, fmt.Sprintf("%-40s ->  %s", p, r.Strategy))
				}
			}
		}
		for _, d := range in.Config.Defer {
			for _, p := range d.Paths {
				if !claims(p) {
					owned = append(owned, fmt.Sprintf("%-40s ->  the deferred `%s` owns it", p, d.Run))
				}
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

// blobDiff counts the lines added and removed between two blobs with git's
// own numstat over objects it already holds. The output is
// "<added>\t<removed>\t<oidA> => <oidB>" (verified 2026-09-09) and "-\t-\t…"
// for a blob git treats as binary, which is reported as not textual.
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
	if aerr != nil || derr != nil {
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

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/wtsync/ -run 'TestRenderPlan|TestState|TestNeedsYou' -v`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./internal/wtsync/
git add internal/wtsync/plan.go internal/wtsync/plan_test.go internal/wtsync/rebase.go
git commit -m "feat(sync): write the plan file and the state a resume needs"
git pull --rebase && git push origin main
```

---

### Task 7: The rebase leaves a contested stop in place, and resume re-enters the loop

**Files:**
- Modify: `internal/wtsync/rebase.go`
- Modify: `internal/wtsync/rebase_test.go`
- Modify: `internal/wtsync/lock.go`, `internal/wtsync/lock_test.go`

**Interfaces:**
- Consumes: `StagedConflicts`, `RebaseProgress`, `RebaseInProgress`, `Apply` (`stage.go`); `resolveConflict`; `WriteSafety`, `Safety`; `Handover`, `HasPlan` (Task 6); `runRepo`, `trunkReq`, `featureWorktree`, `gitIn`, `gitCmd` (existing test helpers).
- Produces:
  - `Result` gains `Left *Handover`; `StopResult` gains `Commit string`; `fail` now sets `Restored` when the restore succeeded.
  - `func Resume(mainRoot string, cfg *Config, req Request, old string, safety Safety, log io.Writer) (Result, error)`
  - `Request` gains `Stacked bool` — set by the caller when this branch has descendants in the same run, which forbids a handover.
  - `func (l *Lock) Keep()`; `func TakeOver(gitDir string, now time.Time, prev LeftLock) (*Lock, error)`
  - `Preflight` returns `Proceed, ""` for `Contested`, and refuses a `Paused` assessment.

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/rebase_test.go`. `runRepo` already declares `v.txt` owned-line and `w.txt` take-trunk with a worktree on `feature`, and `trunkReq(wt, epoch)` builds the request — use them.

```go
// A stop nothing claims is left in place, not aborted: the rebase is still
// in progress, the strategy's answer for the claimed file is staged, and the
// handover names what is left.
func TestRebaseLeavesAContestedStopInPlace(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
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
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want true", busy, err)
	}
	unmerged, err := StagedConflicts(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmerged) != 1 || unmerged[0].Path != "a.txt" {
		t.Fatalf("unmerged = %+v, want only a.txt", unmerged)
	}
}

// A branch with children in the same run may not be left mid-rebase: the
// children would be stranded on a base that no longer exists, which is the
// half-applied stack the spec forbids.
func TestRebaseRestoresAContestedStopOnAStackParent(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	req := trunkReq(wt, 1)
	req.Stacked = true
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left != nil || !res.Restored {
		t.Fatalf("res = %+v, want restored with no handover", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored to the old tip")
	}
}

// Resume drives the same loop: with the unclaimed file resolved by hand and
// staged, the rebase finishes and the branch moves.
func TestResumeFinishesTheRebase(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")

	out, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
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

// A resume never resets the worktree: a failure there would throw away a
// person's own resolution. An unstaged tracked change makes git refuse to
// continue; the loop must report that and leave everything alone.
func TestResumeNeverRestores(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	// Staged, then changed again in the working tree: git refuses.
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil); err == nil {
		t.Fatal("Resume = nil error, want the refusal git made")
	}
	if busy, _ := RebaseInProgress(wt); !busy {
		t.Fatal("the rebase was thrown away; a resume must never restore")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(got) != "unstaged\n" {
		t.Fatalf("a.txt = %q; the person's work was overwritten", got)
	}
}

// Someone ran git rebase --continue themselves and it finished: resume
// accepts that and reports the finished rebase rather than failing.
func TestResumeToleratesAFinishedRebase(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	gitIn(t, wt, "-c", "core.editor=true", "rebase", "--continue")

	out, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left != nil || out.NewTip == "" {
		t.Fatalf("Resume = %+v", out)
	}
}

// A rebase somebody aborted is not this run's result. Certifying it would
// let a later undo discard commits the run never made.
func TestResumeRefusesARebaseThatWasAborted(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	gitIn(t, wt, "rebase", "--abort")

	if _, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil); err == nil {
		t.Fatal("Resume accepted an aborted rebase as finished")
	}
}

func TestPreflightLetsContestedProceedAndRefusesPaused(t *testing.T) {
	if v, why := Preflight(Assessment{Class: Contested, Files: []FileOutcome{{Path: "a.txt"}}}); v != Proceed {
		t.Fatalf("contested = %v (%s), want Proceed", v, why)
	}
	if v, why := Preflight(Assessment{Class: Contested, Paused: true}); v != RefuseRun || !strings.Contains(why, "resume") {
		t.Fatalf("paused = %v (%s), want a refusal naming resume", v, why)
	}
}
```

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

Run: `go test ./internal/wtsync/ -run 'TestRebaseLeaves|TestRebaseRestoresA|TestResume|TestPreflightLets|TestTakeOver' -v`
Expected: FAIL — `undefined: Resume`, `res.Left undefined`, `undefined: TakeOver`.

- [ ] **Step 3: Extract the driver in `rebase.go`**

Add `-c rerere.autoupdate=false` to `rebaseConfig`, with the comment:

```go
	// Not passing --rerere-autoupdate does not disable autoupdate: a user
	// with rerere.autoupdate=true would still get cached resolutions staged,
	// which removes them from ls-files -u before any strategy sees them and
	// before a handover can record what it staged. The run plan believed the
	// flag's absence was enough; it is not.
	"-c", "rerere.autoupdate=false",
```

Give `Request` its new field, `StopResult` its `Commit`, and `Result` its `Left`:

```go
	// Stacked says this branch has descendants in the same run. A stop a
	// person owns is then restored rather than handed over: a parent left
	// mid-rebase strands every child on a base that is about to be
	// rewritten, which is the half-applied stack §4 forbids.
	Stacked bool
```
```go
	// Left is the stop the run handed over to a person: the rebase is still
	// in progress in the worktree, with every strategy's answer staged.
	// Nil when the rebase finished or was restored.
	Left *Handover
```

Then restructure. The loop body, the restore, and the trailing read-back move verbatim except where marked:

```go
// driver is one rebase in flight: Rebase starts one and drives it, Resume
// re-enters the same loop for one a previous run left stopped.
type driver struct {
	mainRoot string
	cfg      *Config
	req      Request
	log      io.Writer
	old      string
	// keep forbids the restore. A run's restore throws away only the tool's
	// own work; a resume's would throw away a person's, so a resume that
	// fails leaves the worktree exactly as it is and says so.
	keep bool
	res  Result
}

func (d *driver) git(args ...string) (string, error) {
	if d.log != nil && os.Getenv("WT_SYNC_TRACE") != "" {
		fmt.Fprintf(d.log, "  $ git %s\n", strings.Join(args, " "))
	}
	return gitEnv(d.req.Path, rebaseEnv, nil, args...)
}

// restore is the existing restore closure, moved verbatim, with git → d.git,
// res → d.res, old → d.old and req → d.req.
func (d *driver) restore() error { /* moved unchanged */ }

func (d *driver) fail(err error) (Result, error) {
	if d.keep {
		return d.res, fmt.Errorf("%w; the worktree is untouched, wt sync undo %s puts it back", err, d.req.Branch)
	}
	rerr := d.restore()
	if rerr == nil {
		d.res.Restored = true
	}
	return d.res, errors.Join(err, rerr)
}
```

`Rebase` becomes:

```go
// Rebase rebases one worktree, applying the declared strategies at each
// stop. A stop no strategy resolves is left in place with what the
// strategies did resolve already staged, and reported as a handover
// (Result.Left): the worktree stays mid-rebase for a person, and
// `wt sync resume` or `wt sync undo` is what moves next. A stack parent is
// the exception: its stop is restored, because leaving it would strand its
// children. An error is a git failure, after a restore to the safety ref,
// or a restore that could not be verified.
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
// verified the worktree against the run's sidecar. A rebase that is no
// longer in progress is checked before it is believed finished: HEAD on the
// branch, the run's onto an ancestor of it, and the tip moved. An aborted or
// reset rebase is not this run's result, and certifying it would let a later
// undo discard commits the run never made.
func Resume(mainRoot string, cfg *Config, req Request, old string, safety Safety, log io.Writer) (Result, error) {
	d := &driver{mainRoot: mainRoot, cfg: cfg, req: req, log: log, old: old, keep: true,
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
		if err := d.verifyFinished(); err != nil {
			return d.res, err
		}
		return d.finish()
	}
	_, cerr := d.git("rebase", "--continue")
	return d.drive(cerr)
}

// verifyFinished proves a rebase nobody is in the middle of actually
// completed, rather than having been aborted, quit or reset.
func (d *driver) verifyFinished() error {
	ref, err := d.git("symbolic-ref", "--quiet", "HEAD")
	if err != nil || ref != "refs/heads/"+d.req.Branch {
		return fmt.Errorf("HEAD is %q, not %s: this is not the rebase that was left here", ref, d.req.Branch)
	}
	head, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head == d.old {
		return fmt.Errorf("%s is back at the tip the run started from: the rebase was aborted, not finished; run wt sync run again", d.req.Branch)
	}
	if _, code, err := gitEnvAllow(d.req.Path, rebaseEnv, nil, 1, "merge-base", "--is-ancestor", d.req.Onto, "HEAD"); err != nil {
		return err
	} else if code == 1 {
		return fmt.Errorf("%s is not on top of what the run was rebasing onto: the rebase did not finish as this run", d.req.Branch)
	}
	return nil
}

// drive runs the stop-resolve-continue loop until the rebase finishes, fails
// or reaches a stop a person owns. err is what the command that got the
// rebase moving returned: nil means it is already finished.
func (d *driver) drive(err error) (Result, error) {
	lastIndex, lastUnmerged := -1, ""
	stops, limit := 0, 0
	for err != nil {
		/* the existing loop body, verbatim, with these changes:

		   - stop carries its commit:
		       stop := StopResult{Index: p.Index, Total: p.Total, Commit: p.Commit, Subject: p.Subject}
		   - the unresolved branch hands over, unless this is a stack parent:
		       if unresolved {
		           if d.req.Stacked {
		               d.res.Restored = true
		               return d.res, d.restore()
		           }
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

// handover records the stop the run is leaving: what a person owns, and what
// each strategy staged — a blob id, or a deletion, which is a resolution a
// script is allowed to make (ResolveInWorktree accepts any outcome that
// leaves nothing unmerged). Resume compares against this to prove nothing
// was hand-merged where a strategy owns the file.
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
		out, err := d.git("ls-files", "--stage", "-z", "--", f.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: reading what %s staged: %w", f.Path, f.Strategy, err)
		}
		rec, _, _ := strings.Cut(out, "\x00")
		if rec == "" {
			h.Deleted = append(h.Deleted, f.Path)
			continue
		}
		meta, _, _ := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s: cannot read its index entry (%q)", f.Path, rec)
		}
		h.Staged[f.Path] = fields[1]
	}
	return h, nil
}
```

In `Preflight`, replace the `Contested` case and add the `Paused` refusal at the top of the first switch (before the class is looked at, with the other untouchable conditions):

```go
	case a.Paused:
		return RefuseRun, "left mid-rebase by an earlier run: wt sync resume, or wt sync undo"
```
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
// does not start where somebody has to finish first. It is not a forever
// lock — LockExpiry still frees it — and the sidecar, not this, is the
// durable marker that a run is waiting.
func (l *Lock) Keep() {
	held.Lock()
	delete(held.locks, l.Path)
	held.Unlock()
}

// TakeOver acquires the lock, displacing the one a run left behind when it
// handed a stop over. It tries an ordinary Acquire first and only displaces
// a lock whose pid and start time are exactly what the run recorded in its
// sidecar, so a live run, or any lock that is not this handover's, is
// respected the way Acquire respects it.
//
// The window Acquire's expiry path already has is not closed here: between
// reading the lock and renaming it aside, another process continuing the
// same handover could acquire, and would then be displaced. Two concurrent
// resumes of one worktree is a user error, and both would be driving the
// same rebase; nothing else can reach this path, because nothing else knows
// the recorded pid.
func TakeOver(gitDir string, now time.Time, prev LeftLock) (*Lock, error) {
	l, err := Acquire(gitDir, now)
	if err == nil {
		return l, nil
	}
	var busy *LockHeld
	if !errors.As(err, &busy) || prev.PID == 0 || busy.PID != prev.PID || busy.Started.Unix() != prev.Started {
		return nil, err
	}
	stale := fmt.Sprintf("%s.stale.%d", busy.Path, os.Getpid())
	if rerr := os.Rename(busy.Path, stale); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return nil, rerr
	}
	_ = os.Remove(stale)
	return Acquire(gitDir, now)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/wtsync/`
Expected: PASS. The existing test that asserted `Restored` on an unclaimed stop now asserts `Left != nil` — rewrite it, and keep separate coverage that a genuine *failure* (a strategy that errors, not one that refuses) still restores and now sets `Restored`.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/rebase.go internal/wtsync/rebase_test.go internal/wtsync/lock.go internal/wtsync/lock_test.go
git commit -m "feat(sync): hand a contested stop over instead of aborting"
git pull --rebase && git push origin main
```

---

### Task 8: `wt sync run` writes the handover and prints the "needs you" line

**Files:**
- Create: `internal/commands/sync_finish.go`
- Modify: `internal/commands/sync_run.go`
- Modify: `internal/commands/sync_run_test.go`

Task 7 and Task 8 could not be split: Task 7 alone makes `Rebase` return `Left` and lets `Preflight` pass a contested worktree, while `SyncRun` still runs its completion tail — it would print "rebased 0 commits", run the deferred steps with an empty `NewTip`, and leave a rebase in place with no handover written. If they are implemented as two commits, the second must follow immediately and `make check` is only expected to pass after it.

**Interfaces:**
- Consumes: `wtsync.Result.Left`, `wtsync.Handover`, `Request.Stacked`, `wtsync.Lock.Keep` (Task 7); `wtsync.RenderPlan`, `WritePlanFile`, `WriteState`, `State`, `RemovePlan`, `HasPlan`, `NeedsYouLine`, `LandingList` (Tasks 5–6); `wtsync.Descendants` (`stack.go`).
- Produces: `handOver(ctx, w, handoverInput) error` and `completeRun(ctx, w, cfg, completeInput) (head string, owed []string, err error)`, both used by Task 9.

- [ ] **Step 1: Write the failing test**

Add to `internal/commands/sync_run_test.go`, using the fixture the file already uses:

```go
// A contested stop is no longer refused: the run rebases up to it, stages
// what the strategies resolved, leaves the rebase in place and writes the
// handover.
func TestSyncRunHandsAContestedStopOver(t *testing.T) {
	// …the file's own fixture, extended so the branch conflicts on the
	// claimed file and on one nothing claims…
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, RunOptions{NoFetch: true, Yes: true, Agents: []wtsync.Agent{}}, &out)
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
	if st.Epoch == 0 || st.Safety == "" || len(st.Resolved) == 0 || st.Lock.PID == 0 {
		t.Fatalf("state = %+v", st)
	}
	if _, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; want the lock kept", ok, err)
	}
	if busy, err := wtsync.RebaseInProgress(wtPath); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left in place", busy, err)
	}
	// No result ref: the run did not finish for this branch.
	if _, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, branch, st.Epoch); err != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a handed-over run pins no result", ok, err)
	}
}

// A second run refuses a worktree waiting on a person, and says what to do.
func TestSyncRunRefusesAWorktreeWaitingOnAPerson(t *testing.T) {
	// After the handover above, call SyncRun again on the same work.
	// Assert the output names "wt sync resume" and the rebase is untouched.
}
```

Write both in full against the fixture.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/commands/ -run TestSyncRun -v`
Expected: FAIL — no plan file is written; the run reports "rebased 0 commits".

- [ ] **Step 3: Write `sync_finish.go`**

```go
package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// handoverInput is what writing a handover needs beyond the rebase's own
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

// handOver leaves the worktree mid-rebase for a person: the §6 brief, the
// sidecar resume and undo verify against, the lock left behind, and the §5
// line. The brief is written first and the sidecar second, both atomically:
// the sidecar is the marker, so a crash between them leaves a brief nothing
// acts on rather than a marker with no brief. The caller must not release
// the lock afterwards — Keep has already taken it off this process's books.
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
	if err := wtsync.WritePlanFile(gitDir, plan); err != nil {
		return err
	}
	st := wtsync.State{
		Branch: in.Branch, Work: in.Work, Trunk: in.TrunkSHA, TrunkRef: in.TrunkRef,
		Onto: in.Onto, Upstream: in.Upstream, Epoch: in.Epoch,
		Safety: in.Res.Safety.Ref, OldTip: in.Res.OldTip,
		Stop: in.Res.Left.Index, Total: in.Res.Left.Total,
		Resolved: in.Res.Left.Staged, Strategy: map[string]string{},
		Deleted:  in.Res.Left.Deleted, Left: in.Res.Left.Left,
	}
	for _, f := range in.Res.Left.Files {
		if f.Resolved {
			st.Strategy[f.Path] = f.Strategy
		}
	}
	if in.Lock != nil {
		st.Lock = wtsync.LeftLock{PID: in.Lock.PID, Started: in.Lock.Started.Unix()}
	}
	if err := wtsync.WriteState(gitDir, st); err != nil {
		return err
	}
	// Only now: until the sidecar exists nothing can take this lock over.
	if in.Lock != nil {
		in.Lock.Keep()
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
// the handover removed, and the push line. owed names the deferred steps
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
	if err := wtsync.RemovePlan(gitDir); err != nil {
		return head, owed, err
	}
	fmt.Fprintf(w, "  push: git -C %s push --force-with-lease\n", in.Path)
	return head, owed, nil
}

// clearHandover removes a handover after a rebase was put back rather than
// finished. A marker left behind would report the worktree as waiting on
// somebody who has nothing to do.
func clearHandover(w io.Writer, path string) {
	gitDir, err := wtsync.GitDir(path)
	if err == nil {
		err = wtsync.RemovePlan(gitDir)
	}
	if err != nil {
		fmt.Fprintf(w, "  note: could not remove the plan file: %v\n", err)
	}
}

var _ = strings.TrimSpace
```

Delete that trailing `var _` line once the file's imports are settled; it is there only so an unused import does not distract from the review.

- [ ] **Step 4: Rework `sync_run.go`**

Mark stack parents before the rebase, so `Rebase` knows it must not hand over:

```go
		req := wtsync.Request{Path: p.wt.Path, Branch: b, Trunk: trunkSHA, Onto: trunkSHA, Epoch: epoch}
		req.Stacked = len(wtsync.Descendants(parents, b)) > 0
```

Say what is coming, before the call:

```go
		if p.a.Class == wtsync.Contested && p.a.Replay.Stop != nil {
			what := "the run stops there and writes a plan"
			if req.Stacked {
				what = "the run stops there and puts the branch back: a stack parent cannot be left waiting"
			}
			fmt.Fprintf(w, "  contested at %d/%d: %s\n", p.a.Replay.Stop.Index, p.a.Replay.Stop.Total, what)
		}
```

Add the handover branch before the `res.Restored` branch:

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

In the `res.Restored` branch, clear any handover a previous run left and the restore has now made meaningless: `clearHandover(w, p.wt.Path)`. Do the same in the `rerr != nil` branch.

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

Name resume in the mid-rebase poison, so a second run says what to do:

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
Expected: PASS. The existing test asserting that `run` refuses a contested worktree now asserts the handover — rewrite it rather than deleting it.

- [ ] **Step 6: Lint and commit**

```bash
gofmt -l internal && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/commands/sync_finish.go internal/commands/sync_run.go internal/commands/sync_run_test.go
git commit -m "feat(sync): run leaves a contested stop with a plan file"
git pull --rebase && git push origin main
```

---

### Task 9: `wt sync resume <work>`

**Files:**
- Create: `internal/commands/sync_resume.go`
- Create: `internal/commands/sync_resume_test.go`
- Modify: `cmd/wt/sync.go` (the `resume` subcommand)

**Interfaces:**
- Consumes: `handOver`, `completeRun` (Task 8); `wtsync.Resume`, `wtsync.TakeOver` (Task 7); `wtsync.ReadState`, `State`, `HasPlan`, `SafetyRef` (Task 6); `Locate`, `workName`, `watchSignals`, `rebaseTracker`, `rebaseInFlight`, `plural`, `short`.
- Produces: `type ResumeOptions struct { Agents []wtsync.Agent; Now func() time.Time }`, `func SyncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) error`, `func verifyHandover(wtPath string, st wtsync.State, w io.Writer) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/commands/sync_resume_test.go` with five tests, each written in full against the fixture `sync_run_test.go` uses:

1. `TestSyncResumeFinishesAHandedOverRebase` — run until the handover, resolve the unclaimed file by hand and `git add` it, resume. Assert: no error; the sidecar and the plan file are gone; the branch tip moved; `wtsync.ResultTip` exists for the run's epoch; the output contains `push:`; `RebaseInProgress` is false.
2. `TestSyncResumeRefusesAHandMergedOwnedFile` — after the handover, overwrite the strategy-resolved file and `git add` it, then resume. Assert: the error names the file and the strategy; `RebaseInProgress` is still true; the sidecar is still there.
3. `TestSyncResumeRefusesWhileSomethingIsUnmerged(t *testing.T)` — resume without staging anything. Assert: the error names the unmerged path and says to `git add` it.
4. `TestSyncResumeRefusesUnstagedTrackedChanges` — stage the resolution, then edit the same file again without staging. Assert: the error says the worktree has unstaged changes; the rebase is still in progress; the file still holds the person's later edit (this is the case that would otherwise reach the loop's non-advance guard and reset the worktree).
5. `TestSyncResumeWithoutAHandover` — a worktree no run touched. Assert the error says it was not left mid-rebase by `wt sync run`.

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
// verifies the worktree is still what the run left, then drives the same
// loop to the end: the strategies at any later stop, the deferred steps, the
// result ref, the push line. A later stop a person owns is handed over
// again, with a fresh plan file.
//
// A rebase somebody finished themselves with `git rebase --continue` is
// tolerated: there is nothing to continue, so only what follows the rebase
// runs — after checking that it really finished rather than being aborted.
// Nothing here ever resets the worktree: a person's own resolution is in it.
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
		return fmt.Errorf("the handover in %s is for %s, not %s; nothing is resumed", gitDir, st.Branch, target.Branch)
	}
	// The sidecar must describe the run it claims to: a safety ref built
	// from a different branch or epoch is somebody else's, and pinning the
	// wrong old tip would make undo restore to the wrong commit.
	if want := wtsync.SafetyRef(st.Branch, st.Epoch); st.Safety != want {
		return fmt.Errorf("the handover names %s, not %s; nothing is resumed", st.Safety, want)
	}
	tip, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", st.Safety)
	if err != nil {
		return fmt.Errorf("the safety ref %s is gone; nothing is resumed", st.Safety)
	}
	if tip != st.OldTip {
		return fmt.Errorf("%s pins %s but the handover says %s; nothing is resumed", st.Safety, short(tip), short(st.OldTip))
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

// verifyHandover refuses to continue a rebase that is not what the run left.
// Three things are checked, and none of them is something the tool may
// repair by re-running a strategy: that would overwrite a person's work.
//
//   - Something still unmerged means the person is not done.
//   - A tracked file changed but not staged makes git refuse to continue,
//     and the loop's did-not-advance guard would read that refusal as a
//     stuck rebase. In a run that means a restore; here it would mean
//     throwing away the resolution. Refusing early is the only safe answer.
//   - A different blob under a strategy's name means a file the brief said
//     never to hand-merge was hand-merged.
//
// The blob comparison only holds while the rebase is still at the stop the
// handover recorded. If somebody continued by hand to a later stop, those
// paths belong to a stop that is now history, so the comparison is skipped
// and said out loud rather than turned into a false refusal.
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
		return fmt.Errorf("still unmerged: %s; resolve them, git add them, then resume", strings.Join(ps, ", "))
	}
	// --untracked-files=no: an untracked file never blocks a rebase, and a
	// script may have left one.
	dirty, err := git.Run(wtPath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	var unstaged []string
	for _, line := range strings.Split(dirty, "\n") {
		// The second status column is the worktree against the index: any
		// mark there is a change git will refuse to continue over.
		if len(line) > 3 && line[1] != ' ' {
			unstaged = append(unstaged, strings.TrimSpace(line[3:]))
		}
	}
	if len(unstaged) > 0 {
		return fmt.Errorf("changed but not staged: %s; git add them (git refuses to continue otherwise), then resume", strings.Join(unstaged, ", "))
	}
	p, err := wtsync.RebaseProgress(wtPath)
	if err != nil {
		return err
	}
	if p.Index != st.Stop {
		fmt.Fprintf(w, "  note: the rebase is at %d/%d, not the %d/%d the plan describes; what the strategies staged there is already committed and is not re-checked\n",
			p.Index, p.Total, st.Stop, st.Total)
		return nil
	}
	var changed []string
	for path := range st.Resolved {
		cur, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path)
		if err != nil {
			return fmt.Errorf("%s is no longer staged and %s owns it; wt sync undo %s and start again", path, st.Strategy[path], st.Work)
		}
		if cur != st.Resolved[path] {
			changed = append(changed, path)
		}
	}
	for _, path := range st.Deleted {
		if _, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path); err == nil {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	for _, p := range changed {
		fmt.Fprintf(w, "  %s was hand-merged; %s owns it\n", p, st.Strategy[p])
		if oid := st.Resolved[p]; oid != "" {
			fmt.Fprintf(w, "    put it back: git -C %s show %s > %s && git -C %s add -- %s\n",
				wtPath, oid, filepath.Join(wtPath, p), wtPath, p)
		} else {
			fmt.Fprintf(w, "    put it back: git -C %s rm --cached -- %s && rm %s\n", wtPath, p, filepath.Join(wtPath, p))
		}
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
		Long: "Pick up the rebase wt sync run left in this worktree. The plan file in\n" +
			".git/worktrees/<name>/wt-sync-plan.md says what landed, what the declared\n" +
			"strategies already resolved and must not be re-opened, and what is yours.\n" +
			"Resolve those, git add them, then run this.\n\n" +
			"Before continuing it checks that nothing is unmerged, that nothing tracked\n" +
			"is changed but unstaged, and that no file a strategy resolved was\n" +
			"hand-merged. Any of those is a refusal that changes nothing: this command\n" +
			"never resets the worktree, because your own work is in it. Then it drives\n" +
			"the rest of the rebase, runs the deferred steps, pins the result ref and\n" +
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

### Task 10: `undo` aborts a handover; `doctor` and the table report one

**Files:**
- Modify: `internal/wtsync/undo.go`, `internal/wtsync/undo_test.go`
- Modify: `internal/wtsync/triage.go` (`Assess`: `Paused`)
- Modify: `internal/commands/sync_doctor.go`, `internal/commands/sync_doctor_test.go`
- Modify: `internal/commands/sync_undo.go`

**Interfaces:**
- Consumes: `wtsync.HasPlan`, `RemovePlan`, `ReadState`, `PlanHolders`, `TakeOver` (Tasks 6–7); `repo.Worktree.Rebasing` (Task 3).
- Produces: `Restored` gains `Aborted bool`; `Assessment.Paused` is set (declared in Task 4).

Two things must not be got wrong here, both from review:

- **Nothing is mutated until every refusal check has passed.** The abort of a handed-over rebase is a mutation. It moves to its own pass *after* the later-run and moved-since checks, not before them; otherwise a sibling failing a later check leaves an already-discarded resolution behind, and the "nothing undone" guarantee is a lie.
- **Undo takes the lock over the same way resume does.** A handover leaves its lock behind, so a plain `Acquire` refuses for up to `LockExpiry` — which would make the `wt sync undo <work>` the plan file recommends fail for half an hour.

- [ ] **Step 1: Write the failing tests**

Add to `internal/wtsync/undo_test.go`, in full:

1. `TestUndoAbortsAHandedOverRebase` — build the handover with `Rebase` as `TestRebaseLeavesAContestedStopInPlace` does, write the sidecar the way `handOver` would (or call it through the command in `sync_undo_test.go` instead — pick one and be consistent), then `Undo`. Assert: no rebase in progress, the branch at the safety tip, `HasPlan` false, and the `Restored` row has `Aborted` set.
2. `TestUndoStillRefusesAForeignRebase` — start a conflicting rebase by hand with no handover, then `Undo`. Assert an error saying it is mid-rebase, and the rebase still in progress.
3. `TestUndoChecksEverythingBeforeAborting` — two branches in one run's epoch, one handed over and one that has moved on since (so it fails the moved-since check). Assert `Undo` returns the moved-since error **and** the handed-over rebase is still in progress: nothing was undone.
4. `TestUndoTakesOverTheLockAHandoverLeft` — with the handover's lock file still present and its pid recorded in the sidecar, `Undo` succeeds rather than reporting the worktree as locked.

Add to `internal/commands/sync_doctor_test.go`: `TestSyncDoctorReportsAWorktreeWaitingOnAPerson` — write a sidecar into a worktree's git dir, run `SyncDoctor`, assert a `plan` row naming the work and `wt sync resume`.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ -run 'TestUndo|TestSyncDoctorReportsAWork' -v`
Expected: FAIL.

- [ ] **Step 3: `undo.go`**

Add `Aborted bool` to `Restored`. In the check loop, take the lock the way a handover needs and treat a wt rebase as a thing to abort rather than a refusal:

```go
	var aborting []repo.Worktree
	…
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		// A handover left its lock behind; undo is one of the two commands
		// entitled to take that exact lock over.
		var prev LeftLock
		if st, ok, serr := ReadState(gitDir); serr == nil && ok {
			prev = st.Lock
		}
		lock, err := TakeOver(gitDir, now, prev)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		locks = append(locks, lock)
		…agent check, unchanged…
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
			// A rebase this tool left: aborting it is what undo is for, and
			// its staged conflicts are not dirt. The abort happens later,
			// once every branch has passed every check.
			aborting = append(aborting, wt)
			continue
		}
		out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		…unchanged…
```

Leave the tips/pins loop exactly where it is — it is all checks and one `WriteRun`, and for a handed-over branch the ref never moved, so `tips[branch] == s.Tip` and it passes without special handling. Then, **after** `WriteRun` and **before** the apply loop:

```go
	// Only now, with every branch of the run checked and the forced-undo
	// pins written: aborting is a mutation, and doing it earlier would let a
	// refusal raised by a later branch leave an already-discarded
	// resolution behind.
	aborted := map[string]bool{}
	for _, wt := range aborting {
		if _, err := gitEnv(wt.Path, rebaseEnv, nil, "rebase", "--abort"); err != nil {
			return nil, fmt.Errorf("%s: rebase --abort: %w", wt.Branch, err)
		}
		if busy, err := RebaseInProgress(wt.Path); err != nil {
			return nil, err
		} else if busy {
			return nil, fmt.Errorf("%s is still mid-rebase after the abort", wt.Branch)
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
	// A branch that was handed over may still have a stale handover even
	// when no rebase is in progress: somebody finished or aborted it by
	// hand. Undoing the run is the end of that handover either way.
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok || aborted[s.Branch] {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		if err := RemovePlan(gitDir); err != nil {
			return nil, err
		}
	}
```

and in the apply loop, `r := Restored{…, Aborted: aborted[s.Branch]}`.

In `internal/commands/sync_undo.go`, print it, before the existing `r.From == r.To` branch:

```go
		if r.Aborted {
			fmt.Fprintf(w, "%s  aborted the rebase; back at %s\n", name, short(r.To))
			continue
		}
```

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
		// a state nobody is in; the plan file has the detail.
		a.Class = Contested
		return a
	}
```

This only works because Task 3 stopped `git worktree list`'s `detached` from short-circuiting `Assess` for a worktree mid-rebase. Without it the `Detached` return above fires first and `Paused` is never reached.

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

`plan` is advisory: not in the blocking list, no `Fix`.

- [ ] **Step 6: The `rebases` row points at the right command**

`rebasesCheck` in `internal/wtsync/doctor.go` tells a person to `git rebase --abort` a worktree stuck mid-rebase. For one carrying a handover that would leave the sidecar behind and the branch reported as waiting forever. Have it skip the worktrees `PlanHolders` covers — the `plan` row names those, with `resume` — and keep its advice for the rest:

```go
func rebasesCheck(worktrees []repo.Worktree) (Check, error) {
	holders := map[string]bool{}
	held, err := PlanHolders(worktrees)
	if err != nil {
		return Check{}, err
	}
	for _, wt := range held {
		holders[wt.Path] = true
	}
	…the existing loop, with `if holders[wt.Path] { continue }` after the IsMain skip…
}
```

- [ ] **Step 7: Run the tests**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 8: Lint and commit**

```bash
gofmt -l internal cmd && go vet ./... && golangci-lint run ./... && go test -race ./...
git add internal/wtsync/undo.go internal/wtsync/undo_test.go internal/wtsync/triage.go internal/wtsync/doctor.go internal/commands/sync_doctor.go internal/commands/sync_doctor_test.go internal/commands/sync_undo.go
git commit -m "feat(sync): undo aborts a handover; doctor reports one"
git pull --rebase && git push origin main
```

---

### Task 11: Help, README, the bats smoke test, and the handoff

**Files:**
- Modify: `cmd/wt/sync.go` (the bare `sync` long help, `run`'s, `undo`'s)
- Modify: `README.md`
- Modify: `test/sync_run.bats`
- Modify: `/Users/anderslindstrom/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md`

- [ ] **Step 1: The bare `wt sync` help**

Four things in it are now false. Replace, exactly:

- `"  recipe     every conflict is claimed by a strategy in .wt-sync.yaml;\n" + "             a run would complete on its own\n"` →
  `"  recipe     every conflict at every stop is claimed by a strategy in\n" + "             .wt-sync.yaml; a run completes on its own. recipe? means a\n" + "             script owns a path and the replay could not be carried past\n" + "             it: a run may still stop later, and hands you a plan if it does\n"`
- `"  contested  some conflict is nobody's; a run would stop there and\n" + "             hand you the files marked ✗\n"` →
  `"  contested  a conflict somewhere in the replay is nobody's; a run rebases\n" + "             up to it, stages what the strategies did resolve, and leaves a\n" + "             plan file — finish it and wt sync resume <work>\n"`
- The flow block gains resume: `"  finish  wt sync resume <work>   continue after you resolved what was yours\n"` before the push line.
- Delete `"A contested worktree is refused by run until resume exists: rebase it by hand.\n"`.
- `"STOP is the first commit a rebase would stop at and the files in\nconflict there."` → `"STOP is the stop that decides the class — the first one that is yours, or\nthe first of a run that resolves throughout — and the files in conflict\nthere."`

- [ ] **Step 2: `run`'s and `undo`'s help**

In `run`'s Long, replace `"A stop no strategy resolves aborts the rebase and restores the worktree;\n"` with a sentence saying such a stop is left in place with a plan file and `wt sync resume`, except on a stack parent, which is restored. Replace `"class\ncontested (rebase those by hand; resume is not built yet), and any\n"` — contested is no longer refused.

In `undo`'s Long, replace `"a checkout involved is dirty, mid-rebase,\n"` — a rebase this tool left is now aborted and restored; only a rebase somebody else started is refused.

- [ ] **Step 3: README**

Add `wt sync resume <work>` to the surfaces table between `run` and `undo` — "continue the rebase a run left at a conflict that was yours" — and correct the `recipe` description to "every conflict at every stop is claimed by a strategy".

- [ ] **Step 4: The bats smoke test**

Add to `test/sync_run.bats`, following the fixture the file already builds:

```bash
@test "sync run hands a contested stop over and resume finishes it" {
  run wt sync run w --no-fetch --yes
  [ "$status" -ne 0 ]
  [[ "$output" == *"needs you"* ]]
  GITDIR="$(git -C "$WT" rev-parse --absolute-git-dir)"
  [ -f "$GITDIR/wt-sync-plan.md" ]
  [ -f "$GITDIR/wt-sync-state.json" ]

  echo resolved > "$WT/a.txt"
  git -C "$WT" add a.txt
  run wt sync resume w
  [ "$status" -eq 0 ]
  [ ! -f "$GITDIR/wt-sync-state.json" ]
  [ ! -f "$GITDIR/wt-sync-plan.md" ]
}
```

- [ ] **Step 5: Full check and install**

```bash
make check
./install.sh
```

- [ ] **Step 6: The handoff**

Append `## What landed for resume (2026-09-09)` to the handoff: the commit list, that `recipe` now means every stop and `recipe?` means unverified, the handover contract, `wt sync resume`, what `undo` does with a handover, the `plan` doctor row, and the residuals.

- [ ] **Step 7: Commit**

```bash
git add cmd/wt/sync.go README.md test/sync_run.bats
git commit -m "docs(sync): state the handover and resume in the help"
git pull --rebase && git push origin main
```

---

## Self-review

**Codex review, 2026-09-09 (read-only, via `/codex:rescue`), what changed.** Twenty-six findings; the ones that changed the plan:

- **A worktree stopped mid-rebase reports `detached` with no branch** (verified here against git 2.55). `Locate`, `Assess`, `Parents` and `Undo.byBranch` all key on the branch, so without a fix nothing about Part 2 was reachable — the handed-over worktree could not be named, resumed or undone. Task 3 is new and reads the sequencer's own `head-name`.
- **`git rebase --continue` refuses over an unstaged tracked change** (verified), and the loop's did-not-advance guard would have read that refusal as a stuck rebase and *restored* — deleting a person's own resolution. Two changes: a resume never restores (`driver.keep`), and `verifyHandover` refuses early on unstaged changes.
- **Absence of a rebase in progress was treated as proof of success.** An abort, a `--quit` or a reset would have been certified as the run's result, letting a later undo discard commits the run never made. `verifyFinished` now checks HEAD is on the branch, is not still the old tip, and has the run's `onto` as an ancestor.
- **Undo aborted before finishing its refusal checks.** The abort pass moved after every check and after `WriteRun`; undo also takes over the lock a handover leaves, which it otherwise could not acquire for 30 minutes.
- **Omitting `--rerere-autoupdate` does not disable autoupdate.** `-c rerere.autoupdate=false` added to `rebaseConfig`; this corrects a claim inherited from the run plan.
- **A script may resolve by deleting its path**, which `rev-parse :0:<path>` cannot record. `Handover.Deleted` added.
- **`Replay.Stops` would have retained three blobs per conflict per stop** — hundreds of megabytes for one assessment of a branch that stops often on a large file. Blobs are kept only for the deciding stop.
- **The plan file told a person both to resolve a refused owned file and never to touch it.** "never hand-merge here" now omits any glob matching a path this stop handed over, and the "yours" entry says which strategy refused it and why.
- **The handover was neither atomic nor ordered.** Both halves are written temp-then-rename, brief first, sidecar second, and the sidecar is the marker `HasPlan` reads.
- **`recipe` on a truncated replay was the same false promise Part 1 removes.** It now prints `recipe?`.
- **Task boundaries left the tree broken** between the replay change and the classification, and between the handover contract and its caller. Tasks merged (old 2+3 → 4) and the 7/8 dependency stated.
- **Fixture claims were wrong**: `gitIn` already returns trimmed output and lives in `config_test.go`; `runRepo`, `trunkReq` and `featureWorktree` are the real helpers; `gitOutIn`, `writeFileIn`, `gitInEnv` and `rebaseFixture` never existed. Every test in this plan now uses the real ones.
- **The chaining test did not test chaining** and its arithmetic was wrong (`max-plus-patch(1.1.0, 2.0.0)` is 2.0.1, not 1.1.1). Rewritten.
- **`mergeTree` and `catFileRaw` had no deadline**, which the expanded replay would have made much more visible. Task 2 is new.
- **The printed repair command redirected into the caller's directory** (`-C` does not move a shell redirect). It now names the absolute path.
- **Octopus merges**: `sha^1..sha^2` misses parents 3+; `LandingList` loops over every parent after the first.

Pushed back on: **the stack finding**. Codex is right that a handed-over parent strands its children and that `Parents` cannot rediscover the relation afterwards. Carrying the whole run's stack through the sidecar and having resume drive the remaining members is the full answer, and it is a plan of its own. This plan takes the conservative route instead — a contested stop on a branch with descendants in the run is **restored**, exactly as today, and the stack is reported. That is spec-conformant ("never half-apply a stack") and is not a regression. It is the first residual.

**Verified against git 2.55 on 2026-09-09, so no task rests on a guess:** a conflicted `merge-tree --write-tree` writes a usable tree with the conflicted paths at their own names; `read-tree`/`ls-files --stage -z`/`update-index -z --index-info`/`write-tree` round-trip against `GIT_INDEX_FILE`; `rev-parse --verify :0:<path>` answers mid-rebase and fails cleanly for an unmerged path; `diff --numstat <blobA> <blobB>` exits 0 and prints `added\tremoved\t<oidA> => <oidB>`, and `-\t-` for binary; `log --first-parent --format='%H %P%x00%s'` and `log --format=%s <sha>^1..<sha>^2` are as used; `worktree list --porcelain` says `detached` for a stopped rebase while `rebase-merge/head-name` holds the branch; `rebase --continue` refuses over an unstaged tracked change and stays in progress.

**Spec coverage:**

- **§1, the simulation and "triage is a promise":** Tasks 1, 2, 4. `clean` stays exact, `recipe` means the strategies carry the whole replay, `contested` carries the first stop a person owns wherever it is, and the endpoint `divergent` check is untouched. Two divergences the spec's "where the two can differ" list does not yet name are now stated in the code: a script-claimed path cannot be carried past (`recipe?`), and `resolvedTree` hashes bytes where a real rebase would run the repository's clean filters. Add both to spec §1 when the plan lands.
- **§5, the after-the-fact line:** `NeedsYouLine` (Task 6), printed by `handOver` (Task 8). The ask protocol, `ack`/`nak`/`wait` and the queue stay out of scope.
- **§6, the plan file:** Task 6 renders every section the spec names. The `rr-cache` line of §6's example is **not** produced: rerere autoupdate is now explicitly off, so the tool never learns that a resolution came from the cache. Cost: a person is not told a conflict was seen before on another branch.
- **§7, `resume`:** Task 9, plus the doctor row and the table note in Task 10.

**Rulings, and what each costs if wrong:**

1. **A script-claimed path truncates the replay; the class prints `recipe?`.** A `--check` pass proves ownership but yields no bytes, and inventing bytes would be the tool guessing. Cost: a `recipe?` worktree can still stop later — which, after Part 2, means a plan file rather than an abort. Neither live repository declares a `script` today.
2. **A contested stop on a stack parent is restored, not handed over.** Cost: the case where a plan file helps most is the case a stack does not get one. Residual, below.
3. **The lock is left behind on a handover and expires normally; `TakeOver` displaces only the exact lock the sidecar records.** Cost: two concurrent resumes of one worktree can both proceed — a pre-existing property of `Acquire`'s expiry path, and both would be driving the same rebase.
4. **Resume verifies staged blobs only while the rebase is at the stop the handover recorded.** Cost: a hand-merge made after somebody continued by hand to a later stop is not caught; the note says so out loud, and the deferred step is still the check that follows.
5. **A resume keeps the run's epoch and the run's trunk SHA.** It is the same run: one safety ref, one result ref, one declaration. Cost: a resume days later replays later commits against a declaration older than trunk's — the conservative direction, since the rebase in progress already targets that trunk.
6. **`undo` aborts a mid-rebase worktree only when it carries a handover.** Cost: a rebase started by hand in a worktree that also has a stale sidecar would be aborted. `RemovePlan` on every terminal path — completion, restore, undo — is what keeps a sidecar from going stale.
7. **A handed-over worktree is reported `contested` by the read-only table with a note, without re-simulating.** Cost: the STOP column is empty for such a row; the plan file has the detail.
8. **A worktree stopped mid-rebase reports its branch and `Detached` becomes false.** Cost: `wt list` and anything else keying on `Detached` now sees a branch where it saw none — which is the honest answer, and `Rebasing` is there for anything that needs the distinction.

**Residuals for the next plan:**

- A stack whose parent hits a contested stop is restored rather than handed over. Carrying the run's ordered members and their pre-run tips through the sidecar, and having resume drive the rest, is the fix.
- Worktree-less dependent refs inside a rewritten range are still not advanced (inherited from the run plan).
- Only Claude sessions are detected; a Codex session in a worktree is invisible to `run`, `resume` and `undo`.
- `resolvedTree` does not model clean filters or end-of-line normalisation.

**Placeholder scan:** the tests in Tasks 8, 9 and 10 are listed case by case with their assertions stated and marked "write in full"; every implementation step carries real code. The one place a test is left to the implementer's judgement — the chaining assertion in Task 4, Step 1 — states the requirement it must meet. No TBDs.

**Type consistency:** `Stop{Index, Total, Commit, Subject, Conflicts, Messages, Files, Resolved}` (Task 4) is what `Assess` classifies from and `stopColumn` reads. `Replay{Commits, Stops, Stop, Truncated, Why, Err}` is used identically in Tasks 4 and 10. `Handover{Index, Total, Commit, Subject, Conflicts, Files, Staged, Deleted, Left}` is declared in Task 6, built in Task 7, consumed by `RenderPlan` (Task 6) and `handOver` (Task 8). `State` fields (Task 6) are written by `handOver` (Task 8) and read by `SyncResume`, `verifyHandover` (Task 9) and `Undo` (Task 10). `Result.Left` (Task 7) is what Tasks 8 and 9 branch on; `Result.Restored` now means "a restore ran and was verified", set by `fail` and by the stack-parent path. `Request.Stacked` (Task 7) is set by `SyncRun` (Task 8) from `wtsync.Descendants`. `LeftLock{PID, Started}` (Task 6) is what `TakeOver` (Task 7) compares and what `Undo` (Task 10) passes. `repo.Worktree.Rebasing` (Task 3) is read by nothing yet and exists so `Detached` can stop lying. `Check{Name, OK, Detail, Fix}` is unchanged. `handoverInput`/`completeInput` (Task 8) are used by Tasks 8 and 9.
