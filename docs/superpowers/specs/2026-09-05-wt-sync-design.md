# wt sync — keeping worktrees on trunk

Design, 2026-09-05.

## The problem

Twenty-three live worktrees across `server` and `accessmanager`, several of them
hundreds of commits behind trunk. Bringing one up to date is mechanical almost
every time, but the mechanical part is invisible until someone looks, and
looking costs a model. The branches that are genuinely hard to rebase are a
small minority, and they are hidden among the easy ones.

Meanwhile several of those worktrees have an agent working in them. A rebase
rewrites that agent's branch under it, so it cannot simply happen.

`wt sync` exists to make the easy majority free and the hard minority visible,
and to knock before rewriting somebody's history.

## What the fleet actually looks like

Measured 2026-09-05 across all 23 worktrees of `server` (trunk `development`)
and `accessmanager` (trunk `main`), with `git merge-tree --write-tree`, which
performs the whole merge in the object store without touching a working tree.

| class | count | meaning |
|---|---|---|
| current | 4 | behind 0 |
| stale | 6 | behind 24–718, **ahead 0** — nothing of their own |
| clean | 4 | no conflict |
| recipe-only | 2 | conflicts are entirely in paths a script owns |
| contested | 7 | some real source overlap |

After the resolvers in this design, the recipe paths drop out of the contested
set too:

| branch | conflicts | left for a person |
|---|---|---|
| `feat_wt/sync_skipped` | 3 | **0** |
| `feat_wt/webkey` | 5 | **0** |
| `feat_wt/extract_webaccess` | 6 | **0** |
| `feat_wt/deployprocess` | 1 | 1 (`AGENTS.md`, additive both sides) |
| `feat_wt/axis_acc` | 7 | 3 |
| `feat_wt/state_stats` | 7 | 4 |
| `feat_wt/arch` | 4 | 4 |
| `spring-boot-4-jackson-3` | 7 | 7 — the spec resolver refuses here (see §2) |
| `april-fools` | 5 | 5 (756 behind, likely dead) |

**Seventeen of twenty-three are fully mechanical.** That ratio is the design.

## Design principles

1. **Negotiation is the exception.** Same principle as `devports`, but computed
   rather than declared: the class is derived from git, not from a config field
   someone has to keep honest.
2. **No component ever merges content by reasoning.** A resolver either owns a
   file shape completely or refuses. There is no "usually right" strategy.
3. **Resolvers are fast and never regenerate.** Regeneration is deferred and
   runs once per rebase, not once per conflicting commit.
4. **A model is involved only where judgement is genuinely required**, and when
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

`claude agents --json` lists interactive sessions as well as background ones,
each with `cwd`, `name`, `sessionId`, `pid` and idle/busy. There is no need to
read process environments the way `devports` does, and therefore no
unknown-versus-gone ambiguity to get wrong.

| class | condition | action |
|---|---|---|
| `current` | behind 0 | nothing; not printed |
| `stale` | ahead 0 | **never rebased.** Reported once: nothing of its own, N behind, consider `wt remove` |
| `clean` | merge-tree exits 0 | rebase |
| `recipe` | every conflicting path is claimed by a resolver | rebase; resolvers handle it |
| `contested` | some conflicting path is claimed by none | rebase; resolvers strip the noise; hand the residue over |

Two modifiers override the class:

- **dirty → never touched.** Not rebased, not stashed. The stash stack is shared
  across every worktree of a repo, so a tool that stashes is a tool that can
  eat another session's work.
- **agent busy → deferred silently.** No message, no rebase.

`merge-tree` tests a *merge*; a rebase replays each commit and can conflict at
an intermediate step the endpoint does not. Triage is a screen, not a promise.
A `clean` prediction that conflicts in reality aborts, restores from the safety
ref, and reclassifies as `contested`. It never leaves a worktree mid-rebase
without a plan on disk.

## 2. Conflict resolvers

A resolver is a script in the repo that owns exactly one conflict shape. It
lives in `bin/conflict/`, matching the existing `bin/github/`, `bin/sqs/` and
`bin/worktree/` convention.

### Contract

