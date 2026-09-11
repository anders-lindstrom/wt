# wt sync idle sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A worktree with only an idle Claude session parked in it is no longer refused by `wt sync run`, `resume` or `undo`. The session is named and you are asked once first, and a finish ends with the spec §5 line to relay to it. A busy session is still refused, and `claude agents --json` gets a 10 s deadline.

**Architecture:** `internal/wtsync/agents.go` learns `status` and `id`, decides `Agent.Idle()`, and returns every session in a worktree as `Sessions` (shallowest first), with `Busy`, `Lead`, `Label` and `Arrived`. `Assessment.Agent *Agent` becomes `Assessment.Sessions Sessions`, and `Preflight` refuses only busy sessions. Each verb follows the same order: refuse busy, name idle, ask once, list the sessions again and refuse anything that changed, then act. `run` re-checks at lock time. `resume` asks before it verifies the handover. `undo` asks in the command, before `wtsync.Undo` takes any lock. `completeRun` ends with `wtsync.RebasedLine` for the idle sessions.

**Tech Stack:** Go 1.26, cobra, `os/exec` with `context.WithTimeout`, a process group and `WaitDelay` (the idiom `git.RunTimeout` uses). Tests are `go test` with the existing throwaway-repo helpers: `runRepo`, `trunkReq`, `completed`, `gitIn` (`internal/wtsync`); `runFixture`, `contestedFixture`, `handedOver`, `noAgents`, `noResumeAgents`, `noAgentsUndo`, `gitOut`, `writeFile` (`internal/commands`).

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` §1 ("Agent detection, and what it does not cover"; the `agent busy` modifier), §5 (the after-the-fact lines) and §7 (asking once). The item is `~/programmering/telcred/misc/handoffs/2026-09-10-wt-sync-next.md`, "What to build next" item 1; Anders ruled on it on 2026-09-10, and the rulings are the constraints below. The overseer session added three requests the same evening; they are listed with the constraints.

## Global Constraints

Anders's rulings (2026-09-10):

- **Busy is refused, as today:** `status: busy`, or a background session `working` or `blocked`.
- **Idle is not a refusal:** `status: idle` on an interactive session.
  - The table's WHO column shows `name (idle)`.
  - `run` names the idle session in the one confirmation it already asks.
  - `--yes` skips the confirmation. With no terminal, it proceeds as today.
  - After a finish, print the §5 line `wt: <work> rebased on <trunk> (+N) …` for relaying to that session.
  - **Never rebase silently under an idle session.** The notice naming it prints whether or not anyone is asked.
- **Several sessions in one worktree show a count:** `name +1`.
- **The same rule applies wherever `resume` and `undo` check sessions.**
- **`claude agents --json` has a 10 s deadline, and a timeout is a refusal** in `run`, `resume` and `undo`. The table notes it and carries on, as it does for any listing failure today.
- **Spec §1's outdated finding is corrected:** the CLI now reports `status`, `pid` and `waitingFor`.

Carried from the earlier sync plans:

- **Unknown is not idle.** A missing or unexpected `status`, any background session, or any non-empty `state` counts as busy.
- **Only `run`, `resume`, `undo` and `doctor --fix/--prune` write.** Bare `wt sync` stays read-only.
- **No worktree is touched before its question is answered.** A "no" leaves every ref, handover, sidecar and lock exactly as it was. (`run`'s trunk fetch before the question is bookkeeping, not a change to a worktree.)
- **Every subprocess has a deadline.**
- **No fleet name in code, tests or help.** Session names are illustrative (`bump-1`, `parked-1`, `login-crash-3a`).
- **Help examples fit in 79 columns** (`TestEveryExampleIsAPastableLine`).
- **Conventional Commits, one semantic unit per commit, imperative, lowercase, under 72 characters. No commit trailers of any kind.**
- Before every commit: `gofmt`, `go vet ./...` and `golangci-lint run` are clean. Before reporting: `make check` and `make build && scripts/verify-help-examples.sh` pass.
- `migrate` and `remove` keep treating any session as in the way (`AgentAt` keeps its meaning). They move or delete a directory, and an idle session's cwd is still inside it.

Decisions this plan makes:

- **Idle is `Status == "idle" && State == "" && Kind != "background"`.** A background session blocked on a question reports `status: idle` (seen live 2026-09-10), and one with an absent state is a shape nobody has seen, so no background session is ever idle.
- **The lead is the first busy session, else the shallowest.** `(idle)` is printed only when the lead is idle, which means every session is. `+N` counts every other session.
- **Sessions are listed again after a question and at lock time**, and a worktree is refused if one is busy now or a session arrived that nobody was told about. A wt lock cannot stop Claude becoming busy, so this narrows the window rather than closing it. That is the same guarantee the dirty re-check gives.
- **"yours to check"** is every path the rebase stopped on, across every handover, at most 3 basenames then `+N`, as `NeedsYouLine` does. The sidecar gains `stopped` so a resume still knows the earlier stops, and the `(see messages)` placeholder is never a path.
- **`undo` gets relay lines of the same shape:** `wt: <work> undone, back at <sha>`, and for an undo that aborted but could not rewind, `wt: <work> undo stopped partway, still at <sha>`. Spec §5 records both.
- **A handover under an idle session prints the notice before the run and no second relay line.** The `needs you` line it already prints is the one to pass on.
- **The question becomes `<verb> <works>? [y/N]`** (`rebase bump?`, `resume bump?`, `undo bump?`). `rebase these 1 worktrees?` reads wrong.
- **`resume` and `undo` gain `--yes`** with help examples, since they now ask.

Added by the overseer session (2026-09-10):

- **The session wt runs under does not count** in the table, `run`, `resume` or `undo`. Today an agent resolving a handover runs `wt sync resume` from inside the worktree, is busy while it does, and refuses itself. Its `pid` is an ancestor of the wt process, found with one `ps` under a deadline. `CLAUDE_CODE_SESSION_ID` is not used: an environment variable outlives its session in anything started from it, such as a tmux server. If the process tree cannot be read, nobody is dropped. `migrate` and `remove` keep counting it, because they move or delete the directory it is in. (Task 2b)
- **The §5 finish line is built here** (`RebasedLine`, Task 4). It has its own unit test, plus command tests for `run` (Task 4) and `resume` (Task 5) finishing clean under an idle session.
- **The owed doc notes go in with the spec change** (Task 7):
  - §1 "where the two can differ" gains the `recipe?` script limit and the raw-bytes-versus-clean-filters gap in `resolvedTree`.
  - §6 notes that the `rr-cache` line is not produced.

Out of scope, reported to Anders instead:

- The resume refusals "changed but not staged" and "still unmerged" advise `git add` then resume, while the `wt-sync` skill treats any refusal as a reason to undo. That belongs to the hardening pass (handoff item 4).
- Pre-existing: `wtsync.Undo` refusing a *later* branch after taking over an earlier branch's handover lock releases that lock. Unchanged here, because the new question is asked before any lock.

## Review rulings (codex-plan-review, 2026-09-10)

| # | Finding | Ruling |
|---|---|---|
| 1 | A decline inside `Undo`'s check loop runs after `TakeOver` and deletes a handover's lock | Confirmed (`lock.go:151-195`). The undo question moves to the command, before `wtsync.Undo`; `UndoBranches` names what it will touch |
| 2 | Sessions are stale by the time a verb mutates | Accepted: re-list after every question, and in `run` at lock time too; refuse if busy now or a session arrived |
| 3 | Resume verifies before an unbounded question and not after | Accepted: resume asks before `verifySequencer`/`verifyHandover`, so they run after the answer |
| 4 | "yours to check" loses stops from before a handover | Accepted: `State.Stopped` carries them |
| 5 | The `--yes` examples are over 79 columns | Confirmed; shortened |
| 6 | A partial undo would relay "undone, back at" | Accepted: its own line |
| 7 | `sync_finish.go` lacks `strings` | Moot: the helpers move to a new `sync_sessions.go` |
| 8 | `(see messages)` would be named as a file | Confirmed (`rebase.go:423`): `wtsync.StopPaths` skips it |
| 9 | Resume names today's trunk, not the recorded one | Accepted: from `st.TrunkRef` |
| 10 | The deadline test does not prove the group kill | Accepted: the test checks the child `sleep` is gone |
| 11 | Help says "a verb" before resume/undo do it; spec §5/§7 gaps | Accepted: each task's help says only what that task makes true; spec §5 and §7 updated |
| 12 | A background session with no state would be idle | Accepted: no background session is idle |

---

## File Structure

```
internal/wtsync/agents.go          Agent.ID/Status, Idle, Sessions, SessionsAt, Arrived, the deadline (modified)
internal/wtsync/agents_test.go                                                                (modified)
internal/wtsync/triage.go          Assessment.Sessions replaces Assessment.Agent                (modified)
internal/wtsync/triage_test.go                                                                (modified)
internal/wtsync/rebase.go          Preflight refuses busy only; StopPaths                        (modified)
internal/wtsync/rebase_test.go                                                                (modified)
internal/wtsync/plan.go            RebasedLine, UndoneLine; State.Stopped                        (modified)
internal/wtsync/plan_test.go                                                                  (modified)
internal/wtsync/undo.go            busy refused, idle passed; UndoBranches                       (modified)
internal/wtsync/undo_test.go                                                                  (modified)
internal/commands/sync_sessions.go idleNotice, tellIdle, listAgain, sessionsChanged, pathsOnce   (new)
internal/commands/sync.go          WHO label, section, run verdict                               (modified)
internal/commands/sync_test.go                                                                (modified)
internal/commands/sync_finish.go   completeInput.Tell…; handoverInput.Earlier; State.Stopped written (modified)
internal/commands/sync_run.go      notice, question, lock-time re-check, relay                   (modified)
internal/commands/sync_run_test.go                                                            (modified)
internal/commands/sync_resume.go   busy refused, idle asked before verification, relay           (modified)
internal/commands/sync_resume_test.go                                                         (modified)
internal/commands/sync_undo.go     busy refused, idle asked before Undo, relay                   (modified)
internal/commands/sync_undo_test.go                                                           (modified)
cmd/wt/sync.go                     confirmAsk, --yes on resume and undo, help                    (modified)
cmd/wt/sync_test.go                                                                           (modified)
scripts/verify-help-examples.sh    the two new --yes examples                                    (modified)
docs/superpowers/specs/2026-09-05-wt-sync-design.md  §1, §5, §7, change log                      (modified)
internal/about/whats-new.md        entry at the top                                              (modified)
README.md                          --yes on resume and undo                                      (modified)
```

---

### Task 1: `claude agents --json` gets a 10 s deadline

**Files:**
- Modify: `internal/wtsync/agents.go:38-51`
- Test: `internal/wtsync/agents_test.go`

**Interfaces:**
- Produces: `var agentsDeadline = 10 * time.Second`. On a timeout, `ListAgents()` returns an error containing `did not answer within`.

- [ ] **Step 1: Write the failing test**

```go
// A claude that never answers must not hold a run hostage. The stub's sleep
// is a child of sh that holds the output pipe, so the deadline has to take
// down the whole process group, not just sh.
func TestListAgentsGivesUpOnAClaudeThatDoesNotAnswer(t *testing.T) {
	stub := t.TempDir()
	pidFile := filepath.Join(stub, "sleep.pid")
	script := "#!/bin/sh\nsleep 30 &\necho $! > " + pidFile + "\nwait\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := agentsDeadline
	agentsDeadline = 200 * time.Millisecond
	t.Cleanup(func() { agentsDeadline = old })
	start := time.Now()
	_, err := ListAgents()
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s; the deadline did not hold", took)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	for gone := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(gone) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("sleep %d outlived the deadline: only sh was killed", pid)
		}
	}
}
```

Test imports: `os`, `path/filepath`, `strconv`, `strings`, `syscall`, `testing`, `time`.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/wtsync/ -run TestListAgentsGivesUp -v`
Expected: FAIL to compile (`undefined: agentsDeadline`).

