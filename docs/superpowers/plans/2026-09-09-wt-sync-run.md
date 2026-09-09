# wt sync run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `wt sync run <work>...` rebases named worktrees onto trunk: a safety ref at the old tip, the declared strategies applied at each real stop, the deferred steps run once at the end and committed when they change tracked files, an unresolved stop aborted and restored, stacks rebased in order. `wt sync undo <work>` puts every ref a run moved back. `wt sync doctor` checks the preconditions.

**Architecture:** The foundation (`internal/wtsync`: config from trunk, strategies as pure functions over three blobs, the object-store simulation, `Assess`) stays untouched in behaviour. This plan adds, in the same package, the pieces that write: safety refs, a lock, reading a live rebase stop from a worktree's index and staging a strategy's answer, the rebase loop, deferred steps, stack ordering, undo, and doctor checks. `internal/commands/sync_run.go`, `sync_undo.go` and `sync_doctor.go` orchestrate and print; `cmd/wt/sync.go` gains the three subcommands. `wt sync` with no verb remains read-only and is not changed except for its help text.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3`, git ≥ 2.40 (Anders runs 2.55). Tests are `go test` with throwaway repositories built the way `internal/wtsync/replay_test.go` (`linearRepo`, `gitIn`) and `internal/commands/sync_test.go` (`syncRepo`, `gitOut`) build them.

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` §3 (`defer`, `commit:`, a failed deferred step, config from trunk), §4 (rebase execution: command, stacks, signing, safety ref, rerere, lock, hooks), §7 (surfaces). The foundation plan `docs/superpowers/plans/2026-09-09-wt-sync-foundation.md` and the handoff `~/programmering/telcred/misc/handoffs/2026-09-09-wt-sync-run-plan.md` carry the rulings this plan inherits.

## Global Constraints

