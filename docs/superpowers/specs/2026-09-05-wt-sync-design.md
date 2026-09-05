# wt sync — keeping worktrees on trunk

Design, 2026-09-05. Revised the same day after an adversarial review against the
live repositories; the findings that changed it are noted inline.

## The problem

Twenty-four worktrees across `server` and `accessmanager`, several of them
hundreds of commits behind trunk. Bringing one up to date is mechanical almost
every time, but the mechanical part is invisible until someone looks, and
looking costs a model. The branches that are genuinely hard to rebase are a
small minority, and they are hidden among the easy ones.

Meanwhile several of those worktrees have an agent working in them. A rebase
rewrites that agent's branch under it, so it cannot simply happen.

`wt sync` exists to make the easy majority free and the hard minority visible,
and to knock before rewriting somebody's history.

## What the fleet actually looks like

Measured 2026-09-05 with `git merge-tree --write-tree`, which performs the whole
merge in the object store without touching a working tree. `server` (trunk
`development`) has 15 worktrees; `accessmanager` (trunk `main`) has 9, one of
which is **detached** — `.claude/worktrees/nostalgic-mcnulty-7b0146`, left by an
earlier agent session. Twenty-four physical worktrees, twenty-three with a
branch.

| class | count | meaning |
|---|---|---|
| current | 4 | behind 0 |
| stale | 6 | behind 24–718, **ahead 0** — nothing of their own |
| clean | 4 | no conflict at the endpoint |
| recipe-only | 2 | conflicts fall entirely in paths a resolver claims |
| contested | 6 | some real source overlap |
| divergent | 1 | `spring-boot-4-jackson-3` — the ground moved (§1) |

After the resolvers in this design:

| branch | conflicts | left for a person |
|---|---|---|
| `feat_wt/sync_skipped` | 3 | **0** |
| `feat_wt/webkey` | 5 | **0** |
| `feat_wt/extract_webaccess` | 6 | **0** |
| `feat_wt/deployprocess` | 1 | 1 (`AGENTS.md`, additive both sides) |
| `feat_wt/axis_acc` | 7 | 3 |
| `feat_wt/state_stats` | 7 | 4 |
| `feat_wt/arch` | 4 | 4 |
| `feat_wt/spring-boot-4-jackson-3` | 7 | **not a rebase** — `divergent`, opens a campaign (§1) |
| `april-fools` | 5 | 5 (756 behind, likely dead) |

### State the ratio honestly

**Seventeen of the twenty-three branch-attached worktrees need no judgement.**
That is the number that matters for a fleet sweep, but it is not a statement
about rebases: ten of the seventeen need no rebase at all — four are current, six
are stale and are never rebased.

Of the **thirteen worktrees that actually need a rebase, seven are predicted
mechanical**. That is the honest figure for "how often does `wt sync` do work
without a model", and it is the one to quote when arguing about cost.

Both numbers are real; using the first where the second belongs overstates the
case. Review finding 13.

## Design principles

1. **Negotiation is the exception.** As in `devports`, but computed rather than
   declared: the class comes from git, not from a config field someone has to
   keep honest.
2. **No component ever merges content by reasoning.** A resolver owns a file
   shape completely or refuses. There is no "usually right" strategy.
3. **Resolvers are fast and never regenerate.** Regeneration is deferred and
   runs once per rebase, not once per conflicting commit.
4. **Read-only by default.** `wt sync` shows; it never changes anything. A verb
   performs, and it names what it will touch first.
5. **Mutation is opt-in per repo.** A repository with no `.wt-sync.yaml` is
   reported, never rebased.
6. **A model is involved only where judgement is genuinely required**, and when
   it is, it is handed a written plan rather than a repository to investigate.

---

## 1. Triage

One `git fetch` per repo serves every worktree — they share an object store.
Then per worktree, **with no working tree touched**:

```
behind    = git rev-list --count <branch>..origin/<trunk>
ahead     = git rev-list --count origin/<trunk>..<branch>
dirty     = git -C <path> status --porcelain
agent     = claude agents --json, matched on .cwd
conflicts = git merge-tree --write-tree --name-only origin/<trunk> <branch>
```

**A worktree with no branch is classified `detached` and skipped** before any
branch-based command runs. One exists today. Review finding 14.

