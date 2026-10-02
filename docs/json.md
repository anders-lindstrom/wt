# wt's JSON output

`wt status`, `wt up`, `wt sweep`, `wt sync` and its verbs, `wt new`,
`wt checkout`, `wt remove`, `wt restore`, `wt quarantine purge` and `wt refs` print JSON on stdout when given `--json`, for
tools that drive wt (a git client, an editor, a script). Their human output is
unchanged without it. A quarantine's `recovery.json` is JSON with a schema too.

## Versions

Each output has a schema with a semantic version, and says which it follows:

- `schema` — the **major** version, an integer. A new major is a change that
  breaks a reader: a field renamed or removed, a type or a meaning changed. It
  comes as a new schema file (`up.v2.json`); v1 is never edited that way.
- `schemaVersion` — the full version, `MAJOR.MINOR.PATCH`. A **minor** adds a
  field or an enumeration value; a **patch** changes only wording.

So within one major, fields are only added and enumerations only grow: a reader
ignores fields it does not know and treats an unknown value as "not one I know",
never as an error. wt's tests fail when a schema file changes without a new
version, and when an output's `schemaVersion` is not its schema's.

| Version | Change |
|---|---|
| 1.0.0 | `wt status --json` and `wt up --json`, as wt 53bc0d8 printed them |
| 1.1.0 | `schemaVersion` in both outputs; the schemas published under `schema/` and by `wt schema` |
| 1.2.0 | `trunkSync` in both: what a run did, or would do, to local trunk after its fetch |

Each schema is versioned on its own; `sweep-plan` and `sweep` started at 1.0.0.
`sweep-plan` 1.1.0 added the `kept` values `operation`, `hiddenChanges`,
`headUnreachable`, `reachUnknown`, `submoduleUnreachable` and `nestedWorktree`.