- [ ] **Step 3: Implement**

```go
// agentsDeadline bounds claude agents --json, the one CLI other than git on
// every acting path. A claude that has not answered by then is stuck, and a
// run that cannot tell who is in a worktree refuses rather than waits.
var agentsDeadline = 10 * time.Second

// ListAgents asks claude for its sessions. No claude on the PATH means no
// sessions, not an error: "nobody to ask" is a normal state. It runs in its
// own process group, so the deadline takes down whatever it forked.
func ListAgents() ([]Agent, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentsDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "agents", "--json")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("claude agents --json did not answer within %s", agentsDeadline)
	}
	if err != nil {
		return nil, errors.New("claude agents --json failed: " + stderrOf(err))
	}
	return ParseAgents(out)
}
```

Imports: add `context`, `fmt`, `syscall`, `time`.

- [ ] **Step 4: Run**

Run: `go test -race ./internal/wtsync/ -run 'Agent' -v && go test ./internal/commands/ -run 'AgentsCannotBeListed'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/agents.go internal/wtsync/agents_test.go
git commit -m "fix(sync): give claude agents --json a 10 s deadline"
```

---

### Task 2: Sessions know whether they are idle

**Files:**
- Modify: `internal/wtsync/agents.go`
- Test: `internal/wtsync/agents_test.go`

**Interfaces:**
- Produces:
  - `Agent.ID string` (json `id`), `Agent.Status string` (json `status`)
  - `func (a Agent) Idle() bool`
  - `type Sessions []Agent`
  - `func SessionsAt(agents []Agent, path string) Sessions`: resolves symlinks in `path` first; shallowest cwd first, ties by shorter cwd, then listing order
  - `func (s Sessions) Busy() Sessions`
  - `func (s Sessions) Lead() *Agent`: the first busy session, else the first; nil when empty
  - `func (s Sessions) Label(name func(*Agent) string) string`, e.g. `"parked-1 (idle) +1"`
  - `func (s Sessions) Arrived(since Sessions) Sessions`: the sessions in `s` not in `since`, matched on `ID` when both have one, else on `Name` and `Cwd`
  - `AgentAt` keeps its signature and meaning: the first of `SessionsAt`, now a copy rather than an alias

- [ ] **Step 1: Write the failing tests**

Replace `agentsJSON` with today's shape: interactive sessions carry no `state` key.

```go
const agentsJSON = `[
 {"id":"a1","pid":11,"cwd":"/repo_wt/feat_wt/one","kind":"background","name":"fix it","state":"blocked","status":"idle"},
 {"id":"a2","pid":12,"cwd":"/repo_wt/feat_wt/two","kind":"interactive","name":"two-3a","status":"idle"},
 {"id":"a3","pid":13,"cwd":"/repo_wt/feat_wt/three","kind":"background","name":"finished","state":"done","status":"idle"},
 {"id":"a4","pid":14,"cwd":"/repo_wt/feat_wt/two/sub/dir","kind":"interactive","name":"deep","status":"busy"}
]`
```

```go
func TestIdleIsAnInteractiveSessionWithAnIdleStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Agent
		want bool
	}{
		{"interactive idle", Agent{Kind: "interactive", Status: "idle"}, true},
		{"interactive busy", Agent{Kind: "interactive", Status: "busy"}, false},
		{"background working", Agent{Kind: "background", State: "working", Status: "busy"}, false},
		{"background blocked on a question", Agent{Kind: "background", State: "blocked", Status: "idle"}, false},
		{"background with no state", Agent{Kind: "background", Status: "idle"}, false},
		{"interactive with a state nobody has seen", Agent{Kind: "interactive", State: "blocked", Status: "idle"}, false},
		{"no status from an older claude", Agent{Kind: "interactive"}, false},
		{"a status nobody has seen", Agent{Kind: "interactive", Status: "starting"}, false},
	} {
		if got := tc.a.Idle(); got != tc.want {
			t.Errorf("%s: Idle() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSessionsAtListsEverySessionInTheWorktreeShallowestFirst(t *testing.T) {
	agents, err := ParseAgents([]byte(agentsJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := SessionsAt(agents, "/repo_wt/feat_wt/two")
	if len(s) != 2 || s[0].Name != "two-3a" || s[1].Name != "deep" {
		t.Fatalf("sessions = %+v", s)
	}
	if busy := s.Busy(); len(busy) != 1 || busy[0].Name != "deep" {
		t.Errorf("busy = %+v", busy)
	}
	if got := s.Label(agentLabel); got != "deep +1" {
		t.Errorf("a busy session leads the label: got %q", got)
	}
	parked := Sessions{{Name: "old-1", Kind: "interactive", Status: "idle"}, {Name: "new-2", Kind: "interactive", Status: "idle"}}
	if got := parked.Label(agentLabel); got != "old-1 (idle) +1" {
		t.Errorf("idle label = %q", got)
	}
	if got := (Sessions{}).Label(agentLabel); got != "" || (Sessions{}).Lead() != nil {
		t.Errorf("empty label = %q", got)
	}
	if SessionsAt(agents, "/repo_wt/feat_wt/three") != nil {
		t.Error("a done session is nobody")
	}
}

func TestSessionsAtResolvesASymlinkedWorktreePath(t *testing.T) {
	real := t.TempDir()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	s := SessionsAt([]Agent{{Name: "in-1", Cwd: resolved}}, link)
	if len(s) != 1 {
		t.Fatalf("sessions = %+v", s)
	}
}

func TestArrivedIsWhatNobodyWasToldAbout(t *testing.T) {
	told := Sessions{{ID: "a1", Name: "parked-1", Cwd: "/w"}, {Name: "no-id", Cwd: "/w"}}
	now := Sessions{{ID: "a1", Name: "renamed", Cwd: "/w"}, {Name: "no-id", Cwd: "/w"}, {ID: "a9", Name: "new-9", Cwd: "/w"}}
	if got := now.Arrived(told); len(got) != 1 || got[0].Name != "new-9" {
		t.Errorf("arrived = %+v", got)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/wtsync/ -run 'Idle|SessionsAt|Arrived' -v`
Expected: FAIL to compile (`a.Idle undefined`, `undefined: Sessions`).

- [ ] **Step 3: Implement**

```go
// Agent is a Claude session, from `claude agents --json`. It is enough to
// answer "is a session living in this worktree, and is it doing anything";
// it says nothing about Codex, a dev server or a running test, so the dirty
// check stays the real guard (spec §1).
type Agent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Cwd    string `json:"cwd"`
	State  string `json:"state"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// Idle is an interactive session waiting for its person: status idle and no
// state. A background session is never idle — blocked on a question it
// reports status idle too — and a listing with no status, or with a value
// nobody has seen, is not known to be idle, so it counts as busy.
func (a Agent) Idle() bool {
	return a.Status == "idle" && a.State == "" && a.Kind != "background"
}

// Sessions are the live sessions in one worktree, shallowest working
// directory first.
type Sessions []Agent

// SessionsAt returns every session whose working directory is the worktree
// at path or a directory inside it. A worktree's path can carry a symlink (a
// macOS /tmp, a mounted home) that a session's reported cwd has already
// resolved, so path is resolved first.
func SessionsAt(agents []Agent, path string) Sessions {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	root := filepath.Clean(path)
	sep := string(filepath.Separator)
	var in Sessions
	for _, a := range agents {
		cwd := filepath.Clean(a.Cwd)
		if cwd == root || strings.HasPrefix(cwd, root+sep) {
			in = append(in, a)
		}
	}
	slices.SortStableFunc(in, func(x, y Agent) int {
		xc, yc := filepath.Clean(x.Cwd), filepath.Clean(y.Cwd)
		return cmp.Or(cmp.Compare(strings.Count(xc, sep), strings.Count(yc, sep)), cmp.Compare(len(xc), len(yc)))
	})
	return in
}