```
bin/conflict/<name> --check   <file>    exit 0 = this file is mine
                                        exit 1 = not mine
bin/conflict/<name> --resolve <file>    resolve in place and `git add` it
                                        exit 0 = resolved
                                        exit 2 = refuse; one line of reason on stderr
```

`--check` must be cheap and must not write. `--resolve` must be deterministic
and must never invoke a build. A resolver that cannot resolve **refuses**; it
never guesses. `wt sync` needs no per-script knowledge beyond the list.

Resolvers are useful on their own: a person hitting the conflict by hand runs
the same script.

### `server`

| script | shape | today | after |
|---|---|---|---|
| `bin/conflict/openapi-version` | `application.yaml`, inside the `openapi:` block | ~60 lines of awk embedded in `bump_openapi.sh` | extracted, milliseconds |
| `bin/conflict/openapi-spec` | `etc/openapi/apidocs/*.json` | `./gradlew webapp:generateOpenApi` per hit | structural 3-way, milliseconds |
| `bin/conflict/gradle-includes` | `settings.gradle` `include` list | by hand | union of the inline list |

### `accessmanager`

| script | shape | today | after |
|---|---|---|---|
| `bin/conflict/spec-client-version` | `**/package.json`, the `@telcred/spec-telcredv2-typescript-axios` pin | `pnpm i` per hit | version rule only, milliseconds |
| `bin/conflict/pnpm-lock` | `pnpm-lock.yaml` | `pnpm i` per hit | mark for the one deferred install |

### Why extraction is worth doing regardless of `wt sync`

`bump_openapi.sh` already parses conflict markers in the `openapi:` block and
collapses them. `bump_axios_client.sh` already refuses on leftover markers, runs
`pnpm i` to reconcile the lock, and prints the `git add … && git rebase
--continue`. Both were written for this case; the capability exists but is
locked inside a larger script that also does network calls and builds.

After extraction the bump scripts **call the resolvers**. One owner per fact,
in code rather than prose, and both scripts get shorter.

### `openapi-spec`: how it resolves without Gradle

The generated spec is a 950 KB JSON document, and it is where the cost is. The
resolver performs a three-way merge over **top-level keys** — each entry under
`paths` and under `components.schemas`, plus `info.version` — taking whichever
side changed a key, and resolving `info.version` by the version rule below.

Measured over the five conflicting server branches, comparing base→ours and
base→trunk key by key:

| branch | ours changed | trunk changed | keys both touched |
|---|---|---|---|
| `feat_wt/webkey` | 28 | 22 | **1** — `info.version` |
| `feat_wt/state_stats` | 22 | 22 | **1** — `info.version` |
| `feat_wt/sync_skipped` | 2 | 7 | **1** — `info.version` |
| `axis_acc` | 15 | 35 | **1** — `info.version` |
| `spring-boot-4-jackson-3` | 124 | 238 | 28 real keys |

Four of five collide on nothing but the version, which is decided by rule. The
fifth is a Spring Boot 4 / Jackson 3 upgrade that rewrites the generator's own
output shape; the resolver **detects the real key overlap and refuses**, which
is the signal you want rather than a case to force.

**Honest limit.** springdoc emits `paths` and `components.schemas` in
registration order, not sorted, so a structural merge cannot guarantee
byte-identical output to what Gradle would produce. The resolver's job is to
unblock the rebase, not to produce the final artifact. The authoritative bytes
come from the deferred regeneration (§4), and CI's own `git diff --exit-code`
over `etc/openapi/apidocs` remains the backstop.

### Version rules

Two, both deterministic, both checked against live cases.

**`max-plus-patch`** (server, `openapi.<api>.version`). Take the higher of ours
and trunk's; if that is trunk's, bump one patch, because the branch still needs
a version of its own. Branch claimed 2.38.3, trunk moved to 2.38.5 → branch
takes 2.38.6.

**`prefer-release`** (accessmanager, the spec client pin). If ours is a
snapshot and trunk's is a release whose version is greater than or equal to the
snapshot's base version, the server change has landed: take trunk's. Otherwise
keep the snapshot. Live case: ours `2.36.0-snapshot.20260831123245`, trunk
`2.37.1` ≥ 2.36.0 → take `2.37.1`.

That second rule performs step 10 of the `api-bump` loop — moving a client off
a snapshot — automatically, as a side effect of rebasing. It is the step the
skill documents as the one everybody forgets.

