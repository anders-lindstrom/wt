# wt sync run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `wt sync run <work>...` rebases named worktrees onto trunk: a safety ref at the old tip, the declared strategies applied at each real stop, the deferred steps run once at the end and committed when they change tracked files, an unresolved stop aborted and restored, stacks rebased in order. `wt sync undo <work>` puts every ref a run moved back. `wt sync doctor` checks the preconditions.

**Architecture:** The foundation (`internal/wtsync`: config from trunk, strategies as pure functions over three blobs, the object-store simulation, `Assess`) stays untouched in behaviour. This plan adds, in the same package, the pieces that write: safety refs, a lock, reading a live rebase stop from a worktree's index and staging a strategy's answer, the rebase loop, deferred steps, stack ordering, undo, and doctor checks. `internal/commands/sync_run.go`, `sync_undo.go` and `sync_doctor.go` orchestrate and print; `cmd/wt/sync.go` gains the three subcommands. `wt sync` with no verb remains read-only and is not changed except for its help text.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3`, git ≥ 2.40 (Anders runs 2.55). Tests are `go test` with throwaway repositories built the way `internal/wtsync/replay_test.go` (`linearRepo`, `gitIn`) and `internal/commands/sync_test.go` (`syncRepo`, `gitOut`) build them.

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` §3 (`defer`, `commit:`, a failed deferred step, config from trunk), §4 (rebase execution: command, stacks, signing, safety ref, rerere, lock, hooks), §7 (surfaces). The foundation plan `docs/superpowers/plans/2026-09-09-wt-sync-foundation.md` and the handoff `~/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md` carry the rulings this plan inherits.

## Global Constraints

- **Only `run`, `undo` and `doctor --fix/--prune` write.** `wt sync` with no verb stays read-only; every `git status` it runs keeps `--no-optional-locks`.
- **A safety ref is written before any ref moves** (`refs/wt-sync/<branch>/<epoch>`, spec §4). Nothing rebases without one. `undo` restores to the newest.
- **The rebase command is exactly** `git -c rebase.backend=merge -c rebase.rebaseMerges=false -c rebase.autoStash=false -c rebase.updateRefs=false rebase --no-update-refs --no-gpg-sign <onto>` (or `--onto <onto> <upstream>` for a stack child), run in the worktree with `GIT_EDITOR=true` and `GIT_SEQUENCE_EDITOR=true` in the environment, so no editor and no signer can ever prompt and no user configuration changes the backend, the merge handling or the stash behaviour. **`--rerere-autoupdate` is deliberately not passed** (ruling, Codex review 2026-09-09): with it, a rerere resolution is staged and vanishes from `ls-files -u` before any strategy sees it, which is the opposite of the validation §4 asks for. Without it rerere still writes its resolution into the working file but leaves the index unmerged, so every stop shows its three stages, the declared strategy recomputes from them and overwrites the working file, and an unclaimed path is refused as before. Rerere's benefit on unclaimed paths is given up in this plan (contested is refused anyway).
- **The config and any `script` are read from `origin/<trunk>`**, never from the worktree being rebased (spec §3).
- **A strategy refuses rather than guesses.** An unresolved stop aborts the rebase and resets to the safety ref; the worktree's tracked state is left exactly as it was found (an untracked file a script created is never deleted: the tool never runs `git clean`). Restoration is verified, not assumed: not mid-rebase, HEAD attached to the branch, HEAD at the old tip, no tracked changes. Nothing is ever hand-merged by the tool.
- **Trunk is fetched first, then read once.** `run` fetches `origin/<trunk>`, resolves it to a SHA, and uses that SHA for the declaration, for materialising scripts, and as the rebase target, so a run never mixes yesterday's declaration with today's tree. A stack child rebases onto its parent's new tip but still reads the declaration and scripts from the trunk SHA (`Request.Trunk`), never from a rewritten parent.
- **A run has one identity, `epoch`, which is `time.Now().UnixNano()`.** Every safety ref of a run shares it; `undo` restores the group; retention keeps a group whole. Seconds are not unique enough (two runs in one second on two branches would undo together).
- **Never half-apply a stack.** Every member of a stack is locked before any member is rebased, dirt and mid-rebase are re-checked after locking, and the lock is held through the deferred steps. A member that cannot be locked poisons the whole stack before anything moves.
- **More than one worktree needs one confirmation** (spec §7): the set about to be touched is printed and, in a terminal, confirmed once; `--yes` skips; without a terminal nothing is asked and the run proceeds. A single named worktree that expands to a stack counts as more than one.
- **Every subprocess has a deadline.** A script (`--check`, `--resolve`) gets 60 seconds, a deferred step 30 minutes, `docker info` 10 seconds; a timeout is an error, reported as such, never a hang.
- **Agent detection failing is a refusal, not a note**, in `run` and `undo`: if `claude agents --json` cannot be read, nothing is rebased. It detects Claude sessions only (agents.go says so); a Codex session is not seen, and the help text says so.
- **A failed deferred step never undoes the rebase** (spec §3). It is reported as owed with its output.
- **Never say "ours" or "theirs".** Stage 2 of a rebase conflict is **trunk** (HEAD during a rebase), stage 3 is **the replayed commit** (the branch). Fields are `Base`, `Trunk`, `Branch`.
- **A worktree with tracked changes, a live agent, no declaration on trunk, or class `divergent` is never touched.** Class `contested` is refused in this plan (resume is not built); the message says to rebase by hand.
- **No fleet fact in code or tests.** Branch names and counts from the spec are illustrations.
- **Paths are repository-root relative**; glob patterns are matched with `MatchGlob`, never expanded against the disk.
- Commits follow Conventional Commits, imperative, lowercase, under 72 characters. Work on `main`, push after every commit (Anders' rule for this repo; no worktrees, no PRs).
- `gofmt`, `go vet ./...` and `golangci-lint run ./...` clean before every commit. Tests run with `-race` at least once per task.
- **Out of scope, stated so nobody builds them by accident:** the plan file (§6), `resume`, the ask protocol (§5), `watch`, `keep`, `queue`, `campaign`, `--safe`, `--pick`, `--all`, `explain`, and `wt sync <work>` detail. `run` on a `contested` worktree refuses; the reclassification-and-plan-file path is the next plan.

## File Structure

```
internal/wtsync/
  safety.go          WriteSafety, LatestSafety, ListSafety, Prunable: refs/wt-sync/<branch>/<epoch>
  safety_test.go
  lock.go            Lock: <gitdir>/wt-sync.lock, O_EXCL, 30-minute expiry
  lock_test.go
  stage.go           StagedConflicts (git ls-files -u), Progress (rebase-merge/msgnum, end), Apply (write + git add)
  stage_test.go
  resolve.go         resolveConflict: the strategy's answer as bytes; tryStrategy (moved from triage.go) wraps it
  script.go          + Script.ResolveInWorktree: `<exe> --resolve <path>` against the worktree's real index; + a deadline on both --check and --resolve
  script_test.go     + its tests
  rebase.go          Verdict/Preflight; Request, Result, StopResult; Rebase: the loop, abort and restore, signatures
  rebase_test.go
  defer.go           RunDeferred: changed paths old..new, run, commit when tracked files change, owed on failure
  defer_test.go
  stack.go           Parents, Members, Order: the ancestor relation across branch-attached worktrees; ambiguous shapes reported
  stack_test.go
  undo.go            Undo: every ref of the newest epoch back to its safety ref
  undo_test.go
  doctor.go          Check, Doctor: declaration, scripts, trunk ref, rerere, hooks, submodules, LFS, Docker, safety refs, locks
  doctor_test.go
internal/commands/
  sync_run.go        SyncRun(ctx, works, RunOptions, w): fetch, expand stacks, preflight, lock, Rebase, RunDeferred, report
  sync_run_test.go
  sync_undo.go       SyncUndo(ctx, work, w)
  sync_undo_test.go
  sync_doctor.go     SyncDoctor(ctx, DoctorOptions, w)
  sync_doctor_test.go
cmd/wt/sync.go       `wt sync` gains `run`, `undo`, `doctor` subcommands; help text states the flow
README.md            the surfaces table
```

Every `git` invocation in the new files goes through the package's existing `gitEnv(dir, env, stdin, args...)` (script.go) so `GIT_EDITOR=true` and the identity are set in one place: add a package-level `var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}` in rebase.go and pass it wherever a rebase step runs.

---

### Task 1: Safety refs

**Files:**
- Create: `internal/wtsync/safety.go`
- Test: `internal/wtsync/safety_test.go`

**Interfaces:**
- Consumes: `gitEnv` (script.go), `gitIn` test helper (config_test.go), `linearRepo` (replay_test.go).
- Produces:
  ```go
  const SafetyPrefix = "refs/wt-sync/"
  type Safety struct { Branch string; Epoch int64; Ref string; Tip string }
  func WriteSafety(mainRoot, branch, tip string, epoch int64) (Safety, error)   // epoch is UnixNano
  func ListSafety(mainRoot string) ([]Safety, error)          // newest first
  func LatestSafety(mainRoot, branch string) (Safety, bool, error)
  func Prunable(all []Safety, now time.Time, keep time.Duration) []Safety
  func DeleteSafety(mainRoot string, s Safety) error
  ```

`Epoch` is nanoseconds since the Unix epoch (one run, one epoch, shared by every branch it touched). Retention works on run groups: a ref is kept when it is younger than `keep`, **or when its epoch is the newest epoch of any branch** (so the group an `undo` would restore stays whole; Codex finding 18).

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"testing"
	"time"
)

