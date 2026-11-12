# .wt-sync.yaml — what wt sync is told about a repository

`wt sync` rebases worktrees onto trunk, or onto the ref `--onto` names.
`.wt-sync.yaml`, at the root of the
repository, tells it the shapes of the conflicts it may resolve and the work
that runs around a rebase. A repository without the file is shown by `wt sync`
and never rebased (`wt up` still takes a conflict-free rebase there).

The file is read from trunk, never from the branch being rebased: every entry
names something wt runs, and a branch must not be able to change that. A
branch that edits the file changes nothing until the edit is on trunk. A run
reads it from the one trunk commit it fetched. A resume reads it from the trunk
commit the run it continues read it from.

A rebase onto another ref (`wt sync rebase <work> --onto <ref>`, or `wt up
--onto <ref>`) reads the file from trunk too, never from the target: an
integration or release branch is rebased onto, its declaration is not taken.
The strategies resolve against the target's side of each conflict (an
`owned-line` version comes out above the target's), the scripts are trunk's, and
a repository whose trunk has no file refuses `wt sync rebase --onto` as it
refuses `wt sync rebase`, while `wt up --onto` still takes a conflict-free rebase.

A key wt does not know is an error, and so is a mistake in a key it does know:
the run refuses and says which entry. `wt sync doctor` checks the file before
the first run.

```yaml
conflicts:
  - paths: [api/src/main/resources/application.yaml]
    strategy: owned-line
    line: '^    version:'
    rule: max-plus-patch
  - paths: [etc/openapi/*.json]
    strategy: openapi
  - paths: [settings.gradle]
    strategy: list-union
    line: '^include '

each_commit:
  - run: ./gradlew compileJava compileTestJava -q --offline
    paths: ["**/*.java", "**/*.gradle", gradle/libs.versions.toml]

defer:
  - run: ./gradlew compileJava compileTestJava
    paths: ["**/*.gradle", gradle/libs.versions.toml]
  - run: ./gradlew api:generateOpenApi
    paths: [etc/openapi/**]
    commit: "chore(api): regenerate the openapi specs after rebase"
    needs: [docker]

dependency_graph:
  - gradle/libs.versions.toml
  - "*/build.gradle"
```

## Paths

Every `paths` is a list of globs, matched against paths from the repository
root. `*` spans one path segment, `**` any number of them, none included.
Globs are matched, never expanded against the disk.

## `conflicts` — who resolves what

A list of rules. Each says that files matching `paths` have one conflict shape,
and names the strategy that resolves it. At a stop, each conflicted file goes
to the first rule whose `paths` match it; a file no rule claims is yours, and
the run hands the stop over with a plan file. A strategy that finds a conflict
not quite its shape refuses it, and the file is yours too: no strategy
guesses.

