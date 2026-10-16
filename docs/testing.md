# Testing wt

Run the tests that cover what you changed while you work, and the whole
check once before you hand the work back. The full suite takes about 20
minutes on a busy machine, nearly all of it in `internal/commands`, whose
tests build real git repositories one after another. Run it once, in the
background, and keep working; never run it in a loop.

## Targets

| target | runs | wall time |
|---|---|---:|
| `make test-unit` | the packages whose tests build no git repository, with `-short` | about 8 s |
| `make test-fast` | `go test -short -timeout 20m ./...` | about 540 s |
| `make test` | `go test -timeout 30m ./...` | about 1120 s |
| `make bats` | builds `bin/wt`, then every `test/*.bats` | about 140 s |
| `make lint` | `golangci-lint run` | |
| `make check` | lint, test and bats | about 21 minutes |

The times were taken on a 16-core Mac with a load average of 17-34 from
other sessions; an idle machine is faster, by an amount nobody measured.
Three full runs at once can pass the 30 minute timeout. Under
`make test-fast` internal/commands alone took 523 s, which is why its
timeout is 20 minutes.

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
| internal/commands, one command family | `go test ./internal/commands -run '<Family>'`, see the examples below; the whole package once before you finish (about 1100 s) |
| a package-level hook variable or `Context` | the whole internal/commands package: many tests swap the same globals |
| the `--json` shape or `schema/*.json` | the schema test and the JSON tests, see the examples below, and update docs/json.md and schema/versions.lock in the same commit |
| cmd/wt flags, help text, completion | `go test ./cmd/wt` (about 16 s) |
| shell/, install.sh, stdout, stderr or exit codes | `make build && scripts/bats.sh test/<file>.bats`, the one file |
| before handing the work back | `make check` once, in the background; after a fix, run the touched family only |

Examples of the -run patterns:

```
go test ./internal/commands -run 'Restore|Purge'
go test ./internal/commands -run 'Sync|Up|Push|FastForward|Handover|Plan'
go test ./internal/commands -run 'Remove|Sweep|Quarantine|Apply'
go test ./schema ./internal/commands -run 'JSON|Schema|Matches'
```

-run picks tests by name, not by file, so a pattern misses the few
tests of a family whose names say something else. The run of the whole
package before you finish covers them.

## Time per package

Wall time of each package in one `go test ./...` run, under the load
above:

| package | s | note |
|---|---:|---|
| internal/commands | 1117 | the critical path; its tests run one after another |
| internal/wtsync | 279 | |
| internal/repo | 45 | |
| internal/superset | 16 | about 4 s of it is real deadlines |
| cmd/wt | 16 | in-process cobra; builds no binary |
| internal/github | 16 | fake `gh` shell scripts |
| internal/git | 7 | |
| schema | 3 | |
| config, find, gittest, about, quarantine, naming | 1-3 each | no git |

Go runs the packages side by side, so the suite takes as long as
internal/commands.

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
- the slow tests, a second or more, in these internal/commands files,
  which build an origin, clones, worktrees and stacks: own_remote*,
  push_to, sync_push, sync_run, sync_run_json, sync_resume, sync_undo
  and trunk_sync_run. One test each of sync undo, sync resume, sync
  push-to and own-remote stays in, so -short still runs those commands:
  TestSyncUndoJSONReportsWhatItPutBack,
  TestSyncResumeJSONReportsTheFinishedRebase,
  TestSyncPushToRecordsSaysAndForgets and
  TestUpRefusesWhenTheFastForwardItselfFails.

internal/wtsync keeps its push and undo tests under -short: they guard
against pushing over someone's commit and against losing one, and the
package finishes long before internal/commands anyway.

`grep -rn -B1 'testing.Short' --include='*_test.go' .` lists them. They
are about 440 s of internal/commands and 22 s of internal/wtsync. Even
without them internal/commands has several hundred tests left that each
build a repository, so `make test-fast` takes half as long as
`make test`, not seconds; `make test-unit` is the loop that finishes in
seconds.

Guard a new test the same way when it waits on a clock, or when it
builds an origin or a stack and you would not want it in a loop.