## 3. `.wt-sync.yaml`

Committed at the top of each repo, like `.dev-ports.yaml`: the quirk is a fact
about the repo, not a personal preference. Resolvers are self-describing via
`--check`, so the file is a thin map.

```yaml
resolvers:
  - bin/conflict/openapi-version
  - bin/conflict/openapi-spec
  - bin/conflict/gradle-includes

defer:
  - run: ./gradlew webapp:generateOpenApi
    when: openapi-spec

verify:
  - git diff --exit-code etc/openapi/apidocs
```

`defer` is what makes a long rebase affordable. Measured cost today, per rebase:

| branch | commits | Gradle regenerates | `pnpm i` runs |
|---|---|---|---|
| `axis_acc` | 86 | **4** | — |
| `feat_wt/webkey` | 27 | **2** | — |
| `feat_wt/state_stats` | 12 | 1 | — |
| `feat_wt/extract_webaccess` | 23 | — | **5** |

With deferral each becomes exactly one, run after the last commit is replayed,
and committed as a final regeneration commit.

`verify` is deliberately **not** `worktree.conf`'s `TEST_COMMAND` — that is
`./gradlew test` and `pnpm test`, both far too slow to run per rebase. The
`git diff --exit-code etc/openapi/apidocs` line is lifted from `test_pr.yaml`;
it is CI's own guard and it catches the real failure mode — a spec that was
never regenerated — in about two seconds.

A repo with no `.wt-sync.yaml` still triages and still rebases. It simply has no
resolvers, so more lands in `contested`.

## 4. Rebase execution

### Command

```
git -C <worktree> rebase --update-refs origin/<trunk>
```

### Safety ref, and why it is not a branch

Before every rebase, `refs/wt-sync/<work>/<epoch>` is written at the old tip.
`wt sync --undo <work>` resets to the most recent one. They are never pruned
automatically.

The marker is a plain ref, deliberately outside `refs/heads/`. Two reasons:

1. In a repo with fifteen worktrees the branch namespace is contended, and a
   branch cannot be checked out in two worktrees.
2. `--update-refs` advances refs under `refs/heads/` that point inside the
   rebased range. `rebase-latest-commit` uses `safe/pre-rebase-…` as a branch
   and must therefore pass `--no-update-refs` to protect it. Keeping the marker
   out of `refs/heads/` is exactly what lets `wt sync` use `--update-refs`.