func TestWriteSafetyPinsTheTipUnderTheBranchAndEpoch(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	s, err := WriteSafety(dir, "feature", tip, 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ref != "refs/wt-sync/feature/1700000000" || s.Tip != tip || s.Branch != "feature" || s.Epoch != 1700000000 {
		t.Fatalf("got %+v", s)
	}
	if got := gitIn(t, dir, "rev-parse", s.Ref); got != tip {
		t.Fatalf("ref points at %s, want %s", got, tip)
	}
}

func TestListSafetyIsNewestFirstAndLatestPicksPerBranch(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	for _, e := range []int64{100, 300, 200} {
		if _, err := WriteSafety(dir, "feature", tip, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteSafety(dir, "team/other", tip, 250); err != nil {
		t.Fatal(err)
	}
	all, err := ListSafety(dir)
	if err != nil {
		t.Fatal(err)
	}
	var epochs []int64
	for _, s := range all {
		epochs = append(epochs, s.Epoch)
	}
	if len(epochs) != 4 || epochs[0] != 300 || epochs[1] != 250 || epochs[2] != 200 || epochs[3] != 100 {
		t.Fatalf("epochs %v", epochs)
	}
	latest, ok, err := LatestSafety(dir, "team/other")
	if err != nil || !ok || latest.Epoch != 250 || latest.Branch != "team/other" {
		t.Fatalf("latest %+v ok=%v err=%v", latest, ok, err)
	}
	if _, ok, _ := LatestSafety(dir, "nobody"); ok {
		t.Fatal("a branch with no safety ref reported one")
	}
}

func TestPrunableKeepsEveryNewestRunGroupAndAnythingYoung(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	ago := func(days int) int64 { return now.Add(-time.Duration(days) * 24 * time.Hour).UnixNano() }
	all := []Safety{
		{Branch: "a", Epoch: ago(1)},  // newest for a, young
		{Branch: "a", Epoch: ago(40)}, // old: prunable
		{Branch: "b", Epoch: ago(50)}, // old but newest for b: kept
		{Branch: "b", Epoch: ago(60)}, // prunable
		{Branch: "c", Epoch: ago(2)},  // young
		{Branch: "c", Epoch: ago(3)},  // young, not newest: kept because young
		{Branch: "p", Epoch: ago(45)}, // old, not p's newest ...
		{Branch: "p", Epoch: ago(10)}, // p's newest
		{Branch: "q", Epoch: ago(45)}, // ... but ago(45) is q's newest: the whole group is kept
	}
	got := Prunable(all, now, 30*24*time.Hour)
	if len(got) != 2 || got[0].Epoch != ago(40) || got[1].Epoch != ago(60) {
		t.Fatalf("prunable %+v", got)
	}
}

func TestDeleteSafetyRemovesTheRef(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	s, err := WriteSafety(dir, "feature", tip, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteSafety(dir, s); err != nil {
		t.Fatal(err)
	}
	all, _ := ListSafety(dir)
	if len(all) != 0 {
		t.Fatalf("still listed: %+v", all)
	}
}
```

- [ ] **Step 2: Run the tests to make sure they fail**

Run: `cd ~/programmering/private/wt && go test ./internal/wtsync/ -run 'Safety|Prunable' 2>&1 | head`
Expected: compile errors, `undefined: WriteSafety` and friends.

- [ ] **Step 3: Implement**

```go
package wtsync

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SafetyPrefix is where a run pins the tip it is about to rewrite. A plain
// ref outside refs/heads: the branch namespace is contended in a repository
// with many worktrees, a branch cannot be checked out twice, and a ref here
// is invisible to --update-refs (spec §4).
const SafetyPrefix = "refs/wt-sync/"

// Safety is one pinned tip: the branch it belonged to, the run that pinned it
// (Epoch, nanoseconds; every ref of one run shares it), and where.
type Safety struct {
	Branch string
	Epoch  int64
	Ref    string
	Tip    string
}

// WriteSafety pins tip as refs/wt-sync/<branch>/<epoch>. It refuses to move
// an existing ref of the same name: two runs in one second would otherwise
// silently share one undo point.
func WriteSafety(mainRoot, branch, tip string, epoch int64) (Safety, error) {
	s := Safety{Branch: branch, Epoch: epoch, Tip: tip, Ref: SafetyPrefix + branch + "/" + strconv.FormatInt(epoch, 10)}
	// The zero-oid old value makes update-ref fail if the ref already exists.
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", s.Ref, tip, "0000000000000000000000000000000000000000"); err != nil {
		return Safety{}, fmt.Errorf("safety ref %s: %w", s.Ref, err)
	}
	return s, nil
}

// ListSafety returns every safety ref, newest epoch first.
func ListSafety(mainRoot string) ([]Safety, error) {
	out, err := gitEnv(mainRoot, nil, nil, "for-each-ref", "--format=%(refname) %(objectname)", SafetyPrefix)
	if err != nil {
		return nil, err
	}
	var list []Safety
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		ref, tip, _ := strings.Cut(line, " ")
		rest := strings.TrimPrefix(ref, SafetyPrefix)
		i := strings.LastIndex(rest, "/")
		if i < 0 {
			continue // not ours to interpret
		}
		epoch, err := strconv.ParseInt(rest[i+1:], 10, 64)
		if err != nil {
			continue
		}
		list = append(list, Safety{Branch: rest[:i], Epoch: epoch, Ref: ref, Tip: tip})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Epoch > list[j].Epoch })
	return list, nil
}

// LatestSafety is the newest safety ref for branch.
func LatestSafety(mainRoot, branch string) (Safety, bool, error) {
	all, err := ListSafety(mainRoot)
	if err != nil {
		return Safety{}, false, err
	}
	for _, s := range all {
		if s.Branch == branch {
			return s, true, nil
		}
	}
	return Safety{}, false, nil
}

// Prunable applies the retention rule: keep anything younger than keep, and
// keep every run group (all refs sharing an epoch) that is the newest run of
// any branch, so what undo would restore stays whole; everything else pins
// abandoned history and blocks garbage collection (spec §4).
func Prunable(all []Safety, now time.Time, keep time.Duration) []Safety {
	newest := map[string]int64{}
	for _, s := range all {
		if s.Epoch > newest[s.Branch] {
			newest[s.Branch] = s.Epoch
		}
	}
	keepEpoch := map[int64]bool{}
	for _, e := range newest {
		keepEpoch[e] = true
	}
	var out []Safety
	for _, s := range all {
		if keepEpoch[s.Epoch] {
			continue
		}
		if now.Sub(time.Unix(0, s.Epoch)) < keep {
			continue
		}
		out = append(out, s)
	}
	return out
}

// DeleteSafety drops one safety ref.
func DeleteSafety(mainRoot string, s Safety) error {
	_, err := gitEnv(mainRoot, nil, nil, "update-ref", "-d", s.Ref, s.Tip)
	return err
}
```

- [ ] **Step 4: Run the tests, then lint**

Run: `go test -race ./internal/wtsync/ -run 'Safety|Prunable' && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, no output from gofmt, lint clean.

Also change `TestListSafetyIsNewestFirstAndLatestPicksPerBranch` and `TestWriteSafetyPinsTheTipUnderTheBranchAndEpoch` to nothing: their small integer epochs are fine, an epoch is just an int64.

Run again after that edit: `go test -race ./internal/wtsync/ -run 'Safety|Prunable'`
Expected: PASS, no output from gofmt, lint clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/safety.go internal/wtsync/safety_test.go
git commit -m "feat(sync): pin a branch tip under refs/wt-sync before a run"
git push origin main
```

---

### Task 2: The lock

**Files:**
- Create: `internal/wtsync/lock.go`
- Test: `internal/wtsync/lock_test.go`

**Interfaces:**
- Produces:
  ```go
  const LockName = "wt-sync.lock"
  const LockExpiry = 30 * time.Minute
  type Lock struct { Path string; PID int; Started time.Time; Owner string }
  func GitDir(wtPath string) (string, error)                        // git rev-parse --absolute-git-dir
  func Acquire(gitDir string, now time.Time) (*Lock, error)         // *LockHeld when another live lock exists
  func (l *Lock) Release() error                                    // removes the file only if it is still ours (pid matches)
  type LockHeld struct { Lock }                                    // error
  func ReadLock(gitDir string) (*Lock, bool, error)
  ```

**Atomicity (Codex finding 7):** the lock's content is written to a private temp file first (`<gitDir>/wt-sync.lock.<pid>`), then linked into place with `os.Link`, which is atomic and fails with `ErrExist` when the lock exists; a contender therefore never reads a half-written lock. Taking over an expired lock renames it aside first (`os.Rename(lock, lock+".stale."+pid)`), which is atomic, so two contenders cannot both take it; the renamed file is then removed. `Release` re-reads the lock and removes it only when the pid is its own, so a displaced owner cannot delete its replacement's lock. A live run longer than `LockExpiry` can still be displaced; the run's longest step (a deferred Gradle regeneration, 1–2 minutes) is far below it, and a renewal is not built.

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWritesTheLockAndReleaseRemovesIt(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if l.PID != os.Getpid() || !l.Started.Equal(now) || l.Path != filepath.Join(dir, LockName) {
		t.Fatalf("lock %+v", l)
	}
	got, ok, err := ReadLock(dir)
	if err != nil || !ok || got.PID != l.PID || !got.Started.Equal(now) {
		t.Fatalf("read %+v ok=%v err=%v", got, ok, err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadLock(dir); ok {
		t.Fatal("lock survived release")
	}
}

func TestAcquireRefusesALiveLockAndReplacesAnExpiredOne(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	first, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir, now.Add(5*time.Minute))
	var held *LockHeld
	if !errors.As(err, &held) || held.PID != first.PID {
		t.Fatalf("second acquire: %v", err)
	}
	second, err := Acquire(dir, now.Add(LockExpiry+time.Second))
	if err != nil {
		t.Fatalf("expired lock was not replaced: %v", err)
	}
	// The displaced owner's Release must not remove the replacement.
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := ReadLock(dir); !ok || !got.Started.Equal(second.Started) {
		t.Fatalf("replacement lock gone or wrong: %+v ok=%v", got, ok)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadLock(dir); ok {
		t.Fatal("lock survived its owner's release")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, LockName+"*")); len(left) != 0 {
		t.Fatalf("temp or stale files left behind: %v", left)
	}
}

func TestGitDirOfAWorktreeIsItsOwnDirectoryUnderTheMainGitDir(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	wt := featureWorktree(t, dir)
	got, err := GitDir(wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".git", "worktrees", filepath.Base(wt.Path))
	if got != want {
		t.Fatalf("git dir %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'Lock|GitDir' 2>&1 | head`
Expected: `undefined: Acquire`.

- [ ] **Step 3: Implement**

```go
package wtsync

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockName is the file a run holds in the worktree's own git dir
// (.git/worktrees/<name>/), so a second run, or the watcher, sees it.
const LockName = "wt-sync.lock"

// LockExpiry is how long a lock is believed. A run that died without
// releasing must not block its worktree forever.
const LockExpiry = 30 * time.Minute

// Lock is a held or observed lock.
type Lock struct {
	Path    string
	PID     int
	Started time.Time
	Owner   string
}

// LockHeld is the error for a live lock held by someone else.
type LockHeld struct{ Lock }

func (e *LockHeld) Error() string {
	return fmt.Sprintf("locked by pid %d (%s) since %s", e.PID, e.Owner, e.Started.Format(time.RFC3339))
}

// GitDir is the worktree's own git dir, absolute.
func GitDir(wtPath string) (string, error) {
	return gitEnv(wtPath, nil, nil, "rev-parse", "--absolute-git-dir")
}

// Acquire takes the lock atomically: the content is written to a private
// temp file and hard-linked into place, so a contender never sees a
// half-written lock. An existing lock younger than LockExpiry is respected;
// an older one is renamed aside (atomic, so only one contender wins) and
// removed.
func Acquire(gitDir string, now time.Time) (*Lock, error) {
	path := filepath.Join(gitDir, LockName)
	owner := "?"
	if u, err := user.Current(); err == nil {
		owner = u.Username
	}
	l := &Lock{Path: path, PID: os.Getpid(), Started: now, Owner: owner}
	tmp := fmt.Sprintf("%s.%d", path, l.PID)
	body := fmt.Sprintf("pid=%d\nstart=%d\nowner=%s\n", l.PID, now.Unix(), owner)
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(tmp) //nolint:errcheck // best effort: the temp file is ours alone
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Link(tmp, path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, ok, rerr := ReadLock(gitDir)
		if rerr != nil {
			return nil, rerr
		}
		if ok && now.Sub(existing.Started) < LockExpiry {
			return nil, &LockHeld{*existing}
		}
		// Expired, or unreadable: take it over. Rename is atomic, so of two
		// contenders exactly one succeeds here; the other retries and finds
		// the winner's fresh lock.
		stale := fmt.Sprintf("%s.stale.%d", path, l.PID)
		if err := os.Rename(path, stale); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		_ = os.Remove(stale)
	}
	return nil, fmt.Errorf("could not acquire %s", path)
}

// Release removes the lock, but only while it is still this process's: a
// displaced owner must not delete its replacement's lock.
func (l *Lock) Release() error {
	cur, ok, err := ReadLock(filepath.Dir(l.Path))
	if err != nil {
		return err
	}
	if !ok || cur.PID != l.PID {
		return nil
	}
	err = os.Remove(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadLock parses an existing lock file; ok is false when there is none.
func ReadLock(gitDir string) (*Lock, bool, error) {
	path := filepath.Join(gitDir, LockName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	l := &Lock{Path: path}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "pid":
			l.PID, _ = strconv.Atoi(v)
		case "start":
			sec, _ := strconv.ParseInt(v, 10, 64)
			l.Started = time.Unix(sec, 0)
		case "owner":
			l.Owner = v
		}
	}
	return l, true, nil
}
```

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ -run 'Lock|GitDir' && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean. G304 and G306 are excluded in `.golangci.yml`, so the variable path and the 0o644 pass; the `//nolint:errcheck` on the deferred temp-file removal is the only directive needed.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/lock.go internal/wtsync/lock_test.go
git commit -m "feat(sync): hold a lock in the worktree's git dir during a run"
git push origin main
```

---

### Task 3: Reading a live stop and staging a resolution

**Files:**
- Create: `internal/wtsync/stage.go`, `internal/wtsync/resolve.go`
- Modify: `internal/wtsync/triage.go` (move `tryStrategy` out; it becomes a wrapper)
- Test: `internal/wtsync/stage_test.go`

**Interfaces:**
- Consumes: `Conflict` (conflict.go), `FileOutcome` (triage.go), `Config.RuleFor`, `FromRule`.
- Produces:
  ```go
  // stage.go
  func StagedConflicts(wtPath string) ([]Conflict, error)        // from git ls-files -u; Incomplete set when a stage is missing or a mode is not a regular file
  type Progress struct { Index, Total int; Commit, Subject string }
  func RebaseInProgress(wtPath string) (bool, error)             // rebase-merge or rebase-apply dir exists
  func RebaseProgress(wtPath string) (Progress, error)           // msgnum, end, stopped-sha, message
  func Apply(wtPath string, c Conflict, content []byte) error     // overwrite the working file (its mode untouched), git add -- path
  // resolve.go
  type Resolution struct { Outcome FileOutcome; Content []byte; InPlace bool }
  func resolveConflict(mainRoot, onto string, cfg *Config, c Conflict, wtPath string) (Resolution, error)
  ```
  `tryStrategy(mainRoot, onto, cfg, c)` keeps its signature and calls `resolveConflict(..., "")`, so triage.go's behaviour and tests are unchanged. `InPlace` is reserved for Task 4 (a script that resolves in the worktree itself); Task 3 always returns it false.

`Conflict` is not changed. `Apply` overwrites the working file that git left in place and never touches its mode: git has already merged the mode (trunk alone making a file executable is a valid, non-conflicting change that a strategy owning the content has no business reversing; Codex finding 21). If the working file does not exist (it always does at a content conflict; belt and braces) it is created 0644.

The fixture helper `linearRepo` is refactored in this task so later tasks can build a base with more files: add `repoWith(t, base map[string]string, trunkEdits, branchEdits []map[string]string) string` to replay_test.go with the body of today's `linearRepo`, taking the base files as a parameter and additionally running `git config user.name t` and `git config user.email t@example.com` in the new repository (production-path commits in later tasks must not depend on the ambient identity); `linearRepo` becomes a one-line call with the old base (`a.txt`, `b.txt`, `v.txt`). All 22 existing callers keep working.

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stoppedRebase starts a real rebase of feature onto main in a worktree and
// returns the worktree path, stopped at the first conflict.
func stoppedRebase(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir, wt string) {
	t.Helper()
	dir = linearRepo(t, trunkEdits, branchEdits)
	w := featureWorktree(t, dir)
	cmd := gitCmd(w.Path, "rebase", "--no-update-refs", "--no-gpg-sign", "main")
	if err := cmd.Run(); err == nil {
		t.Fatal("rebase did not stop")
	}
	return dir, w.Path
}

func TestStagedConflictsReadsTheThreeStagesOfEachUnmergedPath(t *testing.T) {
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "a2\n"}})
	cs, err := StagedConflicts(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Path != "v.txt" {
		t.Fatalf("conflicts %+v", cs)
	}
	c := cs[0]
	if string(c.Base) != "1.0.0\n" || string(c.Trunk) != "1.0.5\n" || string(c.Branch) != "1.0.1\n" || c.Incomplete != "" {
		t.Fatalf("stages %q %q %q incomplete %q", c.Base, c.Trunk, c.Branch, c.Incomplete)
	}
}

func TestStagedConflictsMarksAModifyDeleteIncomplete(t *testing.T) {
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, nil)
	w := featureWorktree(t, dir)
	gitIn(t, w.Path, "rm", "-q", "v.txt")
	gitIn(t, w.Path, "commit", "-q", "-m", "drop v")
	if err := gitCmd(w.Path, "rebase", "--no-gpg-sign", "main").Run(); err == nil {
		t.Fatal("rebase did not stop")
	}
	cs, err := StagedConflicts(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Incomplete == "" {
		t.Fatalf("conflicts %+v", cs)
	}
}

func TestRebaseProgressReadsTheStopIndexAndSubject(t *testing.T) {
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"a.txt": "a2\n"}, {"v.txt": "1.0.1\n"}})
	ok, err := RebaseInProgress(wt)
	if err != nil || !ok {
		t.Fatalf("in progress %v %v", ok, err)
	}
	p, err := RebaseProgress(wt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Index != 2 || p.Total != 2 || !strings.HasPrefix(p.Subject, "branch 2") || len(p.Commit) < 7 {
		t.Fatalf("progress %+v", p)
	}
}

func TestApplyWritesTheContentKeepsTheModeAndStagesIt(t *testing.T) {
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	if err := os.Chmod(filepath.Join(wt, "v.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	cs, _ := StagedConflicts(wt)
	if err := Apply(wt, cs[0], []byte("1.0.6\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("file %q", got)
	}
	if st, _ := os.Stat(filepath.Join(wt, "v.txt")); st.Mode()&0o100 == 0 {
		t.Fatal("Apply changed the file's mode")
	}
	if out := gitIn(t, wt, "ls-files", "-u", "--", "v.txt"); out != "" {
		t.Fatalf("still unmerged: %s", out)
	}
	if out := gitIn(t, wt, "diff", "--cached", "--name-only"); out != "v.txt" {
		t.Fatalf("staged %q", out)
	}
}

func TestResolveConflictReturnsTheStrategysBytes(t *testing.T) {
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	c := Conflict{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("1.0.5\n"), Branch: []byte("1.0.1\n")}
	r, err := resolveConflict(dir, "main", triageCfg(t), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Outcome.Resolved || string(r.Content) != "1.0.6\n" || r.InPlace {
		t.Fatalf("resolution %+v", r)
	}
}
```

`gitCmd(dir, args...) *exec.Cmd` is a small test helper to add to config_test.go next to `gitIn`: same environment, but returns the command so the caller can inspect a non-zero exit. `triageCfg` (triage_test.go) declares `v.txt` as owned-line max-plus-patch: confirm by reading `triageYAML` before relying on it.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'Staged|RebaseProgress|Apply|ResolveConflict' 2>&1 | head`
Expected: `undefined: StagedConflicts`.

- [ ] **Step 3: Implement stage.go**

```go
package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StagedConflicts reads a stopped rebase's unmerged paths from the
// worktree's index. Stage 1 is the base, stage 2 is trunk (HEAD while a
// rebase replays), stage 3 is the replayed commit. A path missing a stage or
// carrying a non-regular mode is Incomplete, exactly as the simulation marks
// it, so no strategy ever sees it.
func StagedConflicts(wtPath string) ([]Conflict, error) {
	out, err := gitEnv(wtPath, nil, nil, "ls-files", "-u", "-z")
	if err != nil {
		return nil, fmt.Errorf("ls-files -u: %w", err)
	}
	type entry struct{ mode, oid string }
	stages := map[string]map[int]entry{}
	var order []string
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
		stage, _ := strconv.Atoi(f[2])
		if stages[path] == nil {
			stages[path] = map[int]entry{}
			order = append(order, path)
		}
		stages[path][stage] = entry{mode: f[0], oid: f[1]}
	}
	var cs []Conflict
	for _, path := range order {
		c := Conflict{Path: path}
		s := stages[path]
		missing := 0
		for i := 1; i <= 3; i++ {
			if _, ok := s[i]; !ok {
				missing++
			}
		}
		switch {
		case missing > 0 && s[1].oid == "" && s[2].oid != "" && s[3].oid != "":
			c.Incomplete = "both sides added it"
		case missing > 0:
			c.Incomplete = "one side deleted or renamed it"
		}
		for i := 1; i <= 3; i++ {
			e, ok := s[i]
			if !ok {
				continue
			}
			if e.mode != "100644" && e.mode != "100755" {
				c.Incomplete = "not a regular file (mode " + e.mode + ")"
			}
		}
		if c.Incomplete != "" {
			cs = append(cs, c)
			continue
		}
		read := func(oid string) ([]byte, error) { return catFileRaw(wtPath, oid) }
		if c.Base, err = read(s[1].oid); err != nil {
			return nil, err
		}
		if c.Trunk, err = read(s[2].oid); err != nil {
			return nil, err
		}
		if c.Branch, err = read(s[3].oid); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// Progress is where a stopped rebase is.
type Progress struct {
	Index   int
	Total   int
	Commit  string
	Subject string
}

func rebaseDir(wtPath string) (string, error) {
	return gitEnv(wtPath, nil, nil, "rev-parse", "--git-path", "rebase-merge")
}

// RebaseInProgress reports whether the worktree is mid-rebase under either
// backend: run forces the merge backend (rebase-merge), but a rebase someone
// started by hand with the apply backend (rebase-apply) must block too.
func RebaseInProgress(wtPath string) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		dir, err := gitEnv(wtPath, nil, nil, "rev-parse", "--git-path", name)
		if err != nil {
			return false, err
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(wtPath, dir)
		}
		_, err = os.Stat(dir)
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

// RebaseProgress reads the sequencer's own bookkeeping: msgnum/end for the
// position, stopped-sha for the commit, message for its subject.
func RebaseProgress(wtPath string) (Progress, error) {
	dir, err := rebaseDir(wtPath)
	if err != nil {
		return Progress{}, err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wtPath, dir)
	}
	readInt := func(name string) (int, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(b)))
	}
	var p Progress
	if p.Index, err = readInt("msgnum"); err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	if p.Total, err = readInt("end"); err != nil {
		return p, fmt.Errorf("rebase progress: %w", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stopped-sha")); err == nil {
		p.Commit = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "message")); err == nil {
		p.Subject, _, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	}
	return p, nil
}

