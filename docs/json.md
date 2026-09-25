# wt's JSON output

Two commands print one JSON object on stdout when given `--json`, for tools that
drive wt (a git client, an editor, a script). Their human output is unchanged
without it.

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

A string field that has no value is `null`, not `""`. Paths are absolute.

## JSON Schema

Both objects have a JSON Schema (draft 2020-12), for validating what you read or
generating types from it:

| Output | Schema | `$id` |
|---|---|---|
| `wt status --json` | [`schema/status.v1.json`](../schema/status.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/status.v1.json` |
| `wt up --json` | [`schema/up.v1.json`](../schema/up.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/up.v1.json` |

They are built into the binary: `wt schema` lists them and `wt schema up` prints
one, so the schema you read is the one for the wt you run. Validate against that
one. Its enumerations are exact for that wt, and a newer wt may add values and
fields within the same schema number — so an older copy of a schema can reject
newer output, and a reader that does not validate treats an unknown value as
unknown. wt's own tests validate every `--json` output against these schemas.

## `wt status [<work>] --json` — the plan

What `wt up` would start on, from local state alone. It **writes nothing**: no
fetch, no rebase simulation, no pull-request cache; every git it runs reads, and
`git status` runs with `--no-optional-locks`. It lists Claude sessions with
`claude agents`. `<work>` defaults to `.`, the worktree you are in.

A worktree `wt up` would not start on is still a plan, with `upEligible: false`
and the reason — never an error exit. The command fails only outside a git
repository.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"status"` | |
| `token` | string \| null | names the inputs of this plan; pass it to `wt up --expect`. Null when not eligible |
| `trunk` | string | trunk as `wt up` resolves it here: `MAIN_BRANCH`, else origin's HEAD |
| `trunkRef` | string | `origin/<trunk>`, what `wt up` rebases onto after its fetch |
| `trunkRefExists` | bool | whether that ref is here, as last fetched |
| `trunkTip` | string \| null | its commit |
| `trunkRefUpdatedAt` | string \| null | when that ref last moved, from its own reflog (RFC 3339, UTC); null without a reflog. Not FETCH_HEAD's time, which any fetch sets |
| `configured` | bool | the repository has a wt configuration that parses |
| `worktree` | object \| null | the worktree named; null when it cannot be found |
| `worktree.work` | string | its work name, as `wt list` prints it |
| `worktree.branch` | string | `""` when detached |
| `worktree.path` | string | |
| `worktree.isMain` | bool | the main checkout |
| `worktree.state` | `clean` \| `dirty` \| `unreadable` | the checkout: `clean` is nothing uncommitted |
| `worktree.behind`, `worktree.ahead` | int \| null | commits against `trunkRef` (the local trunk when that is not here); null when they cannot be counted |
| `upEligible` | bool | `wt up` would start on it. Dirt, conflicts and busy sessions are **not** checked here: `wt up` checks them and its result says so |
| `upIneligibleCode` | string \| null | `mainCheckout`, `noConfiguration`, `configurationInvalid`, `detachedHead`, `onTrunk`, `handedOver` (an earlier `wt sync` run waits on a person there), `notAWorktree` |
| `upIneligibleReason` | string \| null | the same as a sentence |
| `stack` | array | every worktree `wt up` would move, parents first, from local refs as of now; just the one when it has no stack |
| `stack[].work`, `.branch`, `.path` | string | |
| `sessions` | array | Claude sessions in the stack's worktrees (Codex sessions are not detected) |
| `sessions[].work`, `.name` | string | the worktree it is in, and its name |
| `sessions[].kind` | `"claude"` | |
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
| `onto` | string \| null | the trunk commit the run rebased onto, after its fetch; null when never determined |
| `fetched` | bool | trunk was fetched (false with `--no-fetch`, or when the run stopped first) |
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
| `refused` | unchanged | refused before anything was touched, for a reason of its own: uncommitted changes, a busy Claude session, a conflict that would be yours |
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