`rebase.updateRefs` is unset at system, global and local scope on this machine
(git's default is `false`), so the flag is passed explicitly rather than relied
upon.

### Dependent refs are participants, not bystanders

`--update-refs` will move a branch **checked out in another worktree with a live
agent in it**, silently. That is the exact failure this tool exists to prevent,
so dependent refs get the same treatment as the primary:

- Before touching anything, compute every ref under `refs/heads/` that falls
  inside the rebase range.
- Triage each: dirty? live agent? busy?
- If any participant is dirty or has a busy agent, **defer the whole rebase.**
  Never half-apply, leaving a stack split across two bases.
- Order topologically: parents before children, and rebase a child onto its
  **new** parent, not onto trunk. Rebasing a child directly onto trunk replays
  the parent's commits a second time.

No stacks exist in `server` today — every worktree branch pair was checked — so
this is prevention rather than a current fix.

**Behaviour to verify during implementation:** what git actually does when
`--update-refs` would advance a branch that is checked out in another worktree.
It may refuse, skip, or update. The gate above must hold whichever it is.

### Tags are detected, never moved

`--update-refs` does not move tags, and it should not: a tag is a fixed label,
and silently re-pointing it rewrites what history claims. `server` has 110 tags
and none currently falls inside a feature-branch range. When one does, `wt sync`
reports it — *"v2.38.1 points inside this range and will refer to abandoned
commits after the rebase"* — and leaves the decision to a person.

### rerere is the compounding lever

`rerere.enabled` is `true` in `server` only, with **97 cached resolutions**. It
is unset in `accessmanager`, `personal-v` and `infrastructure`, whose caches are
empty. The cache lives in the *common* git dir, so all fifteen `server`
worktrees share it: a conflict resolved carefully once resolves itself in the
other fourteen.

`wt sync doctor` enables it where missing. The skill states the consequence:
resolve it well once, the fleet inherits it. This converts the expensive class
from per-worktree to per-conflict-shape.

Note the contrast with `rebase-latest-commit`, which passes
`-c rerere.enabled=false` for its interactive autosquash rebase. Different job,
opposite need.

### Lock

`.git/worktrees/<name>/wt-sync.lock`, created `O_EXCL` holding pid, start time
and owner. Thirty-minute expiry, released on exit. The watcher respects it. Two
`wt sync` runs cannot collide on one worktree.

## 5. The protocol

Generated by `wt`, relayed verbatim by an agent, never composed by hand. One
free field, 60 characters, **rejected rather than truncated**. Prefix `wt:`.

```
→  wt: rebase state_stats on development? +90 commits, 4 conflicts. lands: pins, statepush, api
←  wt: ack state_stats
←  wt: nak state_stats - mid test run
←  wt: wait state_stats - 20m
```

After the fact:

```
wt: state_stats rebased on development (+90). yours to check: SyncWorker.java, CommonPersistence.java
wt: state_stats needs you. 4 left after resolvers: SyncWorker.java +3 · wt sync state_stats --resume
```

**Where `lands:` comes from, without spending a token.** Trunk is composed
entirely of merge commits — 60 of the last 60 in `server` — so
`git log --first-parent <base>..origin/<trunk>` is the landing list, and it
works identically when someone commits straight to trunk with no PR at all.
There is no no-PR special case. The field is the top conventional-commit
*scopes* by count: `feat(pins)×6, fix(statepush)×4, chore(api)×2` becomes
`pins, statepush, api`. Computed, never written. A model reads the actual log
only on `wt sync --explain <work>`, and a PR body only when that is not enough.

**Reply discipline.** Say yes unless a build or test is running against that
tree right now. Wanting to rebase later, or having started first, is not a
reason. Same rule as `dev-ports`, same reasoning.

Neither side rewords a message. Both ends are generated, which is what stops
the format drifting and makes brevity structural rather than a rule someone has
to remember.

## 6. The plan file

`wt sync` writes `.git/worktrees/<name>/wt-sync-plan.md` before handing a
contested rebase over. Every section removes a specific expense.

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
./gradlew webapp:generateOpenApi

## verify — narrow, not the suite
git diff --exit-code etc/openapi/apidocs
```

- **`scopes:`** — no `git log` to read.
- **"do not re-open"** — the largest saving. Without it, an agent seeing a
  regenerated 950 KB `openapi_v3.json` staged in the rebase will try to review
  it.
- **Per-file trunk-side subject** — `git log --oneline <base>..<trunk> -- <file>`,
  free. This answers "why did trunk change this" without opening a PR or a diff.
- **`additive only`** — computed from the conflict hunks; marks the files that
  are a five-second read.
- **Narrow verify** — replaces "typecheck, then tests, then format".

## 7. Surfaces

| | |
|---|---|
| `wt sync` | triage this repo; act on the safe classes, print the rest |
| `wt sync <work>` | one worktree |
| `wt sync --pick` | fzf over the triage table; act on the marked rows |
| `wt sync --all` | every repo under `$WT_ROOTS` |
| `wt sync --dry-run` | the table, change nothing |
| `wt sync --watch` | the daemon: fetch on an interval, rebase the safe classes, queue asks |
| `wt sync --queue` | pending asks, as lines to relay |
| `wt sync --resume <work>` | continue a contested rebase after resolution |
| `wt sync --undo <work>` | reset to the safety ref |
| `wt sync --explain <work>` | the full first-parent log of what landed |
| `wt sync doctor` | rerere, resolver presence, `.wt-sync.yaml` validity |

### Watch mode spends nothing

`--watch` is a pure binary. It fetches, triages, and rebases the classes that
need no conversation — seventeen of twenty-three — and **queues** anything
needing an ask. It never invokes a model, because it cannot: `SendMessage` is an
agent tool and a binary cannot call it. A model drains the queue later, when one
is present. Overnight the mechanical work still happens; only the conversations
wait.

## 8. Skills

### `wt-sync`, new, in `anders-lindstrom/skills`

At `skills/wt-sync/SKILL.md`, unprefixed and standalone, per that repo's own
rule: promote to a `skills/wt/` domain only when a second skill joins, since
renaming changes the slash command. Added to `skills.sh.json` in a grouping
alongside `dev-ports` — the same shape of problem, agents contending over a
shared thing and negotiating in generated messages.

**Noticed in passing: `dev-ports` is absent from `skills.sh.json`**, which
violates that repo's rule 6. Fix it in the same change.

The skill covers driving `wt sync`, the four reply shapes, and the resolution
discipline. It does **not** restate the resolvers — those are executable data
that no model reads, and prose describing them would be a second copy of a fact
`.wt-sync.yaml` owns.

Its description must be sharp enough to win against `resolving-merge-conflicts`
(mattpocock, upstream, unmodifiable), whose steps are the two expensive things
this design removes:

> 2. Find the primary sources for each conflict. Understand deeply why each
>    change was made … read the commit messages, check the PRs, check original
>    issues/tickets.
> 4. Discover the project's automated checks and run them, typically typecheck,
>    then tests, then format.

The skill states plainly: in a wt-managed worktree this replaces that one — do
not run the deep-investigation steps, the plan file already answers them.

### `api-bump`, amended, in `Telcred/telcred-skills`

One new section: what to do when a rebase conflicts on API-version paths, and a
pointer to `bin/conflict/`. A pointer, not a copy — `api-bump` keeps ownership
of the four-repo loop, snapshot-versus-release semantics and step 10.

## Ownership map

| layer | owns | lives |
|---|---|---|
| `bin/conflict/*` | resolving one conflict shape | each repo, committed |
| `.wt-sync.yaml` | which resolvers, what to defer, how to verify | each repo, committed |
| `wt sync` | triage, rebase mechanics, refs, locks, protocol | `wt` |
| `wt-sync-plan.md` | *this* rebase | generated, `.git/worktrees/<n>/` |
| `wt-sync` skill | driving the tool and the reply discipline | `anders-lindstrom/skills` |
| `api-bump` | the four-repo loop, snapshots, step 10 | `Telcred/telcred-skills` |

## Cost

| situation | tokens |
|---|---|
| watch mode; trunk moves; 17 of 23 mechanical | **0** |
| `wt sync` on a normal day | **0** — the tool prints, no model reads |
| a rebase needing an ask | one line out, one line back |
| a contested rebase | only the residual files; resolvers already gone |

## Out of scope

- **Automatic ordered multi-branch stack rebasing.** v1 detects dependent refs
  and refuses to proceed when a participant is unsafe. Full ordered stack
  rebasing follows once stacks actually exist locally. `gh-stack` and
  `ops-stacked-prs` own the PR-level story either way.
- **Moving tags.** Detected and reported only.
- **Rebasing `stale` branches.** Resetting an ahead-0 branch onto trunk is
  meaningless; six are in that state and are probably dead. Reported with a
  `wt remove` suggestion.
- **Touching a dirty worktree**, including stashing it.
- **Merge-conflict resolution by reasoning inside `wt`.** A resolver owns a
  shape completely or refuses.

## Verification plan

- Triage output for all 23 worktrees matches the table in this document.
- `bin/conflict/openapi-spec` resolves `sync_skipped`, `webkey`, `state_stats`
  and `axis_acc`, and **refuses** `spring-boot-4-jackson-3`.
- `bin/conflict/spec-client-version` picks `2.37.1` for `extract_webaccess`.
- `bump_openapi.sh` and `bump_axios_client.sh` behave identically after
  delegating to the extracted resolvers.
- A rebase with `--update-refs` and a dependent branch checked out in another
  worktree does the documented thing; if git's behaviour differs, the gate
  changes, not the guarantee.
- `wt sync --undo` restores the pre-rebase tip exactly.
- `wt sync --watch` over a simulated trunk move touches nothing dirty, messages
  nobody, and queues the asks it should.

## Open questions

1. How long `./gradlew webapp:generateOpenApi` actually takes. It sets the value
   of deferral, which is assumed large and has not been measured.
2. Whether `wt` should refuse to run at all in a repo whose `.wt-sync.yaml`
   names a resolver that is missing, or degrade to `contested`. Leaning refuse,
   since a silently-absent resolver looks like a hard conflict.