// Apply writes a strategy's answer over the conflicted working file, leaving
// the mode git merged, and stages it, which clears the unmerged entries.
func Apply(wtPath string, c Conflict, content []byte) error {
	full := filepath.Join(wtPath, filepath.FromSlash(c.Path))
	// WriteFile's permission argument applies only when it creates the file.
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return err
	}
	_, err := gitEnv(wtPath, nil, nil, "add", "--", c.Path)
	return err
}
```

`catFileRaw(mainRoot, oid)` exists in replay.go and runs `git cat-file blob`; it works from any directory of the same repository, so passing the worktree path is fine.

- [ ] **Step 4: Implement resolve.go and slim triage.go**

Move `tryStrategy` from triage.go into resolve.go as this:

```go
package wtsync

import "fmt"

// Resolution is what a strategy answered for one conflict: the outcome as
// triage reports it, and the bytes to write when it resolved. InPlace means
// the strategy already wrote and staged the file itself (a script's
// --resolve, Task 4), so there is nothing to Apply.
type Resolution struct {
	Outcome FileOutcome
	Content []byte
	InPlace bool
}

// resolveConflict asks the declared strategy for its answer. A conflict
// without three regular blobs is refused before any strategy sees it. wtPath
// is "" at triage, where nothing may be written; a run passes the worktree.
// The error return is for things going wrong — a strategy that cannot be
// built, a script that cannot run — as opposed to a refusal, which is a
// normal outcome carried in the Note.
func resolveConflict(mainRoot, onto string, cfg *Config, c Conflict, wtPath string) (Resolution, error) {
	r := Resolution{Outcome: FileOutcome{Path: c.Path, Note: "unclaimed"}}
	if c.Incomplete != "" {
		r.Outcome.Note = c.Incomplete
		return r, nil
	}
	if cfg == nil {
		return r, nil
	}
	rule, ok := cfg.RuleFor(c.Path)
	if !ok {
		return r, nil
	}
	r.Outcome.Strategy = rule.Strategy
	s, err := FromRule(rule, mainRoot, onto)
	if err != nil {
		r.Outcome.Note = err.Error()
		return r, fmt.Errorf("%s: %w", c.Path, err)
	}
	content, err := s.Resolve(c)
	if err != nil {
		if ref := refusalOf(err); ref != nil {
			r.Outcome.Note = ref.Reason
			r.Outcome.Keys = ref.Keys
			return r, nil
		}
		r.Outcome.Note = err.Error()
		return r, fmt.Errorf("%s: %w", c.Path, err)
	}
	r.Outcome.Resolved, r.Outcome.Note = true, ""
	r.Content = content
	return r, nil
}

// tryStrategy is the triage view of resolveConflict: the outcome only.
func tryStrategy(mainRoot, onto string, cfg *Config, c Conflict) (FileOutcome, error) {
	r, err := resolveConflict(mainRoot, onto, cfg, c, "")
	return r.Outcome, err
}
```

Delete the old `tryStrategy` body from triage.go (`fmt` stays used there). `messagesPath` lives in replay.go.

- [ ] **Step 5: Run the whole package, then lint**

Run: `go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: every existing triage test still passes; the new ones pass.

- [ ] **Step 6: Commit and push**

```bash
git add internal/wtsync/stage.go internal/wtsync/stage_test.go internal/wtsync/resolve.go internal/wtsync/triage.go internal/wtsync/config_test.go internal/wtsync/replay_test.go
git commit -m "feat(sync): read a stopped rebase's stages and stage a strategy's answer"
git push origin main
```

---

### Task 4: The script escape hatch resolves in the worktree

**Files:**
- Modify: `internal/wtsync/script.go`, `internal/wtsync/resolve.go`
- Test: `internal/wtsync/script_test.go`

**Interfaces:**
- Produces: `func (s Script) ResolveInWorktree(wtPath, path string) error` — runs `<exe> --resolve <path>` with cwd = the worktree and the worktree's real index; exit 0 means the script wrote and staged the file (verified: `git ls-files -u -- path` is empty afterwards), exit 2 is a refusal with stderr as the reason, anything else is an error. `resolveConflict` with a non-empty `wtPath` and a `script` rule calls this and returns `InPlace: true` on success.
- Also: `Script` gains `Timeout time.Duration` (zero means `ScriptTimeout`, a package constant of 60 seconds); both `Resolve` (`--check`) and `ResolveInWorktree` run through `exec.CommandContext` with that deadline and report `timed out after <d>` as an error, never a refusal. This closes the foundation handoff's deferred "no timeout on the script invocation".

The contract the bash oracle established (PR bodies of Telcred/server#584, now merged and deleted; the README of that PR is the reference): `--claims` lists paths, `--check <file>` exits 0/1/2, `--resolve <file>` resolves from the index stages and stages the result, exit 2 to refuse. The script is materialised from `origin/<trunk>` (`materialise` in script.go) exactly as `--check` is; it never runs from the worktree's checkout.

- [ ] **Step 1: Write the failing tests**

Read `script_test.go` first: it already has a helper that commits a fake script to the fixture's trunk and fetches it into origin. Reuse it. Add:

```go
func TestResolveInWorktreeRunsTheScriptAgainstTheRealIndex(t *testing.T) {
	// A script that resolves by taking the branch side (stage 3) and staging it.
	script := "#!/bin/sh\ncase \"$1\" in\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	dir, wt := stoppedRebaseWithScript(t, script) // fixture: trunk declares v.txt -> script bin/resolve; rebase stopped on v.txt
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	if err := s.ResolveInWorktree(wt, "v.txt"); err != nil {
		t.Fatal(err)
	}
	if out := gitIn(t, wt, "ls-files", "-u", "--", "v.txt"); out != "" {
		t.Fatalf("still unmerged: %s", out)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.1\n" {
		t.Fatalf("content %q", got)
	}
}

func TestResolveInWorktreeExitTwoIsARefusalWithTheReason(t *testing.T) {
	script := "#!/bin/sh\necho 'not my shape' >&2; exit 2\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	err := s.ResolveInWorktree(wt, "v.txt")
	if !IsRefusal(err) || !strings.Contains(err.Error(), "not my shape") {
		t.Fatalf("err %v", err)
	}
}

func TestResolveInWorktreeAScriptThatExitsZeroWithoutStagingIsAnError(t *testing.T) {
	script := "#!/bin/sh\nexit 0\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	err := s.ResolveInWorktree(wt, "v.txt")
	if err == nil || IsRefusal(err) || !strings.Contains(err.Error(), "left v.txt unmerged") {
		t.Fatalf("err %v", err)
	}
}

func TestScriptTimesOutInsteadOfHanging(t *testing.T) {
	script := "#!/bin/sh\nsleep 5\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve", Timeout: 300 * time.Millisecond}
	err := s.ResolveInWorktree(wt, "v.txt")
	if err == nil || IsRefusal(err) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
}

func TestResolveConflictInAWorktreeUsesTheScriptInPlace(t *testing.T) {
	script := "#!/bin/sh\ncase \"$1\" in\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	cfg, _ := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/resolve\n"))
	cs, _ := StagedConflicts(wt)
	r, err := resolveConflict(dir, "origin/main", cfg, cs[0], wt)
	if err != nil || !r.Outcome.Resolved || !r.InPlace || r.Content != nil {
		t.Fatalf("resolution %+v err %v", r, err)
	}
}
```

`stoppedRebaseWithScript(t, script) (dir, wt string)`: build `linearRepo` with trunk `v.txt: 1.0.5` and branch `v.txt: 1.0.1`; write `bin/resolve` (mode 0755) and a `.wt-sync.yaml` declaring it on trunk, commit on main; `git remote add origin <dir>` + `git fetch -q origin` (the pattern `syncRepo` in commands uses, so `origin/main` exists); add the feature worktree; start the rebase and confirm it stopped.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'ResolveInWorktree|InAWorktree' 2>&1 | head`
Expected: `undefined: ResolveInWorktree` / `stoppedRebaseWithScript`.

- [ ] **Step 3: Implement**

In script.go:

```go
// ResolveInWorktree runs `<script> --resolve <path>` in the worktree, against
// its real index, for a stopped rebase. The script is still read from trunk.
// Exit 0 means the script wrote and staged the file, which is verified; exit
// 2 is a refusal carrying stderr; anything else is an error.
func (s Script) ResolveInWorktree(wtPath, path string) error {
	exe, cleanup, err := materialise(s.Root, s.Trunk, s.Run)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--resolve", path)
	cmd.Dir = wtPath
	cmd.Env = withEnv("GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s --resolve %s: timed out after %s", s.Run, path, s.timeout())
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		out, lerr := gitEnv(wtPath, nil, nil, "ls-files", "-u", "--", path)
		if lerr != nil {
			return lerr
		}
		if out != "" {
			return fmt.Errorf("%s --resolve exited 0 but left %s unmerged", s.Run, path)
		}
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "refused without a reason"
		}
		return Refuse(path, "%s", reason)
	default:
		return fmt.Errorf("%s --resolve %s: %v: %s", s.Run, path, err, strings.TrimSpace(stderr.String()))
	}
}
```

In resolve.go, after `s, err := FromRule(...)`:

```go
	if sc, ok := s.(Script); ok && wtPath != "" {
		if err := sc.ResolveInWorktree(wtPath, c.Path); err != nil {
			if ref := refusalOf(err); ref != nil {
				r.Outcome.Note = ref.Reason
				return r, nil
			}
			r.Outcome.Note = err.Error()
			return r, fmt.Errorf("%s: %w", c.Path, err)
		}
		r.Outcome.Resolved, r.Outcome.Note, r.InPlace = true, "", true
		return r, nil
	}
```

Update the `Script` type comment: `--check` through a temporary index at triage, `--resolve` in the worktree during a run, both under a deadline. Add `const ScriptTimeout = 60 * time.Second`, the `Timeout` field, and `func (s Script) timeout() time.Duration` returning the field or the constant; convert the existing `--check` path in `Resolve` to `exec.CommandContext` with the same deadline and the same `timed out` error. Existing script tests keep passing (none takes a minute).

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/script.go internal/wtsync/script_test.go internal/wtsync/resolve.go
git commit -m "feat(sync): let a declared script resolve a stop in the worktree"
git push origin main
```

---

### Task 5: The rebase loop, abort and restore

**Files:**
- Create: `internal/wtsync/rebase.go`
- Test: `internal/wtsync/rebase_test.go`

**Interfaces:**
- Consumes: `Assessment` (triage.go), `WriteSafety` (Task 1), `StagedConflicts`, `RebaseInProgress`, `RebaseProgress`, `Apply` (Task 3), `resolveConflict` (Tasks 3–4), `gitEnv`, `messagesPath` (replay.go).
- Produces:
  ```go
  type Verdict int
  const ( Proceed Verdict = iota; SkipRun; RefuseRun )   // not Go/Skip/Refuse: Refuse is already the refusal constructor in conflict.go
  func Preflight(a Assessment) (Verdict, string)

  var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}
  var rebaseConfig = []string{"-c", "rebase.backend=merge", "-c", "rebase.rebaseMerges=false", "-c", "rebase.autoStash=false", "-c", "rebase.updateRefs=false"}

  type Request struct {
      Path     string   // the worktree
      Branch   string
      Trunk    string   // the trunk SHA (or ref) the declaration and scripts are read from; never a parent's tip
      Onto     string   // what to rebase onto: Trunk, or a stack parent's new tip
      Upstream string   // "" for a plain rebase; the parent's old tip for a stack child (git rebase --onto Onto Upstream)
      Epoch    int64
  }
  type StopResult struct { Index, Total int; Subject string; Files []FileOutcome }
  type Result struct {
      Branch, OldTip, NewTip string
      Safety   Safety
      Replayed int
      Stops    []StopResult
      Restored bool          // an unresolved stop: aborted and verified back at OldTip
      SignaturesDropped int
  }
  func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error)
  ```

**Behaviour, exactly:**

1. `OldTip = rev-parse --verify <branch>` in the worktree; `Safety = WriteSafety(mainRoot, branch, OldTip, epoch)`.
2. `SignaturesDropped` = the number of commits in `<Upstream or Onto>..OldTip` whose `%G?` is not `N` (a signature cannot survive rewriting; spec §4). It is a count of signed commits about to be rewritten, nothing more.
3. Run `git <rebaseConfig> rebase --no-update-refs --no-gpg-sign <Onto>` (or `--onto <Onto> <Upstream>`) in the worktree with `rebaseEnv`.
4. While the command exits non-zero and `RebaseInProgress` is true: read `RebaseProgress` and `StagedConflicts`. **Loop guard:** if this stop has the same `Index` as the previous iteration and either nothing is unmerged or the unmerged set is identical to the previous one, git did not advance: treat the last command's error as the failure and go to step 6 with it. Otherwise, for each conflict call `resolveConflict(mainRoot, req.Trunk, cfg, c, req.Path)`; a resolved, non-InPlace answer is `Apply`ed. Record a `StopResult`. If any file is unresolved → step 6 (no error). Otherwise run `git rebase --continue`; git itself drops a commit whose resolution made it empty (verified on git 2.55: `--continue` with a clean index moves on and the commit is gone; no `--skip` is issued by the tool, because a `--skip` after a `--continue` that already advanced would skip the *next* commit). Loop.
5. When the command exits zero and no rebase is in progress: `NewTip = rev-parse HEAD`, `Replayed = rev-list --count <Onto>..HEAD`. Return.
6. Restore: `git rebase --abort`; if still in progress, `git rebase --quit`; then `git reset --hard <Safety.Ref>` unless HEAD already equals `OldTip`. Then **verify**: not in progress, `git symbolic-ref --quiet HEAD` is `refs/heads/<branch>`, `rev-parse HEAD == OldTip`, `git --no-optional-locks status --porcelain --untracked-files=no` is empty. If verification fails, return an error naming the safety ref (`not restored: <what failed>; the old tip is <ref>`), and the command layer prints `NOT restored` rather than `restored`. If step 6 was entered because of an unresolved stop (not a git failure), `Restored = true` and the last `StopResult` carries the unresolved files; the return error is nil. A git failure returns its error joined with any restore error.
7. Every `git` call is logged to `log` as one line `  $ git <args>` only when `log` is non-nil and `WT_SYNC_TRACE` is set (the trace covers the calls made through the `git` closure in `Rebase`; helper calls are not traced). Otherwise `log` receives one line per stop: `  stop 6/27 "subject" application.yaml✓ owned-line  spec.json✗ unclaimed`.

`Preflight(a)`:

| condition | verdict | reason |
|---|---|---|
| `a.Err != nil` | RefuseRun | `assessment failed: <err>` |
| `a.Class == Detached` | RefuseRun | `no branch` |
| `a.NoConfig` | RefuseRun | `no declaration on trunk` |
| `a.Dirty` | RefuseRun | `tracked changes in the worktree` |
| `a.Agent != nil` | RefuseRun | `an agent session is in it: <name>` |
| `a.Class == Current` | SkipRun | `already on trunk` |
| `a.Class == Stale` | SkipRun | `nothing ahead of trunk` |
| `a.Class == Divergent` | RefuseRun | `divergent: <first reason>` |
| `a.Class == Contested` | RefuseRun | `contested at <index>/<total>: <unresolved files>; rebase by hand (resume is not built yet)` |
| `a.Class == Unknown` | RefuseRun | `class unknown` |
| `Clean`, `Recipe` | Proceed | "" |

Order matters: an error, a missing branch, no declaration, dirt and an agent are checked before the class, so a dirty current worktree says "tracked changes" rather than "already on trunk".

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runRepo builds a repository whose trunk declares v.txt owned-line
// max-plus-patch and w.txt take-trunk, with origin pointing at itself so
// origin/main exists, and a worktree on feature. The base has w.txt so a
// w.txt conflict is a genuine three-way one, not an add/add.
func runRepo(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir string, wt string, cfg *Config) {
	t.Helper()
	dir = repoWith(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "v.txt": "1.0.0\n", "w.txt": "w\n"}, trunkEdits, branchEdits)
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n  - paths: [w.txt]\n    strategy: take-trunk\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	w := featureWorktree(t, dir)
	return dir, w.Path, cfg
}

