# Testing wt

Run the tests that cover what you changed while you work, and the whole
check once before you hand the work back. The full suite takes about 2
minutes on a busy machine. The packages run side by side, most of their
tests run in parallel within them, and they build real git repositories;
the slowest are `internal/commands/retire` and `synckeep`. Run the suite
once, in the background, and keep working; never run it in a loop.

## Targets

| target | runs | wall time |
|---|---|---:|
| `make test-unit` | the packages whose tests build no git repository, with `-short` | about 8 s |
| `make test-fast` | `go test -short -timeout 20m ./...` | minutes |
| `make test` | `go test -timeout 30m ./...` | about 120 s |
| `make test-race` | `make test` under the race detector, as CI runs it | slower than `make test` |
| `make bats` | builds `bin/wt`, then every `test/*.bats` | about 140 s |
| `make test-mod` | the wt mod's suite, `claude plugin test mods/wt` | about 2 s |
| `make lint` | `golangci-lint run` | |
| `make check` | lint, test, bats and test-mod | about 7 minutes |

The `make test` time was taken on a 16-core Mac where other sessions kept
the load average at 10-20; the suite itself drives it past 50, so it is
bound by CPU, and a machine with fewer cores gains less from the parallel
tests. The other times are older and were taken under a heavier load.
Several full runs at once can pass the 30 minute timeout.

`make test-unit` covers internal/config, find, naming, about, quarantine,
gittest, superset, textwrap, jsonout, schema, and internal/commands/refpins
and testkit. internal/git, internal/github and internal/reposel are not in
it: their tests build real git repositories.

## What to run when

| you changed | run |
|---|---|
| a comment, docs, whats-new, wording only | nothing, or `go vet ./...` |
| one test or one function | `go test ./internal/<pkg> -run 'TestName$' -count=1` |
| internal/config, find, naming, about, quarantine, superset, github, git, repo, reposel, jsonout or gittest | `go test ./internal/<pkg>/...`; each is under 50 s, most under 5 s |
| internal/wtsync | `go test ./internal/wtsync -run '<Area>'` first, then the whole package once (about 110 s) |
| internal/agents | `go test ./internal/agents` (a few seconds); some of its tests read the real process table |
| internal/commands/syncrun | `go test ./internal/commands/syncfix ./internal/commands/synccmd -run 'Push\|OwnRemote\|FastForward\|Finish'`: syncrun's behaviour is tested from those two packages, so `go test ./internal/commands/syncrun` alone covers almost nothing |
| goroutines, locks, signals or anything shared between them | `make test-race` once, in the background: CI runs the suite with `-race` and `make check` does not |
| internal/commands, one command family | `go test ./internal/commands/<pkg> -run '<Family>'`, see the examples below; the whole `./internal/commands/...` once before you finish (about 120 s) |
| a package-level hook variable or `Context` | `./internal/commands/...` once: the hooks live in kit (`FindGitHub`, `MachineAgents`), supersetws (`Probe`, `HostCommand`) and session (`SessionsDeadline`); `testkit.Main` sets kit's, every TestMain the rest, and serial tests swap them, see "Parallel tests" |
| the `--json` shape or `schema/*.json` | the schema test and the JSON tests, see the examples below, and update docs/json.md in the same commit |
| cmd/wt flags, help text, completion | `go test ./cmd/wt` (about 16 s) |
| mods/wt | `make test-mod`; a change to its fixtures also `go test ./internal/commands/testkit -run Mod` |
| shell/, install.sh, stdout, stderr or exit codes | `make build && scripts/bats.sh test/<file>.bats`, the one file |
| before handing the work back | `make check` once, in the background; after a fix, run the touched family only |

The command packages, and what each holds:

| package | holds |
|---|---|
| `internal/commands` | wt list and wt status, and the tests that cross packages |
| `internal/commands/kit` | `Context`, Open/Locate, runs across repositories (`Bulk`, `AcrossRepos`) and the one question they ask, GitHub glue and its cache, tables, the signal watch |
| `internal/commands/testkit` | fixtures and fakes the tests share; `testkit.Main` is the TestMain of every command package but kit |
| `internal/commands/refpins` | the `refs/wt-swept/` pin store |
| `internal/commands/session` | wt session, wt attach |
| `internal/commands/provision` | wt new, checkout, setup, init, config, port |
| `internal/commands/supersetws` | registering a worktree as a Superset workspace, and deleting that workspace once wt removes the worktree |
| `internal/commands/retire` | wt remove, wt sweep |
| `internal/commands/refs` | wt refs sweep and restore, wt restore |
| `internal/commands/purge` | wt purge, wt refs purge |
| `internal/commands/syncrun` | what a sync run is made of: local trunk's fast-forward, own remotes, push, the idle-session check, finishing a rebase, the run's journal, what a signal says about a rebase in flight |
| `internal/commands/synccmd` | wt sync, wt up, wt sync rebase, push-to, doctor |
| `internal/commands/syncfix` | wt sync resume, wt sync undo |
| `internal/commands/synckeep` | wt sync keep; `keepstate` beneath it holds the keeper's state, lock and launchd job |
| `internal/commands/admin` | wt doctor, migrate, adopt, pr, hook, repos |

The `<pkg>test` packages (provisiontest, retiretest, refstest, purgetest,
synccmdtest) hold the fixtures of one family for the tests of other
packages.

Examples of the -run patterns:

```
go test ./internal/commands/refs -run 'Restore|Refs'
go test ./internal/commands/purge -run 'Purge'
go test ./internal/commands/synccmd -run 'Sync|Up|Push|FastForward|Handover|Plan'
go test ./internal/commands/syncfix -run 'Resume|Undo'
go test ./internal/commands/synckeep -run 'Keep'
go test ./internal/commands/retire -run 'Remove|Sweep|Quarantine|Apply'
go test ./schema ./internal/commands/... -run 'JSON|Schema|Matches'
```

-run picks tests by name, not by file, so a pattern misses the few
tests of a family whose names say something else. Name the package: a
-run pattern against the wrong one selects no tests, and go test then
prints `ok` with `[no tests to run]`, which looks like a pass. The run of
`./internal/commands/...` before you finish covers the rest.

## JSON output

A test of a `--json` output passes it to `testkit.ValidateJSON`, usually
through the family's helper (`retiretest.RemoveJSON`,
`synccmdtest.DecodeSyncRun`, `provisiontest.NewPlanOf`). It validates the
output against the schema in `schema/` with every listed object closed:
a key the schema does not declare fails the test, at any depth. A field
added to the output therefore needs its schema property, the docs/json.md
row and an x-version minor in the same commit. The shipped schemas stay
open; only the test closes them.

The wt mod's tests (mods/wt/tests) answer it with wt output kept in
`mods/wt/tests/fixtures/<schema>.ts`, each `export default` and the JSON,
because a mod can import only code. `TestTheModsFixturesMatchTheirSchemas`
in internal/commands/testkit validates every one against its current
schema, so a JSON change that breaks the mod's input fails `go test`; bring
the fixture up to the new schema in the same commit.

## Bats

`test/helpers.bash` keeps the bats tests off the machine running them, as
`gittest.Isolate` does for the Go tests: HOME, XDG_CONFIG_HOME and git's
global config are the run's own (with an identity to commit as), git reads
no system config and no GIT_ variable from the environment, WT_ROOTS and
CODEX_HOME are unset, and no claude is on the PATH. Go's caches stay where
they were. Every `test/*.bats` loads it; a test that wants a claude puts its
own fake first on the PATH, and one that wants its own git config sets
GIT_CONFIG_GLOBAL in its setup. CI runs bats through `scripts/bats.sh`, under
the same deadline as `make bats`.

## Parallel tests