| class | condition | what `wt sync run` would do |
|---|---|---|
| `detached` | no branch | skipped; reported |
| `current` | behind 0 | nothing; not printed |
| `stale` | ahead 0 | not rebased by default (§7) |
| `clean` | merge-tree exits 0 | rebase |
| `recipe` | every conflicting path is *claimed* by a resolver | rebase; resolvers attempt it |
| `contested` | some conflicting path is claimed by none | rebase; resolvers strip what they can; hand the residue over |
| `divergent` | the branch and trunk have moved apart structurally (below) | **never rebased automatically.** Opens a campaign |

Two modifiers override the class:

- **dirty → never touched.** Not rebased, not stashed. The stash stack is shared
  across every worktree of a repo, so a tool that stashes is a tool that can eat
  another session's work.
- **agent busy → deferred silently.** No message, no rebase.

### Triage is a screen, not a promise

`merge-tree` tests a *merge*; a rebase replays each commit and can conflict at an
intermediate step the endpoint does not. Two consequences, both deliberate:

- A `clean` prediction that conflicts in reality aborts, restores from the safety
  ref, and reclassifies as `contested`. It never leaves a worktree mid-rebase
  without a plan on disk.
- The `recipe` class is **provisional**. Triage only knows that a resolver
  *claims* the path; whether it can actually resolve that particular conflict is
  known only when the conflict exists. A resolver that refuses at rebase time
  reclassifies the worktree to `contested`. Review finding 6.

On the four currently-clean branches, replay was checked against the trunk-side
changes and **none of the four is predicted to conflict during replay** —
`pruning_keyset_index` and `setting_pin` have no path overlap at all;
`controller_stats` and `pruning_cron` have single-commit overlaps that merge
cleanly. The screen is adequate for today's fleet.

### `divergent`: when a rebase is not a rebase

Some branches have not merely fallen behind — the ground has moved under them.
`feat_wt/spring-boot-4-jackson-3` is 284 behind, touches nine build files
including `gradle/libs.versions.toml`, has **49 files where both sides moved**,
and makes the spec resolver refuse because the framework upgrade rewrites the
generator's own output. Nothing about that is mechanical. Handing it a plan file
saying "7 conflicts, good luck" would be the tool lying about the size of the
job.

Compare `feat_wt/state_stats`: 90 behind, 15 files where both sides moved, **no**
build files, and the resolvers absorb three of its seven conflicts. That is a
rebase.

The class is computed from three signals, any one of which is enough:

| signal | why it means "not mechanical" |
|---|---|
| a resolver **refuses** (as opposed to no resolver claiming the path) | a shape that is normally deterministic has genuinely diverged |
| the branch changes the dependency graph — `gradle/libs.versions.toml`, any `build.gradle`, `package.json` beyond a pin | trunk's code has been written against a different set of libraries |
| both sides moved more than 30 of the same files | the branch is being replayed onto code it no longer recognises |

Thresholds are tunable and are wrong at first. What matters is that the tool
**names the difference** rather than presenting a six-week workstream as a
rebase with seven conflicts.

#### What `wt sync` does with one

It never rebases it, never queues it, and never asks an agent for permission —
there is nothing to permit yet. It offers a **campaign**, and the brief it
produces is the opposite of the terse plan file a contested rebase gets:

- **What landed, in full.** Every first-parent commit between the two bases,
  grouped by scope, with the merged ranges expanded — not the 60-character
  `lands:` summary.
- **What moved underneath.** For each subsystem the branch touches, the
  trunk-side changes to that same subsystem. This is the actual work: not
  "resolve this conflict" but "trunk's serialization layer changed; the branch's
  16 commits assume the old one".
- **Where the branch will have to change rather than merge.** The both-moved
  file list, with each side's commit subjects.
- **An honest size estimate**, in commits and files, stated as a workstream.

#### Staged rebase

A 284-commit gap should not be crossed in one jump. `wt sync campaign` proposes
**intermediate bases** — trunk merge commits at intervals across the gap — and
rebases to each in turn. Each stage has a comprehensible amount of change to
reason about, each stage's resolutions are cached by rerere for the next, and a
stage that goes badly is undone without losing the ones before it.

This is why rerere matters more here than anywhere else: the same conflict shape
recurs at every stage, and paying for it once is the difference between a
tractable campaign and an intractable one.

#### It is allowed to be a big piece of work

A campaign is not a `wt sync` operation that happens to take longer. It is a
separate workstream with its own branch, its own budget and possibly its own
plan, and the tool's job is to set it up honestly and then get out of the way.
The alternative — quietly leaving `spring-boot-4-jackson-3` out of the fleet
sweep because it is inconvenient — is how a branch gets to 284 behind.

### Agent detection, and what it does not cover

