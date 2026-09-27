# wt's JSON output

`wt status`, `wt up`, `wt sweep`, `wt sync` and its verbs, `wt new` and
`wt checkout` print JSON on stdout when given `--json`, for tools that drive wt
(a git client, an editor, a script). Their human output is unchanged without
it.

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

| Schema | Version | Change |
|---|---|---|
| `sync` | 1.0.0 | `wt sync --json`, the overview |
| `sync-run` | 1.0.0 | `wt sync run`, `resume` and `undo --json`, the result |
| `new-plan`, `checkout-plan` | 1.0.0 | `wt new` and `wt checkout --dry-run --json`, the plan |
| `new`, `checkout` | 1.0.0 | `wt new` and `wt checkout --json`, the result |

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
| `wt sync --json` | [`schema/sync.v1.json`](../schema/sync.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sync.v1.json` |
| `wt sync run\|resume\|undo --json` | [`schema/sync-run.v1.json`](../schema/sync-run.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/sync-run.v1.json` |
| `wt new --dry-run --json` | [`schema/new-plan.v1.json`](../schema/new-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/new-plan.v1.json` |
| `wt new --json` | [`schema/new.v1.json`](../schema/new.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/new.v1.json` |
| `wt checkout --dry-run --json` | [`schema/checkout-plan.v1.json`](../schema/checkout-plan.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/checkout-plan.v1.json` |
| `wt checkout --json` | [`schema/checkout.v1.json`](../schema/checkout.v1.json) | `https://raw.githubusercontent.com/anders-lindstrom/wt/main/schema/checkout.v1.json` |

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
| `trunk`, `trunkRef` | string \| null | `main`, `origin/main` |
| `onto` | string \| null | the trunk commit assessed against; null when trunk is not known |
| `fetched` | bool | trunk was fetched first |
| `fetchError` | string \| null | the fetch failed; the overview is against trunk as last fetched |
| `declared` | bool | trunk declares `.wt-sync.yaml`; without it nothing is rebased |
| `token` | string \| null | names what a run would start on; pass it to `wt sync run --expect` or `wt sync --run --expect`. Null when a run would start on nothing |
| `error` | string \| null | why there is no overview: trunk not known here, the repository not readable. The exit code is then non-zero |
| `sessionsError` | string \| null | Claude sessions could not be listed; `sessions` are then empty, not known empty |
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
| `sessions` | array | Claude sessions in it: `work`, `name`, `kind` (`"claude"`), `state` (`busy` \| `idle`) |
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
is the same for a branch that exists (below). A name that cannot be created is
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
| `configured` | bool | the repository has a wt configuration that parses |
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
(`.`, `/`, too many slashes, a branch name git refuses, nothing derivable), `noConfiguration`,
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
| `branchCommit` | string \| null | the local branch's commit now; null when there is no such branch |

`<branch>` is a local branch's exact name; a revision expression such as
`main~1` is `branchMissing`. Its token pins `branchCommit`, so
`wt checkout --expect` refuses a branch that has moved since the plan.

## `wt new … --json` and `wt checkout … --json` — the run

The same run as without `--json`: the plan is recomputed, then the worktree is
created and provisioned as it always is. Progress goes to **stderr**; stdout
carries exactly one object. Neither asks anything.

A plan with a problem is refused, creating nothing. With `--expect <token>` the
recomputed plan's token must equal the one given (an empty one is refused), or
the run is refused with `planChanged`, creating nothing: the base or branch moved, the name or the
configuration changed. That check is the last thing before `git worktree add`;
afterwards wt reads the new worktree's HEAD back, and a HEAD that is not the
pinned commit (the branch moved in between, or a hook committed) is
`headMoved` — created, never a quiet success.

A handled SIGINT or SIGTERM, from the moment the command starts, writes the
object too — the step in flight `interrupted`, the rest as they stood — then
stops a `provision.sh`, build command or git still running, each with its whole
process group, and exits 130. SIGKILL or a crash may write none; treat a
missing object as unknown. A usage error (a missing argument, an unknown flag,
`--expect` without `--json`) is reported as without `--json`, with no object.

| Field | Type | Meaning |
|---|---|---|
| `schema` | 1 | the major version |
| `schemaVersion` | string | the full version, `1.<minor>.<patch>` |
| `command` | `"new"` \| `"checkout"` | one schema each |
| `outcome` | see below | |
| `error` | string \| null | why it was refused, or stopped before planning (not in a git repository) |
| `problems` | array | as in the plan, plus `planChanged` and `headMoved` |
| `type`, `work`, `branch`, `path` | string \| null | from the plan |
| `base` | string \| null | `wt new`: what the branch was cut from. Null for `wt checkout` |
| `expectedCommit` | string \| null | the commit the plan pinned: `baseCommit` or `branchCommit` |
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
| `branch` | `created` (`wt new`), `untouched` (`wt checkout`) |
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