func trunkReq(wt string, epoch int64) Request {
	return Request{Path: wt, Branch: "feature", Trunk: "origin/main", Onto: "origin/main", Epoch: epoch}
}

func TestRebaseReplaysACleanBranchAndPinsASafetyRef(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 42), &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldTip != old || res.Restored || res.Replayed != 1 || len(res.Stops) != 0 {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, dir, "rev-parse", "refs/wt-sync/feature/42") != old {
		t.Fatal("safety ref does not pin the old tip")
	}
	if gitIn(t, wt, "rev-parse", "HEAD~1") != gitIn(t, dir, "rev-parse", "origin/main") {
		t.Fatal("not rebased onto origin/main")
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != res.NewTip {
		t.Fatal("NewTip is not HEAD")
	}
}

func TestRebaseResolvesARecipeStopAndContinues(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"a.txt": "a2\n"}, {"v.txt": "1.0.1\n"}})
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 2 || len(res.Stops) != 1 {
		t.Fatalf("result %+v", res)
	}
	s := res.Stops[0]
	if s.Index != 2 || s.Total != 2 || len(s.Files) != 1 || !s.Files[0].Resolved || s.Files[0].Strategy != "owned-line" {
		t.Fatalf("stop %+v", s)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("v.txt %q", got)
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase still in progress")
	}
	if !strings.Contains(log.String(), "stop 2/2") {
		t.Fatalf("log %q", log.String())
	}
}

func TestRebaseDropsACommitTheResolutionMadeEmpty(t *testing.T) {
	// The branch's only change in its first commit is to w.txt, which is
	// taken from trunk: the commit becomes empty and git drops it on
	// --continue. The second commit survives.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"w.txt": "trunk\n"}},
		[]map[string]string{{"w.txt": "branch\n"}, {"b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || len(res.Stops) != 1 || !res.Stops[0].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "w.txt")); string(got) != "trunk\n" {
		t.Fatalf("w.txt %q", got)
	}
	if gitIn(t, wt, "log", "-1", "--format=%s") != "branch 2" {
		t.Fatalf("top commit %q", gitIn(t, wt, "log", "-1", "--format=%s"))
	}
}

func TestRebaseAbortsAndRestoresOnAnUnclaimedStop(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 7), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 1 || res.Stops[0].Files[0].Resolved || res.Stops[0].Files[0].Note != "unclaimed" {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "rev-parse", "feature") != old {
		t.Fatal("not restored to the old tip")
	}
	if gitIn(t, wt, "symbolic-ref", "HEAD") != "refs/heads/feature" {
		t.Fatal("HEAD is detached after restore")
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase left in progress")
	}
	if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("worktree not clean: %q", out)
	}
}

func TestRebaseRestoresOnASecondUnclaimedStop(t *testing.T) {
	// First stop resolves (v.txt, owned-line); second is unclaimed (a.txt).
	// The first stop's resolution must not survive the restore.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}, {"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 8), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 2 || !res.Stops[0].Files[0].Resolved || res.Stops[1].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.1\n" {
		t.Fatalf("v.txt after restore %q", got)
	}
}

func TestRebaseACommitAlreadyOnTrunkIsDroppedNotCounted(t *testing.T) {
	dir, wt, cfg := runRepo(t, nil, nil)
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "add c")
	// The same change lands on trunk (a cherry-pick; -q is not a cherry-pick flag).
	gitIn(t, dir, "cherry-pick", "feature")
	gitIn(t, dir, "fetch", "-q", "origin")
	res, err := Rebase(dir, cfg, trunkReq(wt, 9), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 0 || len(res.Stops) != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestRebaseOntoAParentTipUsesUpstreamAndReadsScriptsFromTrunk(t *testing.T) {
	// A stack child: rebase only the child's own commits onto the parent's
	// new tip. Built by hand: parent branch p with one commit, child c on
	// top with one more, then p rewritten onto origin/main (as the parent's
	// run would have done).
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, nil)
	gitIn(t, dir, "branch", "p", "feature")
	gitIn(t, wt, "checkout", "-q", "p")
	if err := os.WriteFile(filepath.Join(wt, "p.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "parent")
	oldParent := gitIn(t, wt, "rev-parse", "HEAD")
	gitIn(t, wt, "checkout", "-q", "-b", "c")
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "child")
	gitIn(t, dir, "checkout", "-q", "p")
	gitIn(t, dir, "rebase", "-q", "--no-gpg-sign", "origin/main")
	newParent := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	req := Request{Path: wt, Branch: "c", Trunk: "origin/main", Onto: newParent, Upstream: oldParent, Epoch: 10}
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || gitIn(t, wt, "rev-parse", "HEAD~1") != newParent {
		t.Fatalf("result %+v parent %s", res, gitIn(t, wt, "rev-parse", "HEAD~1"))
	}
}

func TestRebaseGivesUpWhenContinueDoesNotAdvance(t *testing.T) {
	// A pre-commit hook that always fails makes every --continue stop at
	// the same commit with nothing unmerged. The loop must notice and
	// restore instead of spinning.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	hooks := gitIn(t, wt, "rev-parse", "--git-path", "hooks")
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(wt, hooks)
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 11), nil)
	if err == nil || !strings.Contains(err.Error(), "did not advance") {
		t.Fatalf("err %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored")
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase left in progress")
	}
	_ = res
}

func TestPreflightOrdersItsReasons(t *testing.T) {
	cases := []struct {
		a      Assessment
		v      Verdict
		reason string
	}{
		{Assessment{Class: Clean, Dirty: true}, RefuseRun, "tracked changes"},
		{Assessment{Class: Current, Dirty: true}, RefuseRun, "tracked changes"},
		{Assessment{Class: Recipe, Agent: &Agent{Name: "x-1"}}, RefuseRun, "x-1"},
		{Assessment{Class: Recipe, NoConfig: true}, RefuseRun, "no declaration"},
		{Assessment{Class: Current}, SkipRun, "already on trunk"},
		{Assessment{Class: Stale}, SkipRun, "nothing ahead"},
		{Assessment{Class: Divergent, Divergent: []string{"openapi refuses spec.json"}}, RefuseRun, "openapi refuses"},
		{Assessment{Class: Contested, Replay: Replay{Stop: &Stop{Index: 2, Total: 5}}, Files: []FileOutcome{{Path: "x.java", Note: "unclaimed"}}}, RefuseRun, "2/5"},
		{Assessment{Class: Recipe}, Proceed, ""},
		{Assessment{Class: Clean}, Proceed, ""},
		{Assessment{Class: Detached}, RefuseRun, "no branch"},
		{Assessment{Class: Unknown}, RefuseRun, "unknown"},
	}
	for i, c := range cases {
		v, reason := Preflight(c.a)
		if v != c.v || !strings.Contains(reason, c.reason) {
			t.Errorf("case %d: got %v %q, want %v containing %q", i, v, reason, c.v, c.reason)
		}
	}
}
```

Note on the hook test: a linked worktree's hooks dir is the main repository's `.git/hooks` (hooks are shared), so the file lands there; `t.TempDir` cleans it up. `core.hooksPath` may be set globally on the developer's machine: the fixture must `git config core.hooksPath` to the repo's own hooks dir explicitly before writing the hook, in `runRepo`, so the test is independent of the ambient configuration. The `--no-verify` flag is **not** passed by `run`: a repository's hooks are part of what a rebase does (spec §4 says doctor preflights them), and this test proves the loop cannot spin when one fails.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'Rebase|Preflight' 2>&1 | head`
Expected: `undefined: Rebase`.

- [ ] **Step 3: Implement**

```go
package wtsync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Verdict is Preflight's answer.
type Verdict int

const (
	Proceed   Verdict = iota // rebase it
	SkipRun                  // nothing to do; say why
	RefuseRun                // must not be touched; say why
)

// rebaseEnv keeps every rebase step from ever prompting.
var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

// rebaseConfig pins the behaviour the loop is written against, whatever the
// user's configuration says: the merge backend (rebase-merge bookkeeping),
// no merge preservation, no autostash, no ref updating.
var rebaseConfig = []string{
	"-c", "rebase.backend=merge",
	"-c", "rebase.rebaseMerges=false",
	"-c", "rebase.autoStash=false",
	"-c", "rebase.updateRefs=false",
}

// Preflight decides from a triage assessment whether a run may start. The
// checks that make a worktree untouchable come before the class, so a dirty
// worktree is refused for its dirt whatever its class.
func Preflight(a Assessment) (Verdict, string) {
	switch {
	case a.Err != nil:
		return RefuseRun, "assessment failed: " + a.Err.Error()
	case a.Class == Detached:
		return RefuseRun, "no branch"
	case a.NoConfig:
		return RefuseRun, "no declaration on trunk"
	case a.Dirty:
		return RefuseRun, "tracked changes in the worktree"
	case a.Agent != nil:
		return RefuseRun, "an agent session is in it: " + agentLabel(a.Agent)
	}
	switch a.Class {
	case Current:
		return SkipRun, "already on trunk"
	case Stale:
		return SkipRun, "nothing ahead of trunk"
	case Divergent:
		reason := "divergent"
		if len(a.Divergent) > 0 {
			reason += ": " + a.Divergent[0]
		}
		return RefuseRun, reason
	case Contested:
		var files []string
		for _, f := range a.Files {
			if !f.Resolved {
				files = append(files, f.Path)
			}
		}
		where := ""
		if a.Replay.Stop != nil {
			where = fmt.Sprintf(" at %d/%d", a.Replay.Stop.Index, a.Replay.Stop.Total)
		}
		return RefuseRun, fmt.Sprintf("contested%s: %s; rebase by hand (resume is not built yet)", where, strings.Join(files, ", "))
	case Clean, Recipe:
		return Proceed, ""
	}
	return RefuseRun, "class unknown"
}

func agentLabel(a *Agent) string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Kind != "":
		return a.Kind
	}
	return "?"
}

// Request names one rebase.
type Request struct {
	Path     string
	Branch   string
	Trunk    string
	Onto     string
	Upstream string
	Epoch    int64
}

// StopResult is one place the rebase stopped and what happened there.
type StopResult struct {
	Index, Total int
	Subject      string
	Files        []FileOutcome
}

// Result is what a rebase did.
type Result struct {
	Branch, OldTip, NewTip string
	Safety                 Safety
	Replayed               int
	Stops                  []StopResult
	Restored               bool
	SignaturesDropped      int
}

// Rebase rebases one worktree, applying the declared strategies at each
// stop. An unresolved stop aborts and restores to the safety ref and is a
// normal outcome (Restored); an error is a git failure, after the same
// restore, or a restore that could not be verified.
func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error) {
	res := Result{Branch: req.Branch}
	git := func(args ...string) (string, error) {
		if log != nil && os.Getenv("WT_SYNC_TRACE") != "" {
			fmt.Fprintf(log, "  $ git %s\n", strings.Join(args, " "))
		}
		return gitEnv(req.Path, rebaseEnv, nil, args...)
	}
	old, err := git("rev-parse", "--verify", req.Branch)
	if err != nil {
		return res, err
	}
	res.OldTip = old
	if res.Safety, err = WriteSafety(mainRoot, req.Branch, old, req.Epoch); err != nil {
		return res, err
	}
	base := req.Upstream
	if base == "" {
		base = req.Onto
	}
	if res.SignaturesDropped, err = signedCount(req.Path, base, old); err != nil {
		return res, err
	}

	restore := func() error {
		_, _ = git("rebase", "--abort")
		if busy, _ := RebaseInProgress(req.Path); busy {
			_, _ = git("rebase", "--quit")
		}
		if head, _ := git("rev-parse", "HEAD"); head != old {
			if _, err := git("reset", "--hard", res.Safety.Ref); err != nil {
				return fmt.Errorf("not restored: reset failed: %w; the old tip is %s", err, res.Safety.Ref)
			}
		}
		if busy, _ := RebaseInProgress(req.Path); busy {
			return fmt.Errorf("not restored: a rebase is still in progress; the old tip is %s", res.Safety.Ref)
		}
		if ref, _ := git("symbolic-ref", "--quiet", "HEAD"); ref != "refs/heads/"+req.Branch {
			return fmt.Errorf("not restored: HEAD is %q, not %s; the old tip is %s", ref, req.Branch, res.Safety.Ref)
		}
		if head, _ := git("rev-parse", "HEAD"); head != old {
			return fmt.Errorf("not restored: HEAD is %s, not %s; the old tip is %s", head, short(old), res.Safety.Ref)
		}
		if status, _ := git("--no-optional-locks", "status", "--porcelain", "--untracked-files=no"); status != "" {
			return fmt.Errorf("not restored: tracked changes remain; the old tip is %s", res.Safety.Ref)
		}
		return nil
	}
	fail := func(err error) (Result, error) {
		return res, errors.Join(err, restore())
	}

	args := append(append([]string{}, rebaseConfig...), "rebase", "--no-update-refs", "--no-gpg-sign")
	if req.Upstream != "" {
		args = append(args, "--onto", req.Onto, req.Upstream)
	} else {
		args = append(args, req.Onto)
	}
	_, err = git(args...)
	lastIndex, lastUnmerged := -1, ""
	for err != nil {
		busy, perr := RebaseInProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		if !busy {
			return fail(fmt.Errorf("rebase: %w", err))
		}
		p, perr := RebaseProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		conflicts, cerr := StagedConflicts(req.Path)
		if cerr != nil {
			return fail(cerr)
		}
		unmerged := pathsOf(conflicts)
		if p.Index == lastIndex && (len(conflicts) == 0 || unmerged == lastUnmerged) {
			return fail(fmt.Errorf("rebase did not advance at %d/%d: %w", p.Index, p.Total, err))
		}
		lastIndex, lastUnmerged = p.Index, unmerged
		stop := StopResult{Index: p.Index, Total: p.Total, Subject: p.Subject}
		unresolved := false
		for _, c := range conflicts {
			r, rerr := resolveConflict(mainRoot, req.Trunk, cfg, c, req.Path)
			if rerr != nil {
				stop.Files = append(stop.Files, r.Outcome)
				res.Stops = append(res.Stops, stop)
				return fail(rerr)
			}
			if r.Outcome.Resolved && !r.InPlace {
				if aerr := Apply(req.Path, c, r.Content); aerr != nil {
					return fail(aerr)
				}
			}
			if !r.Outcome.Resolved {
				unresolved = true
			}
			stop.Files = append(stop.Files, r.Outcome)
		}
		if len(conflicts) == 0 {
			// Stopped with nothing unmerged: rerere or a hook did it all, or
			// the sequencer stopped for a reason we do not handle. One
			// --continue is the honest move; the guard above catches a
			// second stop in the same place.
			stop.Files = append(stop.Files, FileOutcome{Path: messagesPath, Note: "stopped with nothing unmerged"})
		}
		logStop(log, stop)
		res.Stops = append(res.Stops, stop)
		if unresolved {
			res.Restored = true
			return res, restore()
		}
		_, err = git("rebase", "--continue")
	}
	if res.NewTip, err = git("rev-parse", "HEAD"); err != nil {
		return fail(err)
	}
	count, err := git("rev-list", "--count", req.Onto+"..HEAD")
	if err != nil {
		return fail(err)
	}
	if res.Replayed, err = strconv.Atoi(count); err != nil {
		return fail(err)
	}
	return res, nil
}

func pathsOf(cs []Conflict) string {
	var ps []string
	for _, c := range cs {
		ps = append(ps, c.Path)
	}
	sort.Strings(ps)
	return strings.Join(ps, "\x00")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func logStop(log io.Writer, s StopResult) {
	if log == nil {
		return
	}
	parts := []string{fmt.Sprintf("  stop %d/%d %q", s.Index, s.Total, s.Subject)}
	for _, f := range s.Files {
		mark, note := "✗", f.Note
		if f.Resolved {
			mark, note = "✓", f.Strategy
		}
		parts = append(parts, f.Path+mark+" "+note)
	}
	fmt.Fprintln(log, strings.Join(parts, "  "))
}

// signedCount counts the commits in base..tip that carry a signature, which a
// rewrite drops (spec §4).
func signedCount(wtPath, base, tip string) (int, error) {
	out, err := gitEnv(wtPath, nil, nil, "log", "--format=%G?", base+".."+tip)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if l != "" && l != "N" {
			n++
		}
	}
	return n, nil
}
```