| key | meaning |
|---|---|
| `paths` | required: the files the rule claims |
| `strategy` | required: `owned-line`, `openapi`, `list-union`, `take-trunk` or `script` |
| `line` | a regular expression (Go's syntax), for `owned-line` and `list-union` |
| `rule` | how a version is decided, for `owned-line` and `openapi` (below) |
| `delimiter` | for `list-union`: what separates the items; `,` when left out |
| `run` | for `script`: the executable, relative to the root |

### `owned-line`

The file has one line, matched by `line`, whose value the branch owns: a
version both sides bump. Every other change must merge clean. Needs `line` and
`rule`.

```yaml
  - paths: [version.txt]
    strategy: owned-line
    line: '^\d'
    rule: max-plus-patch
```

Version lines pair across the sides by where they sit: the lines they nest
under by indentation, and the part of the line `line` matches. A version line
one side added is that side's and stays; one a side removed is refused. In a
file where several owned lines sit in the same place, such as a flat list of
`version:` lines, they pair by order, and a side with a different number of
them is refused.

### `openapi`

A generated OpenAPI document (JSON). Its paths, schemas and tags are merged key
by key; the same key changed on both sides is refused. `info.version` is
decided by `rule`, `max-plus-patch` when left out. It never regenerates: a
deferred step declared beside it does that once the rebase is done.

### `list-union`

The contested line, matched by `line`, is a list both sides only add to, such
as Gradle's `include`. The result is the union, trunk's order first, then the
branch's additions. An item one side removed is refused: a removal is a
decision, not a merge.

### `take-trunk`

Trunk's copy of the file, whole: a lockfile or a generated file nobody should
merge by hand. A deferred step declared beside it rebuilds it from the merged
sources.

### `script`

An executable in the repository, `run`, relative to the root and inside it. It
is read from trunk into a private directory and run from there, never from a
worktree. It is asked `--check <path>` against an index holding the conflict's
three versions, and `--resolve <path>` in the worktree during a run, where it
writes and stages the file. To `--check`, exit 0 claims the file, 1 does not
claim it, 2 refuses it with the reason on stderr; to `--resolve`, exit 0 says
the file is written and staged, which wt verifies. Anything else is an error. Each call has 60 seconds. A script can
only be checked by a real run, so the overview marks a worktree it claims a
file of `recipe?`.

### Rules for a version

| rule | the version the branch keeps |
|---|---|
| `max-plus-patch` | the higher of the two; when trunk's is higher, one patch above trunk's, so the branch keeps a version of its own |
| `shift-bump` | the pick's own bump made again on trunk's version, keeping its kind and size: a minor stays a minor, a patch a patch. The highest component the branch raised over the pick's parent is raised by the same amount on trunk's version; the components above it come from trunk, the ones below it from the branch. A pick that did not bump keeps trunk's version; one that lowered the version, or a version that is not X.Y.Z, is refused |
| `keep-branch` | the branch's |
| `keep-trunk` | trunk's |

A rule with `max-plus-patch` or `shift-bump` also lifts: a commit that changes
a claimed file which git merges without a conflict, while trunk bumped the same
version, has the version lifted too, at that commit, and the stop's note says
what it shifted.

## `each_commit` — a check after every replayed commit

Steps that run after each commit the rebase replays, at that commit: every
commit of a stack has to build on its own, because each is reviewed, bisected
and reverted on its own.

| key | meaning |
|---|---|
| `run` | required: a shell command, run with `sh -c` in the worktree |
| `paths` | the step runs at a commit that changes a path these match; empty or left out, at every commit |
| `needs` | what the step cannot run without (below) |

A check runs once the commit is final: after any stop at it is resolved and any
lift amended in. A commit git drops as empty is not checked. Steps run in the
order declared, each under its own 30 minutes; the first that fails stops the
rest. A check must not change tracked files: one that does has failed.

A check that fails stops the rebase at that commit and hands it over: HEAD is
the commit, nothing is unmerged, and the plan file names the step, its exit
and the end of its output. Fix it, amend the fix in with `git commit --amend`
(or add a commit), and `wt sync resume <work>` runs the check again at HEAD
and goes on only when it passes. A branch with a stack above it in the run is
put back instead, and the stack is not rebased.

In `wt sync rebase <work> --stack --autosquash`, which folds a line's
`fixup!` commits into their targets, a check runs at a target once its
fixups are folded in. `.wt-sync.yaml` has no key for `--stack`: a stack
held on one line is rebased with the same declaration as any branch.

`wt sync` says how many commits a run would check, and `wt sync <work>` which,
without running anything. After the last commit, `defer` is still the place
for work that runs once.

```yaml
each_commit:
  - run: make build
    paths: ["src/**", Makefile]
```

## `defer` — work after the rebase

Steps that run once, after the last commit is replayed, in the order declared;
or, with `each` or `fold`, at the end of every layer inside the rebase.

| key | meaning |
|---|---|
| `run` | required: a shell command, run with `sh -c` in the worktree |
| `paths` | the step runs when the rebase changed a path these match, comparing the branch before and after; empty or left out, after every rebase |
| `commit` | the message to commit the step's output with, when it changes tracked files |
| `needs` | what the step cannot run without (below) |
| `each` | `layer`: the step runs at the end of every layer, inside the rebase (below) |
| `fold` | `owner`: the step's output is folded into the commit that owns it (below); needs `paths` |

Each step has 30 minutes. A step that changes tracked files and declares no
`commit` has failed. A step that fails is owed: the rebase stands, the steps
after it do not run, and a worktree with anything owed is not pushed. Run the
step by hand, then push, or run `wt sync steps <work>`. A stack parent's owed
step does not stop the run from rebasing its children.

```yaml
defer:
  - run: npm install --package-lock-only
    paths: [package.json]
    commit: "chore: refresh the lockfile after rebase"
```

### `each: layer` and `fold: owner` — a step at every layer tip

A generated file that CI checks on every pull request (run the generator, then
`git diff --exit-code`) has to be right at every layer of a stack, and belongs
in the commit that changed it, not in a commit of its own on top.

```yaml
defer:
  - run: ./gradlew api:generateOpenApi
    paths: [etc/openapi/**]
    each: layer
    fold: owner
    commit: "chore(api): regenerate the openapi specs"
    needs: [docker]
```

With `each: layer` the step runs inside the rebase, at the end of every layer,
bottom first: in `wt sync rebase <work> --stack` each layer branch, and the
line's own commits above its top layer as one more; in any other run the
branch is the one layer, so a stack of worktrees gets the step at the end of
each branch's own rebase. At a layer's end HEAD is that layer's new tip, and
the step runs when its `paths` match a path that differs between the layer's
old and new tip, or that a commit of the layer changes. A line says which
layer it is at (`▸ step <run> layer 3/6 <branch>`).

What the step changes under its `paths` is its output. With `fold: owner` it
goes into the owner, the last commit of the layer that changed one of those
paths: that commit and every commit of the layer after it are rewritten with
those files taken from the step's output, and nothing else changes. Before the
rebase goes on, wt proves that the rewritten tip holds exactly the step's
output and that no rewritten commit differs from the one it replaces outside
the step's `paths`. The layers above are then replayed onto the folded tip,
in the same rebase. A layer where no commit changed those paths gets the step's
output as a commit of its own at its tip, with the `commit` message; with no
`commit` declared the step has failed there. Without `fold: owner`, an
`each: layer` step commits its output that way at every layer. `fold: owner`
without `each: layer` takes everything the rebase replayed as one layer; a
`--stack` run refuses it before anything moves, since it would rewrite
commits under the layers' markers.

A step that fails at a layer, changes a tracked file outside its `paths`, or
whose fold is not proved is owed at that layer and every layer above, where
it does not run again; the layers below stay folded and proved, the rebase goes
on to the end, and nothing owed is pushed. A rename or copy the step stages counts at both
ends: either one outside its `paths` is output outside them. A rebase handed
over to you keeps what its layer stops did, and `wt sync resume` goes on from
there: the same steps, the plan as they changed it, and what is owed.

Above a fold, a replayed commit that changes the same generated file can
conflict with the folded output. When the declared strategy refuses it, or
nothing claims the file, the replayed commit's side is taken and said
(`each: layer took the commit's side`): the stop at the end of that layer
writes the file again. Such a conflict is never handed to a person.

An `each_commit` check runs after each pick as before, so before the fold;
one whose `paths` take in a file the step wrote is run again at every commit
the fold rewrote, and at the step's own commit. `--autosquash` and
`wt sync steps --stack` move nothing onto trunk, and the line's content must
come out the same outside the per-layer steps' `paths`.

`wt sync steps <work>` runs what the last run left owed, without rebasing
onto trunk: each run records what it owes for the branch, and this clears
what it settles. `--all` runs every declared step, `--stack` at every layer
tip of the line. The JSON says, per layer and step, whether it ran and why,
the commit it folded into, its commit, what is owed, and the proof
(`layerSteps`, docs/json.md).

## `needs` — what a step cannot run without

A step under `defer` or `each_commit` may name what it cannot run without. The
names are wt's own; for now there is one.

| need | probed by |
|---|---|
| `docker` | `docker info`, within 10 seconds |

Before a run moves any branch, it probes each need once, and only when a step
with it may run: an `each_commit` step with a commit to check, or a `defer`
step whose `paths` match a path the rebase may change (what trunk changed
since the branch's merge base, or what the branch's commits change). A need
that does not answer refuses the worktrees whose steps need it, and what sits
on them in a stack, before anything moves: the line names the step, the need
and what the probe said. `--allow-owed` rebases them anyway: the step is not
run, and is owed as a failed one is. An `each_commit` step is then owed once
for the branch, not checked at any commit.

`wt sync --json` reports each need and what its probe answered, so a tool can
decide before it starts, and `wt sync doctor` checks Docker when a step
declares it.

```yaml
defer:
  - run: ./gradlew api:generateOpenApi
    paths: [etc/openapi/**]
    commit: "chore(api): regenerate the openapi specs"
    needs: [docker]
```

## `dependency_graph` — build files worth a note

Globs of files whose change on both sides is worth a line in the overview: a
build file or a version catalog trunk and the branch both changed. It is a
note, never a class: it changes nothing a run does.

```yaml
dependency_graph:
  - gradle/libs.versions.toml
  - build.gradle
```
