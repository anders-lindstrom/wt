# wt's JSON output

`wt status`, `wt up` and `wt sweep` print one JSON object on stdout when given
`--json`, for tools that drive wt (a git client, an editor, a script). Their
human output is unchanged without it.

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

Each schema is versioned on its own; `sweep-plan` and `sweep` started at 1.0.0.

A string field that has no value is `null`, not `""`. Paths are absolute.

## JSON Schema

Every object has a JSON Schema (draft 2020-12), for validating what you read or
generating types from it:

| Output | Schema | `$id` |
|---|---|---|
| `wt status --json` | [`schema/status.v1.json`](../schema/status.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/status.v1.json` |
| `wt up --json` | [`schema/up.v1.json`](../schema/up.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/up.v1.json` |
| `wt sweep --dry-run --json` | [`schema/sweep-plan.v1.json`](../schema/sweep-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sweep-plan.v1.json` |
| `wt sweep --yes --json` | [`schema/sweep.v1.json`](../schema/sweep.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sweep.v1.json` |

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

## `wt sweep --dry-run --json` — the sweep's plan

The plan `wt sweep` prints, as one object: every row, why it counts as merged or
why it is kept, and a token. It deletes nothing. It fetches origin first, as a
sweep does, unless `--no-fetch`; it asks GitHub about the branches' pull requests
and `claude agents` about sessions, as a sweep does. `wt sweep --json` without
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
| `bases` | array | what merged is measured against, first first: `{name, tip}` for `origin/<trunk>` as fetched, then `<trunk>` |
| `fetched` | bool | origin was fetched first |
| `token` | string \| null | names this plan; pass it to `wt sweep --expect`. Null when there is nothing to remove or delete, or on error |
| `error` | string \| null | why no plan could be made |
| `items` | array | the rows, in the order `wt sweep` prints them |

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
| `dirty` | uncommitted changes, untracked files included |
| `statusUnknown` | `git status` failed there, so its changes could not be seen |
| `detached` | a worktree with no branch whose HEAD trunk contains; sweep never removes one (`wt remove <path>` does) |
| `session` | a Claude session is in it, idle or busy |
| `sessionsUnknown` | `claude agents` failed, so no worktree is known to be free of one |
| `lockHeld` | git's worktree lock, and its holder is still running |
| `directoryMissing` | git lists it, but its directory is gone (`git worktree prune`) |
| `heldByRebase`, `heldByBisect` | a rebase or bisect in that worktree holds the branch |
| `mainCheckout` | the main checkout is on the branch |
| `branchNotDeletable` | `wt remove` would not delete the branch with the worktree |
| `notMerged` | for `upstreamGone`: trunk lacks its commits |

The **token** covers the repository, trunk's name, the wt configuration file,
and every row: its category, branch, commit, worktree, evidence, pull request
(number, state, base) and reasons, codes and words. So a branch that moved, a
new merged branch, a worktree that turned dirty, a session that arrived or
changed, a pull request GitHub no longer answers for — any of them is a
different token. A newer trunk commit that changes no row is not.

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

Each item:

| Field | Type | Meaning |
|---|---|---|
| `category`, `branch`, `tip`, `work`, `path` | | as in the plan |
| `result` | see below | |
| `reason` | string \| null | why, for everything but `removed` and `deleted` |
| `worktreeRemoved` | bool | the worktree is gone now |
| `branchDeleted` | bool | the branch is gone now |
| `restoreCommand` | array of string \| null | when the branch was deleted: `["git", "-C", repo, "branch", branch, tip]`, which puts it back at that commit (not its upstream setting) |

`worktreeRemoved` and `branchDeleted` are read from the repository after the
row, not from what was attempted.

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