A restore after an unresolved stop that itself fails verification returns `Restored: true` together with the error: the caller prints `NOT restored` from the error and knows which stop caused it.

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ -run 'Rebase|Preflight' -v 2>&1 | tail -30 && go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/rebase.go internal/wtsync/rebase_test.go
git commit -m "feat(sync): rebase a worktree, resolving each stop or restoring it"
git push origin main
```

---

### Task 6: Deferred steps

**Files:**
- Create: `internal/wtsync/defer.go`
- Test: `internal/wtsync/defer_test.go`

**Interfaces:**
- Consumes: `Deferred` (config.go), `MatchGlob`, `gitEnv`, `rebaseEnv`.
- Produces:
  ```go
  type DeferredResult struct {
      Step     Deferred
      Ran      bool
      Why      string        // when !Ran: "no listed path changed", or "not run: an earlier step failed"
      Output   string        // combined stdout+stderr, trimmed
      Err      error         // the step failed, timed out, or left tracked changes with no commit: declared
      Commit   string        // short sha when Commit: made one
      Files, Insertions, Deletions int
      Elapsed  time.Duration
  }
  func ChangedPaths(wtPath, oldTip, newTip string) ([]string, error)   // git diff --name-only -z old new
  func RunDeferred(wtPath string, steps []Deferred, oldTip, newTip string, log io.Writer) ([]DeferredResult, error)
  ```

**Behaviour:** for each step in order: if `Paths` is non-empty and no changed path matches any → not run, `Why` set. Else run `sh -c <Run>` with cwd = the worktree and `rebaseEnv` in the environment under a 30-minute deadline (`DeferTimeout`, `exec.CommandContext`), capture combined output, measure elapsed. Non-zero exit or timeout → `Err` set (`exit N` / `timed out after 30m0s`), the step is owed, **and no later step runs**: each remaining step gets `Why: "not run: an earlier step failed"`. (A failed step may have half-written tracked files; a later step's `git add -u` would commit that debris under the wrong message. Codex finding 10.) After a successful step: `git --no-optional-locks status --porcelain --untracked-files=no`; if non-empty and `Commit` is set → `git add -u`, `git commit --no-gpg-sign -q -m <Commit>`, record the short sha and `git diff --shortstat HEAD~1 HEAD` numbers; if non-empty and `Commit` is empty → `Err = "left tracked changes but declares no commit:"`, which also stops later steps. The returned error is only for a git failure; step failures live in the results. On a git failure the current result is appended before returning so its output is not lost.

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deferRepo(t *testing.T) (wt, oldTip, newTip string) {
	t.Helper()
	dir := linearRepo(t, []map[string]string{{"gen/in.txt": "trunk\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	w := featureWorktree(t, dir)
	oldTip = gitIn(t, w.Path, "rev-parse", "HEAD")
	gitIn(t, w.Path, "rebase", "-q", "--no-gpg-sign", "main")
	newTip = gitIn(t, w.Path, "rev-parse", "HEAD")
	return w.Path, oldTip, newTip
}

func TestChangedPathsIsTheDiffBetweenTheTips(t *testing.T) {
	wt, old, cur := deferRepo(t)
	got, err := ChangedPaths(wt, old, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "gen/in.txt" {
		t.Fatalf("changed %v", got)
	}
}

func TestRunDeferredRunsAMatchingStepAndCommitsTrackedOutput(t *testing.T) {
	wt, old, cur := deferRepo(t)
	steps := []Deferred{{Run: "cp gen/in.txt gen/out.txt && echo regenerated", Paths: []string{"gen/**"}, Commit: "chore: regenerate"}}
	// gen/out.txt must be tracked for `add -u` to pick it up.
	if err := os.WriteFile(filepath.Join(wt, "gen", "out.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "track out")
	cur = gitIn(t, wt, "rev-parse", "HEAD")
	rs, err := RunDeferred(wt, steps, old, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	if !r.Ran || r.Err != nil || r.Commit == "" || r.Files != 1 || !strings.Contains(r.Output, "regenerated") {
		t.Fatalf("result %+v", r)
	}
	if gitIn(t, wt, "log", "-1", "--format=%s") != "chore: regenerate" {
		t.Fatal("no regeneration commit")
	}
	if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("dirty after: %q", out)
	}
}

func TestRunDeferredSkipsAStepWhoseListedPathsDidNotChange(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "exit 1", Paths: []string{"other/**"}}}, old, cur, nil)
	if err != nil || rs[0].Ran || rs[0].Why == "" {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

func TestRunDeferredAStepWithoutPathsAlwaysRunsAndNeedsNoCommit(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo hi > /dev/null"}}, old, cur, nil)
	if err != nil || !rs[0].Ran || rs[0].Err != nil || rs[0].Commit != "" {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

func TestRunDeferredAFailingStepIsOwedStopsLaterStepsAndTheRebaseStays(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo boom >&2; exit 3"}, {Run: "true"}}, old, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "exit 3") || !strings.Contains(rs[0].Output, "boom") {
		t.Fatalf("rs[0] %+v", rs[0])
	}
	if rs[1].Ran || !strings.Contains(rs[1].Why, "earlier step failed") {
		t.Fatalf("second step ran after a failure: %+v", rs[1])
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != cur {
		t.Fatal("HEAD moved")
	}
}

func TestRunDeferredTimesOut(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := runDeferredWithTimeout(wt, []Deferred{{Run: "sleep 5"}}, old, cur, nil, 300*time.Millisecond)
	if err != nil || rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "timed out") {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

func TestRunDeferredAStepThatDirtiesTrackedFilesWithoutACommitIsReported(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo x >> b.txt"}}, old, cur, nil)
	if err != nil || rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "no commit") {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'Deferred|ChangedPaths' 2>&1 | head`
Expected: `undefined: RunDeferred`.

- [ ] **Step 3: Implement**

```go
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
// step's commit. The error return is for git failing, not for a step failing.
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
			fmt.Fprintf(log, "  defer %s\n", step.Run)
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "sh", "-c", step.Run)
		cmd.Dir = wtPath
		cmd.Env = withEnv(rebaseEnv...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		runErr := cmd.Run()
		cancel()
		r.Elapsed = time.Since(start)
		r.Output = strings.TrimSpace(out.String())
		if runErr != nil {
			var exit *exec.ExitError
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				r.Err = fmt.Errorf("timed out after %s", timeout)
			case errors.As(runErr, &exit):
				r.Err = fmt.Errorf("exit %d", exit.ExitCode())
			default:
				r.Err = runErr
			}
			failed = true
			results = append(results, r)
			continue
		}
		status, err := gitEnv(wtPath, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return append(results, r), err
		}
		switch {
		case status == "":
		case step.Commit == "":
			r.Err = errors.New("left tracked changes but declares no commit:")
			failed = true
		default:
			if _, err := gitEnv(wtPath, rebaseEnv, nil, "add", "-u"); err != nil {
				return append(results, r), err
			}
			if _, err := gitEnv(wtPath, rebaseEnv, nil, "commit", "--no-gpg-sign", "-q", "-m", step.Commit); err != nil {
				return append(results, r), err
			}
			sha, err := gitEnv(wtPath, nil, nil, "rev-parse", "--short", "HEAD")
			if err != nil {
				return append(results, r), err
			}
			r.Commit = sha
			stat, err := gitEnv(wtPath, nil, nil, "diff", "--shortstat", "HEAD~1", "HEAD")
			if err != nil {
				return append(results, r), err
			}
			if m := shortstatRE.FindStringSubmatch(stat); m != nil {
				r.Files, _ = strconv.Atoi(m[1])
				r.Insertions, _ = strconv.Atoi(m[2])
				r.Deletions, _ = strconv.Atoi(m[3])
			}
		}
		results = append(results, r)
	}
	return results, nil
}

func anyMatches(patterns, paths []string) bool {
	for _, p := range paths {
		if matchesAny(patterns, p) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ -run 'Deferred|ChangedPaths' && go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/defer.go internal/wtsync/defer_test.go
git commit -m "feat(sync): run the deferred steps once after a rebase and commit their output"
git push origin main
```

---

### Task 7: Stacks

**Files:**
- Create: `internal/wtsync/stack.go`
- Test: `internal/wtsync/stack_test.go`

**Interfaces:**
- Consumes: `repo.Worktree`, `gitEnv`.
- Produces:
  ```go
  // Parents maps each branch-attached, non-main worktree's branch to the branch
  // of its nearest ancestor among the others, when there is one. A branch whose
  // ancestors are not a chain (it merges two incomparable worktree branches) has
  // no nearest ancestor; it is listed in ambiguous with all its ancestors and run
  // refuses it.
  func Parents(mainRoot string, worktrees []repo.Worktree) (parents map[string]string, ambiguous map[string][]string, err error)
  // Members is the whole stack a branch belongs to: itself, every ancestor and every descendant, transitively.
  func Members(parents map[string]string, branch string) []string
  // Order sorts branches parents first (a topological order over parents). Branches outside parents keep their input order among themselves.
  func Order(parents map[string]string, branches []string) []string
  ```

**Behaviour:** two branches a, b (a ≠ b) among the worktrees: a is an ancestor of b when `git merge-base --is-ancestor a b` exits 0 **and** a's tip is not b's tip (two branches at the same commit are neither parent nor child of each other). b's parent is the nearest ancestor: the candidate `cand` such that every other ancestor `other` of b is an ancestor of `cand` **or sits at the same tip as `cand`**; when several candidates share a tip, the lexically smallest branch name wins (branches are visited in sorted order and the first qualifying one is taken). When no candidate qualifies (b merges two ancestors neither of which contains the other), b is `ambiguous`: it gets no parent, and `run` refuses it with the names. A branch equal to trunk's tip is Current and never a parent, but `Parents` does not know about trunk: the command layer passes only worktrees it will consider.

- [ ] **Step 1: Write the failing tests**

```go
package wtsync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// stackRepo: main; p (1 commit on main); c (1 more on p); d (1 more on c); lone (1 on main).
func stackRepo(t *testing.T) (string, []repo.Worktree) {
	t.Helper()
	dir := linearRepo(t, nil, nil)
	add := func(branch, from, file string) repo.Worktree {
		gitIn(t, dir, "branch", branch, from)
		path := dir + "-" + branch
		gitIn(t, dir, "worktree", "add", "-q", path, branch)
		if err := os.WriteFile(filepath.Join(path, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, path, "add", "-A")
		gitIn(t, path, "commit", "-q", "-m", file)
		return repo.Worktree{Path: path, Branch: branch}
	}
	p := add("p", "main", "p.txt")
	c := add("c", "p", "c.txt")
	d := add("d", "c", "d.txt")
	lone := add("lone", "main", "lone.txt")
	return dir, []repo.Worktree{p, c, d, lone, {Path: dir, Branch: "main", IsMain: true}, {Path: dir + "-x", Detached: true}}
}

func TestParentsFindsTheNearestAncestorAmongWorktrees(t *testing.T) {
	dir, wts := stackRepo(t)
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"c": "p", "d": "c"}
	if !reflect.DeepEqual(got, want) || len(amb) != 0 {
		t.Fatalf("parents %v ambiguous %v, want %v", got, amb, want)
	}
}

func TestParentsReportsAMergeOfTwoBranchesAsAmbiguous(t *testing.T) {
	dir, wts := stackRepo(t)
	// m merges lone and p, which are incomparable.
	gitIn(t, dir, "branch", "m", "lone")
	path := dir + "-m"
	gitIn(t, dir, "worktree", "add", "-q", path, "m")
	gitIn(t, path, "merge", "-q", "--no-edit", "--no-gpg-sign", "p")
	wts = append(wts, repo.Worktree{Path: path, Branch: "m"})
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["m"]; ok {
		t.Fatalf("m got a parent: %v", got)
	}
	if !reflect.DeepEqual(amb["m"], []string{"lone", "p"}) {
		t.Fatalf("ambiguous %v", amb)
	}
}

func TestParentsIgnoresABranchAtTheSameCommit(t *testing.T) {
	dir, wts := stackRepo(t)
	gitIn(t, dir, "branch", "twin", "p")
	gitIn(t, dir, "worktree", "add", "-q", dir+"-twin", "twin")
	wts = append(wts, repo.Worktree{Path: dir + "-twin", Branch: "twin"})
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["twin"]; ok {
		t.Fatalf("twin got a parent: %v", got)
	}
	if got["p"] != "" {
		t.Fatalf("p got a parent: %v", got)
	}
	// c's parent is p, not twin: among candidates at the same tip the
	// lexically smaller name wins, and the shape is not ambiguous.
	if got["c"] != "p" || len(amb) != 0 {
		t.Fatalf("c's parent %q ambiguous %v", got["c"], amb)
	}
}

func TestMembersAndOrder(t *testing.T) {
	parents := map[string]string{"c": "p", "d": "c", "y": "x"}
	if got := Members(parents, "c"); !reflect.DeepEqual(got, []string{"p", "c", "d"}) {
		t.Fatalf("members of c: %v", got)
	}
	if got := Members(parents, "lone"); !reflect.DeepEqual(got, []string{"lone"}) {
		t.Fatalf("members of lone: %v", got)
	}
	got := Order(parents, []string{"d", "lone", "y", "c", "x", "p"})
	pos := map[string]int{}
	for i, b := range got {
		pos[b] = i
	}
	if len(got) != 6 || pos["p"] > pos["c"] || pos["c"] > pos["d"] || pos["x"] > pos["y"] {
		t.Fatalf("order %v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run 'Parents|Members|Order' 2>&1 | head`
Expected: `undefined: Parents`.

- [ ] **Step 3: Implement**

```go
package wtsync

import (
	"sort"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Parents computes the stack relation across branch-attached worktrees: for
// each branch, the nearest other worktree branch that is its ancestor. A
// branch at the very same commit as another is neither parent nor child of
// it. A branch whose ancestors do not form a chain is ambiguous and gets no
// parent. --update-refs cannot do this for branches checked out in
// worktrees (spec §4), which is every branch here, so the relation is
// explicit.
func Parents(mainRoot string, worktrees []repo.Worktree) (map[string]string, map[string][]string, error) {
	var branches []string
	tips := map[string]string{}
	for _, wt := range worktrees {
		if wt.IsMain || wt.Detached || wt.Branch == "" {
			continue
		}
		tip, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", wt.Branch)
		if err != nil {
			return nil, nil, err
		}
		branches = append(branches, wt.Branch)
		tips[wt.Branch] = tip
	}
	sort.Strings(branches)
	isAncestor := func(a, b string) (bool, error) {
		if tips[a] == tips[b] {
			return false, nil
		}
		_, err := gitEnv(mainRoot, nil, nil, "merge-base", "--is-ancestor", a, b)
		if err == nil {
			return true, nil
		}
		if isExit(err, 1) {
			return false, nil
		}
		return false, err
	}
	parents := map[string]string{}
	ambiguous := map[string][]string{}
	for _, b := range branches {
		var ancestors []string
		for _, a := range branches {
			if a == b {
				continue
			}
			ok, err := isAncestor(a, b)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				ancestors = append(ancestors, a)
			}
		}
		if len(ancestors) == 0 {
			continue
		}
		// The nearest ancestor descends from every other ancestor, or shares
		// its tip with it; the first qualifying name in sorted order wins.
		found := false
		for _, cand := range ancestors {
			nearest := true
			for _, other := range ancestors {
				if other == cand || tips[other] == tips[cand] {
					continue
				}
				ok, err := isAncestor(other, cand)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					nearest = false
					break
				}
			}
			if nearest {
				parents[b] = cand
				found = true
				break
			}
		}
		if !found {
			ambiguous[b] = ancestors
		}
	}
	return parents, ambiguous, nil
}

// Members is the stack a branch belongs to, root first, then descendants in
// name order at each depth.
func Members(parents map[string]string, branch string) []string {
	root := branch
	seen := map[string]bool{root: true}
	for p, ok := parents[root]; ok && !seen[p]; p, ok = parents[root] {
		seen[p] = true
		root = p
	}
	children := map[string][]string{}
	for c, p := range parents {
		children[p] = append(children[p], c)
	}
	var out []string
	var walk func(string)
	walk = func(b string) {
		out = append(out, b)
		kids := children[b]
		sort.Strings(kids)
		for _, k := range kids {
			walk(k)
		}
	}
	walk(root)
	return out
}

// Order sorts branches so every parent precedes its children; branches that
// are not in a stack keep their relative input order.
func Order(parents map[string]string, branches []string) []string {
	index := map[string]int{}
	for i, b := range branches {
		index[b] = i
	}
	depth := func(b string) int {
		d := 0
		seen := map[string]bool{}
		for p, ok := parents[b]; ok && !seen[p]; p, ok = parents[p] {
			seen[p] = true
			d++
		}
		return d
	}
	out := append([]string(nil), branches...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := depth(out[i]), depth(out[j])
		if di != dj {
			return di < dj
		}
		return index[out[i]] < index[out[j]]
	})
	return out
}
```