// AgentAt returns the shallowest session in the worktree at path, or nil.
func AgentAt(agents []Agent, path string) *Agent {
	if s := SessionsAt(agents, path); len(s) > 0 {
		return &s[0]
	}
	return nil
}

// Busy is the sessions that are not idle.
func (s Sessions) Busy() Sessions {
	var busy Sessions
	for _, a := range s {
		if !a.Idle() {
			busy = append(busy, a)
		}
	}
	return busy
}

// Lead is the session a label names: the first busy one, which is what
// keeps a verb off the worktree, else the first. Nil when there are none.
func (s Sessions) Lead() *Agent {
	for i := range s {
		if !s[i].Idle() {
			return &s[i]
		}
	}
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

// Label names the sessions in one worktree: the lead by name, "(idle)" when
// the lead is idle — and since a busy one leads, that means all of them are
// — and a count of the rest, as in "parked-1 (idle) +1".
func (s Sessions) Label(name func(*Agent) string) string {
	lead := s.Lead()
	if lead == nil {
		return ""
	}
	label := name(lead)
	if lead.Idle() {
		label += " (idle)"
	}
	if n := len(s) - 1; n > 0 {
		label += " +" + strconv.Itoa(n)
	}
	return label
}

// Arrived is the sessions in s that since does not have: a session opened
// after the others were named. Sessions match on id when both carry one, and
// otherwise on name and working directory.
func (s Sessions) Arrived(since Sessions) Sessions {
	var arrived Sessions
	for _, a := range s {
		if !slices.ContainsFunc(since, func(b Agent) bool {
			if a.ID != "" && b.ID != "" {
				return a.ID == b.ID
			}
			return a.Name == b.Name && a.Cwd == b.Cwd
		}) {
			arrived = append(arrived, a)
		}
	}
	return arrived
}
```

Imports: add `cmp`, `slices`, `strconv`. Delete the old `AgentAt` body.

- [ ] **Step 4: Run**

Run: `go test -race ./internal/wtsync/ && go test ./internal/commands/ -run 'Migrate|Remove'`
Expected: PASS, including the two existing `TestAgentAt…` tests, unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/agents.go internal/wtsync/agents_test.go
git commit -m "feat(sync): read whether each claude session is idle"
```

---

### Task 2b: The session wt runs under does not count

**Files:**
- Modify: `internal/wtsync/agents.go`, and the four live listings in the sync commands: `internal/commands/sync.go` (`syncInputs`), `internal/commands/sync_run.go`, `internal/commands/sync_resume.go`, `internal/commands/sync_undo.go`
- Test: `internal/wtsync/agents_test.go`

**Interfaces:**
- Consumes: `ListAgents`, `agentsDeadline` (Task 1).
- Produces:
  - `Agent.PID int` (json `pid`)
  - `func WithoutCaller(agents []Agent, ancestors map[int]bool) []Agent`
  - `func Ancestors() (map[int]bool, error)`
  - `func ListOtherAgents() ([]Agent, error)`
  - The table and every sync verb list through `ListOtherAgents`. `migrate` and `remove` keep `ListAgents`. A caller-supplied `Agents` slice is never filtered.

- [ ] **Step 1: Write the failing tests**

```go
func TestWithoutCallerDropsTheSessionWtRunsUnder(t *testing.T) {
	agents := []Agent{{Name: "me", PID: 100}, {Name: "other", PID: 200}, {Name: "no-pid"}}
	got := WithoutCaller(agents, map[int]bool{100: true})
	if len(got) != 2 || got[0].Name != "other" || got[1].Name != "no-pid" {
		t.Fatalf("got %+v", got)
	}
}

func TestAncestorsIncludeTheParentButNotThisProcess(t *testing.T) {
	a, err := Ancestors()
	if err != nil {
		t.Fatal(err)
	}
	if !a[os.Getppid()] || a[os.Getpid()] {
		t.Fatalf("ancestors %v; parent %d, self %d", a, os.Getppid(), os.Getpid())
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/wtsync/ -run 'Caller|Ancestors' -v`
Expected: FAIL to compile (`unknown field PID`, `undefined: WithoutCaller`).

- [ ] **Step 3: Implement**

Add `PID int `json:"pid"`` to `Agent`, then:

```go
// WithoutCaller drops the sessions wt is itself running under: a session
// whose pid is an ancestor of this process. An agent resolving a handed-over
// worktree runs wt sync resume from inside it and is busy while it does;
// counting it would make it refuse itself.
func WithoutCaller(agents []Agent, ancestors map[int]bool) []Agent {
	var others []Agent
	for _, a := range agents {
		if a.PID > 1 && ancestors[a.PID] {
			continue
		}
		others = append(others, a)
	}
	return others
}

// Ancestors is the pid of every process above this one, read from one ps
// under the same deadline as claude agents.
func Ancestors() (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentsDeadline)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	parent := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, perr := strconv.Atoi(f[0])
		ppid, qerr := strconv.Atoi(f[1])
		if perr == nil && qerr == nil {
			parent[pid] = ppid
		}
	}
	ancestors := map[int]bool{}
	for pid := os.Getppid(); pid > 1 && !ancestors[pid]; pid = parent[pid] {
		ancestors[pid] = true
	}
	return ancestors, nil
}

// ListOtherAgents is ListAgents without the session wt runs under. When the
// process tree cannot be read nobody is dropped: the caller then counts like
// any other session, which refuses rather than rebases.
func ListOtherAgents() ([]Agent, error) {
	agents, err := ListAgents()
	if err != nil || len(agents) == 0 {
		return agents, err
	}
	ancestors, aerr := Ancestors()
	if aerr != nil {
		return agents, nil
	}
	return WithoutCaller(agents, ancestors), nil
}
```

Import `os` in `agents.go`. In `syncInputs`, `SyncRun`, `SyncResume` and `SyncUndo`, `wtsync.ListAgents()` becomes `wtsync.ListOtherAgents()`. `sessionIn` in `migrate.go` is unchanged.

- [ ] **Step 4: Run**

Run: `go test -race ./internal/wtsync/ -run 'Caller|Ancestors|Agent' -v && go test ./internal/commands/`
Expected: PASS. `TestSyncRunRefusesWhenAgentsCannotBeListed` still refuses: `ListOtherAgents` passes the listing error through.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/agents.go internal/wtsync/agents_test.go internal/commands/sync.go internal/commands/sync_run.go internal/commands/sync_resume.go internal/commands/sync_undo.go
git commit -m "feat(sync): leave out the session wt itself runs under"
```

---

### Task 3: An assessment carries every session, and the row names them

Behaviour is unchanged here: any session still refuses and still files the row under skipped. Only the label changes, to `(idle)` and `+N`.

**Files:**
- Modify: `internal/wtsync/triage.go:94,115-123`, `internal/wtsync/rebase.go:61-62`, `internal/commands/sync.go:130-143,199-209`
- Test: `internal/wtsync/triage_test.go:273-286`, `internal/wtsync/rebase_test.go:254`, `internal/commands/sync_test.go:258-285,368-385`

**Interfaces:**
- Consumes: `Sessions`, `SessionsAt`, `Sessions.Label`, `Sessions.Lead`, `Sessions.Busy` (Task 2).
- Produces: `Assessment.Sessions wtsync.Sessions` (the `Agent` field is gone). In `commands`: `func whoLabel(s wtsync.Sessions) string`, e.g. `"session parked-1 (idle) +1"`, or `"an unnamed session"` when the lead has neither name nor kind.

- [ ] **Step 1: Update the tests to the new field, and add the label cases**

`triage_test.go`, `TestAssessReportsDirtyTrackedChangesAndTheAgent`:

```go
	if len(a.Sessions) != 1 || a.Sessions[0].Name != "busy" {
		t.Errorf("sessions = %+v", a.Sessions)
	}
```

`rebase_test.go`, the `x-1` case: `{Assessment{Class: Recipe, Sessions: Sessions{{Name: "x-1"}}}, RefuseRun, "x-1"},`

`sync_test.go`: in `TestSyncGroupsAWorktreeByWhatToDoAboutIt`, `agent := &wtsync.Agent{Name: "busy"}` becomes `busy := wtsync.Sessions{{Name: "busy"}}` and `Agent: agent` becomes `Sessions: busy`. Replace `TestHeldByNamesTheSessionByNameThenKindThenAQuestionMark` with:

```go
func TestHeldByNamesTheSessionsInTheWorktree(t *testing.T) {
	parked := wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}
	for _, tc := range []struct {
		name string
		a    wtsync.Assessment
		want string
	}{
		{"named", wtsync.Assessment{Sessions: wtsync.Sessions{{Name: "busy"}}}, "session busy"},
		{"kind only", wtsync.Assessment{Sessions: wtsync.Sessions{{Kind: "codex"}}}, "session codex"},
		{"nothing to call it", wtsync.Assessment{Sessions: wtsync.Sessions{{}}}, "an unnamed session"},
		{"dirty, nobody in it", wtsync.Assessment{Dirty: true}, "dirty"},
		{"idle", wtsync.Assessment{Sessions: parked}, "session parked-1 (idle)"},
		{"two idle", wtsync.Assessment{Sessions: append(wtsync.Sessions{{Name: "old-1", Kind: "interactive", Status: "idle"}}, parked...)}, "session old-1 (idle) +1"},
		{"a busy one leads", wtsync.Assessment{Sessions: append(wtsync.Sessions{{Name: "new-2", Status: "busy"}}, parked...)}, "session new-2 +1"},
		{"idle and dirty", wtsync.Assessment{Dirty: true, Sessions: parked}, "session parked-1 (idle)  dirty"},
		{"busy and dirty", wtsync.Assessment{Dirty: true, Sessions: wtsync.Sessions{{Name: "busy"}}}, "session busy"},
	} {
		if got := heldBy(tc.a); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ 2>&1 | head`
Expected: FAIL to compile (`unknown field Sessions`).

- [ ] **Step 3: Implement**

`triage.go`: the field is `Sessions  Sessions // every live session in the worktree, shallowest first`. `Assess` drops its own `EvalSymlinks` block and sets `Sessions: SessionsAt(agents, wt.Path)`; `SessionsAt` resolves the path now.

`rebase.go` `Preflight`:

```go
	case len(a.Sessions) > 0:
		return RefuseRun, "an agent session is in it: " + a.Sessions.Label(agentLabel)
```

`sync.go`:

```go
	case len(a.Sessions) > 0, a.Class == wtsync.Detached, a.Class == wtsync.Stale:
		return sectionSkipped
```

```go
// heldBy is who and what is in the worktree: its sessions, and its dirt
// unless a busy session already keeps a run off it.
func heldBy(a wtsync.Assessment) string {
	var held []string
	if len(a.Sessions) > 0 {
		held = append(held, whoLabel(a.Sessions))
	}
	if a.Dirty && len(a.Sessions.Busy()) == 0 {
		held = append(held, "dirty")
	}
	return strings.Join(held, "  ")
}

// whoLabel is the sessions in a worktree as a row names them: "session
// parked-1 (idle) +1", or "an unnamed session" when there is nothing to
// call the lead by.
func whoLabel(s wtsync.Sessions) string {
	label := s.Label(sessionLabel)
	if lead := s.Lead(); lead != nil && (lead.Name != "" || lead.Kind != "") {
		return "session " + label
	}
	return label
}
```

- [ ] **Step 4: Run**

Run: `go test -race ./internal/wtsync/ ./internal/commands/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/triage.go internal/wtsync/triage_test.go internal/wtsync/rebase.go internal/wtsync/rebase_test.go internal/commands/sync.go internal/commands/sync_test.go
git commit -m "feat(sync): name every session in a worktree and mark idle ones"
```

---

### Task 4: `run` goes ahead under an idle session, asking first and telling it after

**Files:**
- Create: `internal/commands/sync_sessions.go`
- Modify: `internal/wtsync/rebase.go` (`Preflight`, `StopPaths`), `internal/wtsync/plan.go` (`RebasedLine`), `internal/commands/sync.go` (`sectionOf`, `runVerdict`), `internal/commands/sync_finish.go` (`completeInput`, `completeRun`), `internal/commands/sync_run.go`, `cmd/wt/sync.go`
- Test: `internal/wtsync/rebase_test.go`, `internal/wtsync/plan_test.go`, `internal/commands/sync_test.go`, `internal/commands/sync_run_test.go`, `cmd/wt/sync_test.go`

**Interfaces:**
- Consumes: `Assessment.Sessions`, `Sessions.Busy`, `Sessions.Arrived`, `whoLabel`, `sessionLabel` (`migrate.go`).
- Produces:
  - `func RebasedLine(work, trunk string, landed int, check []string) string`, `func StopPaths(stops []StopResult) []string` (`wtsync`)
  - `commands`:
    - `func idleNotice(work string, s wtsync.Sessions) string`
    - `func tellIdle(w io.Writer, s wtsync.Sessions, line string)`
    - `func listAgain(given []wtsync.Agent, relist func() ([]wtsync.Agent, error)) ([]wtsync.Agent, error)`
    - `func sessionsChanged(told, now wtsync.Sessions) string`
    - `func pathsOnce(lists ...[]string) []string`
  - `completeInput` gains `Tell wtsync.Sessions`, `Trunk string`, `Landed int`, `Check []string`. `RunOptions` gains `Relist func() ([]wtsync.Agent, error)`.
  - `cmd/wt`: `func confirmAsk(in io.Reader, out io.Writer, verb string) func([]string) (bool, error)` replaces `confirmRun`.

- [ ] **Step 1: Write the failing tests**

`plan_test.go`:

```go
func TestRebasedLineNamesWhatToCheck(t *testing.T) {
	for _, tc := range []struct {
		check []string
		want  string
	}{
		{nil, "wt: bump rebased on main (+3)"},
		{[]string{"src/A.java", "b.txt"}, "wt: bump rebased on main (+3). yours to check: A.java, b.txt"},
		{[]string{"a", "b", "c", "d", "e"}, "wt: bump rebased on main (+3). yours to check: a, b, c +2"},
	} {
		if got := RebasedLine("bump", "main", 3, tc.check); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
```

`rebase_test.go`, `TestPreflightOrdersItsReasons`, add:

```go
		{Assessment{Class: Recipe, Sessions: Sessions{{Name: "x-1", Kind: "interactive", Status: "idle"}}}, Proceed, ""},
		{Assessment{Class: Recipe, Sessions: Sessions{{Name: "x-1", Kind: "interactive", Status: "idle"}, {Name: "x-2", Status: "busy"}}}, RefuseRun, "busy in it: x-2 +1"},
		{Assessment{Class: Recipe, Dirty: true, Sessions: Sessions{{Name: "x-1", Kind: "interactive", Status: "idle"}}}, RefuseRun, "tracked changes"},
```

and:

```go
func TestStopPathsSkipsTheMessagesPlaceholder(t *testing.T) {
	stops := []StopResult{
		{Files: []FileOutcome{{Path: "v.txt"}, {Path: messagesPath}}},
		{Files: []FileOutcome{{Path: "a.txt"}, {Path: "v.txt"}}},
	}
	if got := StopPaths(stops); !reflect.DeepEqual(got, []string{"v.txt", "a.txt", "v.txt"}) {
		t.Errorf("got %v", got)
	}
}
```

(Add `reflect` to `rebase_test.go` imports if missing.)

`sync_test.go`, `TestSyncGroupsAWorktreeByWhatToDoAboutIt`, add with `parked := wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}`:

```go
		{"idle session in a recipe", wtsync.Assessment{Class: wtsync.Recipe, Sessions: parked}, sectionReady},
		{"idle session in a contested", wtsync.Assessment{Class: wtsync.Contested, Sessions: parked}, sectionNeedsYou},
```

and:

```go
func TestRunVerdictSaysItAsksFirstUnderAnIdleSession(t *testing.T) {
	a := wtsync.Assessment{Class: wtsync.Clean, Sessions: wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}}
	if got := runVerdict("bump", a); got != "wt sync run bump; asks first: session parked-1 (idle) is in it" {
		t.Errorf("got %q", got)
	}
}

func TestSessionsChangedNamesWhatIsNewOrBusy(t *testing.T) {
	parked := wtsync.Agent{ID: "a1", Name: "parked-1", Cwd: "/w", Kind: "interactive", Status: "idle"}
	busy := parked
	busy.Status = "busy"
	other := wtsync.Agent{ID: "a2", Name: "new-2", Cwd: "/w", Kind: "interactive", Status: "idle"}
	told := wtsync.Sessions{parked}
	for _, tc := range []struct {
		now  wtsync.Sessions
		want string
	}{
		{wtsync.Sessions{parked}, ""},
		{nil, ""},
		{wtsync.Sessions{busy}, "an agent session is busy in it now: parked-1"},
		{wtsync.Sessions{parked, other}, "a session arrived since it was checked: new-2 (idle)"},
	} {
		if got := sessionsChanged(told, tc.now); got != tc.want {
			t.Errorf("now %+v: got %q, want %q", tc.now, got, tc.want)
		}
	}
}
```

`sync_run_test.go`:

```go
// idleIn is one interactive session parked in path, as claude lists it: the
// cwd resolved.
func idleIn(t *testing.T, path, name string) []wtsync.Agent {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return []wtsync.Agent{{ID: name, Name: name, Cwd: resolved, Kind: "interactive", Status: "idle"}}
}

func TestSyncRunStillRefusesASessionThatIsNotIdle(t *testing.T) {
	ctx, bump := runFixture(t, false)
	resolved, _ := filepath.EvalSymlinks(bump)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	for _, a := range []wtsync.Agent{
		{Name: "bump-1", Cwd: resolved, Kind: "interactive", Status: "busy"},
		{Name: "bump-1", Cwd: resolved, Kind: "background", State: "blocked", Status: "idle"},
	} {
		opts := noAgents()
		opts.Agents = []wtsync.Agent{a}
		var out bytes.Buffer
		if err := SyncRun(ctx, []string{"bump"}, opts, &out); err == nil || !strings.Contains(out.String(), "busy in it: bump-1") {
			t.Fatalf("%+v: err %v\n%s", a, err, out.String())
		}
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved under a busy session")
	}
}

func TestSyncRunNamesAnIdleSessionInTheQuestionAndStopsOnNo(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if len(asked) != 1 || asked[0] != "bump" {
		t.Fatalf("asked %v; one worktree under an idle session is asked about", asked)
	}
	if s := out.String(); !strings.Contains(s, "⚠ bump: session bump-1 (idle) is in it") || !strings.Contains(s, "nothing rebased") {
		t.Fatalf("out:\n%s", s)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved after no")
	}
}

func TestSyncRunUnderAnIdleSessionWithNoTerminalSaysSoAndEndsWithALineToRelay(t *testing.T) {
	ctx, bump := runFixture(t, false)
	opts := noAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	notice := strings.Index(s, "session bump-1 (idle) is in it")
	rebased := strings.Index(s, "✓ rebased")
	if notice < 0 || rebased < 0 || notice > rebased {
		t.Fatalf("the notice must come before anything moves:\n%s", s)
	}
	if !strings.Contains(s, "⚠ tell bump-1, idle in it:\n    wt: bump rebased on main (+1). yours to check: v.txt\n") {
		t.Fatalf("no relay line:\n%s", s)
	}
}

func TestSyncRunYesSkipsTheIdleQuestionButNotTheNotice(t *testing.T) {
	ctx, bump := runFixture(t, false)
	opts := noAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Yes = true
	opts.Confirm = func([]string) (bool, error) { t.Fatal("asked despite --yes"); return false, nil }
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "session bump-1 (idle) is in it") {
		t.Fatalf("out:\n%s", out.String())
	}
}

// Between triage and the lock a session can wake up, or a new one open. The
// lock-time re-check lists them again and refuses either.
func TestSyncRunRefusesASessionThatChangedSinceTriage(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	for _, tc := range []struct {
		name string
		then []wtsync.Agent
		want string
	}{
		{"became busy", func() []wtsync.Agent { a := idleIn(t, bump, "bump-1"); a[0].Status = "busy"; return a }(), "busy in it now: bump-1"},
		{"arrived", append(idleIn(t, bump, "bump-1"), idleIn(t, bump, "bump-2")...), "arrived since it was checked: bump-2 (idle)"},
		{"arrived in an empty worktree", idleIn(t, bump, "bump-3"), "arrived since it was checked: bump-3 (idle)"},
	} {
		opts := noAgents()
		opts.Agents = idleIn(t, bump, "bump-1")
		if tc.name == "arrived in an empty worktree" {
			opts.Agents = []wtsync.Agent{}
		}
		opts.Relist = func() ([]wtsync.Agent, error) { return tc.then, nil }
		var out bytes.Buffer
		if err := SyncRun(ctx, []string{"bump"}, opts, &out); err == nil || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%s: err %v\n%s", tc.name, err, out.String())
		}
		if gitOut(t, bump, "rev-parse", "HEAD") != old {
			t.Fatalf("%s: HEAD moved", tc.name)
		}
	}
}
```

`cmd/wt/sync_test.go`:

```go
func TestConfirmAskNamesTheWorktreesAndDefaultsToNo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"\n", false},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"", false}, // ^D declines
	} {
		var out bytes.Buffer
		got, err := confirmAsk(strings.NewReader(tc.in), &out, "rebase")([]string{"a", "b"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "rebase a, b? [y/N]") {
			t.Errorf("question %q", out.String())
		}
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ ./cmd/wt/ 2>&1 | tail -20`
Expected: FAIL to compile (`undefined: RebasedLine`, `undefined: StopPaths`, `undefined: sessionsChanged`, `undefined: confirmAsk`).

- [ ] **Step 3: Implement**

`plan.go`, beside `NeedsYouLine` (imports `strings` if not already):

```go
// RebasedLine is spec §5's after-the-fact line for a rebase that finished:
// how many trunk commits it took in and, for a session whose picture of the
// worktree is now out of date, the files the rebase stopped on.
func RebasedLine(work, trunk string, landed int, check []string) string {
	line := fmt.Sprintf("wt: %s rebased on %s (+%d)", work, trunk, landed)
	if len(check) == 0 {
		return line
	}
	shown := check[:min(len(check), 3)]
	names := make([]string, len(shown))
	for i, p := range shown {
		names[i] = path.Base(p)
	}
	line += ". yours to check: " + strings.Join(names, ", ")
	if n := len(check) - len(shown); n > 0 {
		line += " +" + strconv.Itoa(n)
	}
	return line
}
```

`rebase.go`:

```go
	case len(a.Sessions.Busy()) > 0:
		return RefuseRun, "an agent session is busy in it: " + a.Sessions.Label(agentLabel)
```

```go
// StopPaths is every path a rebase stopped on, stop by stop, leaving out the
// stand-in for a stop with nothing unmerged.
func StopPaths(stops []StopResult) []string {
	var paths []string
	for _, s := range stops {
		for _, f := range s.Files {
			if f.Path != messagesPath {
				paths = append(paths, f.Path)
			}
		}
	}
	return paths
}
```

`sync.go`: `sectionOf` skips on `len(a.Sessions.Busy()) > 0`, and its comment says a *busy* session outranks the state. `runVerdict`'s `Proceed` case:

```go
	case wtsync.Proceed:
		verdict := "wt sync run " + work
		if stop := a.Replay.Stop; a.Class == wtsync.Contested && stop != nil {
			verdict = fmt.Sprintf("wt sync run %s rebases up to %d/%d and hands that stop to you", work, stop.Index, stop.Total)
		}
		if len(a.Sessions) > 0 {
			verdict += "; asks first: " + whoLabel(a.Sessions) + " is in it"
		}
		return verdict
```

`sync_sessions.go` (new):

```go
package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// idleNotice is said before anything moves under an idle session, whether or
// not anybody is asked: a verb never changes files under one silently.
func idleNotice(work string, s wtsync.Sessions) string {
	return fmt.Sprintf("⚠ %s: %s is in it; the files it has read will change", work, whoLabel(s))
}

// tellIdle prints a §5 line for the idle sessions in a worktree, on a line of
// its own so it can be relayed to them verbatim. Nothing when there are none.
func tellIdle(w io.Writer, s wtsync.Sessions, line string) {
	if len(s) == 0 {
		return
	}
	names := make([]string, len(s))
	for i := range s {
		names[i] = sessionLabel(&s[i])
	}
	fmt.Fprintf(w, "  ⚠ tell %s, idle in it:\n    %s\n", strings.Join(names, ", "), line)
}

// listAgain lists the sessions a second time, for the check after a question
// or at the lock: through relist when a caller supplied one, the caller's own
// list when it supplied that, and claude otherwise.
func listAgain(given []wtsync.Agent, relist func() ([]wtsync.Agent, error)) ([]wtsync.Agent, error) {
	switch {
	case relist != nil:
		return relist()
	case given != nil:
		return given, nil
	}
	return wtsync.ListOtherAgents()
}

// sessionsChanged is why the sessions in a worktree are no longer the ones a
// verb checked: one is busy now, or one arrived that nobody was told about.
// Empty when nothing changed that matters.
func sessionsChanged(told, now wtsync.Sessions) string {
	if len(now.Busy()) > 0 {
		return "an agent session is busy in it now: " + now.Label(sessionLabel)
	}
	if arrived := now.Arrived(told); len(arrived) > 0 {
		return "a session arrived since it was checked: " + arrived.Label(sessionLabel)
	}
	return ""
}

// pathsOnce joins lists of paths, keeping the first of each.
func pathsOnce(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}
```

`sync_finish.go`, `completeInput` gains:

```go
	// Tell are the idle sessions in the worktree. With any, the finish ends
	// with the line to relay to them: Landed trunk commits on Trunk, and the
	// Check files the rebase stopped on.
	Tell   wtsync.Sessions
	Trunk  string
	Landed int
	Check  []string
```

`completeRun`'s first statement. It is only called once a rebase has finished, so the files moved however it returns:

```go
	defer tellIdle(w, in.Tell, wtsync.RebasedLine(in.Work, in.Trunk, in.Landed, in.Check))
```

`sync_run.go`:

- `RunOptions.Confirm`'s comment: "is asked once before rebasing more than one worktree, or any worktree an idle session is in."
- New field: `// Relist lists the sessions again at the lock. Nil lists them the way Agents did.` `Relist func() ([]wtsync.Agent, error)`

The `going` loop and the question:

```go
	var going []string
	underIdle := false
	for _, b := range branches {
		p := parts[b]
		if p.verdict != wtsync.Proceed || poisoned[b] != "" {
			continue
		}
		going = append(going, p.work)
		// Preflight lets sessions through only when every one is idle.
		if len(p.a.Sessions) > 0 {
			fmt.Fprintln(w, idleNotice(p.work, p.a.Sessions))
			underIdle = true
		}
	}
	if (len(going) > 1 || underIdle) && opts.Confirm != nil && !opts.Yes {
```

The body of that `if` is unchanged. Directly after it, before `now := opts.Now`:

```go
	// Listed again now: a question can sit unanswered for as long as it
	// likes, and a triage over a large fleet takes a while.
	fresh, err := listAgain(opts.Agents, opts.Relist)
	if err != nil {
		return fmt.Errorf("cannot list agent sessions again (%v); nothing is rebased", err)
	}
```

In the lock loop, after the tracked-changes check:

```go
		if why := sessionsChanged(p.a.Sessions, wtsync.SessionsAt(fresh, p.wt.Path)); why != "" {
			poison(b, p.work+": changed since triage: "+why)
			continue
		}
```

The `completeRun` call:

```go
		head, owed, derr := completeRun(ctx, w, cfg, completeInput{
			Work: p.work, Branch: b, Path: p.wt.Path, Epoch: epoch, Res: res,
			Tell: p.a.Sessions, Trunk: trunk, Landed: p.a.Behind, Check: pathsOnce(wtsync.StopPaths(res.Stops)),
		})
```

`cmd/wt/sync.go`: rename `confirmRun` to `confirmAsk`:

```go
// confirmAsk asks the one question an acting command gets before it changes
// anything, defaulting to no. What it is about has been printed by then.
func confirmAsk(in io.Reader, out io.Writer, verb string) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		_, _ = fmt.Fprintf(out, "%s %s? [y/N] ", verb, strings.Join(works, ", "))
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil {
			// EOF on a terminal is ^D: the user declined rather than answered.
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	}
}
```

`run`'s `RunE` uses `confirmAsk(cmd.InOrStdin(), cmd.OutOrStdout(), "rebase")`. Flag: `"do not ask first: several worktrees, or an idle session in one"`.

Help, `wt sync` Long, the act line:

```
"                                  asks once when more than one worktree is involved or a\n" +
"                                  session is idle in one (--yes skips)\n" +
```

```
"  skipped    a busy session is in it, nothing is ahead of trunk, or no branch\n" +
```

Replace the closing sentences:

```
"line. A session named on a row is a Claude session in that worktree: a busy\n" +
"one keeps wt sync run off it. One marked (idle) is waiting for its person;\n" +
"wt sync run names it, asks first, and ends with a wt: line to pass on to\n" +
"it. +N counts the other sessions there. The lines under a row are advisory\n" +
"and never change the class.",
```

`run` Long, the refused paragraph:

```
"Refused, and never touched: a worktree with tracked changes, one a busy\n" +
"Claude session is in (Codex sessions are not detected), class divergent,\n" +
"one an earlier run already left waiting on you, and any repository whose\n" +
"trunk declares no .wt-sync.yaml. You are asked once when more than one\n" +
"worktree would be rebased or a Claude session is idle in one; --yes skips\n" +
"that. The idle session is named before anything moves either way, and a\n" +
"finished rebase ends with a wt: line to pass on to it. Sessions are listed\n" +
"again at the lock: one busy by then, or new since, is refused.\n\n" +
```

- [ ] **Step 4: Run**

Run: `go test -race ./internal/wtsync/ ./internal/commands/ ./cmd/wt/ && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/rebase.go internal/wtsync/rebase_test.go internal/wtsync/plan.go internal/wtsync/plan_test.go internal/commands/sync_sessions.go internal/commands/sync.go internal/commands/sync_test.go internal/commands/sync_finish.go internal/commands/sync_run.go internal/commands/sync_run_test.go cmd/wt/sync.go cmd/wt/sync_test.go
git commit -m "feat(sync): rebase under an idle session after asking once"
```

---

### Task 5: `resume` applies the same rule

**Files:**
- Modify: `internal/wtsync/plan.go` (`State.Stopped`), `internal/commands/sync_finish.go` (`handoverInput.Earlier`, `handOver`), `internal/commands/sync_run.go` (nothing but the `handOver` call site, which leaves `Earlier` empty), `internal/commands/sync_resume.go`, `cmd/wt/sync.go`, `scripts/verify-help-examples.sh`
- Test: `internal/commands/sync_resume_test.go`

**Interfaces:**
- Consumes: `SessionsAt`, `Sessions.Busy`, `Sessions.Label`, `StopPaths`, `idleNotice`, `tellIdle`, `listAgain`, `sessionsChanged`, `pathsOnce`, `completeInput.Tell/Trunk/Landed/Check`, `confirmAsk`, `idleIn`.
- Produces: `State.Stopped []string` (json `stopped`); `handoverInput.Earlier []string`; `ResumeOptions.Yes`, `ResumeOptions.Confirm`, `ResumeOptions.Relist`.

- [ ] **Step 1: Write the failing tests**

```go
// resolvedByHand is handedOver with the person's part done: a.txt merged and
// staged, ready for resume.
func resolvedByHand(t *testing.T) (ctx *Context, bump, gitDir string) {
	t.Helper()
	ctx, bump, gitDir, _ = handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	return ctx, bump, gitDir
}

func TestSyncResumeRefusesABusySession(t *testing.T) {
	ctx, bump, gitDir := resolvedByHand(t)
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Agents[0].Status = "busy"
	var out bytes.Buffer
	err := SyncResume(ctx, "bump", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "busy in bump: bump-1") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if has, _ := wtsync.HasPlan(gitDir); !has {
		t.Fatal("the refusal removed the handover")
	}
}

func TestSyncResumeAsksBeforeFinishingUnderAnIdleSession(t *testing.T) {
	ctx, bump, gitDir := resolvedByHand(t)
	st, _, _ := wtsync.ReadState(gitDir)
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if len(asked) != 1 || asked[0] != "bump" {
		t.Fatalf("asked %v", asked)
	}
	if s := out.String(); !strings.Contains(s, "⚠ bump: session bump-1 (idle) is in it") || !strings.Contains(s, "nothing resumed") {
		t.Fatalf("out:\n%s", s)
	}
	if busy, _ := wtsync.RebaseInProgress(bump); !busy {
		t.Fatal("the rebase moved on after no")
	}
	if has, _ := wtsync.HasPlan(gitDir); !has {
		t.Fatal("the handover is gone after no")
	}
	if l, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok || l.PID != st.Lock.PID || l.Started.Unix() != st.Lock.Started {
		t.Fatalf("lock %+v %v %v; a no must leave the run's lock alone", l, ok, err)
	}
}

// A person can still be editing while the question waits: resume verifies
// the handover after the answer, not before.
func TestSyncResumeVerifiesTheHandoverAfterTheAnswer(t *testing.T) {
	ctx, bump, _ := resolvedByHand(t)
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) {
		writeFile(t, bump, "a.txt", "changed while you were asked\n")
		return true, nil
	}
	var out bytes.Buffer
	err := SyncResume(ctx, "bump", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "changed but not staged: a.txt") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
}

func TestSyncResumeRefusesASessionThatWokeWhileAsked(t *testing.T) {
	ctx, bump, gitDir := resolvedByHand(t)
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) { return true, nil }
	opts.Relist = func() ([]wtsync.Agent, error) {
		a := idleIn(t, bump, "bump-1")
		a[0].Status = "busy"
		return a, nil
	}
	var out bytes.Buffer
	err := SyncResume(ctx, "bump", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "busy in it now: bump-1") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if has, _ := wtsync.HasPlan(gitDir); !has {
		t.Fatal("the refusal removed the handover")
	}
}

func TestSyncResumeUnderAnIdleSessionEndsWithALineToRelay(t *testing.T) {
	ctx, bump, _ := resolvedByHand(t)
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "⚠ tell bump-1, idle in it:\n    wt: bump rebased on main (+2). yours to check: a.txt, v.txt\n") {
		t.Fatalf("no relay line:\n%s", out.String())
	}
}

// The files of a stop the run resolved before it handed over are still the
// session's to check once resume finishes: the sidecar carries them.
func TestSyncResumeRelaysTheStopsBeforeTheHandoverToo(t *testing.T) {
	ctx, bump := runFixture(t, false)
	main := ctx.Repo.MainRoot
	writeFile(t, bump, "a.txt", "branch\n")
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "a on the branch")
	writeFile(t, main, "a.txt", "trunk\n")
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "a on trunk")
	gitOut(t, main, "fetch", "-q", "origin")
	gitDir, st := handOverNow(t, ctx, bump)
	if st.Stop != 2 || !reflect.DeepEqual(st.Stopped, []string{"v.txt", "a.txt"}) {
		t.Fatalf("handed over at %d/%d with stopped %v; the fixture is not two stops", st.Stop, st.Total, st.Stopped)
	}
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "bump-1")
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "yours to check: v.txt, a.txt\n") {
		t.Fatalf("no earlier stop in the relay:\n%s", out.String())
	}
	_ = gitDir
}
```

Add `reflect` to the test imports.

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/commands/ -run 'SyncResume(RefusesABusy|AsksBefore|VerifiesTheHandover|RefusesASessionThatWoke|UnderAnIdle|RelaysTheStops)' -v`
Expected: FAIL to compile (`opts.Confirm undefined`, `st.Stopped undefined`).

- [ ] **Step 3: Implement**

`plan.go`, `State`: `Stopped  []string          `json:"stopped"``, with the comment "every path the rebase stopped on up to the handover, across every earlier handover of the same run".

`sync_finish.go`: `handoverInput` gains `Earlier []string // the paths earlier handovers of this run stopped on`. `handOver` sets `Stopped: pathsOnce(in.Earlier, wtsync.StopPaths(in.Res.Stops)),` in the `State` literal. The run's call site leaves `Earlier` empty. The resume re-handover call site passes `Earlier: st.Stopped`.

`ResumeOptions` gains:

```go
	// Yes, Confirm and Relist are RunOptions' own. Resume asks only when idle
	// sessions are in the worktree; a nil Confirm never asks.
	Yes     bool
	Confirm func(works []string) (bool, error)
	Relist  func() ([]wtsync.Agent, error)
```

Replace the `agentPath` and `AgentAt` block, which sits after the safety-ref checks and before the config load and verification, with:

```go
	sessions := wtsync.SessionsAt(agents, target.Path)
	if len(sessions.Busy()) > 0 {
		return fmt.Errorf("an agent session is busy in %s: %s; nothing is resumed", name, sessions.Label(sessionLabel))
	}
	// An idle session is named and asked about before the handover is
	// verified: a person can go on editing while the question waits, so the
	// verification has to see what they left after answering.
	var landed int
	if len(sessions) > 0 {
		count, err := git.Run(ctx.Repo.MainRoot, "rev-list", "--count", st.OldTip+".."+st.Trunk)
		if err == nil {
			landed, err = strconv.Atoi(count)
		}
		if err != nil {
			return fmt.Errorf("%s: counting what landed: %w; nothing is resumed", name, err)
		}
		fmt.Fprintln(w, idleNotice(name, sessions))
		if opts.Confirm != nil && !opts.Yes {
			ok, err := opts.Confirm([]string{name})
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(w, "nothing resumed")
				return nil
			}
			fresh, err := listAgain(opts.Agents, opts.Relist)
			if err != nil {
				return fmt.Errorf("cannot list agent sessions again (%v); nothing is resumed", err)
			}
			if why := sessionsChanged(sessions, wtsync.SessionsAt(fresh, target.Path)); why != "" {
				return fmt.Errorf("%s: %s; nothing is resumed", name, why)
			}
		}
	}
```

The `completeRun` call:

```go
	resolved := make([]string, 0, len(st.Resolved))
	for p := range st.Resolved {
		resolved = append(resolved, p)
	}
	sort.Strings(resolved)
	_, owed, cerr := completeRun(ctx, w, cfg, completeInput{
		Work: name, Branch: st.Branch, Path: target.Path, Epoch: st.Epoch, Res: res,
		Tell: sessions, Trunk: strings.TrimPrefix(st.TrunkRef, "origin/"), Landed: landed,
		Check: pathsOnce(st.Stopped, st.Left, resolved, st.Deleted, wtsync.StopPaths(res.Stops)),
	})
```

Import `strconv`; drop `path/filepath` if nothing else uses it.

`cmd/wt/sync.go`, resume `RunE`:

```go
			opts := commands.ResumeOptions{Yes: yes, Push: pushMode(push, noPush)}
			if isTerminal(os.Stdin) {
				if !yes {
					opts.Confirm = confirmAsk(cmd.InOrStdin(), cmd.OutOrStdout(), "resume")
				}
				opts.ConfirmPush = confirmPush(cmd.InOrStdin(), cmd.OutOrStdout())
			}
```

Flags: `resume.Flags().BoolVar(&yes, "yes", false, "do not ask first when a session is idle in the worktree")`. The comment above becomes "run's variables: only one of these commands parses flags in any one invocation."

Resume Long: "and that no\nClaude session is in the worktree." becomes "and that no\nbusy Claude session is in the worktree." After "handed over again with a fresh plan file.":

```
" A Claude session idle in\n" +
"the worktree is named first and you are asked (--yes skips; with no\n" +
"terminal it goes ahead), before anything is verified, and the finish ends\n" +
"with a wt: line to pass on to it.\n\n" +
```

Keep the existing paragraph break structure: the `\n\n` that followed the fresh-plan-file sentence now follows this addition.

Example, appended (73 columns):

```
"  wt sync resume login-crash --yes      # not asked about an idle session"
```

`scripts/verify-help-examples.sh`, after the resume `--no-push` line: `check resume "" "" 'wt sync resume login-crash --yes'`

- [ ] **Step 4: Run**

Run: `go test -race ./internal/commands/ ./cmd/wt/ && golangci-lint run`
Expected: PASS, including `TestEveryFlagAppearsInAnExample` and `TestEveryExampleIsAPastableLine`.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/plan.go internal/commands/sync_finish.go internal/commands/sync_resume.go internal/commands/sync_resume_test.go cmd/wt/sync.go scripts/verify-help-examples.sh
git commit -m "feat(sync): resume under an idle session after asking once"
```

---

### Task 6: `undo` applies the same rule

**Files:**
- Modify: `internal/wtsync/undo.go`, `internal/wtsync/plan.go` (`UndoneLine`), `internal/commands/sync_undo.go`, `cmd/wt/sync.go`, `scripts/verify-help-examples.sh`
- Test: `internal/wtsync/undo_test.go`, `internal/wtsync/plan_test.go`, `internal/commands/sync_undo_test.go`

**Interfaces:**
- Consumes: `SessionsAt`, `Sessions.Busy`, `Sessions.Label`, `agentLabel`, `idleNotice`, `tellIdle`, `listAgain`, `sessionsChanged`, `confirmAsk`, `idleIn`.
- Produces:
  - `func UndoBranches(mainRoot, branch string) ([]string, error)` (`wtsync`)
  - `func UndoneLine(work, at string, rewound bool) string`
  - `UndoOptions.Yes`, `UndoOptions.Confirm`, `UndoOptions.Relist`
  - `wtsync.Undo` keeps its signature. It refuses busy sessions only: announcing idle ones is its caller's.

- [ ] **Step 1: Write the failing tests**

`undo_test.go`:

```go
func TestUndoPassesAnIdleSessionAndRefusesABusyOne(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	resolved, _ := filepath.EvalSymlinks(wt)
	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	busy := []Agent{{Name: "f-1", Cwd: resolved, Kind: "interactive", Status: "idle"}, {Name: "f-2", Cwd: resolved, Kind: "interactive", Status: "busy"}}
	if _, err := Undo(dir, wts, busy, "feature", time.Now(), false); err == nil || !strings.Contains(err.Error(), "busy in it: f-2 +1") {
		t.Fatalf("err %v", err)
	}
	idle := busy[:1]
	if _, err := Undo(dir, wts, idle, "feature", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored under an idle session")
	}
}

func TestUndoBranchesNamesEveryBranchOfTheNewestRun(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := UndoBranches(dir, "feature"); err != nil || !reflect.DeepEqual(got, []string{"feature"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := UndoBranches(dir, "nothing-here"); err == nil || !strings.Contains(err.Error(), "no run to undo") {
		t.Fatalf("err %v", err)
	}
}
```

Add `reflect` to `undo_test.go` imports. `TestUndoRefusesACheckoutWithAnAgent` keeps passing: its agent has no status, so it is busy. Its assertion on `f-1` still holds.

`plan_test.go`:

```go
func TestUndoneLineSaysWhereTheBranchIs(t *testing.T) {
	if got := UndoneLine("bump", "abc1234", true); got != "wt: bump undone, back at abc1234" {
		t.Errorf("got %q", got)
	}
	if got := UndoneLine("bump", "def5678", false); got != "wt: bump undo stopped partway, still at def5678" {
		t.Errorf("got %q", got)
	}
}
```

`sync_undo_test.go`:

```go
func TestSyncUndoUnderAnIdleSessionAsksThenTellsIt(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rebased := gitOut(t, bump, "rev-parse", "HEAD")

	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var no bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &no); err != nil {
		t.Fatalf("err %v\n%s", err, no.String())
	}
	if len(asked) != 1 || asked[0] != "bump" || !strings.Contains(no.String(), "nothing undone") ||
		!strings.Contains(no.String(), "⚠ bump: session bump-1 (idle) is in it") {
		t.Fatalf("asked %v\n%s", asked, no.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != rebased {
		t.Fatal("HEAD moved after no")
	}

	opts.Confirm = func([]string) (bool, error) { return true, nil }
	var yes bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &yes); err != nil {
		t.Fatalf("err %v\n%s", err, yes.String())
	}
	want := "⚠ tell bump-1, idle in it:\n    wt: bump undone, back at " + old[:7] + "\n"
	if !strings.Contains(yes.String(), want) {
		t.Fatalf("no relay line %q:\n%s", want, yes.String())
	}
}

// A no to undo on a handed-over worktree must leave the handover's own lock:
// the question comes before any lock is taken over.
func TestSyncUndoNoLeavesAHandoverAndItsLock(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) { return false, nil }
	var out bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if has, _ := wtsync.HasPlan(gitDir); !has {
		t.Fatal("the handover is gone after no")
	}
	if l, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok || l.PID != st.Lock.PID || l.Started.Unix() != st.Lock.Started {
		t.Fatalf("lock %+v %v %v; a no must leave the run's lock alone", l, ok, err)
	}
}

func TestSyncUndoRefusesASessionThatWokeWhileAsked(t *testing.T) {
	ctx, bump := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rebased := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) { return true, nil }
	opts.Relist = func() ([]wtsync.Agent, error) {
		a := idleIn(t, bump, "bump-1")
		a[0].Status = "busy"
		return a, nil
	}
	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &undoOut); err == nil || !strings.Contains(err.Error(), "busy in it now: bump-1") {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != rebased {
		t.Fatal("HEAD moved")
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/wtsync/ ./internal/commands/ 2>&1 | tail`
Expected: FAIL to compile (`undefined: UndoBranches`, `undefined: UndoneLine`, `opts.Confirm undefined`).

- [ ] **Step 3: Implement**

`plan.go`:

```go
// UndoneLine is the line for a session idle in a worktree wt sync undo put
// back: its files moved again. An undo that aborted a handover but stopped
// before rewinding it says where the branch still is.
func UndoneLine(work, at string, rewound bool) string {
	if !rewound {
		return fmt.Sprintf("wt: %s undo stopped partway, still at %s", work, at)
	}
	return fmt.Sprintf("wt: %s undone, back at %s", work, at)
}
```

`undo.go`: extract the head of `Undo` into `newestRun` and export the branch list:

```go
// newestRun is the safety refs of the newest run that touched branch, and
// every safety ref there is.
func newestRun(mainRoot, branch string) (run, all []Safety, err error) {
	latest, ok, err := LatestSafety(mainRoot, branch)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("no run to undo for %s", branch)
	}
	if all, err = ListSafety(mainRoot); err != nil {
		return nil, nil, err
	}
	for _, s := range all {
		if s.Epoch == latest.Epoch {
			run = append(run, s)
		}
	}
	return run, all, nil
}

// UndoBranches is every branch Undo would put back for branch: the branches
// of the newest run that touched it. A caller names what it is about to touch,
// and asks, before Undo takes a single lock.
func UndoBranches(mainRoot, branch string) ([]string, error) {
	run, _, err := newestRun(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	branches := make([]string, len(run))
	for i, s := range run {
		branches[i] = s.Branch
	}
	return branches, nil
}
```

`Undo` starts with `run, all, err := newestRun(mainRoot, branch)` in place of its first 19 lines. Its session check:

```go
		if sessions := SessionsAt(agents, wt.Path); len(sessions.Busy()) > 0 {
			return nil, fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", s.Branch, sessions.Label(agentLabel))
		}
```

This drops the `agentPath`/`EvalSymlinks` lines. In the doc comment, "(clean, not mid-rebase, no agent)" becomes "(clean, not mid-rebase, no busy agent session; idle ones are for the caller to name and ask about before calling)".

`sync_undo.go`:

```go
type UndoOptions struct {
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	Now    func() time.Time
	// Force undoes a branch that has moved since the run, pinning a fresh
	// safety ref at the tip it discards first.
	Force bool
	// Yes, Confirm and Relist are RunOptions' own. Undo asks only when idle
	// sessions are in a checkout it would put back; a nil Confirm never asks.
	Yes     bool
	Confirm func(works []string) (bool, error)
	Relist  func() ([]wtsync.Agent, error)
}
```

In `SyncUndo`, after `agents` is listed and before `now`:

```go
	name := workName(ctx, target.Branch)
	branches, err := wtsync.UndoBranches(ctx.Repo.MainRoot, target.Branch)
	if err != nil {
		return err
	}
	paths := map[string]string{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			paths[wt.Branch] = wt.Path
		}
	}
	// Every session is named and asked about here, before wtsync.Undo takes
	// a single lock: taking over a handover's lock and then hearing no would
	// release it.
	told := map[string]wtsync.Sessions{}
	for _, b := range branches {
		path, ok := paths[b]
		if !ok {
			continue
		}
		sessions := wtsync.SessionsAt(agents, path)
		if len(sessions.Busy()) > 0 {
			return fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", b, sessions.Label(sessionLabel))
		}
		if len(sessions) > 0 {
			told[b] = sessions
			fmt.Fprintln(w, idleNotice(workName(ctx, b), sessions))
		}
	}
	if len(told) > 0 && opts.Confirm != nil && !opts.Yes {
		ok, err := opts.Confirm([]string{name})
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "nothing undone")
			return nil
		}
		if agents, err = listAgain(opts.Agents, opts.Relist); err != nil {
			return fmt.Errorf("cannot list agent sessions again (%v); nothing undone", err)
		}
		for _, b := range branches {
			if path, ok := paths[b]; ok {
				if why := sessionsChanged(told[b], wtsync.SessionsAt(agents, path)); why != "" {
					return fmt.Errorf("%s: %s; nothing undone", b, why)
				}
			}
		}
	}
```

The report loop:

```go
	for _, r := range restored {
		work := workName(ctx, r.Branch)
		fmt.Fprintln(w, restoredLine(work, r))
		if r.From == r.To && !r.Aborted {
			continue // nothing moved under anybody
		}
		if r.NotRewound {
			tellIdle(w, told[r.Branch], wtsync.UndoneLine(work, short(r.From), false))
			continue
		}
		tellIdle(w, told[r.Branch], wtsync.UndoneLine(work, short(r.To), true))
	}
	return err
```

`cmd/wt/sync.go`, undo `RunE`:

```go
			opts := commands.UndoOptions{Force: force, Yes: yes}
			if isTerminal(os.Stdin) && !yes {
				opts.Confirm = confirmAsk(cmd.InOrStdin(), cmd.OutOrStdout(), "undo")
			}
			return commands.SyncUndo(ctx, args[0], opts, cmd.OutOrStdout())
```

Flag: `undo.Flags().BoolVar(&yes, "yes", false, "do not ask first when a session is idle in a checkout")`.

Undo Long: "has a Claude\nsession in it," becomes "has a busy\nClaude session in it,". Append:

```
"\n\nA Claude session idle in a checkout is named and you are asked first\n" +
"(--yes skips; with no terminal it goes ahead), and each branch put back\n" +
"under one ends with a wt: line to pass on to it."
```

Example, appended (69 columns):

```
"  wt sync undo login-crash --yes    # not asked about an idle session"
```

Now that all three verbs follow the rule, the top-level `wt sync` Long's closing sentences say "a busy one keeps every verb off it. One marked (idle) is waiting for its person; a verb names it, asks first, and ends with a wt: line to pass on to it."

`scripts/verify-help-examples.sh`, after the undo `--force` line: `check sync "" 'wt sync run login-crash --yes' 'wt sync undo login-crash --yes'`

- [ ] **Step 4: Run**

Run: `go test -race ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/wtsync/undo.go internal/wtsync/undo_test.go internal/wtsync/plan.go internal/wtsync/plan_test.go internal/commands/sync_undo.go internal/commands/sync_undo_test.go cmd/wt/sync.go scripts/verify-help-examples.sh
git commit -m "feat(sync): undo under an idle session after asking once"
```

---

### Task 7: Spec, README and what's new

**Files:**
- Modify: `docs/superpowers/specs/2026-09-05-wt-sync-design.md`, `README.md:86-88`, `internal/about/whats-new.md`

- [ ] **Step 1: Spec §1, the modifier**

Replace `- **agent busy → deferred silently.** No message, no rebase.` with:

```markdown
- **agent busy → refused.** A session with `status: busy`, or any background
  session that is not `done`, keeps `run`, `resume` and `undo` off the
  worktree, and the table files it under skipped. **An idle session is not a
  refusal** (2026-09-10): the table marks it `name (idle)`, and a verb names
  it before anything moves, asks once (`--yes` skips; no terminal proceeds),
  lists the sessions again after the answer, and ends with the §5 line to
  relay to it. A status nobody has seen is busy. Several sessions in one
  worktree show as `name +1`.
```

- [ ] **Step 2: Spec §1, the finding**

Replace the first paragraph of "Agent detection, and what it does not cover" with:

```markdown
`claude agents --json` lists interactive sessions as well as background ones,
with `cwd`, `id`, `kind`, `name`, `pid`, `sessionId`, `startedAt`, `state` and
`status`. The first draft found no `pid` field and no idle/busy flag.
Execution finding, 2026-09-10: **the CLI now reports `status`, `pid` and
`waitingFor`**. `status` is `idle` or `busy` on every session. None of the 24
sessions listed that day carried `waitingFor`, so nothing reads it. `state` is
absent for every interactive session and `working`, `blocked` or `done` for
background ones. A blocked background session reports `status: idle` while it
waits on a question, so idle alone does not mean parked: only an interactive
session is ever idle. **Finished background sessions stay in the list** with
`state: done`. A `done` session is nobody; the tool filters it out or it will
knock on empty rooms. The call has a 10 s deadline, and a timeout is a refusal
like any other failure to list. It is enough to answer "is a Claude session
living in this worktree, and is it doing anything", which removes the
`devports` process-environment scan and its unknown-versus-gone ambiguity.
```

- [ ] **Step 3: Spec §5, §7 and the change log**

§5, under "After the fact:", add the undo lines to the block:

```
wt: state_stats undone, back at 3f2a9c1
wt: state_stats undo stopped partway, still at 8d04e7b
```

Add one sentence under the block: "A finish or an undo under an idle session prints its line for relaying to that session."

§7: "Every acting form prints what it is about to touch and, for more than one worktree, asks once before starting." becomes "Every acting form prints what it is about to touch, and asks once before starting when `run` has more than one worktree or any verb has an idle session in one."

Above "What changed on 2026-09-09", add:

```markdown
## What changed on 2026-09-10, in one place

- **An idle session is no longer a refusal** (§1). `claude agents --json` now
  reports `status`; only a busy session keeps a verb off a worktree. An idle
  one is named, asked about once, and told what moved with a §5 line (§5 gains
  the undo lines).
- **`claude agents --json` has a 10 s deadline**, and a timeout is a refusal.
```

- [ ] **Step 3b: The notes owed to §1 and §6, and the caller**

Append to the Step 1 modifier: "The session wt itself runs under is not counted. It is recognised by its `pid` being an ancestor of the wt process: an agent resolving a handover runs `wt sync resume` from inside the worktree."

§1 "where the two can differ", after the rerere bullet:

```markdown
- **A path a `script` claims cannot be simulated past.** A script can only be
  asked `--check` in the object store, never for the bytes, so the replay
  stops at that stop and the class reads `recipe?`: a run may still stop
  later, and hands over if it does.
- **`resolvedTree` hashes the resolved bytes as they are.** A real rebase runs
  clean filters and end-of-line normalisation on what a strategy writes, so in
  a repository whose filters rewrite resolver output the real tree can differ
  from the simulated one.
```

§6, under the example plan: "The `rr-cache` line in the example is not produced. A run passes `rerere.autoupdate=false` (§4), so a cached resolution is never staged, and `RenderPlan` has nothing to count."

- [ ] **Step 4: README and what's new**

`README.md`: the `resume` row gains `(--yes)`; the `undo` row's `(--force)` becomes `(--force, --yes)`.

`internal/about/whats-new.md`, directly under the intro paragraph, stamped with `date '+%Y-%m-%d %H:%M'` at commit time:

```markdown
## <date> <time> — an idle session no longer blocks wt sync

- A Claude session waiting for its person no longer keeps `wt sync run`,
  `resume` or `undo` off its worktree. The table shows it as `name (idle)`;
  the verb names it and asks first (`--yes` skips), and a finished rebase
  ends with a `wt: …` line to pass on to it. A busy session is still refused,
  and several sessions in one worktree show as `name +1`.
- The session running the command no longer counts against its own
  worktree, so an agent can `wt sync resume` the handover it resolved.
- `claude agents --json` gets 10 s to answer; a timeout is a refusal.
```

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/specs/2026-09-05-wt-sync-design.md README.md internal/about/whats-new.md
git commit -m "docs(sync): record session status and the idle-session rule"
```

---

### Task 8: Verify, hand-test, rebase, push, report

- [ ] **Step 1: Full checks**

Run: `make check` then `make build && scripts/verify-help-examples.sh`
Expected: both exit 0. The verifier lists the two new `--yes` examples as `ok`, and names no unrun example besides the known hook line.

- [ ] **Step 2: Hand test in a throwaway repo, with a stub claude**

`$CLAUDE_JOB_DIR/tmp/hand.sh` builds a repo shaped like `make_sync`, using this worktree's `./bin/wt`. A stub `claude` sits first on `PATH` and prints one interactive session in the `login-crash` worktree, first as `idle`, then as `busy`, then hanging. Check:

1. `wt sync` shows `session login-crash-3a (idle)` on a row under `ready`.
2. `wt sync login-crash` says `asks first`.
3. With the stub `busy`, `run` refuses with `busy in it`.
4. With the stub `idle`, `wt sync run login-crash --no-push </dev/null` prints the notice before `✓ rebased` and ends with the `tell` line.
5. `wt sync undo login-crash </dev/null` ends with `wt: login-crash undone, back at …`.
6. With the stub reporting a *busy* session whose `pid` is the script's own shell (an ancestor of wt), `run` and `undo` go ahead: the caller does not count.
7. With a stub that sleeps, `run` refuses after about 10 s with `did not answer within 10s`.

- [ ] **Step 3: Read-only look at real repos**

Run `./bin/wt sync` in `~/programmering/telcred/server` and `~/programmering/telcred/accessmanager`. Rows with idle sessions show `(idle)` and `+N`, and are no longer skipped for that reason. Never `run`, `resume` or `undo` there.

- [ ] **Step 4: Rebase onto origin/main, re-check, push**

```bash
git fetch origin && git rebase origin/main
```

A conflict in `internal/about/whats-new.md` (e.g. from `feat_wt/sweep`) keeps both entries, newest first by stamp. Re-run `make check` and the verifier if the rebase brought commits in. Then `git push --force-with-lease -u origin feat_wt/sync-idle-sessions`.

- [ ] **Step 5: Report**

Message the session named "overseer agent setup" with the head commit, each check and its result, a short summary, and the two out-of-scope notes above. No PR.
