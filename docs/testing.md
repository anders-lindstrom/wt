# Testing wt

Run the tests that cover what you changed while you work, and the whole
check once before you hand the work back. The full suite takes about 4
minutes on a busy machine. The command packages under `internal/commands`
run side by side, and their tests build real git repositories; the
slowest is `internal/commands/synccmd`. Run the suite once, in the
background, and keep working; never run it in a loop.

## Targets

| target | runs | wall time |
|---|---|---:|
| `make test-unit` | the packages whose tests build no git repository, with `-short` | about 8 s |
| `make test-fast` | `go test -short -timeout 20m ./...` | minutes |
| `make test` | `go test -timeout 30m ./...` | about 240 s |
| `make bats` | builds `bin/wt`, then every `test/*.bats` | about 140 s |
| `make lint` | `golangci-lint run` | |
| `make check` | lint, test and bats | about 7 minutes |

The `make test` time was taken on a 16-core Mac with a load average of
15-20 from other sessions; an idle machine is faster, by an amount nobody
measured. The other times are older and were taken under a heavier load.
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
| internal/wtsync | `go test ./internal/wtsync -run '<Area>'` first, then the whole package once (about 280 s) |
| internal/commands, one command family | `go test ./internal/commands/<pkg> -run '<Family>'`, see the examples below; the whole `./internal/commands/...` once before you finish (about 240 s) |
| a package-level hook variable or `Context` | `./internal/commands/...` once: the hooks live in kit (`FindGitHub`), provision (`ProbeSuperset`, `SupersetHostCommand`), session (`ListSessions`, `ListClaudeSessions`) and retire (`ScanProcesses`), and many packages' tests swap them |
| the `--json` shape or `schema/*.json` | the schema test and the JSON tests, see the examples below, and update docs/json.md and schema/versions.lock in the same commit |
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

## Time per package

Wall time of each package in one `go test -count=1 ./...` run, taken after
the command packages were split, under the load above:

| package | s | note |
|---|---:|---|
| internal/commands/synccmd | 237 | the critical path |
| internal/wtsync | 203 | |
| internal/commands/retire | 194 | |
| internal/commands/syncfix | 132 | |
| internal/commands/refs | 117 | |
| internal/commands | 91 | wt list and status, and tests across packages |
| internal/commands/purge | 66 | |
| internal/commands/synckeep | 64 | |
| internal/commands/admin | 62 | |
| internal/commands/provision | 54 | |
| internal/repo | 36 | |
| internal/github | 28 | fake `gh` shell scripts |
| internal/superset | 21 | about 4 s of it is real deadlines |
| internal/commands/session | 21 | |
| cmd/wt | 17 | in-process cobra; builds no binary |
| internal/git | 6 | |
| internal/commands/kit | 1 | |
| schema, config, find, gittest, about, quarantine, naming | 1-3 each | no git |

Go runs the packages side by side, so the suite takes about as long as
the slowest package. Before the split internal/commands alone took 785 s
of a 786 s run.

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
