# wt sync — keeping worktrees on trunk

Design, 2026-09-05. Revised the same day after an adversarial review against the
live repositories; the findings that changed it are noted inline as "review
finding N". Revised again 2026-09-08 after the resolver plan was executed and
every rebase in the fleet was simulated commit by commit; those changes are
marked "execution finding".

## What changed on 2026-09-09, in one place

- **The merge logic moves into `wt`.** The resolvers built on 2026-09-08 put a
  three-way OpenAPI merge, an owned-line rule and a list union into two
  application repositories, each with its own copy of `lib.sh` and 71 tests of
  logic that has nothing to do with access control. That is the drift `wt` was
  written to end, and it contradicts the tool's own first line: one
  implementation, per-repo configuration. Strategies are built into `wt`; a
  repository **declares** its conflict shapes in `.wt-sync.yaml` and owns no
  code (§2, §3). The executable contract survives only as the `script` escape
  hatch. The bash implementation stays on the draft PRs `Telcred/server#584`
  and `Telcred/accessmanager#224` as the reference and oracle for the Go port.

## What changed on 2026-09-08, in one place

- **Triage is a promise, not a screen.** Every rebase is simulated commit by
  commit in the object store before anything is touched (§1). The `clean` class
  is exact; the others carry the exact first stop.
- **Resolvers exist and are verified against the live fleet**, on branches
  `feat_wt/conflict-resolvers` in `server` and `accessmanager` (§2). Two of the
  plan's rules were wrong on live data and are corrected below.
- **The bump scripts do not delegate to the resolvers.** Dropped (§2).
- **`.wt-sync.yaml` has no mini-DSL and no `verify:` list** (§3).
- **The deferred spec regeneration needs Docker** and doubles as the compile
  check (§3).
- **The file-count signal is not a `divergent` classifier** (§1).
- **The stack the first draft found no longer exists**; the fleet changed in
  three days, so nothing in the tool or its tests may encode fleet facts (§4).
- **`wt sync` stays in `wt`**, for a concrete reason (Ownership map).

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
| `feat_wt/state_stats` | 7 | **5** — the yaml also conflicts on a whole `controller-statistics:` block, which `openapi-version` correctly refuses (execution finding; the first draft said 4) |
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
  another session's work. Dirty means **tracked** changes
  (`git status --porcelain --untracked-files=no`): untracked files do not block a
  rebase, and where one collides with an incoming file git refuses on its own,
  which aborts and restores like any other failure. Execution finding; today all
  22 worktrees are clean either way.
- **agent busy → deferred silently.** No message, no rebase.

### Triage is a promise: the rebase is simulated first

`merge-tree` on the two tips tests a *merge*; a rebase replays each commit and
can conflict at an intermediate step the endpoint does not. The first draft
accepted that as "a screen, not a promise". Execution finding: the replay can be
simulated exactly, in the object store, at a few milliseconds per commit.

```
onto = origin/<trunk>
for c in git rev-list --reverse --right-only --cherry-pick --no-merges origin/<trunk>...<branch>:
    tree = git merge-tree --write-tree --merge-base=c^ onto c     # the cherry-pick
    if it conflicts: stop; this is the first stop, with its exact file list
    onto = git commit-tree tree -p onto                          # chain it
```

`--right-only --cherry-pick --no-merges` is the same commit selection `git
rebase` makes, so the simulation replays what the rebase would. The throwaway
commits are unreachable and garbage-collected. Consequences:

- **`clean` is exact.** A branch whose every commit replays without conflict is
  rebased with no model and no surprise. Measured 2026-09-08: `controller_stats`
  (3 commits), `setting_pin` (14), `fix_rev_path` (1) replay cleanly end to end.
- **Every other class carries its first stop**: which commit, which files. On
  today's fleet the first stop differs from the endpoint on every contested
  branch (`state_stats` stops at commit 2 of 12 on `SyncWorker.java` alone;
  `webkey` at 6 of 27 on `application.yaml` and the *remote* spec, which the
  endpoint view had not singled out).