- **Only `run`, `undo` and `doctor --fix/--prune` write.** `wt sync` with no verb stays read-only; every `git status` it runs keeps `--no-optional-locks`.
- **A safety ref is written before any ref moves** (`refs/wt-sync/<branch>/<epoch>`, spec §4). Nothing rebases without one. `undo` restores to the newest.
- **The rebase command is exactly** `git rebase --no-update-refs --no-gpg-sign --rerere-autoupdate <onto>` (or `--onto <onto> <upstream>` for a stack child), run in the worktree with `GIT_EDITOR=true` in the environment, so no editor and no signer can ever prompt.
- **The config and any `script` are read from `origin/<trunk>`**, never from the worktree being rebased (spec §3).
- **A strategy refuses rather than guesses.** An unresolved stop aborts the rebase and resets to the safety ref; the worktree is left exactly as it was found. Nothing is ever hand-merged by the tool.
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
  script.go          + Script.ResolveInWorktree: `<exe> --resolve <path>` against the worktree's real index
  script_test.go     + its tests
  rebase.go          Verdict/Preflight; Request, Result, StopResult; Rebase: the loop, abort and restore, signatures
  rebase_test.go
  defer.go           RunDeferred: changed paths old..new, run, commit when tracked files change, owed on failure
  defer_test.go
  stack.go           Parents, Members, Order: the ancestor relation across branch-attached worktrees
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
  func WriteSafety(mainRoot, branch, tip string, epoch int64) (Safety, error)
  func ListSafety(mainRoot string) ([]Safety, error)          // newest first
  func LatestSafety(mainRoot, branch string) (Safety, bool, error)
  func Prunable(all []Safety, now time.Time, keep time.Duration) []Safety
  func DeleteSafety(mainRoot string, s Safety) error
  ```

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

func TestPrunableKeepsTheNewestPerBranchAndAnythingYoung(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	day := int64(86400)
	all := []Safety{
		{Branch: "a", Epoch: now.Unix() - 1*day},  // newest for a, young
		{Branch: "a", Epoch: now.Unix() - 40*day}, // old: prunable
		{Branch: "b", Epoch: now.Unix() - 50*day}, // old but newest for b: kept
		{Branch: "b", Epoch: now.Unix() - 60*day}, // prunable
		{Branch: "c", Epoch: now.Unix() - 2*day},  // young
		{Branch: "c", Epoch: now.Unix() - 3*day},  // young, not newest: kept because young
	}
	got := Prunable(all, now, 30*24*time.Hour)
	if len(got) != 2 || got[0].Epoch != now.Unix()-40*day || got[1].Epoch != now.Unix()-60*day {
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

// Safety is one pinned tip: the branch it belonged to, when it was pinned,
// and where.
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

// Prunable applies the retention rule: keep the newest per branch and
// anything younger than keep; everything else pins abandoned history and
// blocks garbage collection (spec §4).
func Prunable(all []Safety, now time.Time, keep time.Duration) []Safety {
	newest := map[string]int64{}
	for _, s := range all {
		if s.Epoch > newest[s.Branch] {
			newest[s.Branch] = s.Epoch
		}
	}
	var out []Safety
	for _, s := range all {
		if s.Epoch == newest[s.Branch] {
			continue
		}
		if now.Sub(time.Unix(s.Epoch, 0)) < keep {
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
  func (l *Lock) Release() error
  type LockHeld struct { Lock }                                    // error
  func ReadLock(gitDir string) (*Lock, bool, error)
  ```

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
	if err := second.Release(); err != nil {
		t.Fatal(err)
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

// Acquire creates the lock with O_EXCL. An existing lock younger than
// LockExpiry is respected; an older one is replaced.
func Acquire(gitDir string, now time.Time) (*Lock, error) {
	path := filepath.Join(gitDir, LockName)
	owner := "?"
	if u, err := user.Current(); err == nil {
		owner = u.Username
	}
	l := &Lock{Path: path, PID: os.Getpid(), Started: now, Owner: owner}
	body := fmt.Sprintf("pid=%d\nstart=%d\nowner=%s\n", l.PID, now.Unix(), owner)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.WriteString(body)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
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
		// Expired, or unreadable: take it over.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not acquire %s", path)
}

// Release removes the lock.
func (l *Lock) Release() error {
	err := os.Remove(l.Path)
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
Expected: PASS, clean. gosec may flag the 0o644 (G306 is excluded) and the file path from a variable (G304): if G304 fires, add a `//nolint:gosec // the path is the worktree's own git dir` on the ReadFile line.

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
  func RebaseInProgress(wtPath string) (bool, error)             // rebase-merge dir exists
  func RebaseProgress(wtPath string) (Progress, error)           // msgnum, end, stopped-sha, message
  func Apply(wtPath string, c Conflict, content []byte) error     // write with the branch side's mode, git add -- path
  // resolve.go
  type Resolution struct { Outcome FileOutcome; Content []byte; InPlace bool }
  func resolveConflict(mainRoot, onto string, cfg *Config, c Conflict, wtPath string) (Resolution, error)
  ```
  `tryStrategy(mainRoot, onto, cfg, c)` keeps its signature and calls `resolveConflict(..., "")`, so triage.go's behaviour and tests are unchanged. `InPlace` is reserved for Task 4 (a script that resolves in the worktree itself); Task 3 always returns it false.

`Conflict` gains one field: `Mode string` — the branch side's mode (`100644` or `100755`) so `Apply` can preserve executability. `mergeTree` in replay.go does not set it (the simulation never writes); `StagedConflicts` does.

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
	if string(c.Base) != "1.0.0\n" || string(c.Trunk) != "1.0.5\n" || string(c.Branch) != "1.0.1\n" || c.Mode != "100644" || c.Incomplete != "" {
		t.Fatalf("stages %q %q %q mode %s incomplete %q", c.Base, c.Trunk, c.Branch, c.Mode, c.Incomplete)
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

func TestApplyWritesTheContentWithTheBranchModeAndStagesIt(t *testing.T) {
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	cs, _ := StagedConflicts(wt)
	if err := Apply(wt, cs[0], []byte("1.0.6\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("file %q", got)
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
		c.Mode = s[3].mode
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

// RebaseInProgress reports whether the worktree is mid-rebase.
func RebaseInProgress(wtPath string) (bool, error) {
	dir, err := rebaseDir(wtPath)
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
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
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

// Apply writes a strategy's answer over the conflicted file, with the mode
// the replayed commit gave it, and stages it, which clears the unmerged
// entries.
func Apply(wtPath string, c Conflict, content []byte) error {
	mode := os.FileMode(0o644)
	if c.Mode == "100755" {
		mode = 0o755
	}
	full := filepath.Join(wtPath, filepath.FromSlash(c.Path))
	if err := os.WriteFile(full, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(full, mode); err != nil {
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

Delete the old `tryStrategy` body from triage.go. Add `Mode string` to `Conflict` in conflict.go with the comment `// Mode is the replayed commit's mode for the path (100644/100755); set by StagedConflicts, unset by the simulation.`

- [ ] **Step 5: Run the whole package, then lint**

Run: `go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: every existing triage test still passes; the new ones pass.

- [ ] **Step 6: Commit and push**

```bash
git add internal/wtsync/stage.go internal/wtsync/stage_test.go internal/wtsync/resolve.go internal/wtsync/triage.go internal/wtsync/conflict.go internal/wtsync/config_test.go
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
	cmd := exec.Command(exe, "--resolve", path)
	cmd.Dir = wtPath
	cmd.Env = withEnv("GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
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

Update the `Script` type comment: `--check` through a temporary index at triage, `--resolve` in the worktree during a run.

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
- Consumes: `Assessment` (triage.go), `WriteSafety` (Task 1), `StagedConflicts`, `RebaseInProgress`, `RebaseProgress`, `Apply` (Task 3), `resolveConflict` (Tasks 3–4), `gitEnv`.
- Produces:
  ```go
  type Verdict int
  const ( Go Verdict = iota; Skip; Refuse )
  func Preflight(a Assessment) (Verdict, string)

  var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

  type Request struct {
      Path     string   // the worktree
      Branch   string
      Onto     string   // origin/<trunk>, or a stack parent's new tip
      Upstream string   // "" for a plain rebase; the parent's old tip for a stack child (git rebase --onto Onto Upstream)
      Epoch    int64
  }
  type StopResult struct { Index, Total int; Subject string; Files []FileOutcome; Skipped bool }
  type Result struct {
      Branch, OldTip, NewTip string
      Safety   Safety
      Replayed int
      Stops    []StopResult
      Restored bool          // an unresolved stop: aborted and reset to OldTip
      SignaturesDropped int
  }
  func Rebase(mainRoot string, cfg *Config, req Request, log io.Writer) (Result, error)
  ```

**Behaviour, exactly:**

1. `OldTip = rev-parse <branch>` in the worktree; `Safety = WriteSafety(mainRoot, branch, OldTip, epoch)`.
2. `SignaturesDropped` = the number of commits in `<merge-base(Onto or Upstream, OldTip)>..OldTip` whose `%G?` is not `N` (a signature cannot survive rewriting; spec §4).
3. Run `git rebase --no-update-refs --no-gpg-sign --rerere-autoupdate <Onto>` (or `--onto <Onto> <Upstream>`) in the worktree with `rebaseEnv`.
4. While the command exits non-zero and `RebaseInProgress` is true: read `RebaseProgress` and `StagedConflicts`; for each conflict call `resolveConflict(mainRoot, req.Onto, cfg, c, req.Path)`; a resolved, non-InPlace answer is `Apply`ed. Record a `StopResult`. If any file is unresolved → step 6. Otherwise, if `git diff --cached --quiet` succeeds (the resolution made the commit empty, e.g. take-trunk on a commit that changed only that file) run `git rebase --skip` and mark the stop `Skipped`; else `git rebase --continue`. Loop.
5. When the command exits zero and no rebase is in progress: `NewTip = rev-parse HEAD`, `Replayed = rev-list --count <Onto>..HEAD`. Return.
6. Unresolved: `git rebase --abort`. Then, whatever abort said, verify `rev-parse HEAD == OldTip`; if not, `git rebase --quit` (ignore its error) and `git reset --hard <Safety.Ref>`. Set `Restored = true`; the last `StopResult` carries the unresolved files. Return with `err == nil`: an unresolved stop is a normal outcome, not a failure. Any *other* git failure (the rebase command failing without a rebase in progress, a cat-file error, a continue that fails while files are all staged) does the same restore and returns the error.
7. Every `git` call is logged to `log` as one line `  $ git <args>` only when `log` is non-nil and `WT_SYNC_TRACE` is set; otherwise `log` receives one line per stop: `  stop 6/27 "subject" application.yaml ✓ owned-line  spec.json ✗ unclaimed`. The command layer prints the rest.

`Preflight(a)`:

| condition | verdict | reason |
|---|---|---|
| `a.Err != nil` | Refuse | `assessment failed: <err>` |
| `a.Class == Detached` | Refuse | `no branch` |
| `a.NoConfig` | Refuse | `<repo> declares no .wt-sync.yaml on trunk` (the caller substitutes the name; the reason here is `no declaration on trunk`) |
| `a.Dirty` | Refuse | `tracked changes in the worktree` |
| `a.Agent != nil` | Refuse | `an agent session is in it: <name>` |
| `a.Class == Current` | Skip | `already on trunk` |
| `a.Class == Stale` | Skip | `nothing ahead of trunk` |
| `a.Class == Divergent` | Refuse | `divergent: <first reason>` |
| `a.Class == Contested` | Refuse | `contested at <index>/<total>: <unresolved files>; rebase by hand (resume is not built yet)` |
| `a.Class == Unknown` | Refuse | `class unknown` |
| `Clean`, `Recipe` | Go | "" |

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
// origin/main exists, and a worktree on feature.
func runRepo(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir string, wt string, cfg *Config) {
	t.Helper()
	dir = linearRepo(t, trunkEdits, branchEdits)
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

func TestRebaseReplaysACleanBranchAndPinsASafetyRef(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 42}, &log)
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
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 1}, &log)
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

func TestRebaseSkipsACommitTheResolutionMadeEmpty(t *testing.T) {
	// The branch's only change to w.txt is taken from trunk: the commit
	// becomes empty and is skipped rather than failing --continue.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"w.txt": "trunk\n"}},
		[]map[string]string{{"w.txt": "branch\n"}, {"b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || len(res.Stops) != 1 || !res.Stops[0].Skipped {
		t.Fatalf("result %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "w.txt")); string(got) != "trunk\n" {
		t.Fatalf("w.txt %q", got)
	}
}

func TestRebaseAbortsAndRestoresOnAnUnclaimedStop(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 1 || res.Stops[0].Files[0].Resolved || res.Stops[0].Files[0].Note != "unclaimed" {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "rev-parse", "feature") != old {
		t.Fatal("not restored to the old tip")
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase left in progress")
	}
	if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("worktree not clean: %q", out)
	}
}

func TestRebaseRestoresWhenAStrategyRefusesMidway(t *testing.T) {
	// Second stop is a refusal (v.txt with two version lines is not the owned
	// shape once the branch changed a second line too); the first stop's
	// resolution must not survive.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}, {"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 2 || !res.Stops[0].Files[0].Resolved || res.Stops[1].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored")
	}
}

func TestRebaseACommitAlreadyOnTrunkIsDroppedNotCounted(t *testing.T) {
	dir, wt, cfg := runRepo(t, nil, nil)
	// Cherry-pick the branch's commit onto trunk so the rebase drops it.
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "noop")
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "add c")
	gitIn(t, dir, "cherry-pick", "-q", "feature")
	gitIn(t, dir, "fetch", "-q", "origin")
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 9}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestRebaseOntoAParentTipUsesUpstream(t *testing.T) {
	// A stack child: rebase only the child's own commits onto the parent's
	// new tip. Built by hand: parent branch p with one commit, child c on
	// top with one more, then p rewritten (amended) to a new tip.
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
	// Rewrite p onto origin/main in the main checkout (simulating the parent's run).
	gitIn(t, dir, "checkout", "-q", "p")
	gitIn(t, dir, "rebase", "-q", "--no-gpg-sign", "origin/main")
	newParent := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	res, err := Rebase(dir, cfg, Request{Path: wt, Branch: "c", Onto: newParent, Upstream: oldParent, Epoch: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || gitIn(t, wt, "rev-parse", "HEAD~1") != newParent {
		t.Fatalf("result %+v parent %s", res, gitIn(t, wt, "rev-parse", "HEAD~1"))
	}
}

func TestPreflightOrdersItsReasons(t *testing.T) {
	cases := []struct {
		a      Assessment
		v      Verdict
		reason string
	}{
		{Assessment{Class: Clean, Dirty: true}, Refuse, "tracked changes"},
		{Assessment{Class: Current, Dirty: true}, Refuse, "tracked changes"},
		{Assessment{Class: Recipe, Agent: &Agent{Name: "x-1"}}, Refuse, "x-1"},
		{Assessment{Class: Recipe, NoConfig: true}, Refuse, "no declaration"},
		{Assessment{Class: Current}, Skip, "already on trunk"},
		{Assessment{Class: Stale}, Skip, "nothing ahead"},
		{Assessment{Class: Divergent, Divergent: []string{"openapi refuses spec.json"}}, Refuse, "openapi refuses"},
		{Assessment{Class: Contested, Replay: Replay{Stop: &Stop{Index: 2, Total: 5}}, Files: []FileOutcome{{Path: "x.java", Note: "unclaimed"}}}, Refuse, "2/5"},
		{Assessment{Class: Recipe}, Go, ""},
		{Assessment{Class: Clean}, Go, ""},
		{Assessment{Class: Detached}, Refuse, "no branch"},
		{Assessment{Class: Unknown}, Refuse, "unknown"},
	}
	for i, c := range cases {
		v, reason := Preflight(c.a)
		if v != c.v || !strings.Contains(reason, c.reason) {
			t.Errorf("case %d: got %v %q, want %v containing %q", i, v, reason, c.v, c.reason)
		}
	}
}
```

Note for the implementer: `TestRebaseRestoresWhenAStrategyRefusesMidway` relies on `a.txt` being unclaimed at the second stop, not on a refusal; rename the test to `...OnASecondUnclaimedStop` and keep the assertion that the first stop's resolution is gone. `TestRebaseACommitAlreadyOnTrunkIsDroppedNotCounted`: the `noop` empty commit is dropped by rebase by default too; `Replayed` counts `origin/main..HEAD`, which is 0 after both are dropped. If git keeps the empty commit on your version, add `--empty=drop` to the rebase command and note it in the plan's Global Constraints line.

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
	"strings"
)

// Verdict is Preflight's answer.
type Verdict int

const (
	Go     Verdict = iota // rebase it
	Skip                  // nothing to do; say why
	Refuse                // must not be touched; say why
)

// rebaseEnv keeps every rebase step from ever prompting.
var rebaseEnv = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

// Preflight decides from a triage assessment whether a run may start. The
// checks that make a worktree untouchable come before the class, so a dirty
// worktree is refused for its dirt whatever its class.
func Preflight(a Assessment) (Verdict, string) {
	switch {
	case a.Err != nil:
		return Refuse, "assessment failed: " + a.Err.Error()
	case a.Class == Detached:
		return Refuse, "no branch"
	case a.NoConfig:
		return Refuse, "no declaration on trunk"
	case a.Dirty:
		return Refuse, "tracked changes in the worktree"
	case a.Agent != nil:
		return Refuse, "an agent session is in it: " + agentLabel(a.Agent)
	}
	switch a.Class {
	case Current:
		return Skip, "already on trunk"
	case Stale:
		return Skip, "nothing ahead of trunk"
	case Divergent:
		reason := "divergent"
		if len(a.Divergent) > 0 {
			reason += ": " + a.Divergent[0]
		}
		return Refuse, reason
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
		return Refuse, fmt.Sprintf("contested%s: %s; rebase by hand (resume is not built yet)", where, strings.Join(files, ", "))
	case Clean, Recipe:
		return Go, ""
	}
	return Refuse, "class unknown"
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
	Onto     string
	Upstream string
	Epoch    int64
}

// StopResult is one place the rebase stopped and what happened there.
type StopResult struct {
	Index, Total int
	Subject      string
	Files        []FileOutcome
	Skipped      bool
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
// restore.
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
		if head, err := git("rev-parse", "HEAD"); err == nil && head == old {
			if ok, _ := RebaseInProgress(req.Path); !ok {
				return nil
			}
		}
		_, _ = git("rebase", "--quit")
		_, err := git("reset", "--hard", res.Safety.Ref)
		return err
	}
	fail := func(err error) (Result, error) {
		return res, errors.Join(err, restore())
	}

	args := []string{"rebase", "--no-update-refs", "--no-gpg-sign", "--rerere-autoupdate"}
	if req.Upstream != "" {
		args = append(args, "--onto", req.Onto, req.Upstream)
	} else {
		args = append(args, req.Onto)
	}
	_, err = git(args...)
	for err != nil {
		inProgress, perr := RebaseInProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		if !inProgress {
			return fail(fmt.Errorf("rebase: %w", err))
		}
		p, perr := RebaseProgress(req.Path)
		if perr != nil {
			return fail(perr)
		}
		stop := StopResult{Index: p.Index, Total: p.Total, Subject: p.Subject}
		conflicts, cerr := StagedConflicts(req.Path)
		if cerr != nil {
			return fail(cerr)
		}
		unresolved := false
		for _, c := range conflicts {
			r, rerr := resolveConflict(mainRoot, req.Onto, cfg, c, req.Path)
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
			// the sequencer stopped for a reason we do not handle. Either
			// way, continuing is the only honest move; a failure restores.
			stop.Files = append(stop.Files, FileOutcome{Path: messagesPath, Note: "stopped with nothing unmerged"})
		}
		logStop(log, stop)
		if unresolved {
			res.Stops = append(res.Stops, stop)
			res.Restored = true
			if rerr := restore(); rerr != nil {
				return res, rerr
			}
			return res, nil
		}
		if _, derr := git("diff", "--cached", "--quiet"); derr == nil {
			stop.Skipped = true
			res.Stops = append(res.Stops, stop)
			_, err = git("rebase", "--skip")
			continue
		}
		res.Stops = append(res.Stops, stop)
		_, err = git("rebase", "--continue")
	}
	if res.NewTip, err = git("rev-parse", "HEAD"); err != nil {
		return fail(err)
	}
	count, err := git("rev-list", "--count", req.Onto+"..HEAD")
	if err != nil {
		return fail(err)
	}
	fmt.Sscanf(count, "%d", &res.Replayed)
	return res, nil
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

`messagesPath` exists in triage.go (the synthetic path for a stop with no files); reuse it. Replace the `fmt.Sscanf` with `strconv.Atoi` and handle its error. `git("diff", "--cached", "--quiet")` exits 1 when there are staged changes: `gitEnv` returns an error then, which is the signal, so the `derr == nil` branch is "nothing staged".

- [ ] **Step 4: Test and lint**

Run: `go test -race ./internal/wtsync/ -run 'Rebase|Preflight' -v 2>&1 | tail -30 && go test -race ./internal/wtsync/ && gofmt -l . && go vet ./... && golangci-lint run ./...`
Expected: PASS, clean. If `--rerere-autoupdate` is rejected on the test machine's git, the version floor in this plan is wrong: stop and report.

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
      Why      string        // when !Ran: "no listed path changed"
      Output   string        // combined stdout+stderr, trimmed
      Err      error         // the step failed, or left tracked changes with no commit: declared
      Commit   string        // short sha when Commit: made one
      Files, Insertions, Deletions int
      Elapsed  time.Duration
  }
  func ChangedPaths(wtPath, oldTip, newTip string) ([]string, error)   // git diff --name-only -z old new
  func RunDeferred(wtPath string, steps []Deferred, oldTip, newTip string, log io.Writer) ([]DeferredResult, error)
  ```

**Behaviour:** for each step in order: if `Paths` is non-empty and no changed path matches any → not run, `Why` set. Else run `sh -c <Run>` with cwd = the worktree and `rebaseEnv` in the environment, capture combined output, measure elapsed. Non-zero exit → `Err` set (`exit N`), the step is owed; continue with the next step (a later step may not depend on it, and the report shows both). After a successful step: `git status --porcelain --untracked-files=no`; if non-empty and `Commit` is set → `git add -u`, `git commit --no-gpg-sign -q -m <Commit>`, record the short sha and `git diff --shortstat HEAD~1 HEAD` numbers; if non-empty and `Commit` is empty → `Err = "left tracked changes but declares no commit:"`. The returned error is only for a git failure; step failures live in the results.

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

func TestRunDeferredAFailingStepIsOwedAndTheRebaseStays(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo boom >&2; exit 3"}, {Run: "true"}}, old, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "exit 3") || !strings.Contains(rs[0].Output, "boom") {
		t.Fatalf("rs[0] %+v", rs[0])
	}
	if !rs[1].Ran || rs[1].Err != nil {
		t.Fatalf("second step did not run: %+v", rs[1])
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != cur {
		t.Fatal("HEAD moved")
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

// RunDeferred runs the steps whose paths the rebase touched, in order, and
// commits a step's output when it changes tracked files and the step
// declares a message. A failed step is owed, never undone; the next step
// still runs. The error return is for git failing, not for a step failing.
func RunDeferred(wtPath string, steps []Deferred, oldTip, newTip string, log io.Writer) ([]DeferredResult, error) {
	changed, err := ChangedPaths(wtPath, oldTip, newTip)
	if err != nil {
		return nil, err
	}
	var results []DeferredResult
	for _, step := range steps {
		r := DeferredResult{Step: step}
		if len(step.Paths) > 0 && !anyMatches(step.Paths, changed) {
			r.Why = "no listed path changed"
			results = append(results, r)
			continue
		}
		r.Ran = true
		if log != nil {
			fmt.Fprintf(log, "  defer %s\n", step.Run)
		}
		start := time.Now()
		cmd := exec.Command("sh", "-c", step.Run)
		cmd.Dir = wtPath
		cmd.Env = withEnv(rebaseEnv...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		runErr := cmd.Run()
		r.Elapsed = time.Since(start)
		r.Output = strings.TrimSpace(out.String())
		if runErr != nil {
			var exit *exec.ExitError
			if errors.As(runErr, &exit) {
				r.Err = fmt.Errorf("exit %d", exit.ExitCode())
			} else {
				r.Err = runErr
			}
			results = append(results, r)
			continue
		}
		status, err := gitEnv(wtPath, nil, nil, "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return results, err
		}
		switch {
		case status == "":
		case step.Commit == "":
			r.Err = errors.New("left tracked changes but declares no commit:")
		default:
			if _, err := gitEnv(wtPath, rebaseEnv, nil, "add", "-u"); err != nil {
				return results, err
			}
			if _, err := gitEnv(wtPath, rebaseEnv, nil, "commit", "--no-gpg-sign", "-q", "-m", step.Commit); err != nil {
				return results, err
			}
			sha, err := gitEnv(wtPath, nil, nil, "rev-parse", "--short", "HEAD")
			if err != nil {
				return results, err
			}
			r.Commit = sha
			stat, err := gitEnv(wtPath, nil, nil, "diff", "--shortstat", "HEAD~1", "HEAD")
			if err != nil {
				return results, err
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
  // of its nearest ancestor among the others, when there is one.
  func Parents(mainRoot string, worktrees []repo.Worktree) (map[string]string, error)
  // Members is the whole stack a branch belongs to: itself, every ancestor and every descendant, transitively.
  func Members(parents map[string]string, branch string) []string
  // Order sorts branches parents first (a topological order over parents). Branches outside parents keep their input order among themselves.
  func Order(parents map[string]string, branches []string) []string
  ```

**Behaviour:** two branches a, b (a ≠ b) among the worktrees: a is an ancestor of b when `git merge-base --is-ancestor a b` exits 0 **and** a is not equal to b's tip (a branch pointing at the same commit as another is neither parent nor child; report neither). b's parent is the ancestor that every other ancestor of b is also an ancestor of, i.e. the nearest. A branch equal to trunk's tip is Current and never a parent, but `Parents` does not know about trunk: the command layer passes only worktrees it will consider.

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
	got, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"c": "p", "d": "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parents %v, want %v", got, want)
	}
}

func TestParentsIgnoresABranchAtTheSameCommit(t *testing.T) {
	dir, wts := stackRepo(t)
	gitIn(t, dir, "branch", "twin", "p")
	gitIn(t, dir, "worktree", "add", "-q", dir+"-twin", "twin")
	wts = append(wts, repo.Worktree{Path: dir + "-twin", Branch: "twin"})
	got, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["twin"]; ok {
		t.Fatalf("twin got a parent: %v", got)
	}
	if got["p"] != "" {
		t.Fatalf("p got a parent: %v", got)
	}
	// c's parent is still p, not twin, and the choice is deterministic: the
	// lexically smaller name wins among equals.
	if got["c"] != "p" {
		t.Fatalf("c's parent %q", got["c"])
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
// branch at the very same commit as another is neither parent nor child.
// --update-refs cannot do this for branches checked out in worktrees (spec
// §4), which is every branch here, so the relation is explicit.
func Parents(mainRoot string, worktrees []repo.Worktree) (map[string]string, error) {
	var branches []string
	tips := map[string]string{}
	for _, wt := range worktrees {
		if wt.IsMain || wt.Detached || wt.Branch == "" {
			continue
		}
		tip, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", wt.Branch)
		if err != nil {
			return nil, err
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
	for _, b := range branches {
		var ancestors []string
		for _, a := range branches {
			if a == b {
				continue
			}
			ok, err := isAncestor(a, b)
			if err != nil {
				return nil, err
			}
			if ok {
				ancestors = append(ancestors, a)
			}
		}
		// The nearest ancestor is the one that descends from all the others.
		for _, cand := range ancestors {
			nearest := true
			for _, other := range ancestors {
				if other == cand {
					continue
				}
				ok, err := isAncestor(other, cand)
				if err != nil {
					return nil, err
				}
				if !ok {
					nearest = false
					break
				}
			}
			if nearest {
				parents[b] = cand
				break
			}
		}
	}
	return parents, nil
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
- Consumes: `Locate` (locate.go), `wtsync.Assess`, `Preflight`, `Rebase`, `RunDeferred`, `Parents/Members/Order`, `Acquire/Release/GitDir`, `LoadFromTrunk`, `ListAgents`.
- Produces:
  ```go
  type RunOptions struct { NoFetch bool; Agents []wtsync.Agent /* nil: ask claude agents */; Now func() time.Time }
  func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error
  ```

**Behaviour, in order:**

1. `cfg := LoadFromTrunk`; `ErrNoConfig` → return the error `"<repo> declares no .wt-sync.yaml on origin/<trunk>: nothing is rebased"`.
2. Unless `NoFetch`: `git fetch --quiet origin <trunk>` in `MainRoot` (one fetch per repo, spec §1). Print `fetched origin/<trunk>` or `against origin/<trunk> (not fetched)`.
3. Resolve each work with `Locate`. A path or name that resolves to the main worktree is refused.
4. `parents := Parents(MainRoot, allWorktrees)`; expand each named branch with `Members`; if the expansion added branches, print `<work> is a stack with <others>: rebasing all of them`. Dedupe. `Order` the result.
5. Assess every participant (with `opts.Agents` or `ListAgents`). For each stack (connected component), if any member's `Preflight` is `Refuse`, refuse the whole stack: `defer <stack>: <member>: <reason>` for each, and nothing in it is touched. A `Skip` member of a stack (its parent is Current, say) is fine: children still rebase onto it.
6. One epoch for the run: `opts.Now().Unix()`.
7. For each branch in order: acquire the lock on its git dir (a `*LockHeld` refuses that worktree and, if it is in a stack, the rest of that stack); build the `Request`: `Onto = origin/<trunk>` for a root, or the parent's `NewTip` with `Upstream = parent's OldTip` for a child whose parent was rebased in this run (a Skip parent means the child rebases onto trunk directly, `Upstream` empty). Call `Rebase` with `w` as the log. Release the lock.
8. If `Restored`: print `  restored: <files> at <index>/<total> were not resolved; rebase by hand` and treat the rest of its stack as refused. Else run `RunDeferred` and print each result (`  defer <run>  <elapsed>  committed <files> files (+<ins> −<del>) as <sha>` / `  defer <run>  <elapsed>` / `  defer <run>  skipped: <why>` / `  defer <run>  OWED: <err>` followed by the last 20 lines of output indented). Then `  rebased <n> commits onto <onto>` (+ `, <k> signatures dropped` when k > 0) and `  push: git -C <path> push --force-with-lease`.
9. Return an error at the end naming everything refused or restored, so the exit code is non-zero when anything did not complete.

Output shape per worktree:

```
webkey  feat_wt/webkey  113 behind, 27 ahead
  safety refs/wt-sync/feat_wt/webkey/1757430000 = a1b2c3d
  stop 6/27 "chore: webkey openapi" application.yaml✓ owned-line  openapi_remote_v3.json✓ openapi
  rebased 27 commits onto origin/development, 2 signatures dropped
  defer ./gradlew webapp:generateOpenApi  1m43s  committed 2 files (+120 −30) as 9f8e7d6
  push: git -C /Users/.../server_wt/server/feat_wt/webkey push --force-with-lease
```

- [ ] **Step 1: Write the failing tests**

Extend `syncRepo` in sync_test.go or write `runFixture(t)` in sync_run_test.go: a main checkout that is its own origin, `.wt-sync.yaml` declaring `v.txt` owned-line max-plus-patch plus a deferred step `Run: "cat v.txt > gen.txt", Paths: [v.txt], Commit: "chore: regen"` (with `gen.txt` tracked), a `feat/bump` worktree one commit ahead on `v.txt`, trunk moved on `v.txt`, then `git fetch origin`. Tests:

```go
func TestSyncRunRebasesARecipeWorktreeAndRunsTheDeferredStep(t *testing.T) {
	ctx, bump := runFixture(t)
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(99, 0) }}, &out)
	if err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"safety refs/wt-sync/feat/bump/99", "stop 1/1", "v.txt✓ owned-line", "rebased 1 commit", "committed 1 file", "as ", "push: git -C"} {
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
}

func TestSyncRunRefusesADirtyWorktreeAndTouchesNothing(t *testing.T) {
	ctx, bump := runFixture(t)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{}}, &out)
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
	ctx, bump := runFixture(t)
	resolved, _ := filepath.EvalSymlinks(bump)
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{{Name: "bump-1", Cwd: resolved}}}, &out)
	if err == nil || !strings.Contains(out.String(), "bump-1") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

func TestSyncRunSkipsACurrentWorktreeWithoutASafetyRef(t *testing.T) {
	ctx, _ := runFixture(t)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"other"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatalf("a skip is not an error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing ahead") && !strings.Contains(out.String(), "already on trunk") {
		t.Fatalf("out %s", out.String())
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesWhenTrunkDeclaresNothing(t *testing.T) {
	ctx := syncRepoWithoutDeclaration(t) // committedRepo + origin, no .wt-sync.yaml
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{}}, &out)
	if err == nil || !strings.Contains(err.Error(), "declares no") {
		t.Fatalf("err %v", err)
	}
}

func TestSyncRunRebasesAStackParentFirstAndChildOntoTheNewParent(t *testing.T) {
	ctx, bump := runFixture(t)
	// child on top of bump, in its own worktree
	var buf bytes.Buffer
	child, err := New(ctx, "feat/child", NewOptions{NoSetup: true, Base: "feat/bump"}, &buf) // check NewOptions for the base field name; fall back to git worktree add + branch from feat/bump
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, child, "add", "-A")
	gitOut(t, child, "commit", "-q", "-m", "child")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"child"}, RunOptions{NoFetch: true, Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "is a stack with") {
		t.Fatalf("out %s", out.String())
	}
	if gitOut(t, child, "rev-parse", "HEAD~1") != gitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatal("child not on the new parent tip")
	}
	if gitOut(t, bump, "rev-parse", "HEAD~1") != gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main") {
		t.Fatal("parent not on trunk")
	}
	if !strings.Contains(gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"), "feat/child/") {
		t.Fatal("no safety ref for the child")
	}
}

func TestSyncRunARefusedStackMemberDefersTheWholeStack(t *testing.T) {
	// as above, then dirty the child; neither moves
}

func TestSyncRunRestoresAndReportsAnUnclaimedStop(t *testing.T) {
	// a worktree that conflicts on a.txt (unclaimed): output has "restored", HEAD unchanged, err != nil
}
```

Write the two sketched tests in full: the stack-deferral test asserts both tips unchanged and the output naming the dirty member; the restore test asserts the output contains `restored` and `a.txt`, HEAD unchanged and a safety ref present (it was written before the attempt; that is correct and the undo target).

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

	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// RunOptions tunes SyncRun for callers and tests.
type RunOptions struct {
	NoFetch bool
	Agents  []wtsync.Agent   // nil asks `claude agents`; an empty slice means none
	Now     func() time.Time // nil is time.Now
}

type participant struct {
	wt      repo.Worktree
	work    string
	a       wtsync.Assessment
	verdict wtsync.Verdict
	reason  string
	result  *wtsync.Result
}

// SyncRun rebases the named worktrees (and the stacks they belong to) onto
// origin/<trunk>: safety ref, strategies at each stop, deferred steps once
// at the end. Anything refused or restored is reported and makes the
// returned error non-nil, so a script sees it.
func SyncRun(ctx *Context, works []string, opts RunOptions, w io.Writer) error {
	trunk := ctx.Config.MainBranch
	onto := "origin/" + trunk
	cfg, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, trunk)
	if errors.Is(err, wtsync.ErrNoConfig) {
		return fmt.Errorf("%s declares no %s on %s: nothing is rebased", ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	if err != nil {
		return err
	}
	if opts.NoFetch {
		fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	} else {
		if _, err := gitQuiet(ctx.Repo.MainRoot, "fetch", "--quiet", "origin", trunk); err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		fmt.Fprintf(w, "fetched %s\n", onto)
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
	// Name each requested worktree, then widen to its stack.
	var named []string
	for _, arg := range works {
		wt, err := Locate(ctx, arg)
		if err != nil {
			return err
		}
		if wt.IsMain {
			return fmt.Errorf("%s is the main checkout; run rebases worktrees", arg)
		}
		if wt.Branch == "" {
			return fmt.Errorf("%s has no branch", arg)
		}
		named = append(named, wt.Branch)
	}
	parents, err := wtsync.Parents(ctx.Repo.MainRoot, worktrees)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var branches []string
	for _, b := range named {
		members := wtsync.Members(parents, b)
		var added []string
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				branches = append(branches, m)
				if m != b {
					added = append(added, workName(ctx, m))
				}
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
			fmt.Fprintf(w, "note: %v\n", err)
		}
	}
	parts := map[string]*participant{}
	for _, b := range branches {
		wt := byBranch[b]
		p := &participant{wt: wt, work: workName(ctx, b)}
		p.a = wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
		p.verdict, p.reason = wtsync.Preflight(p.a)
		parts[b] = p
	}
	// A refused member poisons its stack: never half-apply (spec §4).
	poisoned := map[string]string{}
	for _, b := range branches {
		if parts[b].verdict == wtsync.Refuse {
			for _, m := range wtsync.Members(parents, b) {
				if poisoned[m] == "" {
					poisoned[m] = fmt.Sprintf("%s: %s", parts[b].work, parts[b].reason)
				}
			}
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	epoch := now().Unix()
	var failures []string
	for _, b := range branches {
		p := parts[b]
		fmt.Fprintf(w, "%s  %s  %d behind, %d ahead\n", p.work, b, p.a.Behind, p.a.Ahead)
		if why, ok := poisoned[b]; ok {
			fmt.Fprintf(w, "  refused: %s\n", why)
			failures = append(failures, p.work)
			continue
		}
		if p.verdict == wtsync.Skip {
			fmt.Fprintf(w, "  skipped: %s\n", p.reason)
			continue
		}
		req := wtsync.Request{Path: p.wt.Path, Branch: b, Onto: onto, Epoch: epoch}
		if parent, ok := parents[b]; ok {
			if pp := parts[parent]; pp != nil && pp.result != nil && !pp.result.Restored {
				req.Onto, req.Upstream = pp.result.NewTip, pp.result.OldTip
			}
		}
		gitDir, err := wtsync.GitDir(p.wt.Path)
		if err != nil {
			return err
		}
		lock, err := wtsync.Acquire(gitDir, now())
		if err != nil {
			fmt.Fprintf(w, "  refused: %v\n", err)
			failures = append(failures, p.work)
			poison(parents, poisoned, b, p.work+": "+err.Error())
			continue
		}
		res, rerr := wtsync.Rebase(ctx.Repo.MainRoot, cfg, req, w)
		_ = lock.Release()
		p.result = &res
		if res.Safety.Ref != "" {
			fmt.Fprintf(w, "  safety %s = %s\n", res.Safety.Ref, short(res.OldTip))
		}
		if rerr != nil {
			fmt.Fprintf(w, "  failed: %v (restored to %s)\n", rerr, short(res.OldTip))
			failures = append(failures, p.work)
			poison(parents, poisoned, b, p.work+" failed")
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
			poison(parents, poisoned, b, p.work+" was restored")
			continue
		}
		line := fmt.Sprintf("  rebased %d commit%s onto %s", res.Replayed, plural(res.Replayed), req.Onto)
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
		fmt.Fprintf(w, "  push: git -C %s push --force-with-lease\n", p.wt.Path)
	}
	if len(failures) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(failures, ", "))
	}
	return nil
}

func poison(parents map[string]string, poisoned map[string]string, b, why string) {
	for _, m := range wtsync.Members(parents, b) {
		if poisoned[m] == "" {
			poisoned[m] = why
		}
	}
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
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
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

`gitQuiet(dir, args...)` — use `git.Run` from `internal/git` (it exists: `Run(dir string, args ...string) (string, error)`); replace the name. `Members` when a branch has no entry in `parents` and no children returns just itself, so the poison loop is safe for lone branches.

In `cmd/wt/sync.go`, turn `newSyncCmd` into a parent that keeps its `RunE` (the bare verb) and adds:

```go
	run := &cobra.Command{
		Use:   "run <work>...",
		Short: "Rebase the named worktrees onto trunk with the declared strategies",
		Long: "Fetch trunk once, then for each named worktree (and the rest of any\n" +
			"stack it belongs to, parents first): pin the old tip under\n" +
			"refs/wt-sync/<branch>/<epoch>, rebase with --no-update-refs --no-gpg-sign\n" +
			"--rerere-autoupdate, apply the declared strategy at every stop, and run\n" +
			"the deferred steps once at the end, committing their output when it\n" +
			"changes tracked files. A stop no strategy resolves aborts the rebase and\n" +
			"restores the worktree exactly; a failed deferred step is reported as owed\n" +
			"and never undoes the rebase.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one an agent\n" +
			"session is in, class divergent, class contested (rebase those by hand;\n" +
			"resume is not built yet), and any repository whose trunk declares no\n" +
			".wt-sync.yaml. Nothing is pushed: the last line per worktree is the push\n" +
			"command to run.",
		Args: cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncRun(ctx, args, commands.RunOptions{NoFetch: noFetch}, cmd.OutOrStdout())
		},
	}
	run.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	sync.AddCommand(run)
```

`openContext()` and `completeWork` exist in cmd/wt (used by remove.go); `run` needs the strict `Open`, not `OpenLenient`, because it rebases. Note that cobra runs the parent's `RunE` only when no subcommand matched, which is the existing behaviour for the bare `wt sync`.

- [ ] **Step 4: Test, lint, and try it read-only on the fleet**

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
  type Restored struct { Branch, From, To, Ref string; Path string /* "" when the branch has no worktree */ }
  func Undo(mainRoot string, worktrees []repo.Worktree, branch string) ([]Restored, error)
  // commands
  func SyncUndo(ctx *Context, work string, w io.Writer) error
  ```

**Behaviour:** `LatestSafety(branch)` → its epoch; every safety ref with that epoch is the same run (one epoch per run, Task 8). For each, in the order `ListSafety` gives: find the worktree checked out on that branch; refuse the whole undo before touching anything if any such worktree is dirty (`status --porcelain --untracked-files=no` non-empty) or mid-rebase (`RebaseInProgress`). Then per ref: with a worktree, `git -C <wt> reset --hard <tip>` (moves the branch, restores the tree); without one, `git update-ref refs/heads/<branch> <tip>`. The safety refs are kept (retention drops them later); a second undo of the same epoch is a no-op that reports `already at <tip>` per branch. No branch of that epoch → error `no run to undo for <branch>`.

- [ ] **Step 1: Write the failing tests**

```go
// undo_test.go
func TestUndoResetsEveryBranchOfTheNewestEpoch(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, Request{Path: wt, Branch: "feature", Onto: "origin/main", Epoch: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") == old {
		t.Fatal("rebase did nothing; the test is vacuous")
	}
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].To != old || got[0].Path != wt {
		t.Fatalf("restored %+v", got)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "status", "--porcelain") != "" {
		t.Fatal("not restored cleanly")
	}
}

func TestUndoRefusesADirtyWorktreeBeforeTouchingAnything(t *testing.T) { /* dirty file in wt → error mentions "tracked changes", HEAD unchanged */ }
func TestUndoWithoutARunIsAnError(t *testing.T) { /* fresh runRepo, Undo → error "no run to undo" */ }
func TestUndoRestoresABranchWithNoWorktreeThroughUpdateRef(t *testing.T) { /* WriteSafety for a branch, move the branch with update-ref, Undo with an empty worktree list → branch back at tip */ }
func TestUndoASecondTimeIsANoOp(t *testing.T) { /* after a successful undo, Undo again → same tip, no error */ }
```

Write the sketched tests in full; each is under fifteen lines using `runRepo`, `gitIn`, `WriteSafety`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/wtsync/ -run Undo 2>&1 | head`
Expected: `undefined: Undo`.

- [ ] **Step 3: Implement**

```go
// undo.go
package wtsync

import (
	"fmt"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Restored is one ref put back.
type Restored struct {
	Branch, From, To, Ref string
	Path                  string
}

// Undo resets every ref the newest run touching branch moved: all safety refs
// sharing that run's epoch. Nothing is reset until every affected worktree is
// known to be clean and not mid-rebase.
func Undo(mainRoot string, worktrees []repo.Worktree, branch string) ([]Restored, error) {
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
		if wt.Branch != "" && !wt.IsMain {
			byBranch[wt.Branch] = wt
		}
	}
	var run []Safety
	for _, s := range all {
		if s.Epoch == latest.Epoch {
			run = append(run, s)
		}
	}
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok {
			continue
		}
		out, err := gitEnv(wt.Path, nil, nil, "status", "--porcelain", "--untracked-files=no")
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

`SyncUndo(ctx, work, w)`: `Locate` → branch; `Undo`; print one line per restored ref: `<work>  <from> → <to>  (<ref>)` or `<work>  already at <to>`. Cobra subcommand `undo <work>` with Short `Put back every ref the last run on this worktree moved`. Write the command test: run `SyncRun` on the fixture, then `SyncUndo`, assert HEAD back and the output line; a second `SyncUndo` prints `already at`.

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
| `hooks` | no active `pre-rebase` or `post-rewrite` hook (executable file without `.sample` in `git rev-parse --git-path hooks`, or under `core.hooksPath`) | lists the active ones; also lists `commit-msg` as a note (OK stays true) because the deferred commit must satisfy it |
| `submodules` | no `.gitmodules` on `origin/<trunk>` | else `submodules are not handled by run` |
| `lfs` | no `.gitattributes` on `origin/<trunk>` containing `filter=lfs` | else `LFS paths are not handled by run` |
| `docker` | only checked when `cfg.Defer` is non-empty; `docker info` (or `opts.Docker`) succeeds | `deferred steps that need Docker will be owed` |
| `safety-refs` | nothing `Prunable` | `N refs pin old history (<list>)`; Fix: delete them |
| `locks` | no `wt-sync.lock` in any worktree's git dir older than `LockExpiry`, and no live one | names them; Fix: remove expired ones |

`Doctor` never fixes by itself; `SyncDoctor` prints a table `CHECK  STATE  DETAIL` and runs `Fix` for `rerere` and `locks` under `--fix`, for `safety-refs` under `--prune`, printing `fixed <name>` after each. Exit code: non-nil error when any check that has no fix is not OK and blocks a run (`trunk`, `declaration`, `scripts`); everything else is advisory.

- [ ] **Step 1: Write the failing tests**

```go
// doctor_test.go
func TestDoctorOnAHealthyRepoIsAllOK(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "config", "rerere.enabled", "true")
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
func TestDoctorFlagsAnActivePreRebaseHook(t *testing.T) { /* write an executable .git/hooks/pre-rebase → hooks !OK, detail names it */ }
func TestDoctorFlagsAMissingScriptOnTrunk(t *testing.T) { /* declaration with strategy: script run: bin/none → scripts !OK, detail names bin/none */ }
func TestDoctorListsPrunableSafetyRefsWithAFix(t *testing.T) { /* two WriteSafety for feature, epochs 40 and 50 days ago, now → safety-refs !OK names the older; Fix deletes exactly it */ }
func TestDoctorFlagsAnExpiredLock(t *testing.T) { /* Acquire with Started 31 min ago in the worktree's git dir → locks !OK; Fix removes it */ }
func TestDoctorChecksDockerOnlyWhenStepsAreDeferred(t *testing.T) { /* declaration with a defer step, Docker returns an error → check docker present and !OK; without defer → no docker check */ }
func TestDoctorFlagsSubmodulesAndLFSOnTrunk(t *testing.T) { /* commit .gitmodules and a .gitattributes with filter=lfs on main, fetch → both !OK */ }
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
  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps
  finish  push with --force-with-lease; wt sync undo <work> puts every ref back
          wt sync doctor          what a run needs, and --fix / --prune
A contested worktree is refused by run until resume exists: rebase it by hand.
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

**Spec coverage (§3, §4, §7):**
- §3 `defer` keyed on changed paths, `commit:` only on tracked changes, a failed step owed and never undoing: Task 6. Config and scripts from trunk: inherited (`LoadFromTrunk`, `materialise`), Task 4 keeps it for `--resolve`.
- §4 command flags: Task 5. `--no-update-refs` plus explicit stack handling, parents first, child onto the new parent, whole stack deferred on one refusal: Tasks 7–8. Signing: `--no-gpg-sign` and the dropped-signature count, Task 5. Safety ref and undo of every ref: Tasks 1, 9. Retention: Tasks 1, 10. rerere `--rerere-autoupdate` and doctor enabling it: Tasks 5, 10. Lock: Tasks 2, 8, 10. Hooks, submodules, LFS preflight: Task 10.
- §7 `run`, `undo`, `doctor`: Tasks 8–10. Everything else in §7 is listed as out of scope in the Global Constraints.
- Worktree-less refs inside the range (§4, "reported, with an opt-in to advance them"): **not covered.** A follow-up; noted for the handoff in Task 11. Cost: a dead local branch pointing into the rewritten range keeps its old SHA, which is what `--no-update-refs` guarantees today by hand.

**Placeholder scan:** the sketched tests in Tasks 8–10 are marked "write in full" with their assertions stated; no TBDs.

**Type consistency:** `Resolution{Outcome, Content, InPlace}` (Task 3) is what Task 5 reads; `Result.Safety` is a `Safety` (Task 1); `Request.Onto/Upstream` (Task 5) is what Task 8 fills from `parents` (Task 7) and `Result.OldTip/NewTip`; `DeferredResult` fields printed in Task 8 match Task 6; `Check{Name, OK, Detail, Fix}` (Task 10) is what `SyncDoctor` reads. `gitEnv`'s error wrapping change (Task 7) is the one edit to foundation code beyond moving `tryStrategy` (Task 3) and adding `Conflict.Mode` (Task 3).