A test that calls `t.Parallel()` runs beside the other parallel tests of
its package, and Go starts them only once every serial test of that
package has finished. Most tests in the command packages, internal/wtsync,
internal/repo, internal/github and internal/superset are parallel: each
builds its own repositories under `t.TempDir()`. HOME and XDG_CONFIG_HOME
are set once per test binary and shared, so a parallel test does not write
under them; `testkit.UserConfigIn` makes a test serial.

A test stays serial when it touches what the whole test binary shares,
and its first line says which, as `// Not parallel: <why>`:

- `t.Setenv` or `t.Chdir`, which Go refuses in a parallel test: a fake
  `git`, `claude` or `superset` on the PATH, HOME, XDG_CONFIG_HOME,
  CODEX_HOME, TMPDIR;
- a package-level variable that other tests read, for example
  `kit.MachineAgents`, `retire`'s `vanishWait`, `refpins.AfterBranchMoved`,
  `supersetws.Probe` and `HostCommand`, provision's
  `cloneDir` and `cloneOneFile`, `synccmd`'s `assessWorkers`,
  superset's `readDeadline`, agents' process listers and `Deadline`;
- signals: `git.Interrupt` and `git.KillRunning` signal the process groups
  `RunBounded` is waiting on, `syncrun.OnInterrupt` removes every temporary
  directory wtsync has open, and a few tests send a signal to the test
  process itself; a test that only re-runs itself through
  `testkit.InterruptedChild` shares nothing with that child and is parallel;
- the registries of the locks held and the interrupt journal, which belong
  to the process;
- a wall-clock bound that a loaded machine can break.

Every serial test of a package finishes before its first parallel test
starts, so a signal from a serial test never reaches a parallel test's git.
Keep it that way: a test that signals or swaps a hook does not call
`t.Parallel()`.

A test's own fakes go with the call, not into a package variable:

- `testkit.FakeGitHub(t, ctx, …)` and `testkit.StubGitHub(ctx, …)` set
  `ctx.GitHubFake`;
- `provisiontest.FakeSuperset`, `Stub` and `SupersetAt` set
  `ctx.SupersetFake`;
- `testkit.ListedSessions(ctx, …)`, `ClaudeLists(ctx, …)` and
  `retire.RealProcesses(ctx)` set `ctx.Agents`, the sessions and processes
  the context sees; `testkit.AgentsOf(ctx)` is that struct, for any other
  field, as a failing listing;
- `testkit.CrashAt(point)`, or any `quarantine.Hooks` (a failing config
  write of a restore among them), goes in the `QuarantineHooks` field of
  the remove, sweep, restore or purge options, and what a removal does
  after its last look at the checkout, or between a branch's ref and its
  config, in `RemoveOptions.AfterFinalCheck` and `AfterRefDeleted`.
- the platform, the home directory and the launchctl the keeper sees go
  in `ctx.Machine` (`kit.Machine`), and the `--all` commands of wt sync
  keep take the machine they open each repository on: a zero field is
  the machine's own, so no test sets HOME or PATH for them.
- the roots `kit.Find` scans go in `ctx.Roots`, so `wt attach` finds a
  test's own fixture without WT_ROOTS; a command across repositories
  (`--all`) still reads WT_ROOTS, and its test is serial;
- the claude `wt session stop` runs goes in the session env's `claude`
  (`fakeClaudeStop`), not on the PATH;
- a keeper pass turns commit signing off with `git.NeverSign`, on the
  gits it starts rather than in the process environment; it is only ever
  turned on, and no test depends on a signed commit, so keeper passes run
  side by side.

The package variables behind them (`kit.FindGitHub`,
`kit.MachineAgents`, `supersetws.Probe`) are set once, in each
package's TestMain: `testkit.Main` sets kit's two, to no gh, no session and
no process, and kit's own TestMain repeats it. Only serial tests swap
them, as `provisiontest.SupersetSaying` and `StaleSuperset` swap
`supersetws.Probe`, and parallel tests only read them. A command that opens a
context of its own, as `--all` does for each repository, does not see the
fakes on the test's context; `testkit.MachineClaudeLists` swaps
`kit.MachineAgents`' claude for such a test, which is then serial.