`claude agents --json` lists interactive sessions as well as background ones,
with `cwd`, `id`, `kind`, `name`, `sessionId`, `startedAt` and `state`. There is
**no `pid` field** and no explicit idle/busy flag — `state` carries values like
`blocked`. It is enough to answer "is a Claude session living in this worktree",
which removes the `devports` process-environment scan and its
unknown-versus-gone ambiguity.

It does **not** see Codex, a dev server, a running test, or an IDE build in an
otherwise clean worktree. `wt sync` therefore treats "no Claude session" as
"nobody to ask", never as "nothing is happening", and the dirty check remains the
real guard. Review finding 10.

## 2. Conflict resolvers

A resolver is a script in the repo that owns exactly one conflict shape, living
in `bin/conflict/` alongside the existing `bin/github/`, `bin/sqs/` and
`bin/worktree/`.

### Contract

```
bin/conflict/<name> --claims               print the path globs this resolver owns
bin/conflict/<name> --check   <file>       0 = I can resolve this conflict
                                           1 = not mine
bin/conflict/<name> --resolve <file>       resolve in place and `git add` it
                                           0 = resolved
                                           2 = refuse; one line of reason on stderr
```

`--claims` is what triage uses, because at triage time the file on disk is clean
and the base/ours/theirs blobs exist only inside `merge-tree` output. `--check`
and `--resolve` are valid only against a genuinely conflicted file, mid-rebase,
where the three stages are in the index. Splitting these two was the fix for
review finding 6: a single path-matching `--check` would over-claim — a
`package.json` conflict that is *not* the spec pin would be silently mishandled.

A resolver that cannot resolve **refuses**; it never guesses.

### `server`

| script | shape |
|---|---|
| `bin/conflict/openapi-version` | `application.yaml`, inside the `openapi:` block |
| `bin/conflict/openapi-spec` | `etc/openapi/apidocs/*.json` |
| `bin/conflict/gradle-includes` | `settings.gradle` `include` list |

### `accessmanager`

| script | shape |
|---|---|
| `bin/conflict/spec-client-version` | `**/package.json`, the `@telcred/spec-telcredv2-typescript-axios` pin |
| `bin/conflict/pnpm-lock` | `pnpm-lock.yaml` |

### Extraction changes the bump scripts' shape, not just their location

`bump_openapi.sh` already parses normal and diff3 markers in the `openapi:`
block, refuses when the two sides differ by more than the version line, and runs
`./gradlew webapp:generateOpenApi`. `bump_axios_client.sh` already collapses
pin-only manifest conflicts, refuses on leftover markers in its target
manifests, runs `pnpm i`, and conditionally prints the stage-and-continue lines.

But the extraction is **not behaviour-preserving**, and the spec should not
pretend otherwise. Review finding 5:

- Both scripts take a version from the user or from a tag listing. A resolver
  gets no version argument, so version *selection* is a separate concern from
  conflict *resolution* and must stay in the bump scripts.
- Neither script stages anything today; a resolver must `git add`.
- `bump_axios_client.sh` rewrites all five manifests in one pass. A per-file
  resolver loses that grouping, so the resolver must be given the whole set or
  the caller must iterate and the deferred install must run once.

So the split is: **resolvers are pure content transformations over a conflicted
file; orchestration, version choice, staging policy, installation and rebase
continuation stay outside them.** The bump scripts call the resolvers for the
marker handling they already do, and keep the rest.

### `openapi-spec`: resolving a 950 KB generated file without Gradle

The resolver performs a three-way merge over top-level keys — each entry under
`paths`, under `components.schemas`, **and under `tags`** — plus `info.version`.

`tags` was missing from the first draft of this design and is a real third
mutable section: on `feat_wt/webkey` the branch adds `Web-key` and `WebKeys`
while trunk adds `Host State Stream`, `Housekeeping` and `State Stream`. The
additions are disjoint, so a union by tag name resolves them, but a resolver
limited to paths and schemas would have had to discard one side's tags. Review
finding 4.

Measured over the five conflicting server branches on `openapi_v3.json`:

| branch | ours changed | trunk changed | keys both touched |
|---|---|---|---|
| `feat_wt/webkey` | 28 | 22 | **1** — `info.version` |
| `feat_wt/state_stats` | 22 | 22 | **1** — `info.version` |
| `feat_wt/sync_skipped` | 2 | 7 | **1** — `info.version` |
| `axis_acc` | 15 | 35 | **1** — `info.version` |
| `feat_wt/spring-boot-4-jackson-3` | 124 | 238 | 28 real keys |