`isExit(err, code)` — add to script.go next to `withEnv` if it does not exist: unwrap `*exec.ExitError` and compare. `gitEnv` wraps the exit error with stderr text via `fmt.Errorf("%s (%s)", msg, err)` which loses the type: change `gitEnv` to wrap with `%w` (`fmt.Errorf("%s (%w)", msg, err)`) so `errors.As` works; check no caller compares the string.

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/stack.go internal/wtsync/stack_test.go internal/wtsync/script.go
git commit -m "feat(sync): find the stacks among worktree branches and order them"
git push origin main
```

---

### Task 8: `wt sync run`

**Files:**
- Create: `internal/commands/sync_run.go`
- Modify: `cmd/wt/sync.go` (add the `run` subcommand; keep the bare verb)
- Test: `internal/commands/sync_run_test.go`

**Interfaces:**
- Consumes: `Locate` (locate.go; it already refuses the main checkout with its own message, so no extra check), `wtsync.Assess`, `Preflight`, `Rebase`, `RunDeferred`, `Parents/Members/Order`, `Acquire/Release/GitDir`, `RebaseInProgress`, `LoadFromTrunk`, `ListAgents`, `git.Run` (internal/git).
- Produces:
  ```go
  type RunOptions struct {
      NoFetch bool
      Yes     bool                                  // skip the multi-worktree confirmation
      Confirm func(works []string) (bool, error)    // nil: never asks (no terminal); the CLI supplies one when stdin is a terminal and !Yes
      Agents  []wtsync.Agent                        // nil: ask `claude agents`; an empty slice means none
      Now     func() time.Time                      // nil: time.Now
  }
  func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error
  ```

**Behaviour, in order:**

1. Unless `NoFetch`: `git fetch --quiet origin <trunk>` in `MainRoot` (one fetch per repo, spec §1). Print `fetched origin/<trunk>` or `against origin/<trunk> (not fetched)`.
2. `trunkSHA := git rev-parse --verify origin/<trunk>`; `cfg := LoadFromTrunk(MainRoot, trunk)` (it reads `origin/<trunk>`, which now *is* `trunkSHA`; the SHA is what every later step uses, so a fetch by someone else mid-run cannot change the declaration under us). `ErrNoConfig` → return `"<repo> declares no .wt-sync.yaml on origin/<trunk>: nothing is rebased"`.
3. Resolve each work with `Locate`. A worktree without a branch is refused.
4. `parents, ambiguous := Parents(MainRoot, allWorktrees)`; a named branch in `ambiguous` is refused up front: `<work> merges <a> and <b>; that shape is not handled`. Expand each named branch with `Members`; if the expansion added branches, print `<work> is a stack with <others>: rebasing all of them`. Dedupe. `Order` the result.
5. Agents: `opts.Agents`, or `ListAgents()`; an error from `ListAgents` is returned as `cannot list agent sessions (<err>); nothing is rebased` (fail closed, Codex finding 19).
6. Assess every participant against `trunkSHA`. For each stack (connected component via `Members`), if any member's `Preflight` is `RefuseRun`, refuse the whole stack: `refused: <member>: <reason>` on each row, nothing in it touched. A `SkipRun` member of a stack is fine: its children rebase onto trunk directly (`Upstream` empty) because a Skip parent is either Current, in which case its children are Current too, or Stale, in which case it contains nothing of its own.
7. **Confirmation:** if more than one branch will actually be rebased (verdict `Proceed`, not poisoned) and `opts.Confirm != nil` and `!opts.Yes`: print `about to rebase: <work1>, <work2>, ...` and call `Confirm`; a `false` answer ends the run with `nothing rebased` and no error.
8. One epoch for the run: `opts.Now().UnixNano()`.
9. **Lock every member of every proceeding stack before rebasing any**: `Acquire(GitDir(path), now)`; a `*LockHeld` (or any error) poisons the whole stack with `locked: <err>` and releases the locks already taken for it. After locking, **re-check** each locked member: `RebaseInProgress` false and `git --no-optional-locks status --porcelain --untracked-files=no` empty; a failure poisons the stack (`changed since triage: <what>`) and releases its locks. Locks are held until the member's deferred steps have finished, then released; on any early return every held lock is released (`defer`).
10. For each branch in order: print the header `<work>  <branch>  <behind> behind, <ahead> ahead`; a poisoned branch prints `  refused: <why>` and counts as a failure; a Skip prints `  skipped: <reason>`. Otherwise build the `Request`: `Trunk = trunkSHA`, `Onto = trunkSHA` for a root or a child whose parent skipped; for a child whose parent was rebased in this run, `Onto = parent's current HEAD` (re-read with `git rev-parse HEAD` in the parent worktree **after** its deferred steps, so a regeneration commit is included; Codex finding 3) and `Upstream = parent's OldTip`. Call `Rebase` with `w` as the log; print `  safety <ref> = <short old tip>` when a safety ref was written.
11. On a `Rebase` error: print `  failed: <err>` (the error text says whether it was restored; a `not restored:` error names the safety ref), count as failure, poison the descendants (`<work> failed`). On `Restored`: print `  restored: <files> at <index>/<total> not resolved; rebase by hand`, count as failure, poison the descendants. Otherwise print `  rebased <n> commit(s) onto <onto label>` (+ `, <k> signature(s) dropped` when k > 0), where the label is `origin/<trunk>` for a root and `<parent work>` for a child; then `RunDeferred` and print each result (`  defer <run>  <elapsed>  committed <files> file(s) (+<ins> −<del>) as <sha>` / `  defer <run>  <elapsed>` / `  defer <run>  skipped: <why>` / `  defer <run>  <elapsed>  OWED: <err>` followed by the last 20 lines of output indented); an owed step counts as a failure but does not poison descendants (the rebase itself is complete and the child rebases onto the parent's HEAD as it stands). Then `  push: git -C <path> push --force-with-lease`.
12. Return an error at the end naming everything refused, restored, failed or owed, so the exit code is non-zero when anything did not complete.

Output shape per worktree:

```
webkey  feat_wt/webkey  113 behind, 27 ahead
  safety refs/wt-sync/feat_wt/webkey/1757430000123456789 = a1b2c3d
  stop 6/27 "chore: webkey openapi" application.yaml✓ owned-line  openapi_remote_v3.json✓ openapi
  rebased 27 commits onto origin/development, 2 signatures dropped
  defer ./gradlew webapp:generateOpenApi  1m43s  committed 2 files (+120 −30) as 9f8e7d6
  push: git -C /Users/.../server_wt/server/feat_wt/webkey push --force-with-lease
```

- [ ] **Step 1: Write the failing tests**

Fixture, in sync_run_test.go. `minimalConf` gives the type suffix `_wt`, so `New(ctx, "feat/bump", ...)` creates the branch **`feat_wt/bump`** with work name `bump`; every assertion below uses the real branch name.

```go
// runFixture: a main checkout that is its own origin; trunk declares v.txt
// owned-line max-plus-patch and, when withDefer, a deferred step that copies
// v.txt to the tracked gen.txt and commits it; worktree bump (feat_wt/bump)
// one commit ahead on v.txt; worktree other (feat_wt/other) current; trunk
// moved on v.txt; origin fetched.
func runFixture(t *testing.T, withDefer bool) (ctx *Context, bump string) {
	t.Helper()
	main := committedRepo(t, minimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"
	if withDefer {
		yaml += "defer:\n  - run: cp v.txt gen.txt\n    paths: [v.txt]\n    commit: \"chore: regen\"\n"
	}
	write(".wt-sync.yaml", yaml)
	write("v.txt", "1.0.0\n")
	write("gen.txt", "stale\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err = New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	gitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := New(ctx, "feat/other", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

func noAgents() RunOptions {
	return RunOptions{NoFetch: true, Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(0, 99) }}
}

func TestSyncRunRebasesARecipeWorktreeAndRunsTheDeferredStep(t *testing.T) {
	ctx, bump := runFixture(t, true)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"safety refs/wt-sync/feat_wt/bump/99", "stop 1/1", "v.txt✓ owned-line", "rebased 1 commit", "committed 1 file", "as ", "push: git -C"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("no regeneration commit")
	}
	if got, _ := os.ReadFile(filepath.Join(bump, "gen.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("gen.txt %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(gitOut(t, bump, "rev-parse", "--absolute-git-dir"), wtsync.LockName+"*")); len(left) != 0 {
		t.Fatalf("lock left behind: %v", left)
	}
}

func TestSyncRunRefusesADirtyWorktreeAndTouchesNothing(t *testing.T) {
	ctx, bump := runFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "tracked changes") {
		t.Fatalf("err %v out %s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesAWorktreeWithAnAgent(t *testing.T) {
	ctx, bump := runFixture(t, false)
	resolved, _ := filepath.EvalSymlinks(bump)
	opts := noAgents()
	opts.Agents = []wtsync.Agent{{Name: "bump-1", Cwd: resolved}}
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "bump-1") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

func TestSyncRunRefusesWhenAgentsCannotBeListed(t *testing.T) {
	// Agents nil means "ask claude agents"; make that fail by pointing PATH
	// at an empty directory so the claude binary is not found.
	ctx, bump := runFixture(t, false)
	t.Setenv("PATH", t.TempDir())
	opts := noAgents()
	opts.Agents = nil
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "agent sessions") {
		t.Fatalf("err %v", err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
}

func TestSyncRunSkipsACurrentWorktreeWithoutASafetyRef(t *testing.T) {
	ctx, _ := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"other"}, noAgents(), &out); err != nil {
		t.Fatalf("a skip is not an error: %v", err)
	}
	if !strings.Contains(out.String(), "skipped:") {
		t.Fatalf("out %s", out.String())
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesWhenTrunkDeclaresNothing(t *testing.T) {
	main := committedRepo(t, minimalConf)
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "declares no") {
		t.Fatalf("err %v", err)
	}
}

// stackFixture adds worktree child (feat_wt/child) on top of feat_wt/bump.
func stackFixture(t *testing.T, ctx *Context) (child string) {
	t.Helper()
	var buf bytes.Buffer
	child, err := New(ctx, "feat/child", NewOptions{NoSetup: true, Base: "feat_wt/bump"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, child, "add", "-A")
	gitOut(t, child, "commit", "-q", "-m", "child")
	return child
}

func TestSyncRunRebasesAStackParentFirstAndChildOntoTheParentsFinalTip(t *testing.T) {
	ctx, bump := runFixture(t, true) // with the deferred commit, so the parent's tip moves after its rebase
	child := stackFixture(t, ctx)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"child"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "is a stack with") {
		t.Fatalf("out %s", out.String())
	}
	parentTip := gitOut(t, bump, "rev-parse", "HEAD")
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("parent has no regeneration commit")
	}
	// The child's own commit sits directly on the parent's final tip (the
	// child's deferred step does not fire: its rebase changed no v.txt of its own
	// relative to its parent, and gen.txt is already regenerated there).
	if gitOut(t, child, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("child not on the parent's final tip:\n%s", out.String())
	}
	if !gitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatal("parent not on trunk")
	}
	refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(refname)", "refs/wt-sync/")
	if !strings.Contains(refs, "feat_wt/child/99") || !strings.Contains(refs, "feat_wt/bump/99") {
		t.Fatalf("safety refs %q", refs)
	}
}

func TestSyncRunARefusedStackMemberDefersTheWholeStack(t *testing.T) {
	ctx, bump := runFixture(t, false)
	child := stackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldBump, oldChild := gitOut(t, bump, "rev-parse", "HEAD"), gitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "child: tracked changes") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != oldBump || gitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunAsksOnceForMoreThanOneWorktreeAndStopsOnNo(t *testing.T) {
	ctx, bump := runFixture(t, false)
	stackFixture(t, ctx)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var asked []string
	opts := noAgents()
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("a declined confirmation is not an error: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(out.String(), "nothing rebased") {
		t.Fatalf("asked %v out %s", asked, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved after no")
	}
	// --yes never asks.
	opts.Yes = true
	asked = nil
	out.Reset()
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if asked != nil {
		t.Fatal("asked despite --yes")
	}
}

func TestSyncRunRestoresAndReportsALaterUnclaimedStop(t *testing.T) {
	// First stop is recipe (v.txt), so triage says recipe and run starts;
	// the branch's second commit conflicts on a.txt, which nobody claims.
	ctx, bump := runFixture(t, false)
	main := ctx.Repo.MainRoot
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "a on branch")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "a on trunk")
	gitOut(t, main, "fetch", "-q", "origin")
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "restored: a.txt at 2/2") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
	if !strings.Contains(gitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-sync/"), "feat_wt/bump/99") {
		t.Fatal("the safety ref, the undo target, is missing")
	}
}
```

`gitAncestor(t, dir, a, b) bool` is a three-line helper around `git merge-base --is-ancestor` using `exec.Command` directly (exit 1 is false, anything else fatal). `a.txt` must exist in the fixture's base for the restore test to be a modify/modify conflict: add `write("a.txt", "a\n")` to `runFixture` before the `declare` commit.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/commands/ -run SyncRun 2>&1 | head`
Expected: `undefined: SyncRun`.

- [ ] **Step 3: Implement sync_run.go**