- **Resolvers are checked, not merely matched, at triage time.** The three blobs
  at a stop are loaded into a temporary index (`GIT_INDEX_FILE` and
  `git update-index --index-info` with stages 1, 2 and 3) and each claimed file
  is put to the resolver's `--check`. `recipe` therefore means "the resolvers
  *did* resolve the first stop", not "a resolver claims the path". Review
  finding 6 is closed by this rather than by the provisional class it proposed.
- A resolver that refuses at a later stop still reclassifies to `contested`
  mid-rebase, with the plan file on disk. That remains the fallback, not the
  design.

`bin/conflict/test/live-check.sh` in each repo does exactly this from the
command line, against a throwaway repository per file; it is how the resolvers
were verified and is the reference for the Go implementation.

#### What "exact" means, stated as a contract

The simulation replays what `git rebase <onto>` replays under the flags §4
fixes (`--no-update-refs --no-gpg-sign --rerere-autoupdate`, the default
flattening backend): the commits of `rev-list --reverse --topo-order
--right-only --cherry-pick --no-merges`, each cherry-picked onto the previous
result, commits that become empty dropped. Where the two can differ, the
simulation errs towards reporting a stop:

- **rerere** may resolve, at rebase time, a conflict the simulation reports.
  The simulation does not consult the cache, so a `recipe` or `contested`
  prediction can turn out `clean`; never the reverse.
- **Untracked files** colliding with incoming ones make a real rebase refuse
  where the simulation saw nothing; that refusal aborts and restores like any
  other failure (§1, dirty).
- A repository configured for `rebase.rebaseMerges`, `rebase.autoStash` or a
  `pre-rebase` hook is not what the simulation models; `wt sync` passes its
  flags explicitly so user config does not apply, and `doctor` reports the
  hook.
- Every `git status` the tool runs uses `--no-optional-locks`: a plain status
  may rewrite the index, and "read-only" is a promise about the index too.

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

The class is computed from one signal:

| signal | why it means "not mechanical" |
|---|---|
| the **`openapi` strategy refuses at the endpoint** — generated output that no longer merges key by key | the framework rewrote the generator's own output; the branch's API surface and trunk's no longer describe the same program |