Four of five collide on nothing but the version, which is decided by rule. The
fifth is a Spring Boot 4 / Jackson 3 upgrade that rewrites the generator's own
output shape; the resolver **detects the real overlap and refuses**, which is the
signal you want rather than a case to force.

**Honest limit.** springdoc emits `paths` and `components.schemas` unsorted —
`springdoc.writer-with-order-by-keys` is not enabled — so a structural merge
cannot guarantee byte-identical output to Gradle's. The resolver unblocks the
rebase; it does not produce the final artifact. That comes from the deferred
regeneration (§3).

### Version rules

**`max-plus-patch`** (server, `openapi.<api>.version`). Take the higher of ours
and trunk's; if that is trunk's, bump one patch, because the branch still needs a
version of its own. Branch claimed 2.38.3, trunk moved to 2.38.5 → 2.38.6.

**Never move a client off a snapshot automatically.** On a spec-client pin
conflict, **keep ours** and report that the branch is still pinned to a snapshot.

The first draft had a `prefer-release` rule — if trunk's release version is
greater than or equal to the snapshot's base, assume the server change landed and
take trunk's. **That rule is wrong, and the live case proves it.**
`feat_wt/extract_webaccess` pins `2.36.0-snapshot.20260831123245` and imports
`WebKeysApi` in five files. That API comes from server `feat_wt/webkey`, which is
**not merged**: trunk's spec at 2.38.2 contains zero web-key paths, while
`feat_wt/webkey` contains eleven. Moving the client to `2.37.1` because 2.37.1 >
2.36.0 would silently select a client without the API the branch depends on, and
break the build. A higher version number is not evidence that a *particular*
branch's server change landed. Review finding 1 — the most valuable finding of
the review.

Moving off a snapshot is `api-bump` step 10: a deliberate act performed once the
server side merges, not a side effect of rebasing. `wt sync` surfaces it and
stops there.

## 3. `.wt-sync.yaml`

Committed at the top of each repo, like `.dev-ports.yaml`: the quirk is a fact
about the repo, not a personal preference.

```yaml
resolvers:
  - bin/conflict/openapi-version
  - bin/conflict/openapi-spec
  - bin/conflict/gradle-includes

defer:
  - run: ./gradlew webapp:generateOpenApi
    when: paths-changed(etc/openapi/apidocs/**)

verify:
  - git diff --exit-code etc/openapi/apidocs
```

### The config and the resolvers are read from trunk, not from the branch

`.wt-sync.yaml` names commands and `bin/conflict/*` are executable. `wt` today
reads a repo's configuration from the caller's checked-out branch, and both main
checkouts are currently sitting on `chore/devports` rather than trunk. If `wt
sync` inherited that, a feature branch could change `defer.run` or a resolver and
watch mode would execute branch-authored code unattended.

**Both the config and the resolver scripts are read from the fetched trunk**
(`origin/<trunk>:.wt-sync.yaml`, and resolvers extracted from that tree to a
temporary directory) rather than from the working tree of the branch being
rebased. Review finding 3.

### `defer` is keyed on what changed, not on what conflicted

A deferred step runs when the rebase changes the paths it depends on — comparing
old HEAD to new HEAD — **not** only when a resolver fired. `feat_wt/arch` and
`april-fools` take trunk-side lockfile changes with no lock conflict at all; a
defer keyed on conflicts would leave `node_modules` stale. Review finding 11.

`accessmanager` needs a second deferred step for the same reason:
`pnpm run generate-git-info` embeds the HEAD hash, branch and timestamp, and
every rebase invalidates it.

```yaml
defer:
  - run: pnpm install
    when: paths-changed(pnpm-lock.yaml, '**/package.json')
  - run: pnpm run generate-git-info
    when: head-changed
```

### What deferral is worth

`./gradlew webapp:generateOpenApi` takes **1–2 minutes** (measured). Commits
touching the spec, per branch:

| branch | commits | commits touching the deferred paths |
|---|---|---|
| `axis_acc` | 86 | 4 |
| `feat_wt/webkey` | 27 | 2 |
| `feat_wt/state_stats` | 12 | 1 |

So `axis_acc` goes from roughly six minutes of Gradle spread across four stops to
one run at the end.

The first draft claimed `feat_wt/extract_webaccess` needed five `pnpm i` runs.
**That was overcounted** — it counted commits touching a manifest, but four of
them touch the branch-new `apps/webaccess/package.json`, which cannot conflict
with trunk. One commit changes the five existing manifests together and the bump
script already resolves them before a single install. The real figure is one
install, possibly two if the lock needs a second reconciliation. Review finding
15. The server rows above were checked and are sound.