```go
package commands

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// RunOptions tunes SyncRun for callers and tests.
type RunOptions struct {
	NoFetch bool
	Yes     bool
	Confirm func(works []string) (bool, error)
	Agents  []wtsync.Agent
	Now     func() time.Time
}

type participant struct {
	wt      repo.Worktree
	work    string
	a       wtsync.Assessment
	verdict wtsync.Verdict
	reason  string
	lock    *wtsync.Lock
	result  *wtsync.Result
	head    string // HEAD after the rebase and the deferred steps; what a child rebases onto
}

// SyncRun rebases the named worktrees (and the stacks they belong to) onto
// origin/<trunk>: safety ref, strategies at each stop, deferred steps once
// at the end. Anything refused, restored, failed or owed is reported and
// makes the returned error non-nil, so a script sees it.
func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error {
	trunk := ctx.Config.MainBranch
	onto := "origin/" + trunk
	if opts.NoFetch {
		fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	} else {
		if _, err := git.Run(ctx.Repo.MainRoot, "fetch", "--quiet", "origin", trunk); err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		fmt.Fprintf(w, "fetched %s\n", onto)
	}
	trunkSHA, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", onto)
	if err != nil {
		return fmt.Errorf("%s is not known here; run git fetch origin", onto)
	}
	cfg, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, trunk)
	if errors.Is(err, wtsync.ErrNoConfig) {
		return fmt.Errorf("%s declares no %s on %s: nothing is rebased", ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	if err != nil {
		return err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if !wt.IsMain && wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	var named []string
	for _, arg := range works {
		wt, err := Locate(ctx, arg)
		if err != nil {
			return err
		}
		if wt.Branch == "" {
			return fmt.Errorf("%s has no branch", arg)
		}
		named = append(named, wt.Branch)
	}
	parents, ambiguous, err := wtsync.Parents(ctx.Repo.MainRoot, worktrees)
	if err != nil {
		return err
	}
	for _, b := range named {
		if anc, ok := ambiguous[b]; ok {
			return fmt.Errorf("%s merges %s; that shape is not handled", workName(ctx, b), strings.Join(anc, " and "))
		}
	}
	seen := map[string]bool{}
	var branches []string
	for _, b := range named {
		var added []string
		for _, m := range wtsync.Members(parents, b) {
			if seen[m] {
				continue
			}
			seen[m] = true
			branches = append(branches, m)
			if m != b {
				added = append(added, workName(ctx, m))
			}
		}
		if len(added) > 0 {
			fmt.Fprintf(w, "%s is a stack with %s: rebasing all of them\n", workName(ctx, b), strings.Join(added, ", "))
		}
	}
	branches = wtsync.Order(parents, branches)

	agents := opts.Agents
	if agents == nil {
		if agents, err = wtsync.ListAgents(); err != nil {
			return fmt.Errorf("cannot list agent sessions (%v); nothing is rebased", err)
		}
	}
	parts := map[string]*participant{}
	for _, b := range branches {
		p := &participant{wt: byBranch[b], work: workName(ctx, b)}
		p.a = wtsync.Assess(ctx.Repo.MainRoot, trunkSHA, cfg, p.wt, agents)
		p.verdict, p.reason = wtsync.Preflight(p.a)
		parts[b] = p
	}
	// A refused member poisons its stack: never half-apply (spec §4).
	poisoned := map[string]string{}
	poison := func(b, why string) {
		for _, m := range wtsync.Members(parents, b) {
			if poisoned[m] == "" {
				poisoned[m] = why
			}
		}
	}
	for _, b := range branches {
		if parts[b].verdict == wtsync.RefuseRun {
			poison(b, parts[b].work+": "+parts[b].reason)
		}
	}
	var going []string
	for _, b := range branches {
		if parts[b].verdict == wtsync.Proceed && poisoned[b] == "" {
			going = append(going, parts[b].work)
		}
	}
	if len(going) > 1 && opts.Confirm != nil && !opts.Yes {
		fmt.Fprintf(w, "about to rebase: %s\n", strings.Join(going, ", "))
		ok, err := opts.Confirm(going)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "nothing rebased")
			return nil
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	epoch := now().UnixNano()

	// Lock every member of every proceeding stack before touching any, and
	// re-check what triage saw: the lock is what makes the check hold.
	release := func(b string) {
		if p := parts[b]; p != nil && p.lock != nil {
			_ = p.lock.Release()
			p.lock = nil
		}
	}
	defer func() {
		for _, b := range branches {
			release(b)
		}
	}()
	for _, b := range branches {
		p := parts[b]
		if poisoned[b] != "" || p.verdict != wtsync.Proceed {
			continue
		}
		gitDir, err := wtsync.GitDir(p.wt.Path)
		if err != nil {
			return err
		}
		lock, err := wtsync.Acquire(gitDir, now())
		if err != nil {
			poison(b, p.work+": locked: "+err.Error())
			continue
		}
		p.lock = lock
		if busy, err := wtsync.RebaseInProgress(p.wt.Path); err != nil || busy {
			poison(b, p.work+": changed since triage: a rebase is in progress")
			continue
		}
		if status, err := git.Run(p.wt.Path, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no"); err != nil || status != "" {
			poison(b, p.work+": changed since triage: tracked changes")
			continue
		}
	}
	for _, b := range branches {
		if poisoned[b] != "" {
			release(b)
		}
	}

	var failures []string
	for _, b := range branches {
		p := parts[b]
		fmt.Fprintf(w, "%s  %s  %d behind, %d ahead\n", p.work, b, p.a.Behind, p.a.Ahead)
		if why, ok := poisoned[b]; ok {
			fmt.Fprintf(w, "  refused: %s\n", why)
			failures = append(failures, p.work)
			continue
		}
		if p.verdict == wtsync.SkipRun {
			fmt.Fprintf(w, "  skipped: %s\n", p.reason)
			continue
		}
		req := wtsync.Request{Path: p.wt.Path, Branch: b, Trunk: trunkSHA, Onto: trunkSHA, Epoch: epoch}
		ontoLabel := onto
		if parent, ok := parents[b]; ok {
			if pp := parts[parent]; pp != nil && pp.result != nil && !pp.result.Restored && pp.head != "" {
				req.Onto, req.Upstream, ontoLabel = pp.head, pp.result.OldTip, pp.work
			}
		}
		res, rerr := wtsync.Rebase(ctx.Repo.MainRoot, cfg, req, w)
		p.result = &res
		if res.Safety.Ref != "" {
			fmt.Fprintf(w, "  safety %s = %s\n", res.Safety.Ref, short(res.OldTip))
		}
		if rerr != nil {
			fmt.Fprintf(w, "  failed: %v\n", rerr)
			failures = append(failures, p.work)
			poison(b, p.work+" failed")
			release(b)
			continue
		}
		if res.Restored {
			last := res.Stops[len(res.Stops)-1]
			var files []string
			for _, f := range last.Files {
				if !f.Resolved {
					files = append(files, f.Path)
				}
			}
			fmt.Fprintf(w, "  restored: %s at %d/%d not resolved; rebase by hand\n", strings.Join(files, ", "), last.Index, last.Total)
			failures = append(failures, p.work)
			poison(b, p.work+" was restored")
			release(b)
			continue
		}
		line := fmt.Sprintf("  rebased %d commit%s onto %s", res.Replayed, plural(res.Replayed), ontoLabel)
		if res.SignaturesDropped > 0 {
			line += fmt.Sprintf(", %d signature%s dropped", res.SignaturesDropped, plural(res.SignaturesDropped))
		}
		fmt.Fprintln(w, line)
		results, err := wtsync.RunDeferred(p.wt.Path, cfg.Defer, res.OldTip, res.NewTip, nil)
		if err != nil {
			return err
		}
		for _, d := range results {
			printDeferred(w, d)
			if d.Err != nil {
				failures = append(failures, p.work+" (owed: "+d.Step.Run+")")
			}
		}
		if p.head, err = git.Run(p.wt.Path, "rev-parse", "HEAD"); err != nil {
			return err
		}
		release(b)
		fmt.Fprintf(w, "  push: git -C %s push --force-with-lease\n", p.wt.Path)
	}
	if len(failures) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(failures, ", "))
	}
	return nil
}

func printDeferred(w io.Writer, d wtsync.DeferredResult) {
	switch {
	case !d.Ran:
		fmt.Fprintf(w, "  defer %s  skipped: %s\n", d.Step.Run, d.Why)
	case d.Err != nil:
		fmt.Fprintf(w, "  defer %s  %s  OWED: %v\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Err)
		for _, l := range lastLines(d.Output, 20) {
			fmt.Fprintf(w, "    %s\n", l)
		}
	case d.Commit != "":
		fmt.Fprintf(w, "  defer %s  %s  committed %d file%s (+%d −%d) as %s\n", d.Step.Run, d.Elapsed.Round(time.Second), d.Files, plural(d.Files), d.Insertions, d.Deletions, d.Commit)
	default:
		fmt.Fprintf(w, "  defer %s  %s\n", d.Step.Run, d.Elapsed.Round(time.Second))
	}
}

func lastLines(s string, n int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
```

`Members` when a branch has no entry in `parents` and no children returns just itself, so `poison` is safe for lone branches. `git.Run` is `internal/git`'s helper (`Run(dir string, args ...string) (string, error)`); check whether it already trims output and returns stderr in its error the way `gitEnv` does, and adapt the `status` check accordingly. If `commands` already has a `short`/`plural` helper, reuse it rather than redefining.

In `cmd/wt/sync.go`, keep `newSyncCmd`'s bare `RunE` and `Args: cobra.NoArgs` (an unknown verb then errors, which is right) and add:

```go
	var noFetch, yes bool
	run := &cobra.Command{
		Use:   "run <work>...",
		Short: "Rebase the named worktrees onto trunk with the declared strategies",
		Long: "Fetch trunk once, then for each named worktree (and the rest of any\n" +
			"stack it belongs to, parents first): pin the old tip under\n" +
			"refs/wt-sync/<branch>/<epoch>, rebase with --no-update-refs --no-gpg-sign,\n" +
			"apply the declared strategy at every stop, and run the deferred steps\n" +
			"once at the end, committing their output when it changes tracked files.\n" +
			"A stop no strategy resolves aborts the rebase and restores the worktree;\n" +
			"a failed deferred step is reported as owed and never undoes the rebase.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one a Claude\n" +
			"session is in (Codex sessions are not detected), class divergent, class\n" +
			"contested (rebase those by hand; resume is not built yet), and any\n" +
			"repository whose trunk declares no .wt-sync.yaml. When more than one\n" +
			"worktree would be rebased you are asked once; --yes skips that. Nothing\n" +
			"is pushed: the last line per worktree is the push command to run.",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RunOptions{NoFetch: noFetch, Yes: yes}
			if !yes && isTerminal(os.Stdin) {
				opts.Confirm = confirmRun(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			return commands.SyncRun(ctx, args, opts, cmd.OutOrStdout())
		},
	}
	run.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	run.Flags().BoolVar(&yes, "yes", false, "do not ask before rebasing more than one worktree")
	sync.AddCommand(run)
```

`confirmRun(in io.Reader, out io.Writer) func([]string) (bool, error)` prints `rebase these <n> worktrees? [y/N] ` and reads one line, `y`/`yes` (case-insensitive) is true; model it on `confirmRemoval` in remove.go. `completeWork` completes only the first argument; that is acceptable for now and noted in the handoff.

- [ ] **Step 4: Test, lint, and build**

Run: `go test -race ./internal/commands/ -run SyncRun -v 2>&1 | tail -20 && go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run ./... && go build -o bin/wt ./cmd/wt && bin/wt sync run --help | head -5`
Expected: PASS, clean, the help prints. Do **not** run `bin/wt sync run` against a real repository in this task; the controller does that after Task 11 on a worktree Anders picks.

- [ ] **Step 5: Commit and push**

```bash
git add internal/commands/sync_run.go internal/commands/sync_run_test.go cmd/wt/sync.go
git commit -m "feat(sync): wt sync run rebases worktrees and their stacks onto trunk"
git push origin main
```

---

### Task 9: `wt sync undo`

**Files:**
- Create: `internal/wtsync/undo.go`, `internal/commands/sync_undo.go`
- Modify: `cmd/wt/sync.go`
- Test: `internal/wtsync/undo_test.go`, `internal/commands/sync_undo_test.go`

**Interfaces:**
- Produces:
  ```go
  // wtsync
  type Restored struct { Branch, From, To, Ref string; Path string /* "" when the branch has no checkout */ }
  func Undo(mainRoot string, worktrees []repo.Worktree, agents []Agent, branch string, now time.Time) ([]Restored, error)
  // commands
  type UndoOptions struct { Agents []wtsync.Agent /* nil: ask */; Now func() time.Time }
  func SyncUndo(ctx *Context, work string, opts UndoOptions, w io.Writer) error
  ```

**Behaviour:** `LatestSafety(branch)` → its epoch; every safety ref with that epoch is the same run (one epoch per run, Task 8). The set of checkouts is every worktree **including the main checkout** (Codex finding 9: a branch a run rewrote may later be checked out in the main checkout; resetting it there through `update-ref` would move the branch under a live checkout with no dirt check). For each ref, find the checkout on that branch. Before touching anything: lock every such checkout (`Acquire`; a `*LockHeld` refuses the whole undo), then refuse the whole undo if any is dirty (`git --no-optional-locks status --porcelain --untracked-files=no` non-empty), mid-rebase (`RebaseInProgress`), or has an agent in it (`AgentAt`). Then per ref: with a checkout, `git reset --hard <tip>` there (moves the branch, restores the tree); without one, `git update-ref refs/heads/<branch> <tip> <current>` (the current value as the old-value guard). Locks released on every path (`defer`). The safety refs are kept (retention drops them later); a second undo of the same epoch is a no-op that reports `already at <tip>` per branch. No safety ref for the branch → error `no run to undo for <branch>`.

- [ ] **Step 1: Write the failing tests**

```go
// undo_test.go
func TestUndoResetsEveryBranchOfTheNewestEpoch(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") == old {
		t.Fatal("rebase did nothing; the test is vacuous")
	}
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}, {Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].To != old || got[0].Path != wt {
		t.Fatalf("restored %+v", got)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "status", "--porcelain") != "" {
		t.Fatal("not restored cleanly")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".git", "worktrees", "*", LockName+"*")); len(left) != 0 {
		t.Fatalf("lock left behind: %v", left)
	}
}

func TestUndoRestoresTwoBranchesThatShareAnEpoch(t *testing.T) {
	// Two safety refs written by one run (same epoch) on two branches; both
	// branches then moved; undoing either restores both.
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "branch", "second", "feature")
	oldF, oldS := gitIn(t, dir, "rev-parse", "feature"), gitIn(t, dir, "rev-parse", "second")
	for _, b := range []string{"feature", "second"} {
		if _, err := WriteSafety(dir, b, gitIn(t, dir, "rev-parse", b), 77); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "moved")
	gitIn(t, dir, "update-ref", "refs/heads/second", "main")
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "second", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || gitIn(t, dir, "rev-parse", "feature") != oldF || gitIn(t, dir, "rev-parse", "second") != oldS {
		t.Fatalf("restored %+v; feature %s second %s", got, gitIn(t, dir, "rev-parse", "feature"), gitIn(t, dir, "rev-parse", "second"))
	}
}

func TestUndoRefusesADirtyCheckoutBeforeTouchingAnything(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	after := gitIn(t, wt, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
	if err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("err %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != after {
		t.Fatal("HEAD moved despite the refusal")
	}
}

func TestUndoRefusesACheckoutWithAnAgent(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(wt)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, []Agent{{Name: "f-1", Cwd: resolved}}, "feature", time.Now())
	if err == nil || !strings.Contains(err.Error(), "f-1") {
		t.Fatalf("err %v", err)
	}
}

func TestUndoRestoresTheMainCheckoutLikeAnyOther(t *testing.T) {
	// The main checkout sits on a branch a run pinned; undo resets it there
	// (with the dirt check), never through update-ref.
	dir, _, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, dir, "rev-parse", "main")
	if _, err := WriteSafety(dir, "main", old, 3); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "moved")
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}}, nil, "main", time.Now())
	if err != nil || len(got) != 1 || got[0].Path != dir || gitIn(t, dir, "rev-parse", "HEAD") != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoWithoutARunIsAnError(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
	if err == nil || !strings.Contains(err.Error(), "no run to undo") {
		t.Fatalf("err %v", err)
	}
}

func TestUndoRestoresABranchWithNoCheckoutThroughUpdateRef(t *testing.T) {
	dir, _, _ := runRepo(t, nil, nil)
	gitIn(t, dir, "branch", "loose", "main")
	old := gitIn(t, dir, "rev-parse", "loose")
	if _, err := WriteSafety(dir, "loose", old, 4); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-ref", "refs/heads/loose", "feature")
	got, err := Undo(dir, nil, nil, "loose", time.Now())
	if err != nil || len(got) != 1 || got[0].Path != "" || gitIn(t, dir, "rev-parse", "loose") != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoASecondTimeIsANoOp(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	if _, err := Undo(dir, wts, nil, "feature", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := Undo(dir, wts, nil, "feature", time.Now())
	if err != nil || len(got) != 1 || got[0].From != old || got[0].To != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run Undo 2>&1 | head`
Expected: `undefined: Undo`.

- [ ] **Step 3: Implement**

```go
// undo.go
package wtsync

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Restored is one ref put back.
type Restored struct {
	Branch, From, To, Ref string
	Path                  string
}

// Undo resets every ref the newest run touching branch moved: all safety
// refs sharing that run's epoch. Every checkout involved, the main one
// included, is locked and checked (clean, not mid-rebase, no agent) before
// anything is reset.
func Undo(mainRoot string, worktrees []repo.Worktree, agents []Agent, branch string, now time.Time) ([]Restored, error) {
	latest, ok, err := LatestSafety(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no run to undo for %s", branch)
	}
	all, err := ListSafety(mainRoot)
	if err != nil {
		return nil, err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			byBranch[wt.Branch] = wt
		}
	}
	var run []Safety
	for _, s := range all {
		if s.Epoch == latest.Epoch {
			run = append(run, s)
		}
	}
	var locks []*Lock
	defer func() {
		for _, l := range locks {
			_ = l.Release()
		}
	}()
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		lock, err := Acquire(gitDir, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		locks = append(locks, lock)
		agentPath := wt.Path
		if resolved, err := filepath.EvalSymlinks(wt.Path); err == nil {
			agentPath = resolved
		}
		if a := AgentAt(agents, agentPath); a != nil {
			return nil, fmt.Errorf("%s: an agent session is in it: %s; nothing undone", s.Branch, agentLabel(a))
		}
		out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return nil, err
		}
		if out != "" {
			return nil, fmt.Errorf("%s has tracked changes; nothing undone", s.Branch)
		}
		if busy, err := RebaseInProgress(wt.Path); err != nil {
			return nil, err
		} else if busy {
			return nil, fmt.Errorf("%s is mid-rebase; nothing undone", s.Branch)
		}
	}
	var out []Restored
	for _, s := range run {
		from, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", s.Branch)
		if err != nil {
			return out, err
		}
		r := Restored{Branch: s.Branch, From: from, To: s.Tip, Ref: s.Ref}
		if wt, ok := byBranch[s.Branch]; ok {
			r.Path = wt.Path
			if from != s.Tip {
				if _, err := gitEnv(wt.Path, rebaseEnv, nil, "reset", "--hard", s.Tip); err != nil {
					return out, err
				}
			}
		} else if from != s.Tip {
			if _, err := gitEnv(mainRoot, nil, nil, "update-ref", "refs/heads/"+s.Branch, s.Tip, from); err != nil {
				return out, err
			}
		}
		out = append(out, r)
	}
	return out, nil
}
```