Two demotions, both from measuring. The first draft counted *any* strategy's
refusal at the endpoint, which contradicted this document's own table:
`state_stats` is refused on an ordinary configuration block and is meant to
stay `contested`; an owned-line refusal is a normal conflict for a person.
The first draft also counted the branch changing the dependency graph
(`dependency_graph` in `.wt-sync.yaml`); a second draft required both sides
to. Execution finding, 2026-09-09, running the Go triage over the live fleet:
both versions fire on `webkey` and `axis_acc`, because over a hundred trunk
commits always touch some `build.gradle`, while **no branch — not even Spring
Boot — conflicts on a dependency-graph file at the endpoint**. A signal that
fires on every long-lived branch is not a signal. The dependency-graph check
is now an **advisory note** on the row ("both sides changed the dependency
graph: …"), like the file count, so a person sees it; it does not classify.
With that, the live fleet classifies exactly as the tables above say.

Two precisions from the final review of the foundation (2026-09-09). Only a
**key collision** counts — the `openapi` strategy also refuses when the branch
edited the document outside the merged sections, when a section is absent, or
when the version is not bare semver, and those are ordinary refusals that
leave the worktree `contested`. And the endpoint check runs **whether or not
the replay is clean**: a branch whose commits replay one by one can still meet
trunk with a rewritten spec at its tip, and that is the case the class exists
for.

The first draft had a third signal, "both sides moved more than 30 of the same
files", tuned on a sample of two. Execution finding: it is not needed for the
one live case (`spring-boot-4-jackson-3` trips both other signals: `openapi-spec`
refuses on 13 paths and 28 schemas, and nine build files change) and it is the
only signal that can be wrong on its own. It is **shown as a column**, not used
as a classifier, until there is data to tune it on.

Note the refusal signal is measured at the **endpoint**, not at the first stop:
Spring Boot's first stop is four Java files no resolver claims, and the spec
refusal only appears when the whole branch meets trunk. Triage runs the
resolvers' `--check` at both.

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

#### Staged rebase, concretely

The review found this section asserted rather than designed. The proposal, for
the campaign plan rather than for v1: intermediate bases are the trunk
first-parent merge commits whose merged range touched any file in the
both-moved list, capped at five stages spread across the gap; each stage is
`git rebase --onto <stage> <previous stage>` with its own safety ref
(`refs/wt-sync/<work>/<epoch>/stage-N`), so a stage is undone on its own; the
brief is regenerated per stage from the same first-parent log. That is a plan,
not a commitment; v1 ships the `divergent` class, the refusal, and `wt sync
campaign` printing the brief and the proposed stages without running them.

#### It is allowed to be a big piece of work

A campaign is not a `wt sync` operation that happens to take longer. It is a
separate workstream with its own branch, its own budget and possibly its own
plan, and the tool's job is to set it up honestly and then get out of the way.
The alternative — quietly leaving `spring-boot-4-jackson-3` out of the fleet
sweep because it is inconvenient — is how a branch gets to 284 behind.

### Agent detection, and what it does not cover

`claude agents --json` lists interactive sessions as well as background ones,
with `cwd`, `id`, `kind`, `name`, `sessionId`, `startedAt` and `state`. There is
**no `pid` field** and no explicit idle/busy flag. Execution finding: `state` is
`null` for every interactive session and `working`, `blocked` or `done` for
background ones, and **finished background sessions stay in the list** with
`state: done`. A `done` session is nobody; the tool filters it out or it will
knock on empty rooms. It is enough to answer "is a Claude session living in this worktree",
which removes the `devports` process-environment scan and its
unknown-versus-gone ambiguity.

It does **not** see Codex, a dev server, a running test, or an IDE build in an
otherwise clean worktree. `wt sync` therefore treats "no Claude session" as
"nobody to ask", never as "nothing is happening", and the dirty check remains the
real guard. Review finding 10.

## 2. Conflict strategies

A **strategy** is a way of resolving one conflict shape, built into `wt`. A
repository does not own any of them; it **declares** which files have which
shape, with the one or two parameters that vary, in `.wt-sync.yaml`. This is
`wt`'s thesis applied to conflicts: one implementation, per-repo configuration.

The first version of this section had each repository carrying the logic as
scripts in `bin/conflict/`. It was built, tested and verified on the live fleet
(the 2026-09-08 findings below all still hold) and then rejected on 2026-09-09
for the reason stated at the top: `server` should no more own a JSON three-way
merge than it owned its worktree scripts.

### The strategies

| strategy | what it does | parameters |
|---|---|---|
| `owned-line` | one line matching a regex is the only thing both sides may change; a rule decides its value; anything else in the block that only one side changed is kept | `line` (regex), `rule` |
| `openapi` | three-way merge of a generated OpenAPI document over `paths`, `components.schemas` and `tags` by key, `info.version` by rule; refuses naming the keys both sides changed; never regenerates | `rule` (default `max-plus-patch`) |
| `list-union` | one line holding a delimited list: union, trunk's order first, then the branch's additions; refuses a removal | `line` (regex), `delimiter` (default `,`) |
| `take-trunk` | trunk's copy of the file, wholesale; a deferred step reconciles it | — |
| `script` | an executable in the repository answering the contract below | `run` |

Rules for a value: `max-plus-patch` (the higher semver, lifted one patch when
trunk's is higher), `keep-branch`, `keep-trunk`.

Every strategy shares the same guarantees, all execution findings from the bash
implementation and all carried into the Go port as tests:

- A strategy that cannot resolve **refuses**, never guesses, and leaves the file
  exactly as it found it: still three-staged, markers intact. A refusal names
  what collided.
- Paths are repository-root relative in the index (`git show :2:<path>`) while
  `git add` is cwd-relative; the tool works from the root.
- Path patterns are matched, never expanded against the disk.
- **git folds an adjacent edit into the same conflict block.** A version line
  whose neighbouring comment trunk rewrote, or a manifest pin next to a
  dependency trunk bumped, arrives as one block with two lines on one side. A
  text-shape strategy resolves that block when only one side touched the other
  lines, judged against the diff3 base section, and refuses when both did.
  `axis_acc`'s `settings.gradle` and every `extract_webaccess` manifest are that
  shape on the live fleet.
- A strategy can be **checked without a rebase**: the three blobs of a conflict
  are loaded into a temporary index (`GIT_INDEX_FILE`, `git update-index
  --index-info`) and the strategy is asked whether it would resolve. Triage
  uses this (§1). For a `script`, that index is all it sees — one file, three
  stages, mode 100644, no `HEAD` and no other entries — which is the limit of
  what `--check` can promise; a script that reads more than the three stages
  can answer differently mid-rebase.
- A conflict that does not carry three regular blobs — a side deleted or
  renamed the file, a submodule, a symlink — is refused before any strategy
  sees it. It is a person's call.

### The `script` escape hatch

For a shape none of the built-in strategies fits, a repository may point at an
executable of its own:

```
<run> --claims               print the path globs it owns
<run> --check   <file>       0 = I can resolve this conflict; 1 = not mine
<run> --resolve <file>       resolve in place and `git add` it
                             0 = resolved; 2 = refuse, one line of reason on stderr
```

It is read from trunk like everything else in the file (§3): the script's
directory is materialised from `origin/<trunk>` into a temporary directory and
run from there, so a feature branch cannot change what runs. A script is
trusted code from trunk; nothing sandboxes it. No repository needs one today;
the contract exists so that the day one does, it is not a reason to put logic
back into `wt`.

### What each repository declares

`server`:

| path | strategy | parameters |
|---|---|---|
| `accessmanagement/src/main/resources/application.yaml` | `owned-line` | `line: '^\s*version:'`, `rule: max-plus-patch` |
| `etc/openapi/apidocs/*.json` | `openapi` | |
| `settings.gradle` | `list-union` | `line: '^include '` |

`accessmanager`:

| path | strategy | parameters |
|---|---|---|
| `package.json`, `apps/*/package.json`, `packages/*/package.json` | `owned-line` | `line: '"@telcred/spec-telcredv2-typescript-axios":'`, `rule: keep-branch` |
| `pnpm-lock.yaml` | `take-trunk` | |

The `keep-branch` rule on the SDK pin is the `prefer-release` correction from
the first review, unchanged: a higher number on trunk is not evidence that the
branch's server change landed. The tool says so when the kept pin is a
snapshot.

### The bump scripts do not delegate to the strategies

The first draft had `bump_openapi.sh` and `bump_axios_client.sh` call the
resolvers for the marker handling they already do, so that a conflict resolved
by hand and one resolved by the tool came out identical. Execution finding: they
must not. The strategies read the **index stages** and recompute the merge, which
is what `wt sync` needs. The bump scripts read the **working file's markers**,
which is what a person needs: after resolving the real conflict in a file by
hand and leaving only the version block, the resolver would recompute from the
stages, reintroduce the resolved conflict and refuse. The two are different
tools for different moments. The bump scripts stay as they are; the only
overlap is the version rule, and `bump_openapi.sh` lets the person choose.

### The `openapi` strategy: resolving a 950 KB generated file without Gradle

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

Execution finding: the same holds at the **replay stops**, where the branch side
is a mid-branch commit, and for the **remote** spec (`openapi_remote_v3.json`),
which `webkey` conflicts on at commit 6 of 27 and which the first draft never
measured. All three spec files resolve on `sync_skipped`, `webkey`, `state_stats`
and `axis_acc`, and both `openapi_v3*.json` refuse on Spring Boot, naming the
paths. The strategy writes the document the way the generator does — Jackson's
pretty printer: `"key" : value` with a space on both sides of the colon, inline
arrays (`[ "id" ]`, `[ {` … `}, {` … `} ]`), `{ }` and `[ ]` for empties, two-space
indentation, no trailing newline — so the deferred regeneration diffs on key
order alone. (The bash prototype and the first Go draft wrote `encoding/json`
style and would have diffed on every line; verified against the live file on
2026-09-09.)

**Honest limit.** springdoc emits `paths` and `components.schemas` unsorted —
`springdoc.writer-with-order-by-keys` is not enabled — so a structural merge
cannot guarantee byte-identical output to Gradle's. The resolver unblocks the
rebase; it does not produce the final artifact. That comes from the deferred
regeneration (§3).

### Version rules

**`max-plus-patch`** (server, `openapi.<api>.version`). Take the higher of ours
and trunk's; if that is trunk's, bump one patch, because the branch still needs a
version of its own. Branch claimed 2.38.3, trunk moved to 2.38.5 → 2.38.6.

The review asked whether this rule is of the same family as `prefer-release`.
It is not: the conflict only exists when the branch changed the version line,
which it does only when it changed the API, and an API change on a branch needs
a version above everything trunk has published, whatever the number. Patch is
the level `api-bump` and `publish_api_snapshot.sh` offer by default. One known
cost: a branch that bumps at several commits is lifted at each stop it conflicts
at, so the final number can be one or two higher than a person would pick. The
deferred regeneration takes the yaml's final value into the spec.

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
conflicts:
  - paths: [accessmanagement/src/main/resources/application.yaml]
    strategy: owned-line
    line: '^\s*version:'
    rule: max-plus-patch
  - paths: [etc/openapi/apidocs/*.json]
    strategy: openapi
  - paths: [settings.gradle]
    strategy: list-union
    line: '^include '

defer:
  - run: ./gradlew webapp:generateOpenApi
    paths: [etc/openapi/apidocs/**]
    commit: "chore(api): regenerate the openapi specs after rebase"

dependency_graph:
  - gradle/libs.versions.toml
  - build.gradle
  - "*/build.gradle"
  - gradle.properties
```

The first draft wrote `when: paths-changed(...)` and `when: head-changed`, a
mini-DSL with no parser. Execution finding: plain keys say the same. A step with
`paths` runs when the rebase changed any of them; a step without runs after
every rebase, which is all `head-changed` ever meant. The files on the draft
branches still use the earlier `resolvers:` key and will be rewritten to this
shape when `wt sync` can read it; nothing reads them until then.

### What `commit:` means, stated rather than assumed

The first draft's plan gave `defer` a `commit:` key in passing. The rule: a
deferred step's output is committed **only when it changes tracked files**, with
the message given, and the commit is reported. It has to be committed: CI's
`git diff --exit-code` guard runs at the PR tip, and a worktree left dirty by
the tool would block its own next sync. `pnpm run generate-git-info` writes
under `src/.generated/`, which is gitignored, so that step has no `commit:` and
never makes one.

### A deferred step can fail, and what happens then

`./gradlew webapp:generateOpenApi` is a Spring Boot test that boots the
application on H2 with a **Testcontainers OpenSearch**, so it needs Docker, and
it fails when the merged code does not compile. Both are execution findings.
The second is welcome: the regeneration doubles as the compile check no other
step provides. The rule: a failed deferred step **never undoes the rebase**. The
rebase is complete and correct as far as git is concerned; the worktree is
reclassified `contested`, the plan file records the step as owed with its
output, and `wt sync doctor` preflights Docker so the keeper does not discover
it fleet-wide at three in the morning.

### There is no `verify:` list

The first draft had `verify: git diff --exit-code etc/openapi/apidocs`, CI's
guard. Execution finding: run after the regeneration commit it is a tautology,
and run before it always fails, because springdoc's ordering makes the
structural merge and the generator disagree by design. The regeneration *is*
the verification; the size of its diff is reported, because a large one means
the merge was wrong.

### The config, and any `script`, are read from trunk, not from the branch

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
conflicts:
  - paths: [package.json, apps/*/package.json, packages/*/package.json]
    strategy: owned-line
    line: '"@telcred/spec-telcredv2-typescript-axios":'
    rule: keep-branch
  - paths: [pnpm-lock.yaml]
    strategy: take-trunk

defer:
  - run: pnpm install
    paths: [pnpm-lock.yaml, "**/package.json"]
    commit: "chore(deps): reconcile the lockfile after rebase"
  - run: pnpm run generate-git-info
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

### Stacks are in scope, because one existed three days ago

The first draft said no stacks existed. It was wrong — the detection script had a
bug. **`perf_wt/pruning_keyset_index` (3 ahead of trunk) was an ancestor of
`feat_wt/pruning_cron` (6 further commits), and both were checked out.** Review
finding 8, independently reproduced.

Execution finding, 2026-09-08: that stack is gone. The `pruning_cron` worktree
now holds `feat_wt/tombstone_pruning`, and no worktree branch is an ancestor of
another. The fleet went from 24 worktrees to 22 in three days. Two conclusions:
the stack handling below stays, because the shape recurs; and **nothing in the
tool or its tests may encode a fleet fact** — every number in this document is
an illustration, and the tool computes.

Both are class `clean`, so a naive `wt sync` would rebase each onto trunk
independently and split the stack: the three shared commits would exist twice
under different SHAs and `pruning_cron` would no longer have `pruning_keyset_index`
as an ancestor. That is a live hazard today, not a hypothetical.

Therefore, in v1:

- Compute the ancestor relation across all branch-attached worktrees before
  touching anything.
- A branch whose tip trunk already contains is no branch's parent: it has no
  commits of its own, and every branch cut from trunk after it contains it.
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

### A sent ask is pending, not delivered

The `agent-sessions` skill records that a message between sessions of different
permission-mode classes is **held for the receiving user to approve**: the send
reports success and the target never sees it. Execution finding: the protocol
must not distinguish "no reply yet" from "never arrived", because it cannot. An
ask is `pending` until an `ack`, `nak` or `wait` comes back, a pending ask is
shown in `wt sync` and `wt sync queue` with its age, and **no timeout turns a
pending ask into a yes**. The keeper already queues rather than sends; a model
draining the queue reports what it sent and what is still pending.

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

## deferred, runs when the rebase completes (needs Docker)
./gradlew webapp:generateOpenApi        (1–2 min; also the compile check)
```

Each section removes a specific expense:

- **`scopes:`** — no `git log` to read.
- **"do not re-open"** — the largest saving. Without it, an agent seeing a
  regenerated 950 KB `openapi_v3.json` staged in the rebase will try to review it.
- **Per-file trunk-side subject** — `git log --oneline <base>..<trunk> -- <file>`,
  free. Answers "why did trunk change this" without opening a PR or a diff.
- **`additive only`** — computed from the conflict hunks; marks the five-second
  reads.
- **The deferred step, named** — replaces "typecheck, then tests, then format":
  the regeneration is the check.

## 7. Surfaces

`wt sync` **shows**. Changing anything takes a verb. The split follows
`devports`, where the bare command lists and `kill` acts.

| looking | |
|---|---|
| `wt sync` | the table: every worktree, its class, behind/ahead, who is in it, and what `run` would do |
| `wt sync --all` | the same across every configured repo under `$WT_ROOTS` |
| `wt sync <work>` | one worktree, in full |
| `wt sync explain <work>` | the first-parent log of what landed, for when the scopes are not enough |
| `wt sync doctor` | rerere, resolvers present on trunk, `lib.sh` drift across repos, config validity, Docker for the deferred steps, hooks, submodules, LFS, safety-ref retention |

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
| strategies | resolving one conflict shape each | `wt`, `internal/sync`, with the bash cases ported as tests |
| `bin/conflict/<script>` | a bespoke shape via the `script` escape hatch | a repo, only if it ever needs one; read from trunk |
| `.wt-sync.yaml` | which files have which shape, what to defer | each repo, committed, read from trunk |
| `wt sync` | triage, rebase mechanics, refs, locks, protocol | `wt` |
| `wt-sync-plan.md` | *this* rebase | generated, `.git/worktrees/<n>/` |
| `wt-sync` skill | driving the tool and the reply discipline | `anders-lindstrom/skills` |
| `api-bump` | the four-repo loop, snapshots, step 10 | `Telcred/telcred-skills` |

### Why `wt sync` is a `wt` subcommand and not a sibling binary

The review reopened this. `wt` is 4,900 lines of Go whose thesis is worktree
lifecycle; sync adds triage, rebase execution, a protocol, a keeper and a TUI,
and `devports` is the precedent for a sibling. The deciding fact is Go's
`internal/` rule: everything sync needs from `wt` — `repo.Discover`,
`Repo.Worktrees`, `DetectMainBranch`, the work-name resolution in `find` and
the naming rules — is internal to the `wt` module, and a sibling could reuse
none of it without `wt` first growing a public API for exactly this consumer.
Sync also *is* lifecycle: create, list, remove, keep current. It stays, as its
own `internal/sync` package with its own tests, and ships in the order the
trust argument dictates: triage and `run` with safety refs and `undo` first;
the plan file and `resume` second; `watch`, `keep`, the protocol and campaigns
after, each as its own plan.

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

Done 2026-09-08, on the `feat_wt/conflict-resolvers` branches, with
`bin/conflict/test/live-check.sh` (replay and `--endpoint` modes) and 71 bats
tests (44 in `server`, 27 in `accessmanager`):

- `openapi-spec` resolves all three spec files on `sync_skipped`, `webkey`,
  `state_stats` and `axis_acc`, at the first replay stop and at the endpoint,
  tags included, and **refuses** both `openapi_v3*.json` on
  `spring-boot-4-jackson-3` naming the paths.
- `openapi-version` resolves the first stops of `sync_skipped` and `webkey`
  (both APIs in one file) and refuses `state_stats` at the endpoint, where the
  yaml conflicts on a whole configuration block.
- `gradle-includes` resolves `axis_acc` at its first stop and at the endpoint,
  where the branch's second `include` line is folded into the block.
- `spec-client-version` keeps `2.36.0-snapshot.20260831123245` on all five
  `extract_webaccess` manifests and reports the snapshot; `pnpm-lock` takes
  trunk's lockfile at the endpoint.
- Every resolver leaves a refused file three-staged and unmodified.

Still to do, for the Go command:

- Every bats case on the draft branches has a Go counterpart, run against the
  same fixture shapes, and `live-check.sh`'s replay and endpoint results are
  reproduced by `wt sync` on the fleet.

- Triage output for every worktree matches the simulation, including the
  detached one.
- `wt sync doctor` reports Docker absent, `lib.sh` drift between repositories,
  and a resolver named in `.wt-sync.yaml` that is missing from trunk.
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
   resolver, or degrade that worktree to `contested`. Decided: refuse, and
   `doctor` reports it. A silently absent resolver looks exactly like a hard
   conflict.
2. ~~Whether the deferred `pnpm install` should use `--frozen-lockfile`.~~ It
   cannot; stated in `accessmanager/.wt-sync.yaml` next to the step.
3. Whether `bump_openapi.sh` should offer `max-plus-patch` as its default on a
   conflicted version, so the two paths agree without sharing code. Small,
   separate, untested today.