`verify` is deliberately **not** `worktree.conf`'s `TEST_COMMAND` — that is
`./gradlew test` and `pnpm test`, far too slow to run per rebase. The
`git diff --exit-code etc/openapi/apidocs` line is lifted from `test_pr.yaml`; it
is CI's own guard and catches the real failure mode in about two seconds.

**A repo with no `.wt-sync.yaml` is triaged and reported, never rebased.** The
default `$WT_ROOTS` reaches many repositories that never opted into any of this —
`private/anygame` alone has 14 worktrees — and `--all` must not rewrite history
in them. Review finding 2.

## 4. Rebase execution

### Command

```
git -C <worktree> rebase --no-update-refs --no-gpg-sign origin/<trunk>
```

### `--update-refs` cannot do the job here — the first draft had this backwards

The first draft claimed `--update-refs` would silently advance a branch checked
out in another worktree, and built a gate around preventing that. **That claim is
false.** `git-rebase(1)` states plainly: *"Any branches that are checked out in a
worktree are not updated in this way."* Confirmed by direct experiment on git
2.55 — a dependent branch checked out elsewhere kept its old SHA and gained no
reflog entry, while the same branch with no worktree was force-updated. Review
finding 7, independently reproduced.

The truth is less convenient. Two real consequences:

1. **The branches that matter are precisely the ones git will not touch.** Every
   feature branch here lives in a worktree, so `--update-refs` does nothing for
   them, and a dependent branch is left pointing at abandoned commits. This is
   the staleness the flag was supposed to prevent.
2. **The branches git *would* update are ones nobody asked about.** `server` has
   84 local branches and only 15 in worktrees — **69 branches with no worktree**,
   mostly dead, any of which pointing inside a rebase range would be
   force-updated without a word.

So `wt sync` passes `--no-update-refs` and handles every dependent ref
explicitly: worktree-attached dependents are rebased in their own worktrees, in
topological order; worktree-less refs inside the range are **reported**, with an
opt-in to advance them.

### Stacks are in scope, because one already exists

The first draft said no stacks existed. It was wrong — the detection script had a
bug. **`perf_wt/pruning_keyset_index` (3 ahead of trunk) is an ancestor of
`feat_wt/pruning_cron` (6 further commits), and both are checked out.** Review
finding 8, independently reproduced.

Both are class `clean`, so a naive `wt sync` would rebase each onto trunk
independently and split the stack: the three shared commits would exist twice
under different SHAs and `pruning_cron` would no longer have `pruning_keyset_index`
as an ancestor. That is a live hazard today, not a hypothetical.

Therefore, in v1:

- Compute the ancestor relation across all branch-attached worktrees before
  touching anything.
- Rebase parents first; rebase a child onto its **new** parent, not onto trunk.
- If any participant is dirty or has a busy agent, **defer the whole stack.**
  Never half-apply.
- `undo` restores every ref the operation changed, not only the primary.

### Signing

Global git config enables SSH commit signing through the 1Password signer. The
agent launcher injects `commit.gpgSign=false` into agent processes, but a
standalone `wt sync keep` will not necessarily inherit it, and an interactive
signing prompt per rewritten commit would hang the run.

The eligible fleet contains **18 signed commits** — 11 on the Spring Boot branch,
3 on `deployprocess`, 2 on `april-fools`, 2 on `setting_pin`. A signature cannot
survive rewriting either way, so the choice is explicit: **`wt sync` passes
`--no-gpg-sign`**, and reports how many signatures a rebase dropped. Review
finding 9.

### Safety ref, and why it is not a branch

Before every rebase, `refs/wt-sync/<work>/<epoch>` is written at the old tip.
`wt sync undo <work>` resets to the most recent.

The marker is a plain ref, deliberately outside `refs/heads/`: in a repo with
fifteen worktrees the branch namespace is contended, and a branch cannot be
checked out twice. It is also immune to `--update-refs`, which matters if that
flag is ever enabled for the worktree-less case.

**Retention:** safety refs pin abandoned history and block garbage collection, so
they are not kept forever. Keep the newest per work item plus anything younger
than 30 days; `wt sync doctor` reports what would be dropped and drops it on
request. Review finding 20.

### rerere needs one more flag than it looks

`rerere.enabled` is `true` in `server` with **97 cached resolutions**; it is unset
in `accessmanager`, `personal-v` and `infrastructure`, whose caches are empty. The
cache lives in the *common* git dir, so all fifteen `server` worktrees share it: a
conflict resolved carefully once resolves itself in the other fourteen.

