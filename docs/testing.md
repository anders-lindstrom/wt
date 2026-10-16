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
| `make bats` | builds `bin/wt`, then every `test/*.bats` | about 140 s |
| `make lint` | `golangci-lint run` | |
| `make check` | lint, test and bats | about 7 minutes |

The `make test` time was taken on a 16-core Mac where other sessions kept
the load average at 10-20; the suite itself drives it past 50, so it is
bound by CPU, and a machine with fewer cores gains less from the parallel
tests. The other times are older and were taken under a heavier load.
Several full runs at once can pass the 30 minute timeout.

`make test-unit` covers internal/config, find, naming, about, quarantine,
gittest, superset and schema. internal/git and internal/github are not
in it: their tests build real git repositories.

## What to run when

| you changed | run |
|---|---|
| a comment, docs, whats-new, wording only | nothing, or `go vet ./...` |
| one test or one function | `go test ./internal/<pkg> -run 'TestName$' -count=1` |
| internal/config, find, naming, about, quarantine, superset, github, git, repo or gittest | `go test ./internal/<pkg>/...`; each is under 50 s, most under 5 s |
| internal/wtsync | `go test ./internal/wtsync -run '<Area>'` first, then the whole package once (about 110 s) |
| internal/commands, one command family | `go test ./internal/commands/<pkg> -run '<Family>'`, see the examples below; the whole `./internal/commands/...` once before you finish (about 120 s) |
| a package-level hook variable or `Context` | `./internal/commands/...` once: the hooks live in kit (`FindGitHub`), provision (`ProbeSuperset`, `SupersetHostCommand`), session (`ListSessions`, `ListClaudeSessions`) and retire (`ScanProcesses`); every TestMain sets them, and serial tests swap them, see "Parallel tests" |
| the `--json` shape or `schema/*.json` | the schema test and the JSON tests, see the examples below, and update docs/json.md in the same commit |
| cmd/wt flags, help text, completion | `go test ./cmd/wt` (about 16 s) |
| shell/, install.sh, stdout, stderr or exit codes | `make build && scripts/bats.sh test/<file>.bats`, the one file |
| before handing the work back | `make check` once, in the background; after a fix, run the touched family only |

The command packages, and what each holds:

| package | holds |
|---|---|
| `internal/commands` | wt list and wt status, and the tests that cross packages |
| `internal/commands/kit` | `Context`, Open/Locate, repository selection, GitHub glue and its cache, tables, the `--json` vocabulary, signals |
| `internal/commands/testkit` | fixtures and fakes the tests share; `testkit.Main` is the TestMain of every command package but kit |
| `internal/commands/refpins` | the `refs/wt-swept/` pin store |
| `internal/commands/session` | wt session, wt attach |
| `internal/commands/provision` | wt new, checkout, setup, init, config, port, Superset registration |
| `internal/commands/retire` | wt remove, wt sweep |
| `internal/commands/refs` | wt refs sweep and restore, wt restore |
| `internal/commands/purge` | wt purge, wt refs purge |
| `internal/commands/synccmd` | wt sync, wt up, own remotes, push, keeper state |
| `internal/commands/syncfix` | wt sync resume, wt sync undo |
| `internal/commands/synckeep` | wt sync keep |
| `internal/commands/admin` | wt doctor, migrate, adopt, pr, hook |

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

## Parallel tests

A test that calls `t.Parallel()` runs beside the other parallel tests of
its package, and Go starts them only once every serial test of that
package has finished. Most tests in the command packages, internal/wtsync,
internal/repo, internal/github and internal/superset are parallel: each
builds its own repositories under `t.TempDir()`. HOME and XDG_CONFIG_HOME
are set once per test binary and shared, so a parallel test does not write
under them; `testkit.UserConfigIn` makes a test serial.
internal/commands/synckeep, internal/commands/session and
internal/commands/kit have no parallel tests.

A test stays serial when it touches what the whole test binary shares,
and its first line says which, as `// Not parallel: <why>`:

- `t.Setenv` or `t.Chdir`, which Go refuses in a parallel test: a fake
  `git`, `claude` or `superset` on the PATH, HOME, XDG_CONFIG_HOME,
  CODEX_HOME, TMPDIR;
- a package-level variable that other tests read, for example
  `session.ListSessions` and `ListClaudeSessions`, `retire.ScanProcesses`
  and `ReapProcesses`, `retire`'s `vanishWait` and `afterFinalCheck`,
  `repo.AfterRefDeleted` and `BeforeConfigAdd`, `refpins.AfterBranchMoved`,
  `provision.ProbeSuperset` and `SupersetHostCommand`, provision's
  `cloneDir` and `cloneOneFile`, `synccmd`'s `assessWorkers` and
  `KeepGOOS`, superset's `readDeadline`, wtsync's process listers and
  deadlines;
- signals: `git.Interrupt` and `git.KillRunning` signal the process groups
  `RunBounded` is waiting on, `kit.OnInterrupt` also removes every temporary
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
- `testkit.CrashAt(point)`, or any `quarantine.Hooks`, goes in the
  `QuarantineHooks` field of the remove, sweep, restore or purge options.

The package variables behind them (`kit.FindGitHub`,
`provision.ProbeSuperset`, the session listers) are set once, in each
package's TestMain, kit's setting only `FindGitHub`. Only serial tests swap
them, as `provisiontest.SupersetSaying` and `StaleSuperset` swap
`ProbeSuperset`, and parallel tests only read them. A command that opens a
context of its own, as `--all` does for each repository, does not see the
fakes on the test's context.

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
| internal/commands/session | 47 | 45 | all serial: a fake claude on the PATH |
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