`errors` is imported for `errors.Is` if the implementer adds a `LockHeld` special case in the message; drop the import otherwise.

`SyncUndo(ctx, work, opts, w)`: `Locate` → branch; agents from `opts.Agents` or `ListAgents()` (an error refuses: `cannot list agent sessions (<err>); nothing undone`); `Undo(MainRoot, worktrees, agents, branch, now)`; print one line per restored ref: `<work>  <from short> → <to short>  (<ref>)` or `<work>  already at <to short>`. Cobra subcommand `undo <work>` with Short `Put back every ref the last run on this worktree moved`. Command test: run `SyncRun` on `runFixture(t, true)`, then `SyncUndo`, assert HEAD back at the pre-run tip, the regeneration commit gone, and the output line; a second `SyncUndo` prints `already at`.

- [ ] **Step 4: Test and lint**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/undo.go internal/wtsync/undo_test.go internal/commands/sync_undo.go internal/commands/sync_undo_test.go cmd/wt/sync.go
git commit -m "feat(sync): wt sync undo puts back every ref the last run moved"
git push origin main
```

---

### Task 10: `wt sync doctor`

**Files:**
- Create: `internal/wtsync/doctor.go`, `internal/commands/sync_doctor.go`
- Modify: `cmd/wt/sync.go`
- Test: `internal/wtsync/doctor_test.go`, `internal/commands/sync_doctor_test.go`

**Interfaces:**
- Produces:
  ```go
  // wtsync
  type Check struct { Name string; OK bool; Detail string; Fix func() error /* nil when nothing to fix */ }
  type DoctorOptions struct { Now time.Time; Keep time.Duration /* 30 days */; Docker func() error /* nil: run `docker info` */ }
  func Doctor(mainRoot, trunk string, worktrees []repo.Worktree, opts DoctorOptions) ([]Check, error)
  // commands
  type DoctorOptions struct { Fix, Prune bool }
  func SyncDoctor(ctx *Context, opts DoctorOptions, w io.Writer) error
  ```

**Checks, in this order, each one `Check`:**

| name | OK when | detail / fix |
|---|---|---|
| `trunk` | `origin/<trunk>` resolves | else `run git fetch origin` |
| `declaration` | `LoadFromTrunk` returns a config | `ErrNoConfig`: `no .wt-sync.yaml on origin/<trunk>: reported only, never rebased`; a parse error: its text |
| `scripts` | every `script` rule's `run` exists on `origin/<trunk>` with mode 100755 (`git ls-tree origin/<trunk> -- <run>`) | names the missing or non-executable ones |
| `rerere` | `git config --get rerere.enabled` is `true` | Fix: `git config rerere.enabled true`; detail notes that autoupdate is passed per run |
| `hooks` | no active `pre-rebase` or `post-rewrite` hook. The hooks dir is `core.hooksPath` when set (a relative value is relative to the worktree root, so resolve it against `mainRoot`), else `git rev-parse --git-path hooks` run in `mainRoot` (linked worktrees share the main repository's hooks); an active hook is an executable file without the `.sample` suffix | lists the active ones; also lists `commit-msg` and `pre-commit` as a note (OK stays true) because the deferred commit must satisfy them and a failing one turns into a "did not advance" restore |
| `submodules` | no `.gitmodules` on `origin/<trunk>` | else `submodules are not handled by run` |
| `lfs` | `git grep -l -e filter=lfs origin/<trunk> -- '.gitattributes' '**/.gitattributes'` finds nothing (nested attribute files count) | else `LFS paths are not handled by run: <files>` |
| `docker` | only checked when `cfg.Defer` is non-empty; `docker info` under a 10-second deadline (or `opts.Docker`) succeeds | `deferred steps that need Docker will be owed` |
| `safety-refs` | nothing `Prunable` | `N refs pin old history (<list>)`; Fix: delete them |
| `locks` | no `wt-sync.lock` in any worktree's git dir older than `LockExpiry`, and no live one | names them; Fix: remove expired ones |

`Doctor` never fixes by itself; `SyncDoctor` prints a table `CHECK  STATE  DETAIL` and runs `Fix` for `rerere` and `locks` under `--fix`, for `safety-refs` under `--prune`, printing `fixed <name>` after each. Exit code: non-nil error when any check that has no fix is not OK and blocks a run (`trunk`, `declaration`, `scripts`); everything else is advisory. `run` does not call `Doctor`: doctor is the thing to run once before the first run in a repository and after anything changes, and its blocking checks are the ones `run` refuses on its own anyway (a missing trunk ref, no declaration, a missing script all surface as refusals). The `hooks`, `submodules` and `lfs` checks are advisory by design; they say what a rebase will meet, not whether it is allowed.

- [ ] **Step 1: Write the failing tests**

```go
// doctor_test.go
func TestDoctorOnAHealthyRepoIsAllOK(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "config", "rerere.enabled", "true")
	// runRepo pins core.hooksPath to the repository's own hooks dir, so an
	// ambient global hooks path cannot make this test's hooks check fail.
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if !c.OK {
			t.Errorf("%s: %s", c.Name, c.Detail)
		}
	}
	names := []string{}
	for _, c := range checks {
		names = append(names, c.Name)
	}
	want := []string{"trunk", "declaration", "scripts", "rerere", "hooks", "submodules", "lfs", "safety-refs", "locks"} // docker only with defer steps
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("checks %v", names)
	}
}

func TestDoctorFlagsRerereOffWithAFix(t *testing.T) { /* runRepo without rerere → check rerere !OK, Fix != nil; call Fix; git config rerere.enabled == true */ }
func TestDoctorFlagsAnActivePreRebaseHook(t *testing.T) { /* write an executable pre-rebase into the dir core.hooksPath names (runRepo set it) → hooks !OK, detail names it */ }
func TestDoctorResolvesARelativeHooksPathAgainstTheMainRoot(t *testing.T) { /* git config core.hooksPath myhooks; write myhooks/post-rewrite executable → hooks !OK names post-rewrite */ }
func TestDoctorFlagsAMissingScriptOnTrunk(t *testing.T) { /* declaration with strategy: script run: bin/none → scripts !OK, detail names bin/none */ }
func TestDoctorListsPrunableSafetyRefsWithAFix(t *testing.T) { /* two WriteSafety for feature, epochs 40 and 50 days ago, now → safety-refs !OK names the older; Fix deletes exactly it */ }
func TestDoctorFlagsAnExpiredLock(t *testing.T) { /* Acquire with Started 31 min ago in the worktree's git dir → locks !OK; Fix removes it */ }
func TestDoctorChecksDockerOnlyWhenStepsAreDeferred(t *testing.T) { /* declaration with a defer step, Docker returns an error → check docker present and !OK; without defer → no docker check */ }
func TestDoctorFlagsSubmodulesAndLFSOnTrunk(t *testing.T) { /* commit .gitmodules and a nested sub/.gitattributes with filter=lfs on main, fetch → both !OK; lfs detail names sub/.gitattributes */ }
```

Write each sketched test in full.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run Doctor 2>&1 | head`
Expected: `undefined: Doctor`.

- [ ] **Step 3: Implement**

`doctor.go` builds the checks in the table order with small helpers: `trunkCheck`, `declarationCheck` (returns the cfg too, for `scripts` and `docker`), `scriptsCheck(cfg)`, `rerereCheck`, `hooksCheck`, `treeFileCheck(name, path, detail)` for submodules and lfs (`git cat-file -e origin/<trunk>:<path>`; for lfs also `git show` and `strings.Contains(..., "filter=lfs")`), `dockerCheck(opts.Docker)` where the default runs `exec.Command("docker", "info")` with stdout/stderr discarded, `safetyCheck(opts)` using `ListSafety` + `Prunable` with a Fix that deletes each, `locksCheck(worktrees, opts.Now)` using `GitDir` + `ReadLock` where a lock older than `LockExpiry` is expired (Fix removes) and a younger one is reported as live (no Fix). Every helper returns a `Check`; `Doctor` returns them in order and only errors on a git failure that stops it reading the repository at all.

`sync_doctor.go`:

```go
type DoctorOptions struct{ Fix, Prune bool }

func SyncDoctor(ctx *Context, opts DoctorOptions, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	checks, err := wtsync.Doctor(ctx.Repo.MainRoot, ctx.Config.MainBranch, worktrees, wtsync.DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour})
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tSTATE\tDETAIL")
	var blocking []string
	for _, c := range checks {
		state := "ok"
		if !c.OK {
			state = "warn"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, state, oneLine(c.Detail))
		if !c.OK && c.Fix == nil && (c.Name == "trunk" || c.Name == "declaration" || c.Name == "scripts") {
			blocking = append(blocking, c.Name)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, c := range checks {
		if c.OK || c.Fix == nil {
			continue
		}
		want := (opts.Fix && c.Name != "safety-refs") || (opts.Prune && c.Name == "safety-refs")
		if !want {
			continue
		}
		if err := c.Fix(); err != nil {
			return fmt.Errorf("fix %s: %w", c.Name, err)
		}
		fmt.Fprintf(w, "fixed %s\n", c.Name)
	}
	if len(blocking) > 0 {
		return fmt.Errorf("run is blocked by: %s", strings.Join(blocking, ", "))
	}
	return nil
}
```

Cobra: `doctor` subcommand with `--fix` and `--prune`, Short `Check what a run needs: trunk, declaration, scripts, rerere, hooks, submodules, LFS, Docker, safety refs, locks`. Command test: healthy fixture prints nine `ok` rows; a fixture with rerere off and `--fix` prints `fixed rerere`.

- [ ] **Step 4: Test and lint**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit and push**

```bash
git add internal/wtsync/doctor.go internal/wtsync/doctor_test.go internal/commands/sync_doctor.go internal/commands/sync_doctor_test.go cmd/wt/sync.go
git commit -m "feat(sync): wt sync doctor checks what a run needs and fixes what it can"
git push origin main
```

---

### Task 11: Help, README, the bats smoke test, and the handoff

**Files:**
- Modify: `cmd/wt/sync.go` (the bare verb's Long text), `README.md` (surfaces table), `test/` (one bats case), `~/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md` (append what landed)

- [ ] **Step 1: Rewrite the bare `wt sync` help**

Replace the paragraph that says run, resume, undo and doctor "are not built yet" with the flow as it now stands:

```
The flow is look, act, finish.
  look    wt sync                 this table; read-only
  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps;
                                  asks once when more than one worktree is involved (--yes skips)
  finish  push with --force-with-lease; wt sync undo <work> puts every ref back
          wt sync doctor          what a run needs, and --fix / --prune
A contested worktree is refused by run until resume exists: rebase it by hand.
Only Claude sessions are detected in WHO; a Codex session is not seen.
```

Keep the Classes block. Update `Short` of `sync` to `Show what rebasing each worktree onto trunk would do; run, undo, doctor act`.

- [ ] **Step 2: README**

In the surfaces table add rows for `wt sync run <work>...`, `wt sync undo <work>`, `wt sync doctor`, one line each, matching the phrasing of the existing `wt sync` row.

- [ ] **Step 3: One bats case**

Look at `test/` for how an existing case builds a repository (`wt new` etc.). Add `test/sync_run.bats` with one case: a repo whose trunk declares `v.txt` owned-line max-plus-patch, a worktree one commit ahead on `v.txt`, trunk bumped; `bin/wt sync` shows `recipe`; `bin/wt sync run <work> --no-fetch` exits 0 and prints `rebased 1 commit`; `bin/wt sync` afterwards prints nothing for that worktree (it is current); `bin/wt sync undo <work>` prints the arrow line and `bin/wt sync` shows `recipe` again. Run: `make bats`.

- [ ] **Step 4: Full check, then the handoff**

Run: `make check` (lint, test, bats). Expected: clean.

Append to the handoff a section `## What landed for run (date)`: the commit list, the surfaces, what is refused, and that the first real test is the controller running `wt sync doctor` then `wt sync run webkey` in `server` (no agent on it at the time of writing; re-check with `wt sync`), with `wt sync undo webkey` as the way back. Note the residuals: resume and the plan file are next.

- [ ] **Step 5: Commit and push**

```bash
git add cmd/wt/sync.go README.md test/sync_run.bats
git commit -m "docs(sync): state the look, act, finish flow now that run exists"
git push origin main
```

---

## Self-review

**Codex review, 2026-09-09 (thread `01a08630-d497-7f13-ba22-f5b5414db019`), what changed:** the verdict constants no longer shadow `Refuse`; the loop has a did-not-advance guard and no `--skip`; a stack child rebases onto its parent's HEAD after the deferred commits and reads scripts from `Request.Trunk`; trunk is fetched, resolved to one SHA and only then read; the epoch is nanoseconds and retention keeps run groups whole; every stack member is locked and re-checked before any moves, locks held through deferred steps; the lock is link-then-rename atomic and `Release` checks ownership; deferred steps stop after a failure; the multi-worktree confirmation with `--yes` exists; undo covers the main checkout, locks and checks agents; `Parents` handles equal tips and reports ambiguous shapes; `Apply` no longer chmods; scripts and deferred steps have deadlines; agent-list failure refuses; restore is verified; the rebase runs with `-c rebase.backend=merge` and friends; fixtures corrected (`w.txt` in the base, no `cherry-pick -q`, `feat_wt/` branch names, a two-stop restore case). Pushed back on: contested stays refused in v1 (accepted by the reviewer as coherent); doctor stays advisory except for the three blocking checks; worktree-less dependent refs stay out of scope (listed below).

**Spec coverage (§3, §4, §7):**
- §3 `defer` keyed on changed paths, `commit:` only on tracked changes, a failed step owed and never undoing: Task 6. Config and scripts from trunk: inherited (`LoadFromTrunk`, `materialise`), Task 4 keeps it for `--resolve`.
- §4 command flags: Task 5. `--no-update-refs` plus explicit stack handling, parents first, child onto the new parent, whole stack deferred on one refusal: Tasks 7–8. Signing: `--no-gpg-sign` and the dropped-signature count, Task 5. Safety ref and undo of every ref: Tasks 1, 9. Retention: Tasks 1, 10. rerere: recomputed from the three stages rather than trusted (`--rerere-autoupdate` deliberately not passed, see Global Constraints); doctor enabling `rerere.enabled`: Task 10. Lock: Tasks 2, 8, 10. Hooks, submodules, LFS preflight: Task 10.
- §7 `run`, `undo`, `doctor`: Tasks 8–10. Everything else in §7 is listed as out of scope in the Global Constraints.
- Worktree-less refs inside the range (§4, "reported, with an opt-in to advance them"): **not covered.** A follow-up; noted for the handoff in Task 11. Cost: a dead local branch pointing into the rewritten range keeps its old SHA, which is what `--no-update-refs` guarantees today by hand.

**Placeholder scan:** the sketched tests in Tasks 8–10 are marked "write in full" with their assertions stated; no TBDs.

**Type consistency:** `Resolution{Outcome, Content, InPlace}` (Task 3) is what Task 5 reads; `Result.Safety` is a `Safety` (Task 1); `Request.Trunk/Onto/Upstream` (Task 5) is what Task 8 fills from `parents` (Task 7), `trunkSHA` and the parent's `head`; `Verdict` values are `Proceed/SkipRun/RefuseRun` everywhere; `Parents` returns three values in Task 7 and Task 8; `Undo` takes `agents` and `now` in Task 9's tests and command; `DeferredResult` fields printed in Task 8 match Task 6; `Check{Name, OK, Detail, Fix}` (Task 10) is what `SyncDoctor` reads. Edits to foundation code: `tryStrategy` moves (Task 3), `linearRepo` delegates to `repoWith` (Task 3), `gitEnv` wraps with `%w` (Task 7), `Script` gains a deadline (Task 4).