But `rerere.autoupdate` is unset, so rerere restores the content and leaves the
index unmerged — the rebase still stops. `wt sync` passes `--rerere-autoupdate`
and still validates the staged result before continuing. Review finding 16.

`wt sync doctor` enables `rerere.enabled` where missing. The skill states the
consequence: resolve it well once, the fleet inherits it.

### Lock

`.git/worktrees/<name>/wt-sync.lock`, created `O_EXCL` holding pid, start time and
owner. Thirty-minute expiry, released on exit. The watcher respects it.

### Hooks, submodules, LFS

Neither repo has an active `pre-rebase` or `post-rewrite` hook today, and neither
has submodules or tracked LFS paths. Both have an active `commit-msg` hook, so the
deferred regeneration commit needs a conforming message. `wt sync doctor`
preflights all of these, because any of them appearing later changes what an
unattended rebase does. Review findings 21 and 22.

## 5. The protocol

Generated by `wt`, relayed verbatim, never composed by hand. One free field, 60
characters, **rejected rather than truncated**. Prefix `wt:`.

```
→  wt: rebase state_stats on development? +90 commits, 4 conflicts. lands: pins, statepush, api
←  wt: ack state_stats
←  wt: nak state_stats - mid test run
←  wt: wait state_stats - 20m
```

After the fact:

```
wt: state_stats rebased on development (+90). yours to check: SyncWorker.java, CommonPersistence.java
wt: state_stats needs you. 4 left after resolvers: SyncWorker.java +3 · wt sync resume state_stats
```

### Where `lands:` comes from

`git log --first-parent <base>..origin/<trunk>` is the landing list. The first
draft claimed trunk was 60 merge commits out of 60; **it is 40 merges and 20
direct commits** — the check behind that claim asked for the last 60 *merges*
rather than the merges among the last 60. Review finding 12, and the correction
matters:

- A **direct** commit carries a conventional subject, so its scope is read
  straight off it.
- A **merge** commit's subject is `Merge pull request #N from Telcred/<branch>`,
  which has no scope. Its scopes come from `git log --format=%s <sha>^1..<sha>^2`
  — the merged range's own commits.

Both are local, both are cheap, and no PR body is fetched. The field is the top
scopes by count: `feat(pins)×6, fix(statepush)×4, chore(api)×2` becomes
`pins, statepush, api`. Computed, never written. A model reads the real log only
on `wt sync explain <work>`.

The "no special case for direct-to-trunk commits" claim survives, but inverted:
direct commits are the easy ones, and it is merge commits that need the extra
step.

### Reply discipline

Say yes unless a build or test is running against that tree right now. Wanting to
rebase later, or having started first, is not a reason. Neither side rewords a
message.

## 6. The plan file

`wt sync` writes `.git/worktrees/<name>/wt-sync-plan.md` before handing a
contested rebase over.

```markdown
# rebase state_stats onto development
90 landed. scopes: pins ×6, statepush ×4, api ×2

## already resolved — do not re-open
application.yaml              bin/conflict/openapi-version -> 2.38.6
etc/openapi/apidocs/*.json    bin/conflict/openapi-spec (structural)
2 of 6 conflicts came from rr-cache (first seen in feat_wt/webkey)

## yours — 4 files
SyncWorker.java               same hunk both sides
    trunk: improvement(statepush): count stream endings by reason
CommonPersistence.java        additive only (trunk +12, ours +3)
    trunk: feat(pins): move pin quality into its own module

## never hand-merge here
etc/openapi/apidocs/*.json  ->  bin/conflict/openapi-spec
pnpm-lock.yaml              ->  the deferred install owns it

## deferred, runs when the rebase completes
./gradlew webapp:generateOpenApi        (1–2 min)

## verify — narrow, not the suite
git diff --exit-code etc/openapi/apidocs
```

Each section removes a specific expense:

- **`scopes:`** — no `git log` to read.
- **"do not re-open"** — the largest saving. Without it, an agent seeing a
  regenerated 950 KB `openapi_v3.json` staged in the rebase will try to review it.
- **Per-file trunk-side subject** — `git log --oneline <base>..<trunk> -- <file>`,
  free. Answers "why did trunk change this" without opening a PR or a diff.
- **`additive only`** — computed from the conflict hunks; marks the five-second
  reads.
- **Narrow verify** — replaces "typecheck, then tests, then format".

## 7. Surfaces