Only the tests of process detection read the real process table:
`testkit.FakeClaude`, which leaves out the session running the test, and
`retire.RealProcesses`. `testkit.Main` gives ps a minute, `agents.Deadline`,
where wt gives it 10 s: a machine running several suites at once has taken
longer than that to start it.

A new test calls `t.Parallel()` first, unless one of the reasons above
applies.

## Time per package

Wall time of each package in one `go test -count=1 ./...` run, under the
load above. "serial" is the part spent before the first parallel test
started: the tests that stay serial run one after another.

| package | s | serial s | note |
|---|---:|---:|---|
| internal/commands/retire | 121 | 75 | the remove and sweep --reap tests: real processes, signals, a 5 s grace |
| internal/commands/synckeep | 119 | 119 | all serial: the keeper sets GIT_CONFIG_* for its process, the tests set HOME |
| internal/commands/synccmd | 112 | 52 | |
| internal/wtsync | 107 | 58 | |
| internal/commands/refs | 93 | 34 | |
| internal/commands/admin | 53 | 41 | |
| internal/commands | 51 | 27 | wt list and status, and tests across packages |
| internal/commands/session | 47 | 45 | all serial: WT_ROOTS set for the binary, a fake claude on the PATH |
| internal/commands/provision | 41 | 23 | |
| internal/commands/syncfix | 40 | 0 | |
| internal/superset | 34 | 21 | about 4 s of it is real deadlines |
| internal/commands/purge | 34 | 0 | |
| cmd/wt | 27 | 27 | in-process cobra; builds no binary |
| internal/github | 15 | | fake `gh` shell scripts |
| internal/repo | 13 | | |
| internal/git | 10 | | |
| schema, config, find, gittest, about, quarantine, naming, kit, refpins | 0-4 each | | no git |

Go runs the packages side by side, so the suite takes about as long as
the slowest package. Before the split internal/commands alone took 785 s
of a 786 s run; before the tests ran in parallel the suite took about
230 s, with internal/commands/synccmd at 223 s.

## What -short skips

A test that is too slow for the working loop starts with

```go
if testing.Short() {
	t.Skip("waits out a real deadline")
}
```

and `make test-fast` leaves it out. `make test` and `make check` still
run it. The guarded tests are:

- the loops that stop a restore, a removal, a purge or a sync at every
  step and run it again over real repositories;
- TestRefsPlanTime, which builds 300 commits and 150 refs to time the
  plan;
- the slowest of the tests that wait out a real deadline, kill a process
  that does not answer, or interrupt a run with a signal; not every one
  is guarded;
- the slow tests, a second or more, which build an origin, clones,
  worktrees and stacks, in the packages refs, purge, synccmd, syncfix,
  synckeep, admin and internal/commands (the own_remote, push_to,
  sync_push, sync_run, sync_resume and sync_undo files among them;
  `grep -rln testing.Short --include='*_test.go' internal/commands`
  lists the files). One test each of sync undo, sync resume, sync
  push-to and own-remote stays in, so -short still runs those commands:
  TestSyncUndoJSONReportsWhatItPutBack,
  TestSyncResumeJSONReportsTheFinishedRebase,
  TestSyncPushToRecordsSaysAndForgets and
  TestUpRefusesWhenTheFastForwardItselfFails.

internal/wtsync keeps its push and undo tests under -short: they guard
against pushing over someone's commit and against losing one, and the
package is not the slowest one anyway.

`grep -rn -B1 'testing.Short' --include='*_test.go' .` lists them. They
were about 440 s of the old single internal/commands package and 22 s of
internal/wtsync. Even without them the command packages have several
hundred tests left that each build a repository, so `make test-fast`
still takes minutes; `make test-unit` is the loop that finishes in
seconds.

Guard a new test the same way when it waits on a clock, or when it
builds an origin or a stack and you would not want it in a loop.