| Schema | Version | Change |
|---|---|---|
| `sync` | 1.0.0 | `wt sync --json`, the overview |
| `sync-run` | 1.0.0 | `wt sync run`, `resume` and `undo --json`, the result |
| `new-plan`, `checkout-plan` | 1.0.0 | `wt new` and `wt checkout --dry-run --json`, the plan |
| `new`, `checkout` | 1.0.0 | `wt new` and `wt checkout --json`, the result |
| `sync-run` | 1.1.0 | `trunkSync`: what the run did to local trunk after its fetch |
| `sweep` | 1.1.0 | `quarantine`, top-level and on each item: the folders `--quarantine` moved worktrees into |
| `sweep-plan` | 1.2.0 | `quarantine`: the folder `--quarantine` names, which the token covers |
| `recovery` | 1.0.0 | `<dir>/recovery.json`, the journal of `--quarantine` and `wt restore` |
| `restore-plan`, `restore` | 1.0.0 | `wt restore <dir> --dry-run --json`, the plan, and `--json`, the result |
| `sweep` | 1.2.0 | `superset` on each item: what came of a removed worktree's Superset workspace |
| `remove-plan`, `remove` | 1.0.0 | `wt remove <work> --dry-run --json`, the plan, and `--yes --json`, the result |
| `checkout-plan`, `checkout` | 1.1.0 | `source`, `remote`, `remoteRef` (and `remoteCommit` in the plan, `upstream` in the result): a branch created from a remote-tracking ref; problems `branchAmbiguous`, `remoteBranchMissing` |
| `new-plan` 1.1.0, `checkout-plan` 1.2.0, `status` 1.3.0 | | a repository with no configuration file runs on detected defaults: `configured` false with no problem; `noConfiguration` only when no trunk can be detected either |
| `recovery` | 1.1.0 | `restore.createdBranch`: this restore made the branch again, so a run after a failed one finishes its config |
| `quarantine-purge-plan`, `quarantine-purge` | 1.0.0 | `wt quarantine purge <dir> --json`, the plan, and `--yes --json`, the result |
| `recovery` | 1.2.0 | `purge`: the journal of `wt quarantine purge`, in `recovery.purging.json`, which replaces `recovery.json` before anything is deleted |
| `restore-plan` | 1.1.0 | the problem `purging`: a purge began, so the restore refuses |
| `status` 1.4.0, `up` 1.3.0, `sync` 1.1.0, `sweep-plan` 1.3.0, `new-plan` 1.2.0, `checkout-plan` 1.3.0 | | [`trunkSource`](#trunksource--how-trunk-was-found): how trunk was found. Also marks the fixed detection order: a conventional name before the checked-out branch |
| `remove-plan`, `sweep-plan` | 1.1.0, 1.4.0 | `keepSuperset`: `--keep-superset` was given, which the token covers |
| `remove`, `sweep` | 1.1.0, 1.3.0 | the `superset` result `optedOut`: `--keep-superset`, Superset was not asked |
| `remove-plan` | 1.2.0 | `forceWith`, top-level and on each problem: the `--force` categories given, and the one that goes past each problem |
| `remove` | 1.2.0 | `forced`: the `--force` categories the removal went past; `forceWith` on each problem |
| `remove-plan`, `remove` | 1.3.0 | [`trunkSource`](#trunksource--how-trunk-was-found): how trunk was found |
| `status` 1.4.1, `up` 1.3.1, `sync` 1.1.1, `sweep-plan` 1.4.1, `new-plan` 1.2.1, `checkout-plan` 1.3.1 | | detection is origin/HEAD, then the first of `development`, `main`, `master`, else it fails: `currentBranchGuess` is no longer produced, and a repository nothing names trunk for is `noConfiguration` — also one whose file has no `MAIN_BRANCH` |
| `refs-sweep-plan`, `refs-sweep`, `refs-restore-plan`, `refs-restore`, `refs-purge-plan`, `refs-purge`, `refs-swept` | 1.0.0 | `wt refs sweep`, `restore` and `purge --json`, each plan and result, and `wt refs swept --json` |
| `sweep-plan` | 1.5.0 | `configFingerprint`: the configuration file the token covers, so two plans can be compared |
| `sweep` | 1.4.0 | `runId`, and `pin` on each item: the branches a sweep deletes are moved into a run under `refs/wt-swept/`. `restoreCommand` is now `wt refs restore <runId> --only refs/heads/<branch> --yes`, or `wt restore <dir>` for a quarantined worktree's branch; the `git branch` form is no longer produced |
| `remove` | 1.4.0 | `runId`, and `pin` on each step: a deleted branch is moved into a run of its own under `refs/wt-swept/`; `restoreCommand` for it is `wt refs restore` |
| `status` 1.5.0, `sync` 1.2.0 | | [Codex sessions](#sessions--claude-and-codex): `sessions[].kind` is `claude` or `codex` |
| `remove-plan` | 1.4.0 | [Codex sessions](#sessions--claude-and-codex): `kind` on each of `sessions` |
| `remove` | 1.4.1 | wording: a session problem is any agent session, and `sessionsUnknown` any listing that failed |

A string field that has no value is `null`, not `""`. Paths are absolute.

## Signals and what wt starts

Every git, script and build wt runs gets a process group of its own. Its
deadline and a handled SIGINT or SIGTERM to wt stop that whole group, however
many processes it forked, before wt writes its object and exits 130. Each verb's
section below says what that object holds.

SIGKILL, or a crash, leaves wt no chance to do that. So a command that can write
or run long — a `provision.sh`, a build, a deferred step, a git that changes
something — is also listed with a small helper wt starts the first time it needs
one: wt's own binary as `wt reaper`, in a process group of its own, holding
none of wt's stdout, stderr or other descriptors. When wt is gone however it went, the reaper
sends each listed group SIGTERM, then SIGKILL after 200 ms, and exits. So a
caller only has to kill wt, or the process group it started wt in, and nothing
wt started keeps writing to a worktree. The groups go within about a quarter of
a second, not in the same instant. A group is taken off the list once its
command has exited, before wt reaps it, so the reaper never signals a group id
that has been reused since. The reaper reads everything wt wrote before it acts.
A process the command left running in its group after the command itself
exited is not stopped, just as a handled signal does not stop it. Reads — `wt
list`, `wt status` (its rebase simulation only adds objects), `wt schema`, the
queries every verb makes — never start a reaper.

Why not run everything in wt's own group, so that one signal from the caller
stops the lot? Then wt could stop its children only by signalling its own group.
That group also holds the other commands of a pipeline (`wt up --json | jq`), or
the script that called wt without job control. Escalating to SIGKILL would kill
wt before it wrote its object. And signalling only its direct children would let
their children run on.

## Sessions — Claude and Codex

A session is an agent working in a worktree. A busy one refuses what would
change files under it; an idle one is waiting for its person. The session wt
itself runs under is never listed. Wherever sessions are listed, `kind` says
whose it is:

| `kind` | Found by | `name` | `state` |
|---|---|---|---|
| `claude` | `claude agents --json` | the session's name | `idle` for an interactive session with status idle; anything else is `busy` |
| `codex` | `ps`, each codex process's working directory, and the turn markers in `$CODEX_HOME/sessions` (default `~/.codex/sessions`) | `codex exec`, `codex`, or `codex (<originator>)` | see below |

A Codex session is one of two things:

- **A codex process working in the worktree**: `codex exec`, `codex review`
  or the interactive `codex`, with its working directory (or `--cd`) there.
  The launcher and the binary it starts are one session. `codex exec` and
  `codex review` are `busy` for as long as they live. An interactive `codex`
  is `idle` when it has a thread of its own logged in its directory since it
  started and no thread it could be running has a turn open; it is `busy`
  when one has — or when no log of its own can be found or read, which is
  not knowing, never idle. Where sessions carry them, `id` is `codex-<pid>`
  and `pid` the topmost process.
- **A thread an app-server runs there** (the Codex desktop app, its managed
  daemon, the Claude Code plugin): no process stands in the worktree, so it
  is a session only while its turn is open, it was written to in the last 24
  hours and an app-server that could be running it is alive, and it is
  always `busy`. `name` is `codex (<originator>)`, `id` the thread id, `pid`
  the app-server's, or null when more than one could be running it. An
  interactive `codex` whose thread an app-server runs is listed as both.

An open turn with nothing alive behind it is a crashed session and is not
listed. Only your own processes are looked at.

The listing fails, which is `sessionsError` or `sessionsUnknown` and never an
empty list, when `ps` fails, when a codex process will not say where it works
and neither its launcher, its `--cd` nor its threads do, when the logs cannot
be walked, and when a log that cannot be read may be a thread an app-server
is running.

Under an app-server nothing names the thread wt was started from, so the
session left out as wt's own is every such thread working in wt's own
working directory.

## JSON Schema

Every object has a JSON Schema (draft 2020-12), for validating what you read or
generating types from it:

| Output | Schema | `$id` |
|---|---|---|
| `wt status --json` | [`schema/status.v1.json`](../schema/status.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/status.v1.json` |
| `wt up --json` | [`schema/up.v1.json`](../schema/up.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/up.v1.json` |
| `wt sweep --dry-run --json` | [`schema/sweep-plan.v1.json`](../schema/sweep-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sweep-plan.v1.json` |
| `wt sweep --yes --json` | [`schema/sweep.v1.json`](../schema/sweep.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sweep.v1.json` |
| `wt sync --json` | [`schema/sync.v1.json`](../schema/sync.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sync.v1.json` |
| `wt sync run\|resume\|undo --json` | [`schema/sync-run.v1.json`](../schema/sync-run.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sync-run.v1.json` |
| `wt new --dry-run --json` | [`schema/new-plan.v1.json`](../schema/new-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/new-plan.v1.json` |
| `wt new --json` | [`schema/new.v1.json`](../schema/new.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/new.v1.json` |
| `wt checkout --dry-run --json` | [`schema/checkout-plan.v1.json`](../schema/checkout-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/checkout-plan.v1.json` |
| `wt checkout --json` | [`schema/checkout.v1.json`](../schema/checkout.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/checkout.v1.json` |
| `<dir>/recovery.json` | [`schema/recovery.v1.json`](../schema/recovery.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/recovery.v1.json` |
| `wt restore --dry-run --json` | [`schema/restore-plan.v1.json`](../schema/restore-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/restore-plan.v1.json` |
| `wt restore --json` | [`schema/restore.v1.json`](../schema/restore.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/restore.v1.json` |
| `wt remove --dry-run --json` | [`schema/remove-plan.v1.json`](../schema/remove-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/remove-plan.v1.json` |
| `wt remove --yes --json` | [`schema/remove.v1.json`](../schema/remove.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/remove.v1.json` |
| `wt quarantine purge --dry-run --json` | [`schema/quarantine-purge-plan.v1.json`](../schema/quarantine-purge-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/quarantine-purge-plan.v1.json` |
| `wt quarantine purge --yes --json` | [`schema/quarantine-purge.v1.json`](../schema/quarantine-purge.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/quarantine-purge.v1.json` |
| `wt refs sweep --dry-run --json` | [`schema/refs-sweep-plan.v1.json`](../schema/refs-sweep-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-sweep-plan.v1.json` |
| `wt refs sweep --yes --json` | [`schema/refs-sweep.v1.json`](../schema/refs-sweep.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-sweep.v1.json` |
| `wt refs restore --dry-run --json` | [`schema/refs-restore-plan.v1.json`](../schema/refs-restore-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-restore-plan.v1.json` |
| `wt refs restore --yes --json` | [`schema/refs-restore.v1.json`](../schema/refs-restore.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-restore.v1.json` |
| `wt refs purge --dry-run --json` | [`schema/refs-purge-plan.v1.json`](../schema/refs-purge-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-purge-plan.v1.json` |
| `wt refs purge --yes --json` | [`schema/refs-purge.v1.json`](../schema/refs-purge.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-purge.v1.json` |
| `wt refs swept --json` | [`schema/refs-swept.v1.json`](../schema/refs-swept.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/refs-swept.v1.json` |

They are built into the binary: `wt schema` lists them and `wt schema up` prints
one, so the schema you read is the one for the wt you run. Validate against that
one. Its enumerations are exact for that wt, and a newer wt may add values and
fields within the same schema number — so an older copy of a schema can reject
newer output, and a reader that does not validate treats an unknown value as
unknown. wt's own tests validate every `--json` output against these schemas.

## `wt status [<work>] --json` — the plan

What `wt up` would start on, from local state alone. It **writes nothing**: no
fetch, no rebase simulation, no pull-request cache; every git it runs reads, and
`git status` runs with `--no-optional-locks`. It lists
[agent sessions](#sessions--claude-and-codex) with `claude agents`, `ps` and
Codex's session logs. `<work>` defaults to `.`, the worktree you are in.

A worktree `wt up` would not start on is still a plan, with `upEligible: false`
and the reason — never an error exit. The command fails only outside a git
repository.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"status"` | |
| `token` | string \| null | names the inputs of this plan; pass it to `wt up --expect`. Null when not eligible |
| `trunk` | string | trunk as `wt up` resolves it here (see [`trunkSource`](#trunksource--how-trunk-was-found)) |
| `trunkSource` | string \| null | since 1.4.0: how `trunk` was found, [below](#trunksource--how-trunk-was-found) |
| `trunkRef` | string \| null | `origin/<trunk>`, what `wt up` rebases onto after its fetch; null with `trunk` null, when nothing names trunk |
| `trunkRefExists` | bool | whether that ref is here, as last fetched |
| `trunkTip` | string \| null | its commit |
| `trunkRefUpdatedAt` | string \| null | when that ref last moved, from its own reflog (RFC 3339, UTC); null without a reflog. Not FETCH_HEAD's time, which any fetch sets |
| `trunkSync` | object | what `wt up` would do to local `<trunk>` after its fetch, judged against `trunkTip` (see [`trunkSync`](#trunksync--local-trunk)); `fastForwarded` is always false here, and `skippedReason` null means a run would fast-forward it (or there is nothing to do) |
| `configured` | bool | the repository has a wt configuration file that parses. False also on **detected defaults**: no file, and wt runs on what `wt init --yes` would write, with no problem named (since 1.3.0; before, that repository was `noConfiguration`) |
| `worktree` | object \| null | the worktree named; null when it cannot be found |
| `worktree.work` | string | its work name, as `wt list` prints it |
| `worktree.branch` | string | `""` when detached |
| `worktree.path` | string | |
| `worktree.isMain` | bool | the main checkout |
| `worktree.state` | `clean` \| `dirty` \| `unreadable` | the checkout: `clean` is nothing uncommitted |
| `worktree.behind`, `worktree.ahead` | int \| null | commits against `trunkRef` (the local trunk when that is not here); null when they cannot be counted |
| `upEligible` | bool | `wt up` would start on it. Dirt, conflicts and busy sessions are **not** checked here: `wt up` checks them and its result says so |
| `upIneligibleCode` | string \| null | `mainCheckout`, `noConfiguration` (no `MAIN_BRANCH` in a configuration file, and no trunk to detect either), `configurationInvalid`, `detachedHead`, `onTrunk`, `handedOver` (an earlier `wt sync` run waits on a person there), `notAWorktree` |
| `upIneligibleReason` | string \| null | the same as a sentence |
| `stack` | array | every worktree `wt up` would move, parents first, from local refs as of now; just the one when it has no stack |
| `stack[].work`, `.branch`, `.path` | string | |
| `sessions` | array | [agent sessions](#sessions--claude-and-codex) in the stack's worktrees |
| `sessions[].work`, `.name` | string | the worktree it is in, and its name |
| `sessions[].kind` | `claude` \| `codex` | whose session it is; `codex` since 1.5.0 |
| `sessions[].state` | `busy` \| `idle` | a busy session refuses `wt up` unless `--force` |
| `sessionsError` | string \| null | the listing failed; `sessions` is then empty, not known empty |

The **token** covers the trunk name, the bytes of the wt configuration file
`wt up` would read, and the stack's branches in order. A newer trunk commit is
not in it.

## `wt up [<work>] --json` — the run

The same run as without `--json`. Progress, and any question, go to **stderr**;
stdout carries exactly one object. To run it unattended, pass `--yes` (yes to
every question, the push included) and `--no-push` to keep the push out.

With `--expect <token>`, `wt up` fetches, then recomputes the token and refuses,
touching no worktree, when it differs: the trunk's name, the configuration or the
stack is not what the plan showed. It is then an `error` with no participants.

A handled SIGINT or SIGTERM writes the object too, through the same path — the
participants so far, the one in flight as `interrupted`, and the way back — then
exits 130. SIGKILL or a crash may write none; treat a missing object as unknown.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"up"` | |
| `trunk`, `trunkRef` | string \| null | null when the run stopped before resolving them |
| `trunkSource` | string \| null | since 1.3.0: how `trunk` was found ([`trunkSource`](#trunksource--how-trunk-was-found)); null with `trunk` |
| `onto` | string \| null | the trunk commit the run rebased onto, after its fetch; null when never determined |
| `fetched` | bool | trunk was fetched (false with `--no-fetch`, or when the run stopped first) |
| `trunkSync` | object \| null | what the run did to local `<trunk>` after its fetch (see [`trunkSync`](#trunksync--local-trunk)); null when it did not fetch |
| `outcome` | see below | |
| `error` | string \| null | why the run stopped before touching any participant: configuration, fetch, `--expect`, the main checkout named, a worktree not found |
| `worktrees` | array | the participants, parents first; empty when `error` stopped the run first |
| `recovery` | string \| null | for `interrupted`: what wt printed as the way back |

Each participant:

| Field | Type | Meaning |
|---|---|---|
| `work`, `branch`, `path` | string | |
| `result` | see below | |
| `reason` | string \| null | why, for everything but `rebased` |
| `before` | string \| null | the branch's commit when the run began |
| `after` | string \| null | its commit when the run ended |
| `failedSteps` | array of string | for `rebasedStepFailed`: each step that did not finish |
| `recovery` | string \| null | for `needsRecovery`, `handedOver`, `interrupted`: how to put it back or finish it |
| `pushCommand` | array of string \| null | for `rebased`: the push, as an argv (`["git", "-C", path, "push", …]`) |
| `pushed` | bool | the run pushed it (`--push`, or `--yes` without `--no-push`) |

`result`, exhaustively:

| Value | The branch | Meaning |
|---|---|---|
| `rebased` | moved | rebased onto trunk, every step after it done |
| `rebasedStepFailed` | moved | rebased, but a later step did not finish: a deferred step, the result ref, the plan's cleanup (`failedSteps`) |
| `skipped` | unchanged | nothing to do: on trunk already, or nothing of its own |
| `refused` | unchanged | refused before anything was touched, for a reason of its own: uncommitted changes, a busy agent session, a conflict that would be yours |
| `notRun` | unchanged | not touched because of another member of its stack |
| `restored` | unchanged | the rebase failed or stopped and was put back: branch, index, HEAD and working tree as the run found them |
| `needsRecovery` | changed | the rebase failed and was not put back; `recovery` says how |
| `handedOver` | changed | stopped at a conflict that is a person's, with a plan file (`wt sync resume`, `wt sync undo`) |
| `interrupted` | changed | the one a signal caught; `recovery` says how |

`outcome`, from the participants:

| Value | When |
|---|---|
| `done` | every participant `rebased` or `skipped` |
| `refused` | nothing changed: every participant `skipped`, `refused`, `notRun` or `restored`, or `error` stopped the run before any |
| `partial` | something changed and not everything finished |
| `interrupted` | a signal ended the run |

"Nothing changed" means each participant's branch, index, HEAD and working tree
are as they were; the fetched trunk ref and the objects a simulation writes are
expected writes.

The exit code keeps its meaning (0 when the run completed); read `outcome`.

### `trunkSource` — how trunk was found

`wt status`, `wt up`, `wt sync`, `wt sweep --dry-run`, and the `wt new` and
`wt checkout` plans say how trunk was determined. The first that applies wins,
and only local refs are read — nothing asks the remote:

| Value | Trunk is |
|---|---|
| `config` | `MAIN_BRANCH` in the wt configuration file |
| `originHead` | the branch `refs/remotes/origin/HEAD` names |
| `conventional` | the first of `development`, `main`, `master` that exists locally or as `origin/<name>` — the order decides, not where it exists |
| `currentBranchGuess` | no longer produced since `status` 1.4.1, `up` 1.3.1, `sync` 1.1.1, `sweep-plan` 1.4.1, `new-plan` 1.2.1, `checkout-plan` 1.3.1, and never by `remove-plan` or `remove`; detection fails instead. Before, the main checkout's branch, which may be a feature branch |

When none of those names trunk, wt does not guess: commands refuse with
"cannot tell which branch is trunk", and the plans report `noConfiguration`
with `trunk` and `trunkSource` null. Null only with `trunk` null. A reader
treats `currentBranchGuess`, and a value it does not know, as no trunk to
rebase onto or judge branches merged against.

### `trunkSync` — local trunk

After fetching trunk, `wt up`, `wt sync run` and `wt sync --run` bring local
`<trunk>` up to `origin/<trunk>` when that is a pure fast-forward and safe:
checked out nowhere, the ref moves by compare-and-swap; checked out in a
checkout that is clean (untracked files count), with no operation in progress
and no agent session busy in it (an idle one does not count, nor the session
running wt), `git merge --ff-only --no-overwrite-ignore` moves
it there. Anything else leaves it, with the reason. Local commits are never
thrown away, and a fast-forward that fails never fails the run.
`--no-ff-trunk`, or `wt config set ff_trunk false`, turns it off.

| Field | Type | Meaning |
|---|---|---|
| `local` | string \| null | local `<trunk>`'s commit after the run (as found, unless fast-forwarded); null when there is no local `<trunk>` |
| `remote` | string \| null | the `origin/<trunk>` commit compared with; null when there is none |
| `localAhead`, `remoteAhead` | int \| null | commits each side has that the other lacks, as found; null when either is missing |
| `fastForwarded` | bool | local `<trunk>` was moved to `remote` |
| `skippedReason` | string \| null | why it was left; null when fast-forwarded or with nothing to do (equal, or either side missing) |

`skippedReason`, exhaustively. `ahead` and `diverged` are reported whenever they
hold, even with `--no-ff-trunk` or `ff_trunk` false: they are facts about the
repository a fast-forward could not get past anyway, and `ahead` is how a caller
learns local trunk has commits origin lacks. `optedOut` is reported only when a
fast-forward was otherwise possible.

| Value | Meaning |
|---|---|
| `optedOut` | `--no-ff-trunk`, or `ff_trunk` false in the user config, and trunk could otherwise have been fast-forwarded |
| `ahead` | local has commits origin lacks, and origin none local lacks |
| `diverged` | both have commits the other lacks |
| `dirty` | the checkout it is on has changes, untracked files included |
| `operation` | a rebase, merge, cherry-pick, revert or bisect is in progress on it |
| `session` | an agent session is busy in the checkout it is on (idle ones, and the session running wt, do not count), or the sessions could not be listed |
| `notFastForward` | it moved, or was checked out or switched away from, while wt was updating it |
| `failed` | git refused the update (an ignored file it would overwrite), or it is not safe to try: trunk checked out in more than one worktree |

## `wt sweep --dry-run --json` — the sweep's plan

The plan `wt sweep` prints, as one object: every row, why it counts as merged or
why it is kept, and a token. It deletes nothing. It fetches origin first, as a
sweep does, unless `--no-fetch`; it asks GitHub about the branches' pull requests
and lists the [agent sessions](#sessions--claude-and-codex), as a sweep does. `wt sweep --json` without
`--yes` prints the same plan. Run it from the repository's main checkout;
`--all`, `--roots` and `--profile` are refused with `--json` (run it in each
repository).

A plan that cannot be made — not the main checkout, no trunk, the fetch failed —
is still one object, with `error` set and no items, and a non-zero exit.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"sweep"` | |
| `repo` | string \| null | the main checkout; null outside a repository |
| `trunk` | string \| null | trunk's branch name |
| `trunkSource` | string \| null | since 1.3.0: how `trunk` was found ([`trunkSource`](#trunksource--how-trunk-was-found)) |
| `bases` | array | what merged is measured against, first first: `{name, tip}` for `origin/<trunk>` as fetched, then `<trunk>` |
| `fetched` | bool | origin was fetched first |
| `token` | string \| null | names this plan; pass it to `wt sweep --expect`. Null when there is nothing to remove or delete, or on error |
| `error` | string \| null | why no plan could be made |
| `items` | array | the rows, in the order `wt sweep` prints them |
| `quarantine` | string \| null | since 1.2.0: with `--quarantine <dir>`, the folder, absolute; a folder that exists or is on another volume sets `error` (the items stay, the token is null). Null without it |
| `keepSuperset` | bool | since 1.4.0: `--keep-superset` was given, which the token covers: the removed worktrees' Superset workspaces are left |
| `configFingerprint` | string \| null | since 1.5.0: the wt configuration the token covers, the lowercase hex SHA-256 of the configuration file wt read (`bin/worktree/worktree.toml` or `worktree.conf`, the worktree's own before the main checkout's). Two plans with the same value read the same file. Null when there is none |

Each item:

| Field | Type | Meaning |
|---|---|---|
| `category` | see below | its part of the plan |
| `branch` | string \| null | null for a detached worktree |
| `tip` | string | the branch's commit, or a detached worktree's HEAD |
| `work`, `path` | string \| null | the worktree; null for a branch in no worktree (`work` is null for a detached one) |
| `merged` | object \| null | why it counts as merged; null for `upstreamGone` |
| `merged.how` | `ancestor` \| `patch` \| `pullRequest` | `into` contains the tip; every commit is on `into` under another id, byte for byte (a rebase merge, a cherry-pick); GitHub merged its pull request into trunk at exactly this tip (a squash merge) |
| `merged.into` | string | the base it is on (`origin/main`, `main`); trunk's name for `pullRequest` |
| `merged.pullRequest` | int \| null | for `pullRequest`: its number |
| `pullRequest` | object \| null | what GitHub says about the branch's pull request, merged or not: `number`, `state` (`OPEN`, `MERGED`, `CLOSED`), `base`, `url`. Null when there is none or GitHub was not asked |
| `kept` | array of string | every reason it is kept (below); empty for `remove` and `delete` |
| `reason` | string | the row as `wt sweep` prints it: why it goes, or why it stays and what to do |
| `ahead` | int \| null | for `upstreamGone`: commits the first base lacks |
| `subject` | string \| null | the tip's commit subject |

`category`, exhaustively:

| Value | `wt sweep` prints it under | Meaning |
|---|---|---|
| `remove` | Will be removed with its branch | merged, and its worktree is safe to remove: the worktree goes the way `wt remove` takes it, then the branch |
| `delete` | Will be deleted | merged, and in no worktree: the branch goes |
| `inUse` | Merged, but in use in a worktree, so kept | merged, but its worktree is kept; `kept` says why |
| `upstreamGone` | Upstream gone, but not merged, so kept | its upstream is gone, but trunk lacks its commits |

`kept`, exhaustively. A worktree is never removed with any of these:

| Value | Meaning |
|---|---|
| `dirty` | uncommitted changes, untracked files, edits to files status is told to skip, and initialised submodules (their content, or a checked-out commit other than the recorded one) included, whatever git's config says |
| `statusUnknown` | `git status` failed there, so its changes could not be seen |
| `detached` | a worktree with no branch whose HEAD trunk contains; sweep never removes one (`wt remove <path>` does) |
| `session` | an agent session is in it, idle or busy |
| `sessionsUnknown` | the sessions could not be listed, so no worktree is known to be free of one |
| `lockHeld` | git's worktree lock, and its holder is still running |
| `directoryMissing` | git lists it, but its directory is gone (`git worktree prune`) |
| `heldByRebase`, `heldByBisect` | a rebase or bisect in that worktree holds the branch |
| `mainCheckout` | the main checkout is on the branch |
| `branchNotDeletable` | `wt remove` would not delete the branch with the worktree |
| `notMerged` | for `upstreamGone`: trunk lacks its commits |
| `operation` | a rebase, merge, cherry-pick, revert or bisect is in progress there |
| `hiddenChanges` | files `git status` is told not to look at (assume-unchanged, skip-worktree) |
| `headUnreachable` | its HEAD holds commits that no ref or other worktree would |
| `reachUnknown` | what the removal would leave unreachable could not be worked out |
| `submoduleUnreachable` | a submodule has commits that only this worktree's copy of it holds |
| `nestedWorktree` | another worktree's checkout is inside this one |

The **token** covers the repository, trunk's name, the wt configuration file
(`configFingerprint` shows which), and every row: its category, branch, commit, worktree, evidence, pull request
(number, state, base) and reasons, codes and words. So a branch that moved, a
new merged branch, a worktree that turned dirty, a session that arrived or
changed, a pull request GitHub no longer answers for — any of them is a
different token. So is the `--quarantine` folder, or its absence: a token read
for a quarantine does not let a sweep delete instead. A newer trunk commit that changes no row is not.

## `wt sweep --yes --json` — the sweep

The same sweep as `wt sweep --yes`. The plan as wt prints it, and the progress,
go to **stderr**; stdout carries exactly one object.

With `--expect <token>` (it needs `--yes --json`), the sweep fetches (unless
`--no-fetch`), makes the plan again and refuses, touching nothing, unless it has
that token: `outcome` `refused`, `error` set, no items, a non-zero exit. Then,
as every sweep does, it reads everything once more immediately before it acts,
and a row that changed in between is kept with the reason.

A handled SIGINT or SIGTERM writes the object too — the rows so far, the one in
flight as `interrupted` with what is true of it now — then exits 130. SIGKILL or
a crash may write none; treat a missing object as unknown.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"sweep"` | |
| `repo`, `trunk` | string \| null | as in the plan |
| `fetched` | bool | origin was fetched first |
| `token` | string \| null | the token of the plan this sweep carried out; null when it stopped first or there was nothing to do |
| `outcome` | see below | |
| `error` | string \| null | why the sweep stopped before touching anything: `--expect`, the fetch, not the main checkout |
| `items` | array | every row of the plan, in its order; empty when `error` stopped it first |
| `recovery` | string \| null | for `interrupted`: what wt printed |
| `quarantine` | string \| null | since 1.1.0: with `--quarantine <dir>`, the folder; null without |
| `runId` | string \| null | since 1.4.0: the run under `refs/wt-swept/` the deleted branches were moved into (see [below](#deleted-branches-are-pinned)); null when no branch was moved |

Each item:

| Field | Type | Meaning |
|---|---|---|
| `category`, `branch`, `tip`, `work`, `path` | | as in the plan |
| `result` | see below | |
| `reason` | string \| null | why, for everything but `removed` and `deleted` |
| `worktreeRemoved` | bool | the worktree is gone now |
| `branchDeleted` | bool | the branch is gone now |
| `pin` | string \| null | since 1.4.0: the ref the deleted branch was moved into, `refs/wt-swept/<runId>/heads/<branch>`, read back holding `tip`; null when it was not moved there |
| `restoreCommand` | array of string \| null | when the branch was deleted, the argv that puts it back (not its upstream setting): `["wt", "refs", "restore", runId, "--only", "refs/heads/<branch>", "--yes"]` for a pinned branch, `["wt", "restore", dir]` for a quarantined worktree's. Run it in `repo`. Null when the branch is there, or gone with no pin. Before 1.4.0 it was `["git", "-C", repo, "branch", branch, tip]`; that form is gone |
| `quarantine` | object \| null | since 1.1.0: for a worktree moved, or being moved, into the quarantine: `dir`, its own folder (`wt restore <dir>` puts it back), and `checkoutMoved`, `adminMoved`, which of the two moves are done; null otherwise |
| `superset` | object \| null | since 1.2.0: for a worktree the sweep removed, what came of its Superset workspace: `result` and `reason` (string \| null, why, for `skipped` and `failed`); null when no worktree was removed |

`superset.result`, exhaustively: `deregistered` — the workspace was deleted,
after the directory had left its path and git no longer listed it;
`notRegistered` — Superset is running and has no workspace there; `skipped` —
the integration is off (`wt config set superset true` turns it on,
`SUPERSET_REGISTER=off` keeps a repository out), Superset is not installed or
not running, or the worktree was still there; `failed` — Superset erred;
`optedOut` (since 1.3.0) — `--keep-superset` was given and Superset was not
asked anything. None of them changes `result`, `outcome` or the exit code.

Superset has no delete that leaves the checkout alone: its `ws delete`
force-removes the checkout it recorded. wt asks for it only once the directory
has left its path and git no longer lists it, read again right before the call,
but the check and the delete are not one step: a worktree made at the same path
in the instant between them would be removed by Superset. A tool that creates
worktrees while it removes others passes `--keep-superset`, which closes that
window by not asking Superset at all.

`worktreeRemoved`, `branchDeleted` and `pin` are read from the repository after
the row, not from what was attempted.

### Deleted branches are pinned

Since 1.4.0 a sweep deletes no branch outright. Each one it deletes — a
`delete` row's, and a `remove` row's once its worktree is gone — is moved into
`refs/wt-swept/<runId>/heads/<branch>` the way `wt refs sweep` moves a backup:
the run's meta blob is written first, then each branch goes in one atomic
`update-ref` transaction that creates the pin at the planned tip and deletes the
branch at that tip, then its `branch.<name>.*` config. One sweep is one run,
begun at the first branch it moves: a sweep that moves none has `runId` null and
leaves nothing under `refs/wt-swept/`. `wt refs swept` lists the run,
`wt refs restore <runId>` puts the branches back, and `wt refs purge <runId>`
deletes them for good. With `--quarantine`, a removed worktree's branch is
pinned by its quarantine under `refs/wt-quarantine/` and not a second time; a
`delete` row's branch is still pinned in the run. A signal after the meta is
written and before any branch moved can leave a run that holds only its meta:
`runId` then names it, and `wt refs purge` clears it.

`result`, exhaustively:

| Value | Meaning |
|---|---|
| `removed` | the worktree and its branch are gone |
| `deleted` | the branch is gone |
| `kept` | left as it was on purpose: the plan kept it (`inUse`, `upstreamGone`), or it changed or became unsafe after the plan was made |
| `failed` | tried and not finished; `worktreeRemoved` and `branchDeleted` say what did happen |
| `notRun` | not reached: the sweep ended before it |
| `interrupted` | the one a signal caught |

`outcome`, from the items to remove or delete:

| Value | When |
|---|---|
| `done` | every one `removed` or `deleted` — or there were none |
| `refused` | nothing was removed or deleted, or `error` stopped the sweep first |
| `partial` | some went, some did not |
| `interrupted` | a signal ended the sweep |

The exit code is non-zero when anything to remove or delete was kept or failed;
read `outcome`.

With `--quarantine <dir>`, `<dir>` must be a new folder whose parent exists, on
the volume of every worktree the plan removes and of their admin dirs; otherwise
the sweep refuses before touching anything (`error` set). Each worktree goes into
`<dir>/<its directory's name>` (numbered `-2`, `-3` on a clash), with its own
`recovery.json`, exactly as `wt remove --quarantine` would move it;
`worktreeRemoved` is then true once its checkout has left its path.

## `<dir>/recovery.json` — a quarantine's journal

`wt remove <work> --quarantine <dir>` (and `wt sweep --quarantine`, per
worktree) moves the worktree aside instead of deleting it. `<dir>` is a new
folder the caller names, on the worktree's volume and outside every checkout of
the repository, its git dir and every other quarantine (a purge of that one would
take it along); it is made with `mkdir`, not `mkdir -p`, and a
folder that exists refuses the removal, as does one inside the repository or a
checkout or admin dir on another volume: every move is a rename. After its last
check the
removal runs these steps, writing `recovery.json` durably (a temp file, fsync,
rename, folder fsync) before anything changes and before and after each step:

1. `lock` — `git worktree lock --reason "wt quarantine <dir>"`, fsynced, so
   nothing prunes the registration while the checkout is away (a stale lock, or
   one `--force` breaks, is released first);
2. `pin` — refs under `refs/wt-quarantine/<id>-<hash>/` hold HEAD and the
   branch's tip: once the branch is deleted and the admin dir is out of the
   repository, nothing else would, and `git gc` would take a squash-merged
   branch's commits;
3. `moveCheckout` — the checkout to `<dir>/checkout`;
4. `moveAdmin` — `.git/worktrees/<id>`, with the worktree's initialised
   submodule repositories under `modules/`, to `<dir>/admin`: git no longer lists
   the worktree. wt never runs `git worktree prune`;
5. `relink` — `<dir>/checkout/.git` names `<dir>/admin`, so a new worktree that
   takes the id is never the one git in the quarantine works on;
6. `branch` — the branch's local config section is recorded, then the branch is
   deleted or renamed as the plan says.

A file written after the last check moves with the checkout. Nothing is deleted:
emptying the folder is the user's own act, or `wt quarantine purge <dir>`'s. The
pins go when the restore is done, with the purge, or at the next `wt sweep` once
the folder has been deleted (its parent still
there: a folder on a volume that is not mounted keeps them).

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | as everywhere |
| `command` | `remove` \| `sweep` | who made it |
| `createdAt` | string | RFC 3339, UTC |
| `repo`, `commonDir` | string | the main checkout and the common git dir |
| `worktreeId` | string | the `<id>` of `.git/worktrees/<id>` |
| `dir` | string | the folder, as the removal named it |
| `checkout`, `admin` | object | `path` (where it was), `quarantined` (where it goes), `device` and `inode` (which a rename keeps: a directory at either place with them is this one, and nothing else is) |
| `gitFile` | string | the checkout's `.git` file as it was, which the restore writes back |
| `pins` | object | the refs: `head`, `tip` (null when there is none) and `dir`, a blob holding the folder's path |
| `head` | string \| null | the commit checked out; null before the first |
| `branch` | object \| null | null for a detached HEAD; else `name`, `tip`, `plan` (`delete`, `keep`, `none`), `keepAs`, `config` (`[{key, value}]`, every value in order; null until captured) and `result` (`deleted`, `renamed`, `untouched`, `kept` — turned down on purpose — or `failed`; null before the step) |
| `steps` | array | `{name, state, error}` for `lock`, `pin`, `moveCheckout`, `moveAdmin`, `relink`, `branch`, in order |
| `restore` | object \| null | the restore's own journal: `action`, `occupiedBy`, `createdBranch` (this restore made the branch again; absent before 1.1.0) and its `steps` (`branch`, `moveAdmin`, `relink`, `moveCheckout`, `unlock`, `unpin`); null until `wt restore` changes something |
| `purge` | object \| null | `{startedAt}`, only in `recovery.purging.json`: the copy of the record `wt quarantine purge` writes, and which replaces `recovery.json`, before it deletes anything. With no `recovery.json` no `wt restore` — of any version — restores it (`purging`). Absent before 1.2.0 |

The step lists and the states are **closed**, unlike the enumerations elsewhere:
wt refuses a record whose steps are not exactly these, in this order, so another
step or state comes as a new major (`recovery.v2.json`), never a minor. Fields
may still be added.

A step's `state` is `pending`, `running`, `done` or `failed`. `running` is
written before the step changes anything, so a step left `running` may or may
not have happened: the two places each directory can be (and its inode) say
which. A removal that left no result — killed, or crashed — is fully described by
the file. A lock `"wt quarantine <dir>"` on a worktree with no `recovery.json`
was stopped before anything moved.

A branch step that fails once the checkout has gone is **partial**, and wt says
"partly done", not "removed": the file's `branch.result` is `failed`.

## `wt restore <dir> --dry-run --json` — the plan

What `wt restore <dir>` would do, changing nothing: where each directory is,
what happens to the branch, and every reason it would refuse.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | |
| `command` | `"restore"` | |
| `dir` | string | the folder, absolute |
| `repo` | string \| null | from `recovery.json` |
| `checkout`, `admin` | object \| null | `path`, `quarantined`, and `location`: `original` (at its path, the same directory by inode — moved back, or never moved), `quarantined`, `taken` (something else is at its path), `missing` |
| `head` | string \| null | the commit to detach at, if it comes to that |
| `branch` | object \| null | `name`, `tip`, `keepAs`, `removal` (the removal's `branch.result`), `action` and `occupiedBy` |
| `locked` | bool | the worktree carries this quarantine's lock, which comes off last |
| `orphan` | string \| null | a worktree locked for `<dir>` with no `recovery.json`: restoring it is unlocking it |
| `problems` | array | `{code, text}`: `noRecord`, `repository`, `checkoutTaken`, `checkoutMissing`, `adminTaken`, `adminMissing`, `volume`, `commitMissing`, `worktreesUnknown`, `purging` (a purge began: its pins may be gone) |
| `error` | string \| null | the problems in words |

`branch.action`, exhaustively, decided in this order:

| Value | When | Then |
|---|---|---|
| `detach` | another worktree has the branch — under its name or the kept name — as HEAD, rebasing it (`head-name`), or bisecting from it (`BISECT_START`) | no branch change; HEAD detached at `head`; `occupiedBy` names that worktree |
| `none` | the branch is at `tip` | nothing |
| `renameBack` | its name is free and the kept name is at `tip` | renamed back, config section with it |
| `recreate` | its name is free and there is no kept branch | made again at `tip` (refused, naming the commit, when it is not in the repository), and its recorded config added to the empty section |
| `attach` | its name holds another commit, the kept name is at `tip` | HEAD names the kept branch |
| `detach` | anything else (the kept branch moved on) | as above, `occupiedBy` null |

Before the admin dir moves back — also when a restore that stopped resumes — the
branch HEAD will name is read again: gone, moved off `tip`, or taken by another
worktree in between, and HEAD is detached instead.

A removal that stopped before its admin dir moved never unregistered the
worktree, which may have been worked in since: `action` is `none` whatever its
branch did, and the restore only unlocks it and deletes the pins.

The recorded config goes back only when this restore made the branch again, or
the branch's section is empty: a branch somebody else made keeps its own config.
It goes back entry by entry, and one already there counts once, so a restore
stopped halfway through it adds the rest. Once it has made the branch
(`createdBranch`) the branch stays its own: a restore run again after a failed
config write adds the entries still missing, leaves a key somebody set to a
value of their own since, and fails unless every other recorded entry is back.

## `wt restore <dir> --json` — the result

The restore: the branch action, then the admin dir back — its parent folders made
again if `git worktree prune` or a tidy took them, never the directory itself —
then the checkout's `.git` as it was, then the checkout, then the quarantine's
lock off and its pins deleted, each journalled in `recovery.json` and idempotent, so
running `wt restore <dir>` again finishes a restore — or a removal — stopped
anywhere. The human output goes to stderr. A handled SIGINT or SIGTERM writes the
object too.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command`, `dir`, `repo` | | as in the plan |
| `outcome` | `restored` \| `refused` \| `partial` \| `interrupted` | restored: registered at its path again, branch action done, lock off. refused: nothing changed. partial: a step failed after others; run it again. interrupted: a signal |
| `error` | string \| null | |
| `checkout`, `admin`, `branch` | | as in the plan, read when the object is written |
| `unlocked` | bool | this run took the quarantine's lock off |
| `steps` | array | the restore's steps from `recovery.json`; empty when it has none |
| `recovery` | string \| null | for `interrupted`: what wt printed |

## `wt quarantine purge <dir> --dry-run --json` — the plan

What `wt quarantine purge <dir>` would delete, for good, and every reason it
would refuse, deleting nothing. `--json` without `--yes` prints it too.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | |
| `command` | `"quarantine purge"` | |
| `dir` | string | the folder, absolute |
| `repo` | string \| null | from `recovery.json` |
| `state` | string \| null | `quarantined`, `restored`, `purging`, `empty` or `unsettled`; null when the folder is not a quarantine purge can read |
| `token` | string \| null | names this plan: the folder by path and identity, its state, its record byte for byte, its entries, every path in it with its type, size and time, each pin and where it is, what would be lost; null when it would refuse |
| `worktree`, `branch`, `createdAt` | string \| null | where the checkout was, its branch, when it was quarantined |
| `entries` | array of string | every name in the folder, sorted; all of it goes |
| `bytes` | integer \| null | the size of its files, symlinks not followed; null when something cannot be read |
| `pins` | array | `{name, ref, oid}` for `head`, `tip`, `dir` as `recovery.json` names them; `oid` null when the pin is gone already |
| `unreachable` | array | `{kind, oid, count}`: a pinned commit (`head` or `tip`) whose `count` commits nothing else holds, which gc may take once the pins go |
| `reachError` | string \| null | why that could not be counted |
| `repositoryGone` | bool | the recorded git dir is gone: deleted, its pins went with it; moved, they stay there until `wt sweep` in it drops them. The purge goes ahead on the record's own checks, every pin listed with `oid` null |
| `problems` | array | `{code, text}`, below |
| `error` | string \| null | the problems in words |

`state`:

| Value | Meaning | The purge |
|---|---|---|
| `quarantined` | a removal moved a worktree here and finished (a `failed` branch step counts: the branch stayed) | deletes the pins, then the folder: the worktree is gone for good |
| `restored` | `wt restore` put it back | deletes what is left: the folder (`recovery.json`) and any pin |
| `purging` | a purge began and stopped: `recovery.purging.json` is there | finishes it |
| `empty` | an empty folder: nothing names what it was (a purge stopped between deleting its journal and the folder leaves one) | refuses (`empty`); `rmdir` takes it |
| `unsettled` | a removal or a restore stopped mid-way | refuses: `wt restore <dir>` settles it |

It refuses, deleting nothing, on any problem:

| Code | When |
|---|---|
| `missing` | no such folder (a purge that finished leaves none) |
| `notAFolder` | `<dir>` is a symlink or a file |
| `noRecord` | no `recovery.json` (nor `recovery.purging.json`) in a folder that is not empty, or one wt cannot trust |
| `wrongFolder` | the record was written for another folder, names places that are not this folder's `checkout` and `admin`, or what is at `checkout` or `admin` is not the directory the removal moved there (by device and inode) |
| `repository` | its repository cannot be opened, or has another git dir now |
| `pinsForeign` | a pin it names is not under this quarantine's `refs/wt-quarantine/<id>-<hash>/`, or holds something the removal did not pin |
| `pinsUnreadable` | git cannot read a pin (a broken ref): not taken for one that is gone |
| `unsettled` | a removal or a restore stopped mid-way |
| `worktreesUnknown` | the worktrees cannot be listed |
| `registered` | git has a worktree registered inside the folder — compared by the directories paths lead to, not by spelling |
| `locked` | a worktree carries this quarantine's lock (`wt quarantine <dir>`) |
| `location` | the folder is inside a checkout or the git dir, or holds the git dir |
| `foreignEntry` | something at its top level a quarantine in its state never leaves there: only `checkout` and `admin` (while quarantined or purging), the journal and its temp file, and `.DS_Store` are |
| `nestedQuarantine` | another quarantine's record (one that validates, not a project file of that name) anywhere below its top level |
| `empty` | an empty folder |

## `wt quarantine purge <dir> --yes --json` — the result

The purge holds a lock on the folder that `wt restore` takes too, reads the
plan again under it and goes ahead only while it is the same (else `planChanged`,
nothing deleted). Then, in this order: `recovery.purging.json` — the record with
`purge.startedAt` — replaces `recovery.json`, so no `wt restore` takes a worktree
whose pins are going; each pin is deleted only while it is at the `oid` read;
git's worktrees are read once more; every entry the plan listed is deleted —
`checkout`, then `admin`, then the rest by name — and one that appeared since
stops it; then `recovery.purging.json`, then the folder.
Every deletion is made inside the folder as opened and checked, never through its
path again, never following a symlink, making a read-only folder writable first.
A purge stopped anywhere but between its last two steps leaves its journal, and
running it again finishes it; stopped there, it leaves an empty folder, its pins
already gone, which it then refuses (`empty`).

Inside the checkout it deletes as `rm -rf` would: into a volume mounted there,
and not past a file flagged immutable (`partial`). The record is trusted as wt
wrote it: a forged one naming this folder, its inodes and its pins passes, and is
not a boundary — whoever can write the folder can delete it.
The human output goes to stderr; a handled SIGINT or SIGTERM writes the object
too.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command`, `dir`, `repo`, `state`, `token` | | as in the plan it went by |
| `outcome` | `purged` \| `refused` \| `partial` \| `interrupted` | purged: every pin and the folder are gone. refused: nothing was deleted. partial: it began and stopped; run it again. interrupted: a signal |
| `error` | string \| null | |
| `problems` | array | as in the plan, plus `planChanged`: `--expect` named another plan, or the plan changed while the question was open or before the lock was taken |
| `pins` | array | `{name, ref, oid, result}`: `oid` as planned; `result` `deleted`, `absent` (gone already) or `kept` (still there, or unreadable), read when the object is written |
| `folderDeleted` | bool | the folder is gone |
| `recovery` | string \| null | for `interrupted`: what wt printed |

## `wt refs sweep --dry-run --json` — the ref sweep's plan

What `wt refs sweep` would move, as one object: every branch and tag whose name
matches a backup pattern — here, and with `--remote` on origin — what it is, and
a token. It moves nothing. It fetches origin first unless `--no-fetch`; with
`--remote` it also runs `git ls-remote origin` and asks GitHub, once, which of
origin's backup branches have an open pull request. `wt refs sweep --json`
without `--yes` prints the same plan. It runs from any worktree of the
repository.

A **backup** is a ref whose short name matches one of `ref_sweep_patterns`
(user config; the default is the nine `backup/*` `safe/*` `safety/*` `backup-*`
`safe-*` `safety-*` `*-backup` `*-safe` `*-safety`, where `*` matches any run of
characters, slashes included). Every other ref under `refs/heads/`,
`refs/tags/` and `refs/remotes/origin/` is a **container** — trunk, protected
and checked-out branches included. For a remote backup only origin's own
branches and tags count. Nothing under `refs/wt-swept/`, `refs/wt-quarantine/`,
`refs/wt-sync/` or `refs/stash` is either.

**All contact with origin goes through one URL.** `--remote` resolves origin's
effective fetch and push URLs (`git remote get-url --all origin` and `--push
--all`, `insteadOf` and `pushInsteadOf` applied) and requires them to be one and
the same URL, else `remoteAmbiguous`. The listing, the fetch into each pin, the
delete push, and later the restore's reads, push and read-back all use it. A URL
that carries a password in any form (`user:secret@host:path` too), or any
userinfo on http, https, ftp or ftps — a token in the user name cannot be told
from a name — is refused with `remoteCredentialsInUrl`, and a
`<transport>::<address>` URL is judged on its address too; a user name on ssh
(`git@host:…`) is fine. The userinfo of a `scheme://` URL runs to the last `@`
before the first `/`, `?` or `#`; in other forms it is what precedes the first
`@` with no `/` before it. These rules are gittree's `RemoteURL`, rule for rule.
Only the URL with all its userinfo removed (`git@host:o/r` → `host:o/r`) is ever
printed or recorded.

A plan that cannot be made — no trunk, the fetch failed, `--remote` without an
origin or with one of those problems — is still one object, with `error` set,
and a non-zero exit. So is an
`--only` that names an id the plan does not have or a row it keeps: the items
are there, the token is null.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"refs sweep"` | |
| `repo` | string \| null | the main checkout; null outside a repository |
| `trunk` | string \| null | trunk's branch name |
| `trunkSource` | string \| null | how `trunk` was found ([`trunkSource`](#trunksource--how-trunk-was-found)) |
| `fetched` | bool | origin was fetched first |
| `remote` | bool | `--remote` was given |
| `patterns` | array of string | `ref_sweep_patterns`, in the order they are tried |
| `age` | string \| null | `ref_sweep_age`, e.g. `14d` |
| `now` | int | unix seconds the plan was made at; age is measured from it |
| `endpoint` | object \| null | with `--remote`: `{url, digest}` — the one URL with its userinfo removed, and `sha256:` with the lowercase hex SHA-256 of the full effective URL. The token covers the digest. Null without `--remote` |
| `runId` | null | always null: the run that applies the plan mints it |
| `token` | string \| null | `rs1-…`: names the plan; pass it to `--yes --expect`. It covers the rows, `--remote`, the patterns, the age, the repository and trunk — not the selection, so `--only` picks from the plan a token names. Null when no row could be swept, or on error |
| `error` | string \| null | why no plan could be made |
| `problems` | array | `{code, text}` for the refusals that have a code: `remoteAmbiguous`, `remoteCredentialsInUrl`; empty otherwise |
| `items` | array | every backup, here first, then origin's |

Each item:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | `refs/heads/<name>` or `refs/tags/<name>`; on origin `origin:refs/heads/<name>` or `origin:refs/tags/<name>`. `--only` takes it — one id per `--only`, never split, since a ref name may contain a comma |
| `kind` | `branch` \| `tag` \| `remoteBranch` \| `remoteTag` | |
| `name` | string | the short name the pattern matched |
| `pattern` | string | the first pattern it matched |
| `category` | `backupContained` \| `backupOld` \| `backupYoung` \| `kept` | below |
| `selected` | bool | a sweep with this plan moves it: `backupContained` and `backupOld` by default, or exactly what `--only` names |
| `tip` | string \| null | the commit; null when it is not here (`objectMissing`) |
| `object` | string | the ref's own value: the tag object of an annotated tag, else the tip |
| `annotated` | bool \| null | tags: an annotated tag; null for branches |
| `containedIn` | string \| null | the first container holding the tip — trunk (`refs/heads/<trunk>`, then `refs/remotes/origin/<trunk>`), then origin's branches, then the rest, each group by name. A remote row names origin's ref as `origin:<ref>` |
| `uniqueCommits` | int \| null | commits on no container; null when contained or not known |
| `date` | int \| null | unix seconds, a point in time: see `dateSource` |
| `dateSource` | `reflog` \| `tagger` \| `commit` \| null | a branch here: the first entry of its reflog, which is when it was made; an annotated tag: its tagger date; anything else: its tip's committer date |
| `kept` | array of string | every reason that holds it; empty unless `kept` is the category |
| `subject` | string \| null | the tip's commit subject |

The categories:

- `backupContained` — a container has its tip: nothing is only on it. Selected.
- `backupOld` — it holds commits no container has, and `date` is older than
  `age`. Selected. Here only: a remote backup is never swept unless contained.
- `backupYoung` — the same, younger. Not selected unless `--only` names it.
  A `git branch backup/x` taken this morning of a three-week-old branch is
  young: its date is the branch's reflog, not its commits.
- `kept` — something holds it; `kept` says what:

| `kept` code | Meaning |
|---|---|
| `worktree` | a worktree has the branch checked out (the main checkout included) |
| `heldByRebase`, `heldByBisect` | a rebase or bisect in a worktree holds it |
| `quarantine` | a live quarantine names it: the branch a `--quarantine` removal moved aside, or the name it was kept under, read from the `recovery.json` its pins under `refs/wt-quarantine/` name; or, when that folder is there and its record cannot be read, the commit its tip pin holds. A quarantine whose pins are gone cannot be seen here |
| `protected` | trunk, the branch origin's HEAD names, or a long-lived branch (`main`, `master`, `develop`, `development`, `staging`, `production`, `release*`) |
| `objectMissing` | the object is not here, so containment cannot be read (a remote ref not fetched) |
| `pullRequestOpen` | a remote branch with an open pull request: deleting it would close the pull request |
| `pullRequestUnknown` | a remote branch, and GitHub is off, unreachable or not logged in |
| `notContained` | a remote ref no ref on origin contains |

## `wt refs sweep --yes --json` — the ref sweep

The same run as `wt refs sweep --yes`. The plan as wt prints it and the progress
go to **stderr**; stdout carries exactly one object.

The run makes the plan (fetching unless `--no-fetch`), and with `--expect
<token>` refuses, moving nothing, unless the plan made now has that token. With
`--only` it moves exactly the rows named (an unknown or kept id refuses the
lot). Then it takes the run id — `--run-id <id>`, or one it mints: the UTC
second it began and four hex digits, `20260930T091500Z-3f2a`. A `--run-id`
must match `^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{4,32}$` and have nothing under
`refs/wt-swept/<id>/` yet (else `runIdTaken`, nothing moved).

Before any ref moves it writes the run's **meta**: a blob — not a commit, so no
history graph shows it — at `refs/wt-swept/<runId>/meta`, holding JSON `{runId,
repo (the git dir, absolute), sweptAt (unix seconds), endpoint ({url, digest} or
null), wtVersion}`. A run stopped anywhere after that is found by its id, and a
remote ref is only restored to the origin the meta names. Then it moves each
selected row, reading it once more right before:

- **here**, one `git update-ref --stdin` transaction per ref: create the pin at
  `object`, delete the ref at `object`. Either both happen or neither; a ref
  that moved since the plan is `kept`. A branch checked out since the plan is
  `kept`. A branch's `branch.<name>.*` config goes with it, as with `git branch
  -D`; a restore does not bring the upstream back.
- **on origin**, `git fetch origin +<ref>:<pin>`, then, only if the pin is at
  `object`, `git push --force-with-lease=<ref>:<object> origin :<ref>`, then
  `git ls-remote` reads it back. A pin at anything else is dropped and the row
  is `kept`.

Each pin is `refs/wt-swept/<runId>/<space>/<name>`, `<space>` being `heads`,
`tags`, `remote-heads` or `remote-tags`. The pins and the meta are the record:
there is no journal file.

A handled SIGINT or SIGTERM writes the object too, with `outcome`
`interrupted`, then exits 130: the rows done keep their result, the row in
flight is `interrupted` with `pinned` and `deleted` read back, the rest are
`notRun`. The same holds for `wt refs restore` and `wt refs purge`.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command`, `repo`, `trunk`, `fetched`, `remote` | | as in the plan |
| `token` | string \| null | the token of the plan the run went by |
| `runId` | string \| null | the run's id; null when the run never began: refused, or nothing selected. A run that ends having moved nothing drops its meta, so nothing of it is left under `refs/wt-swept/` |
| `outcome` | `done` \| `refused` \| `partial` \| `interrupted` | done: every selected row was swept (also when none was selected). refused: nothing was pinned or deleted (`error` says why, e.g. `--expect`). partial: some rows were, some not. interrupted: a signal |
| `error` | string \| null | why the run stopped before moving anything |
| `problems` | array | `{code, text}`: the plan's, and `planChanged` (`--expect` named another plan, or the plan changed while the question was open) or `runIdTaken` |
| `recovery` | string \| null | for `interrupted`: what wt printed |
| `items` | array | every row of the plan |

Each item: `id`, `kind`, `name`, `category`, `selected`, `tip`, `object` as in
the plan, and:

| Field | Type | Meaning |
|---|---|---|
| `result` | `swept` \| `kept` \| `failed` \| `notRun` \| `interrupted` \| `notSelected` | swept: pinned and deleted. kept: the plan keeps it (`reason` is its codes) or it moved or was taken after the plan. failed: tried and not finished. notRun: the run ended before it. interrupted: a signal caught it. notSelected: the plan did not select it |
| `reason` | string \| null | why, for kept and failed |
| `pin` | string \| null | the pin, for a selected row once the run began |
| `pinned` | bool | the pin holds `object`, read back after the row |
| `deleted` | bool | the ref is gone, here or on origin, read back after the row |
| `restoreCommand` | array \| null | for a row pinned and deleted: `["wt","refs","restore",<runId>,"--only",<id>,"--yes"]` |

## `wt refs restore <runId> --dry-run --json` — the plan

What `wt refs restore <runId>` would put back, as one object: each pin of the
run, and what would be done with it. It changes nothing; for a remote pin it asks
origin (`ls-remote`) what is at the name. `wt refs restore <runId> --json`
without `--yes` prints the same plan. `--only <id>`, one id per flag, restores
just those; an id the run does not have, or one whose name is taken, refuses.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | |
| `command` | `"refs restore"` | |
| `repo` | string \| null | |
| `runId` | string | as given, also when it is not a run id (then `error` says so) |
| `token` | string \| null | `rr1-…`: covers the repository, the run, and each pin with its object and action. Null when nothing can go back, or on error |
| `error` | string \| null | a run id that is not one, a run with nothing under `refs/wt-swept/<runId>/`, `--only` refused |
| `items` | array | `{id, kind, name, pin, object, action, selected}` |

`action` is `create` (the name is free: the ref is made at `object`), `none` (it
is there at `object` already: only the pin goes), `occupied` (the name holds
something else here), `remoteOccupied` (origin has something there) or `pinGone`
(the pin is not there), and for a remote ref `endpointChanged` (origin's one
URL, resolved as the sweep does, no longer has the digest the run's meta
recorded, or no longer resolves to one URL) or `endpointUnknown` (the run has no
meta). A pin that moves or goes between the plan and the run is `kept`, its
`reason` starting `pinGone`. Local refs are not affected by the endpoint. `selected` is every row whose action
is `create` or `none`, or what `--only` names.

## `wt refs restore <runId> --yes --json` — the result

Here each ref is one `update-ref --stdin` transaction: create the ref at
`object` (only where it is not), delete the pin at `object`. On origin it is
`git push --force-with-lease=<ref>: origin <pin>:<ref>` (only where origin has
nothing), then `git ls-remote` must show the ref at `object`, and only then is
the pin deleted, at its own value. A push that was rejected, timed out or was
interrupted, or a read-back that does not show it, keeps the pin (`failed`); a
later restore that finds origin holding the ref at `object` plans `none` and
only drops the pin. A branch comes back without its upstream setting. A run
whose pins are all restored leaves nothing under `refs/wt-swept/<runId>/`: its
meta goes once its last pin does.
`--expect <token>` refuses unless the plan made now has that token. The human
output goes to stderr; a handled SIGINT or SIGTERM writes the object too.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command`, `repo`, `runId`, `token` | | as in the plan it went by |
| `outcome` | `done` \| `refused` \| `partial` \| `interrupted` | done: every selected row restored. refused: nothing changed. partial: some. interrupted: a signal |
| `error`, `recovery` | string \| null | as in the sweep |
| `items` | array | the plan's items, each with `result` (`restored`, `kept`, `failed`, `notRun`, `interrupted`, `notSelected`), `reason`, `restored` (the ref is at `object`, read back) and `pinDeleted` (the pin is gone, read back) |

## `wt refs purge (<runId>… | --older-than <age>) --dry-run --json` — the plan

What `wt refs purge` would delete for good: the pins of the runs named, or of
every run older than `--older-than` (`14d`, `2w`, `36h`) by its `sweptAt`: the
meta's, else the time in its id. It deletes nothing. A run named that has
nothing under `refs/wt-swept/<runId>/` is an error; one with only its meta (a
run stopped before it moved anything) is purged like any other.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | |
| `command` | `"refs purge"` | |
| `repo` | string \| null | |
| `olderThan` | string \| null | `--older-than` as given; null when runs were named |
| `token` | string \| null | `rp1-…`: covers the runs, their pins and each pin's object. Null when there is nothing to purge, or on error |
| `error` | string \| null | |
| `runs` | array | `{runId, sweptAt, meta, endpoint, refs, unreachable}`: `sweptAt` unix seconds, the meta's, else from the id; `meta` the run's meta ref or null; `endpoint` as the meta records it, or null; `refs` `{pin, id, object}`; `unreachable` `{id, count}` for each pin whose commits no ref outside this purge and no worktree's HEAD reaches — what `git gc` may take once it goes. A commit two purged pins share counts for both; reflogs are not counted |

## `wt refs purge … --yes --json` — the result

Each pin is deleted only while it holds `object`; once every pin of a run is
gone, the run's pins are read again and, with none left, its meta goes last, at
the value read. A pin that appeared meanwhile (a sweep still running under that
id) keeps the meta. The human output goes to
stderr; a handled SIGINT or SIGTERM writes the object too.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command`, `repo`, `token` | | as in the plan it went by |
| `outcome` | `purged` \| `refused` \| `partial` \| `interrupted` | purged: every pin is gone (also when there was nothing to purge). refused: nothing was deleted. partial: some pins are gone; run it again. interrupted: a signal |
| `error`, `recovery` | string \| null | |
| `runs` | array | `{runId, sweptAt, refs, meta, metaDeleted}`: `metaDeleted` read back; each ref `{pin, id, object, result, reason}`: `result` `deleted`, `absent` (gone already), `kept` (still there: it moved, or git refused), `notRun` or `interrupted` |

## `wt refs swept --json` — the runs

Every run with pins or a meta still under `refs/wt-swept/`, oldest first. A run
that wrote its meta and was stopped before it moved anything is listed with no
refs. It changes nothing.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion` | | |
| `command` | `"refs swept"` | |
| `repo` | string \| null | |
| `error` | string \| null | |
| `runs` | array | `{runId, sweptAt, endpoint, refs}`: `sweptAt` unix seconds, the meta's, else from the id; `endpoint` as the meta records it, or null; each ref `{id, kind, name, pin, object}` |

## `wt sync --json` — the overview

What a rebase onto trunk would do to every worktree of the repository, as
`wt sync` prints it grouped. It fetches trunk first (`--no-fetch` compares with
trunk as last fetched; a fetch that fails is `fetchError`, and the overview goes
on), then simulates each rebase in the object store: it writes loose objects and
moves no ref. `wt sync <work> --json` is refused; the overview covers them all.

With `--all`, `--roots` or `--profile` stdout is an **array** of these objects,
one per repository, in the selection's order; `wt status --json` takes none of
those, so there is nothing to mirror. A repository that could not be read is an
object with `error` set and no worktrees, and the exit code is non-zero.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"sync"` | |
| `repo` | string | the main checkout, as in `wt sweep --json` |
| `name` | string | the repository's name: the main checkout's directory name |
| `trunk`, `trunkRef` | string \| null | `main`, `origin/main`; null when nothing names trunk, and `error` says so |
| `trunkSource` | string \| null | since 1.1.0: how `trunk` was found ([`trunkSource`](#trunksource--how-trunk-was-found)) |
| `onto` | string \| null | the trunk commit assessed against; null when trunk is not known |
| `fetched` | bool | trunk was fetched first |
| `fetchError` | string \| null | the fetch failed; the overview is against trunk as last fetched |
| `declared` | bool | trunk declares `.wt-sync.yaml`; without it nothing is rebased |
| `token` | string \| null | names what a run would start on; pass it to `wt sync run --expect` or `wt sync --run --expect`. Null when a run would start on nothing |
| `error` | string \| null | why there is no overview: trunk not known here, the repository not readable. The exit code is then non-zero |
| `sessionsError` | string \| null | Agent sessions could not be listed; `sessions` are then empty, not known empty |
| `worktrees` | array | every worktree but the main checkout, in git's order |

Each worktree:

| Field | Type | Meaning |
|---|---|---|
| `work`, `path` | string | |
| `branch` | string \| null | null when detached |
| `group` | `ready` \| `needsYou` \| `skipped` \| `current` | the overview's group; `current` (on trunk already) is left out of the human overview |
| `class` | `conflict-free` \| `recipe` \| `contested` \| `divergent` \| `stale` \| `current` \| `detached` \| `unknown` | what the simulated rebase found; `unknown` when the assessment failed |
| `verified` | bool | false is the overview's `recipe?`: a script owns a path and the replay could not be carried past it |
| `verdict` | `proceed` \| `skip` \| `refuse` | what `wt sync run <work>` would do with it: start on it (a contested one is rebased up to its stop and handed over), skip it, or refuse it untouched |
| `runnable` | bool | `wt sync --run`, with nothing named, takes it |
| `reason` | string \| null | why it is not runnable; null when it is |
| `behind`, `ahead` | int | commits against trunk |
| `dirty` | bool | tracked changes |
| `handedOver` | bool | an earlier run left a conflict here for a person |
| `planFile` | string \| null | that handover's plan |
| `sessions` | array | [agent sessions](#sessions--claude-and-codex) in it: `work`, `name`, `kind` (`claude` \| `codex`, the latter since 1.2.0), `state` (`busy` \| `idle`) |
| `stack` | array | every worktree a run on this one moves, parents first: `work`, `branch`, `path` |
| `stops` | array | every stop the simulated rebase reached, in order |
| `stops[].index`, `.total` | int | the commit's place in the replay, `1/3` |
| `stops[].commit` | string \| null | the commit replayed there |
| `stops[].subject` | string | |
| `stops[].resolved` | bool | every conflicted file there resolved by a strategy |
| `stops[].yours` | bool | the first stop no strategy resolves: a run hands it to a person |
| `stops[].files[]` | object | `path`, `resolved`, `strategy` (null when nothing claims it), `note` (why a strategy refused it, or what a lift did) |
| `strategies` | array of string | the declared strategies that resolved something, each once |
| `notes` | array of string | advisory; they never change the class |
| `error` | string \| null | the assessment failed |

The **token** covers the trunk's name, the wt configuration, the `.wt-sync.yaml`
on trunk, and every worktree whose `verdict` is `proceed`: its branch, class,
`verified`, `runnable` and stack. A newer trunk commit is not in it, but what it
changes about those is: a worktree that stops being ready after a fetch changes
the token.

## `wt sync run|resume|undo --json` — the result

`wt sync run [<work>...] --json` (also spelled `wt sync [<work>...] --run
--json`), `wt sync resume <work> --json` and `wt sync undo <work> --json` do
what they do without it, and print one object in the shape of `wt up --json`'s,
with more said about each worktree. Progress, and any question, go to
**stderr**; stdout carries exactly that object, also when a handled SIGINT or
SIGTERM ends the verb (exit 130). `--all`, `--roots` and `--profile` are refused
with `--json`: a run across repositories has no single result.

To run unattended: a run with nothing named, and no terminal, rebases nothing
without `--yes` (it is then `refused` with `error` saying so), as without
`--json`. `--yes` says yes to every question, the push included; `--no-push`
keeps the push out, on `run` and `resume` (undo never pushes). A named worktree
goes ahead without `--yes`, and with no terminal nothing is asked.

With `--expect <token>`, `wt sync run` and `wt sync --run` fetch, recompute the
overview's token and refuse, touching no worktree, when it differs: what a run
would start on is not what `wt sync --json` showed. It is then an `error` with
no participants and a non-zero exit.

The fields are `wt up --json`'s (above), with `command` one of `"sync run"`,
`"sync resume"`, `"sync undo"`, and:

| Field | Type | Meaning |
|---|---|---|
| `repo` | string \| null | the main checkout, as in `wt sweep --json`; null when the verb stopped before reading it |
| `onto` | string \| null | for resume, the trunk commit the handed-over run recorded; null for undo |
| `trunkSync` | object \| null | as for `wt up`; always null for resume and undo, which do not fetch |
| `error` | string \| null | as for `wt up`; also a question answered no (or nobody there to answer it), and for undo, why it stopped after putting some back |

Each participant has `wt up`'s fields, and:

| Field | Type | Meaning |
|---|---|---|
| `safetyRef` | string \| null | the ref pinning the branch's tip from before the run, `refs/wt-sync/<branch>/<epoch>` |
| `deferred` | array | what each deferred step of a finished rebase came to: `step` (its `run` line), `result` (`done` \| `committed` \| `failed` \| `skipped`), `reason` (why failed or skipped), `commit` (for committed) |
| `undoCommand` | array of string \| null | what puts it back where the run found it, as an argv (`["wt", "sync", "undo", work]`); null when nothing is there to undo |
| `planFile` | string \| null | for `handedOver`: the plan saying what is yours |

`result` has `wt up`'s values and one more, `undone`: `wt sync undo` put the
branch back at its safety ref, or aborted its handed-over rebase. A branch undo
finds at its old tip already is `skipped`; one it aborted but could not rewind
is `needsRecovery`. `outcome` follows the same rules, with `undone` counting as
`rebased` does. Undo lists every branch of the run it undoes before putting any
back, each `notRun` until it is reported; a signal partway marks `interrupted`
each of those whose tip, or the rebase it was waiting in, changed. The exit code
keeps its meaning; read `outcome`.

## `wt new <type>/<work> [--base <ref>] --dry-run --json` — the plan

What `wt new` would create, from local state alone. It **writes nothing**: no
fetch, no branch, no directory. `wt checkout <branch> [<work>] --dry-run --json`
is the same for a branch that exists, locally or on a remote (below). A name that cannot be created is
still a plan, with its `problems` and no token — never an error exit. The
command fails only outside a git repository.

The provisioning flags (`--no-setup`, `--no-build`, `--no-superset`) change
`provision`, `buildCommand` and `superset` as they would change the run.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"new"` | |
| `token` | string \| null | names the inputs of this plan; pass it to `wt new --json --expect`. Null when there is a problem |
| `trunkSource` | string \| null | since `new-plan` 1.2.0, `checkout-plan` 1.3.0: how the repository's trunk was found ([`trunkSource`](#trunksource--how-trunk-was-found)) |
| `configured` | bool | the repository has a wt configuration file that parses. False also on **detected defaults**: no file, and wt runs on what `wt init --yes` would write, with no problem named (since `new-plan` 1.1.0, `checkout-plan` 1.2.0; before, that repository was `noConfiguration`) |
| `types` | array of string | the worktree types the repository declares |
| `defaultType` | string \| null | the type a bare work name takes |
| `type`, `work` | string \| null | as parsed from the argument; null when it does not parse |
| `branch`, `path` | string \| null | the branch and the worktree path it would create; null when the name or type is not valid |
| `base` | string \| null | what the branch is cut from: `--base`, else trunk |
| `baseCommit` | string \| null | the commit `base` resolves to now; null when it does not |
| `provision` | bool | the repository's `bin/worktree/provision.sh` would run |
| `buildCommand` | string \| null | the build command that would run; null when build initialisation is off, or `--no-build`, `--no-setup` |
| `superset` | bool | wt would try to register the worktree in Superset: you opted in, `SUPERSET_REGISTER` is `auto` or `on`. Whether Superset is running and tracks the repository is not checked |
| `problems` | array | why it would not be created; empty when it would |
| `problems[].code` | see below | |
| `problems[].message` | string | the same as a sentence, as wt would print it |

`problems[].code`: `branchExists`, `pathExists`, `unknownType`, `invalidName`
(`.`, `/`, too many slashes, a branch name git refuses, nothing derivable), `noConfiguration`
(no `MAIN_BRANCH` in a configuration file, and no trunk wt can detect: `wt init` names one),
`configurationInvalid`, `branchMissing` and `branchCheckedOut` (checkout only:
git gives a branch one worktree), `baseMissing`.

The **token** covers the command, the branch and path, `baseCommit`, and the bytes
of the wt configuration file. `--base <oid>` with a full commit id, as a git
client names a commit, cuts the branch from exactly that commit and gives it no
upstream; `--base origin/main` would track `origin/main` under git's
`branch.autoSetupMerge`.

## `wt checkout <branch> [<work>] --dry-run --json` — the plan

As `wt new`'s, with `command` `"checkout"`, `type` the default type, and in place
of `base` and `baseCommit`:

| Field | Type | Meaning |
|---|---|---|
| `branchCommit` | string \| null | the local branch's commit now; null when there is no such local branch, as for `source` `remote` |
| `source` | `local` \| `remote` \| null | since 1.1.0: where the branch comes from (below); null when nothing has it |
| `remote` | string \| null | since 1.1.0: for `remote`, the remote's name |
| `remoteRef` | string \| null | since 1.1.0: for `remote`, its remote-tracking ref, `refs/remotes/<remote>/<branch>` |
| `remoteCommit` | string \| null | since 1.1.0: for `remote`, the commit `remoteRef` is at now |

The argument is resolved from local state alone, in this order:

1. a local branch of exactly that name — `source` `local`, used as it is. A
   local branch literally called `origin/topic` wins over the remote;
2. `<remote>/<branch>` for a configured remote whose remote-tracking ref
   `refs/remotes/<remote>/<branch>` exists — `source` `remote`;
3. a bare `<branch>` that exactly one remote has — `source` `remote`. When
   several do, git's `checkout.defaultRemote` picks one if it names one of
   them; otherwise it is `branchAmbiguous`, the message naming each.

For `remote`, `branch` is the local branch the run would create at
`remoteCommit`, with `remoteRef` as its upstream (`branch.<b>.remote` and
`branch.<b>.merge`). That branch existing already is `branchExists`: check it out
by its own name. Nothing is fetched — the remote-tracking refs are as the last
`git fetch` left them, so a branch pushed since is `remoteBranchMissing`
(`<remote>/<branch>` named) or `branchMissing` (a bare name) until you fetch.

`<branch>` is a branch name; a revision expression such as `main~1` is
`branchMissing`. The token pins the commit the worktree would land on —
`branchCommit`, or `remoteCommit` together with `remoteRef` — so
`wt checkout --expect` refuses a branch or remote-tracking ref that has moved
since the plan, and a plan made for a remote branch is refused once a local
branch of that name appears.

## `wt new … --json` and `wt checkout … --json` — the run

The same run as without `--json`: the plan is recomputed, then the worktree is
created and provisioned as it always is. Progress goes to **stderr**; stdout
carries exactly one object. Neither asks anything.

A plan with a problem is refused, creating nothing. With `--expect <token>` the
recomputed plan's token must equal the one given (an empty one is refused), or
the run is refused with `planChanged`, creating nothing: the base or branch moved, the name or the
configuration changed. `planChanged` comes first, followed by any problem the
recomputed plan has — a plan made for `origin/topic` gives `planChanged` and
`branchExists` once a local `topic` appears. That check is the last thing before `git worktree add`;
afterwards wt reads the new worktree's HEAD back, and a HEAD that is not the
pinned commit (the branch moved in between, or a hook committed) is
`headMoved` — created, never a quiet success. `wt checkout` from a remote creates
the branch only if it does not exist: one that appeared after that last check is
left as it is and the run refused as `planChanged`, with nothing created.

A handled SIGINT or SIGTERM, from the moment the command starts, writes the
object too — the step in flight `interrupted`, the rest as they stood — then
stops a `provision.sh`, build command or git still running, each with its whole
process group, and exits 130. SIGKILL or a crash may write none; treat a
missing object as unknown. Its reaper then stops what was running (see
[Signals](#signals-and-what-wt-starts)). A usage error (a missing argument, an unknown flag,
`--expect` without `--json`) is reported as without `--json`, with no object.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"new"` \| `"checkout"` | one schema each |
| `outcome` | see below | |
| `error` | string \| null | why it was refused, or stopped before planning (not in a git repository) |
| `problems` | array | as in the plan, plus `planChanged` and `headMoved` |
| `source`, `remote`, `remoteRef` | string \| null | `wt checkout` only, since 1.1.0: from the plan |
| `upstream` | string \| null | `wt checkout` only, since 1.1.0: the branch's upstream ref, read back after the run; null when it has none or there is no branch |
| `type`, `work`, `branch`, `path` | string \| null | from the plan |
| `base` | string \| null | `wt new`: what the branch was cut from. Null for `wt checkout` |
| `expectedCommit` | string \| null | the commit the plan pinned: `baseCommit`, `branchCommit` or `remoteCommit` |
| `commit` | string \| null | the new worktree's HEAD, read after `git worktree add`; null when there is none |
| `steps` | array | every side effect, always all seven, in this order |
| `steps[].step` | `worktree`, `branch`, `config`, `provision`, `submodules`, `build`, `superset` | |
| `steps[].result` | see below | |
| `steps[].commit` | string \| null | `worktree`: its HEAD; `branch`: its tip |
| `steps[].reason` | string \| null | why, for `skipped`, `failed` and `interrupted` |

`steps[].result`, by step:

| Step | Results |
|---|---|
| `worktree` | `created`, `failed` (`git worktree add` failed and left no worktree, or the path was taken). `created` with a `reason` when git failed after making it, as when a `post-checkout` hook fails; the steps after it are then `notRun` |
| `branch` | `created` (`wt new`; `wt checkout` from a remote, with the reason `tracking <remote>/<branch>`, or that the upstream could not be set), `untouched` (`wt checkout` of a local branch) |
| `config` | `done`, `skipped` (no source checkout), `failed` (an entry could not be copied, or escapes the worktree) |
| `provision` | `done`, `skipped` (no `provision.sh`), `failed` |
| `submodules` | `done`, `skipped` (no `.gitmodules`), `failed` |
| `build` | `done`, `skipped` (`--no-build`, or off in the configuration), `failed` |
| `superset` | `registered` (also when it already had the workspace), `skipped` (not opted in, `SUPERSET_REGISTER=off`, `--no-superset`, or under `auto` not installed, not running, no project for the repository), `failed` (Superset erred, or under `on` did not take it) |
| any | `notRun` (the run stopped before it), `interrupted` |

`--no-setup` skips every step after `branch`.

`outcome`, from the steps:

| Value | When |
|---|---|
| `created` | `worktree` created and every other step done, skipped, `untouched` or `registered`; no problem |
| `createdWithProblems` | `worktree` created, and a step `failed` or `notRun`, or `headMoved` |
| `refused` | nothing created: a problem in the plan, or `planChanged` |
| `failed` | `worktree` failed and there is no worktree; `branch` says whether `wt new`'s branch was made anyway |
| `interrupted` | a signal ended the run |

The exit code keeps its meaning: non-zero for a refusal, a failed
`git worktree add` and a failed `provision.sh`; zero for a failed build,
submodule initialisation or Superset registration. Read `outcome`.

## `wt remove <work> --dry-run --json` — the plan

The plan `wt remove` prints, as one object: the checkout, its HEAD, where the
branch stands and what becomes of it, every reason the removal would refuse,
what it would leave unreachable, and a token. It changes nothing. `wt remove
<work> --json` without `--yes` prints the same plan. Like the removal it lists the
[agent sessions](#sessions--claude-and-codex) and never fetches: merged is measured against
`origin/<trunk>` as last fetched and `<trunk>`.

A plan the removal would refuse is still the whole object, with `error` set, a
null token and a non-zero exit; so is one that cannot be made (no such
worktree, the main checkout), with the worktree fields null.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command` | 1, string, `"remove"` | |
| `repo`, `trunk` | string \| null | the main checkout, trunk's name |
| `trunkSource` | string \| null | since 1.3.0: how `trunk` was found ([`trunkSource`](#trunksource--how-trunk-was-found)); the token covers it |
| `token` | string \| null | names this plan; pass it to `wt remove --yes --json --expect`. Null when it would refuse, or on error |
| `error` | string \| null | why the removal would refuse, or why there is no plan |
| `path` | string \| null | the checkout |
| `adminDir` | string \| null | its git dir, `.git/worktrees/<id>` |
| `head` | string \| null | the commit checked out; null before the first commit |
| `detached` | bool | no branch is checked out, so none is touched |
| `branch` | object \| null | null when detached |
| `branch.name`, `branch.tip` | string, string \| null | the tip is null when the branch is already gone |
| `branch.merge` | `merged` \| `applied` \| `unmerged` \| `unknown` | `base` contains the tip; every commit is on `base` under another id, byte for byte; `base` lacks commits of it; nothing to compare with |
| `branch.base`, `branch.ahead` | string \| null, int | the base the standing is about, and the commits it lacks |
| `branch.pullRequest` | int \| null | a pull request GitHub merged into trunk at exactly `tip`, as `wt list` or `wt sweep` recorded it; makes an `unmerged` branch deletable |
| `branch.outcome` | `delete` \| `keep` \| `none` | deleted after the checkout, only at `tip`; renamed to `keepAs`; left as it is, `reason` says why. `recovery.json`'s `branch.plan` names |
| `branch.keepAs`, `branch.reason` | string \| null | |
| `bases` | array | `{name, tip}`: `origin/<trunk>`, then `<trunk>` |
| `dirty`, `statusError` | bool, string \| null | anything uncommitted, submodules included; why the status could not be read |
| `hiddenFiles` | array of string | unedited files `git status` is told not to look at |
| `movedSubmodules` | array of string | submodules at another commit than the recorded one |
| `hasSubmodules` | bool | the checkout has a `.gitmodules` |
| `nestedWorktrees` | array of string | other worktrees inside this one |
| `operation` | `rebase` \| `merge` \| `cherry-pick` \| `revert` \| `sequencer` \| `bisect` \| null | an operation stopped halfway there; `sequencer` is a cherry-pick or revert sequence whose HEAD marker is gone |
| `sessions` | array | `{id, name, kind, pid, state}` for each [agent session](#sessions--claude-and-codex) in the checkout, `kind` `claude` or `codex` (since 1.4.0), `state` `idle` or `busy`; the one running wt is not listed |
| `sessionsError` | string \| null | why they could not be listed |
| `lock` | object \| null | git's lock: `reason`, `holder` (who wt worked out is behind it), `pid`, `held` (still running; a stale one is released) |
| `unreachable` | array | each tip nothing surviving the removal holds: `{kind, oid, count, path, restoreCommand}`. `kind` `branch` is the branch it deletes — listed, not refused, with the `git branch` argv that brings its commits back; `head` and `submodule` (with `path`) refuse |
| `reachError` | string \| null | why that could not be worked out |
| `quarantine` | string \| null | with `--quarantine <dir>`, the folder |
| `keepSuperset` | bool | since 1.1.0: `--keep-superset` was given: the Superset workspace is left, and Superset is not asked |
| `force` | bool | bare `--force` was given: the problems with `force` true do not refuse. False for `--force=<list>`, which `forceWith` names |
| `forceWith` | array of string | since 1.2.0: the `--force` categories given, all five for bare `--force`; empty without it |
| `problems` | array | every problem: `{code, message, force, forceWith}`, `force` for one `--force` goes past, `forceWith` (since 1.2.0; null when `force` is false) the category that does. The removal refuses on any, less those whose `forceWith` is in the top-level `forceWith` |

`problems[].code`, exhaustively; the codes it shares with `sweep-plan`'s `kept`
mean the same there:

| Value | Meaning |
|---|---|
| `dirty` | uncommitted changes, submodules included, or a submodule at another commit |
| `statusUnknown` | the status, the git dir or HEAD could not be read |
| `session` | an agent session is in it: one problem for the idle ones (`idle-sessions`), one for the busy ones (`busy-sessions`) |
| `sessionsUnknown` | the sessions could not be listed (`sessions-unknown`) |
| `lockHeld` | git's lock, and its holder is still running (`lock`) |
| `hiddenChanges` | files `git status` is told not to look at (`hidden-files`) |
| `operation` | a rebase, merge, cherry-pick, revert or bisect in progress |
| `headUnreachable` | its HEAD holds commits nothing surviving would |
| `submoduleUnreachable` | a submodule commit only this worktree's copy holds |
| `reachUnknown` | what would be lost could not be worked out |
| `nestedWorktree` | another worktree is inside it |
| `quarantineUnusable` | the `--quarantine` folder exists, is on another volume, or is inside the repository |
| `planChanged` | result only: `--expect` named another plan |

`--force=<list>` names what the removal may go past, comma-separated or
repeated: `idle-sessions`, `busy-sessions`, `sessions-unknown`, `hidden-files`,
`lock`; `busy-sessions` covers idle sessions too, so a session that stops
working does not turn a forced removal into a refusal. Bare `--force` is all
five; `--force=` is none; an unknown name, `true` or `false` included, is a
usage error. Nothing goes past the others. Right before the checkout is deleted or moved,
its status and the sessions are read again: a session that turned busy or
arrived since the plan refuses the removal unless `--force` names its state
now, and a listing that fails refuses unless it names `sessions-unknown`.

The **token** covers the repository, trunk's name, how it was found and both
bases' commits, the wt configuration file, and everything the removal turns on: the checkout, its
admin dir, HEAD, the branch, its tip, standing, pull request and outcome, the
commit its kept name holds, anything uncommitted or hidden, moved submodules, nested worktrees, the
operation, the sessions by id and state (idle or busy), the lock (reason,
pid, held), what would be lost, the `--quarantine` folder or its absence, the
`--force` categories, whether the
Superset integration is on, and `--keep-superset`. Any difference is
a different token — a new trunk commit too, since the base commit is part of
what the removal was judged against.

## `wt remove <work> --yes --json` — the removal

The same removal as `wt remove <work> --yes`. The plan as wt prints it, and the
progress, go to **stderr**; stdout carries exactly one object. It never asks.

With `--expect <token>` (it needs `--yes --json`), the plan is made again and
the removal refuses, touching nothing, unless it has that token: `outcome`
`refused`, `planChanged` in `problems` with the plan's own problems, a non-zero
exit. Then, as every removal does, the checkout is read once more right before
it goes, and the branch's merge is asked again right before it is deleted.

A handled SIGINT or SIGTERM, from the moment the repository is open, writes
the object too, read from the disk as it is then — between the two moves of a
quarantine that is `partlyMoved`, `branch` `notRun` — and exits 130. SIGKILL
or a crash may write none: `<dir>/recovery.json` still says how far a
quarantine got. A usage error is reported as without `--json`, with no object.

| Field | Type | Meaning |
|---|---|---|
| `schema`, `schemaVersion`, `command` | 1, string, `"remove"` | |
| `repo`, `trunk` | string \| null | |
| `trunkSource` | string \| null | since 1.3.0: as in the plan |
| `token` | string \| null | the plan's token |
| `outcome` | see below | |
| `error` | string \| null | why it refused or stopped |
| `problems` | array | for `refused`: as in the plan, the ones that refused it, and `planChanged` |
| `path`, `branch`, `tip`, `keepAs` | string \| null | from the plan |
| `steps` | array | `worktree`, `branch`, `superset`, always all three, in that order: `{step, result, commit, reason, pin}`; `commit` is `branch`'s tip; `pin` (since 1.4.0) is, for `branch`, the ref the deleted branch was moved into, read back, and null otherwise |
| `forced` | array of string | since 1.2.0: the `--force` categories the removal went past, in the plan or in the final read; empty when none, or when it did not get that far |
| `quarantine` | object \| null | with `--quarantine`, once its `recovery.json` is written: `dir`, `checkoutMoved`, `adminMoved` (each directory, by identity, in the quarantine), `recoveryFile`, and `steps` as `recovery.json` records them |
| `restoreCommand` | array of string \| null | the argv that puts back what went: `wt restore <dir>` once a quarantine wrote its `recovery.json`; since 1.4.0 `wt refs restore <runId> --only refs/heads/<name> --yes`, run in `repo`, for a branch moved into a pin; else `git -C <repo> branch <name> <tip>` for a branch gone from its name (renamed to `keepAs`) |
| `recovery` | string \| null | for `interrupted`: what wt printed |
| `runId` | string \| null | since 1.4.0: the run under `refs/wt-swept/` the deleted branch was moved into; null when it was not moved |

Since 1.4.0 a removal without `--quarantine` deletes a merged branch the way a
sweep does (see [Deleted branches are pinned](#deleted-branches-are-pinned)):
into `refs/wt-swept/<runId>/heads/<name>`, in a run of its own, begun only when
the branch step deletes. An unmerged branch wt made is renamed to `keepAs`,
which keeps its commits under a name, so nothing is pinned and `runId` is null;
so it is for a branch left `untouched` or `kept`. With `--quarantine`, the
quarantine pins the branch under `refs/wt-quarantine/`, and `wt restore <dir>`
puts it back with the worktree.

`steps[].result`, by step:

| Step | Results |
|---|---|
| `worktree` | `removed`, `quarantined` (both moves), `partlyMoved` (the checkout moved, its admin dir not), `kept` (still there; `reason` says why) |
| `branch` | `deleted`, `renamed` (to `keepAs`), `untouched`, `kept` (turned down on purpose: it moved, is no longer merged, or a worktree took it), `failed` (git failed) |
| `superset` | `deregistered`, `notRegistered`, `skipped`, `failed`, and since 1.1.0 `optedOut` (`--keep-superset`), as in `sweep`, where the risk `--keep-superset` closes is explained |
| any | `notRun` (not reached), `interrupted` (a signal caught it, and its state does not say it finished) |

`outcome`:

| Value | When |
|---|---|
| `removed` | the worktree is gone or quarantined, and the branch step did what the plan said |
| `removedWithBranchProblem` | the worktree is gone, and the branch `kept` |
| `refused` | nothing changed |
| `partial` | something changed and a later step failed: a quarantine that stopped after writing `recovery.json` (its lock and pins are taken), the second move, or the branch step (`failed`); `wt restore <dir>` puts a quarantine back |
| `interrupted` | a signal ended the run |

The exit code is zero only for `removed`.