`wt sync` **shows**. Changing anything takes a verb. The split follows
`devports`, where the bare command lists and `kill` acts.

| looking | |
|---|---|
| `wt sync` | the table: every worktree, its class, behind/ahead, who is in it, and what `run` would do |
| `wt sync --all` | the same across every configured repo under `$WT_ROOTS` |
| `wt sync <work>` | one worktree, in full |
| `wt sync explain <work>` | the first-parent log of what landed, for when the scopes are not enough |
| `wt sync doctor` | rerere, resolvers, config validity, hooks, submodules, LFS, safety-ref retention |

| acting | |
|---|---|
| `wt sync watch` | the live table: mark rows, act on what you marked |
| `wt sync run <work>...` | rebase the named worktrees |
| `wt sync run --pick` | fzf multi-select, for a pipeline or a quick one-off |
| `wt sync campaign <work>` | open a workstream on a `divergent` branch |
| `wt sync run --safe` | every worktree whose class needs no conversation |
| `wt sync resume <work>` | continue a contested rebase after resolution |
| `wt sync undo <work>` | reset every ref the last run changed |
| `wt sync keep` | the background keeper: fetch on an interval, run the safe classes, queue asks |
| `wt sync queue` | pending asks, as lines to relay |

Every acting form prints what it is about to touch and, for more than one
worktree, asks once before starting. `--yes` skips that; `wt sync keep` implies
it, which is why `keep` acts only on classes that need no conversation.

### Selecting what to run

`wt sync watch` is the primary way to choose, and it is `devports watch` in a
different domain: a live table that re-fetches on an interval, marks rows with
space, and acts on the marked set. `devports/internal/tui` is 306 lines of Bubble
Tea doing exactly this, so it is a port rather than an invention, and `wt` takes
the same two dependencies.

It has to be a real TUI rather than fzf for the reason `devports` already hit:
**fzf 0.74 has no timer event**, so a self-refreshing table there means running
it with `--listen` and poking it from a background process. Bubble Tea just
ticks — and the table wants to tick, because trunk moves and an agent's state
changes while you are looking at it.

`run --pick` keeps fzf for the one-shot case: piping the table through
`fzf --multi` when you already know what you want, or when something else is
driving. It is the lighter path, not the main one.

A `divergent` row is shown in the table but cannot be marked for `run`. Marking
it offers `campaign` instead.

### Stale branches are a lifecycle question, not an inference

Six branches are ahead 0, up to 718 behind. Ahead 0 proves the tip is reachable
from trunk and that no committed branch-only work would be lost. It does **not**
prove the checkout is unused, and `wt remove` may delete ignored provisioned
files — the copied `override.properties`, decrypted secrets and `.env.local`
files that let a worktree work without an AWS session.

So `stale` is reported with two offers rather than one inference: fast-forward it
to trunk (safe, keeps the provisioned checkout) or remove it. Neither happens
automatically. Review finding 19.

### The keeper spends nothing

`wt sync keep` is a pure binary. It fetches, triages, and rebases the classes that need
no conversation, and **queues** anything needing an ask. It never invokes a model,
because it cannot: `SendMessage` is an agent tool and a binary cannot call it. A
model drains the queue later, when one is present.

## 8. Skills

### `wt-sync`, new, in `anders-lindstrom/skills`

At `skills/wt-sync/SKILL.md`, unprefixed and standalone, per that repo's rule:
promote to a `skills/wt/` domain only when a second skill joins, since renaming
changes the slash command. Added to `skills.sh.json` in a grouping alongside
`dev-ports` — the same shape of problem.

**`dev-ports` is currently absent from `skills.sh.json`**, violating that repo's
rule 6. Fix it in the same change.

The skill covers driving `wt sync`, the four reply shapes, and the resolution
discipline. It does **not** restate the resolvers — those are executable data no
model reads, and prose describing them would be a second copy of a fact
`.wt-sync.yaml` owns.

Its description must be sharp enough to win against `resolving-merge-conflicts`
(mattpocock, upstream, unmodifiable), whose steps are the two expensive things
this design removes — deep investigation of each conflict's origin, and running
the full check suite. The skill says plainly: in a wt-managed worktree this
replaces that one; the plan file already answers those questions.

### `api-bump`, amended, in `Telcred/telcred-skills`

One new section: what a rebase conflict on API-version paths means, that
`bin/conflict/` owns the mechanics, and — the important part — that **a rebase
never moves a client off a snapshot**, so step 10 remains a deliberate act.

## Ownership map

| layer | owns | lives |
|---|---|---|
| `bin/conflict/*` | resolving one conflict shape | each repo, committed, read from trunk |
| `.wt-sync.yaml` | which resolvers, what to defer, how to verify | each repo, committed, read from trunk |
| `wt sync` | triage, rebase mechanics, refs, locks, protocol | `wt` |
| `wt-sync-plan.md` | *this* rebase | generated, `.git/worktrees/<n>/` |
| `wt-sync` skill | driving the tool and the reply discipline | `anders-lindstrom/skills` |
| `api-bump` | the four-repo loop, snapshots, step 10 | `Telcred/telcred-skills` |

## Cost

| situation | tokens |
|---|---|
| `wt sync keep`; trunk moves; the mechanical classes | **0** |
| `wt sync` on a normal day | **0** — it only prints, and no model reads it |
| a rebase needing an ask | one line out, one line back |
| a contested rebase | only the residual files; resolvers already gone |

Seven of the thirteen worktrees that currently need a rebase are predicted to
complete with no model involvement.

## Out of scope

- **Moving tags.** Detected and reported only; `--update-refs` does not move them
  and neither does `wt sync`.
- **Automatically rebasing or removing `stale` branches.**
- **Touching a dirty worktree**, including stashing it.
- **Merge resolution by reasoning inside `wt`.** A resolver owns a shape
  completely or refuses.
- **Moving a client off a snapshot.** Reported, never performed.
- **Finishing a campaign.** `wt sync` sets one up and stages the rebase; the
  work of adapting a branch to a framework it was not written against is a
  workstream, not a tool feature.

## Known trade-offs accepted

**Intermediate commits carry a structurally-merged spec, not a generator-produced
one.** `test_pr.yaml` runs the tests and its `git diff --exit-code` guard at the
PR tip, so a final regeneration commit satisfies CI. But a bisect that runs the
same guard, or per-commit CI if it is ever introduced, can report a false failure
on an intermediate commit. This is accepted deliberately: the alternative is one
to two minutes of Gradle at every conflicting commit. Review finding 17.

**`bin/` can collide with build output.** `wt`'s own `Makefile` builds to `bin/wt`
and its `.gitignore` had `bin/`, which is why `wt` could not read a config from
`bin/worktree/`. Any repo adopting `bin/conflict/` needs the same check. Fixed
for `wt` in this branch.

## Verification plan

- Triage output for all 24 worktrees matches the tables here, including the
  detached one.
- `bin/conflict/openapi-spec` resolves `sync_skipped`, `webkey`, `state_stats` and
  `axis_acc` — tags included — and **refuses** `spring-boot-4-jackson-3`.
- `bin/conflict/spec-client-version` **keeps** `2.36.0-snapshot.20260831123245` on
  `extract_webaccess` and reports the snapshot rather than moving to `2.37.1`.
- `bump_openapi.sh` and `bump_axios_client.sh` behave identically after delegating
  their marker handling.
- Rebasing `perf_wt/pruning_keyset_index` and `feat_wt/pruning_cron` leaves the
  stack intact, with the parent still an ancestor of the child.
- A rebase over the 11 signed commits on the Spring Boot branch completes without
  a signing prompt and reports the dropped signatures.
- `wt sync undo` restores every ref the operation changed.
- `wt sync` with no verb changes nothing, in any repo, in any class.
- `feat_wt/spring-boot-4-jackson-3` classifies `divergent`, and `wt sync run`
  refuses it while `wt sync campaign` produces a brief naming the 49 both-moved
  files and the nine build files.
- `feat_wt/state_stats` classifies `contested`, not `divergent`, on the same
  thresholds.
- A staged campaign across the 284-commit gap reaches trunk, and rerere's cache
  grows between stages rather than the same conflict being resolved twice.
- `wt sync run --all` in a repo with no `.wt-sync.yaml` reports and changes nothing.
- A `.wt-sync.yaml` modified on a feature branch has no effect on that branch's
  own rebase.
- `wt sync keep` over a simulated trunk move touches nothing dirty, messages
  nobody, and queues the asks it should.

## Open questions

1. Whether `wt sync` should refuse outright when `.wt-sync.yaml` names a missing
   resolver, or degrade that worktree to `contested`. Leaning refuse: a silently
   absent resolver looks exactly like a hard conflict.
2. Whether the deferred `pnpm install` should use `--frozen-lockfile` as
   `worktree.conf` does. It cannot, when the lockfile is what is being
   reconciled — so the deferred install and the provisioning install are not the
   same command, and the difference should be stated somewhere better than here.
