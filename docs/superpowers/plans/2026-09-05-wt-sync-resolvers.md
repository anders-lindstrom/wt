# wt sync — Conflict Resolvers Implementation Plan

> **Status: executed 2026-09-08.** The code landed on branch
> `feat_wt/conflict-resolvers` in `server` (7 commits, 44 bats tests) and in
> `accessmanager` (4 commits, 27 bats tests), unpushed. **The code blocks below
> are the plan as written, not what landed** — the branches are the truth. Read
> the corrections first; every one was found by running the code, several only
> against live branches.
>
> ## Corrections found in execution
>
> | where | what was wrong | what landed |
> |---|---|---|
> | Task 1 `helper.bash` | `printf '%s'` with `"base\n"` wrote a literal backslash-n; the first test could never pass | tests pass real newlines (`$'base\n'`) |
> | Task 1 `conflict_main` | `$claims` unquoted: run from the root, `--claims` printed the three real spec files instead of the glob | `set -f` in `lib.sh`; a test proves the glob survives when files match it |
> | Task 1 contract | `git show :2:<path>` is root-relative, `git add` is cwd-relative; nothing entered the root | resolvers enter the root; absolute paths inside the checkout are rewritten (tested, including macOS `/var` vs `/private/var`) |
> | Task 2 awk | `printf "%%s\n"` prints a literal `%s`; the whole awk-calls-bash construction was fragile | a bash walker over `git merge-file --diff3` output, shared in `lib.sh` as `merge_stages` + `walk_conflicts` |
> | Tasks 2, 4, 6 | git folds an adjacent edit into the same conflict block, so "exactly one line per side" refused `axis_acc`'s `settings.gradle` and would refuse manifests | `collapse_owned_line` in `lib.sh`: keep the side that alone changed the other lines, judged against the diff3 base |
> | Task 3 live check | verified only `openapi_v3.json`; `webkey`'s first stop conflicts on `openapi_remote_v3.json` | all three files verified at replay stops and at the endpoint |
> | Task 4 rule | assumed one `include` line; `axis_acc` has two | any number of include lines, union within the block, quoted names validated |
> | Task 6 rule | "blank the pin, the manifests must be identical" **refused the live case**: trunk had added two exports to `packages/shared/package.json`; the plan's own tests passed | merge with git, collapse the pin-only block; a test pins trunk's other manifest changes |
> | Task 6 `resolve_it` | copied a temp file the `RETURN` trap had already deleted | rewritten on `merge_stages` |
> | Tasks 5, 8 delegation | index-based resolvers cannot see a person's partial resolution; delegating from the bump scripts would have regressed them | **dropped**; the bump scripts are unchanged (spec §2) |
> | Tasks 5, 8 tests | grep-based characterisation tests; `setup()` copied the whole repository per test | gone with the delegation |
> | Task 5 `.wt-sync.yaml` | `when: paths-changed(...)` mini-DSL; `verify:` tautological after the regen commit; no note that `generateOpenApi` needs Docker | plain `paths:`; no `verify:`; Docker and compile-check documented (spec §3) |
> | Task 8 `.wt-sync.yaml` | `commit:` on `generate-git-info`, whose output is gitignored | no commit for that step |
> | Task 7 test | asserted the lockfile shows in `git diff --cached`; it equals HEAD mid-rebase and never does | asserts the path is no longer unmerged |
>
> Added beyond the plan: `bin/conflict/test/live-check.sh` in both repos, which
> replays a real branch to its first stop (or takes the endpoint) and runs the
> resolvers against those exact three stages in a throwaway repository. It is
> the reference for the Go triage.


> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `server` and `accessmanager` a `bin/conflict/` directory of single-purpose scripts that resolve the repo's recurring rebase conflicts deterministically, with no build and no reasoning.

**Architecture:** Each resolver is a bash script answering a three-verb contract — `--claims`, `--check <file>`, `--resolve <file>`. It reads the three conflict stages out of the git index, transforms content, stages the result, and **refuses** rather than guessing. A shared `lib.sh` holds stage access and the refusal helpers. `.wt-sync.yaml` in each repo lists the resolvers and the work deferred to the end of a rebase. The existing bump scripts delegate their conflict handling to the new resolvers.

**Tech Stack:** bash (matching all 27 existing scripts in `server/bin`), `jq` 1.8.2 (already a `REQUIRED_BINS` entry in `server`'s `worktree.conf`), `git merge-file` for three-way text merges, `bats` for tests.

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` (in this repo, `wt`). §2 and §3 are what this plan implements.

**Repos:** This plan's code lands in **two other repositories**, not in `wt`:
- Tasks 1–5: `~/programmering/telcred/server` (trunk `development`)
- Tasks 6–8: `~/programmering/telcred/accessmanager` (trunk `main`)

Create a worktree in each with `wt new feat/conflict-resolvers` before starting that repo's tasks. The plan and spec stay here; each task states its repo explicitly.

**Not in this plan:** the `wt sync` Go command (triage, rebase execution, protocol, plan file, picker), the `wt-sync` skill, and the `api-bump` amendment. Those are separate plans. The resolvers ship value on their own — a person hitting one of these conflicts by hand runs the script — and they are testable against live branches today, which is why they go first.

## Global Constraints

- **Never say "ours" or "theirs" in these scripts.** During a rebase git swaps them: stage 2 (`--ours`) is **trunk**, stage 3 (`--theirs`) is **the branch commit being replayed**. Verified directly. Every version rule depends on the distinction, so the helper names are `stage_trunk` and `stage_branch` and the ambiguous words never appear.
- **A resolver never invokes a build.** No `gradle`, no `pnpm i`, no code generation. Regeneration is a deferred step run once at the end of a rebase, declared in `.wt-sync.yaml`.
- **A resolver refuses rather than guesses.** Exit 2 with one line on stderr. Exit 1 means "not my file". Exit 0 means resolved and staged.
- **A rebase never moves a client off a snapshot.** On a spec-client pin conflict the branch's pin is kept. `feat_wt/extract_webaccess` pins `2.36.0-snapshot.20260831123245` and imports `WebKeysApi`, which comes from the unmerged `feat_wt/webkey`; trunk's spec at 2.38.2 has zero web-key paths. A higher release number is not evidence that a particular branch's server change landed.
- **Commits follow Conventional Commits.** `type(scope): description`, imperative, lowercase, under 72 characters, no trailing period. **No `Co-Authored-By` or any AI-attribution trailer, ever.**
- Both repos have an active `commit-msg` hook; messages must satisfy it.
- `bash` is `/bin/bash` 3.2 on this machine, but every script in `server/bin` uses `#!/usr/bin/env bash`, which resolves to Homebrew bash 5. Match that. `mapfile` and `[[ ]]` are therefore available.

---

### Task 1: The resolver contract and its harness

**Repo:** `server`

**Files:**
- Create: `bin/conflict/lib.sh`
- Create: `bin/conflict/test/helper.bash`
- Create: `bin/conflict/test/lib.bats`
- Create: `bin/conflict/README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `stage_base FILE`, `stage_trunk FILE`, `stage_branch FILE` (each writes that stage to stdout); `refuse MSG` (exit 2); `not_mine` (exit 1); `require_conflicted FILE`; `semver_max_plus_patch BRANCH_V TRUNK_V` (echoes the version the branch should claim); `conflict_main CLAIMS CHECK_FN RESOLVE_FN "$@"`. Tasks 2, 3, 4, 6 and 7 all source this.

- [ ] **Step 1: Write the failing test**

Create `bin/conflict/test/helper.bash`:

```bash
# Build a throwaway repo whose HEAD is a real, in-progress rebase conflict, so
# resolvers are exercised against actual index stages rather than a mock.
#
# make_conflict <path> <base-content> <trunk-content> <branch-content>
# Leaves $TESTDIR as cwd, mid-rebase, with <path> conflicted.
make_conflict() {
    local path=$1 base=$2 trunk=$3 branch=$4
    TESTDIR=$(mktemp -d)
    cd "$TESTDIR" || return 1
    git init -q .
    git config user.email t@example.com
    git config user.name  t
    git config commit.gpgsign false
    mkdir -p "$(dirname "$path")"
    printf '%s' "$base" > "$path"
    git add -A && git commit -qm base
    git branch trunk
    git checkout -qb feature
    printf '%s' "$branch" > "$path"
    git commit -qam "branch change"
    git checkout -q trunk
    printf '%s' "$trunk" > "$path"
    git commit -qam "trunk change"
    git checkout -q feature
    git rebase trunk >/dev/null 2>&1 || true
}

teardown() {
    [ -n "${TESTDIR:-}" ] && rm -rf "$TESTDIR"
    return 0
}
```

Create `bin/conflict/test/lib.bats`:

```bash
#!/usr/bin/env bats

load helper

LIB="${BATS_TEST_DIRNAME}/../lib.sh"

@test "stage_trunk is the upstream side and stage_branch is the replayed commit" {
    make_conflict v.txt "base\n" "trunk\n" "branch\n"
    source "$LIB"
    run stage_base   v.txt; [ "$output" = "base"   ]
    run stage_trunk  v.txt; [ "$output" = "trunk"  ]
    run stage_branch v.txt; [ "$output" = "branch" ]
}

@test "require_conflicted refuses a file that is not conflicted" {
    make_conflict v.txt "base\n" "trunk\n" "branch\n"
    printf 'x\n' > other.txt
    source "$LIB"
    run require_conflicted other.txt
    [ "$status" -eq 2 ]
    [[ "$output" == *"not a three-stage conflict"* ]]
}

@test "semver_max_plus_patch keeps a branch version already above trunk" {
    source "$LIB"
    run semver_max_plus_patch 2.39.0 2.38.5
    [ "$output" = "2.39.0" ]
}

@test "semver_max_plus_patch lifts a branch version trunk has overtaken" {
    source "$LIB"
    run semver_max_plus_patch 2.38.3 2.38.5
    [ "$output" = "2.38.6" ]
}

@test "semver_max_plus_patch lifts a branch version equal to trunk" {
    source "$LIB"
    run semver_max_plus_patch 2.38.5 2.38.5
    [ "$output" = "2.38.6" ]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bats bin/conflict/test/lib.bats`
Expected: FAIL — `lib.sh` does not exist, so `source` fails on every test.

- [ ] **Step 3: Write the implementation**

Create `bin/conflict/lib.sh`:

```bash
#!/usr/bin/env bash
# Shared helpers for bin/conflict/* resolvers.
#
# The names say trunk and branch, never ours and theirs. During a rebase git
# swaps the familiar meanings: stage 2 (--ours) is the upstream being rebased
# onto, and stage 3 (--theirs) is the commit being replayed. Every version rule
# here turns on that distinction, so the ambiguous words are not used at all.

conflict_stage() { git show ":$1:$2"; }
stage_base()   { conflict_stage 1 "$1"; }   # merge base
stage_trunk()  { conflict_stage 2 "$1"; }   # --ours   during a rebase
stage_branch() { conflict_stage 3 "$1"; }   # --theirs during a rebase

refuse()   { printf '%s\n' "$*" >&2; exit 2; }
not_mine() { exit 1; }

# A resolver only ever runs against a file carrying all three stages. Anything
# else is a caller error, not a conflict this script can have an opinion about.
require_conflicted() {
    local stages
    stages=$(git ls-files -u -- "$1" | awk '{print $3}' | sort -u | tr -d '\n')
    [[ $stages == "123" ]] || refuse "$1 is not a three-stage conflict"
}

path_matches() {
    local file=$1 glob
    shift
    for glob in "$@"; do
        # shellcheck disable=SC2053  # the glob is meant to be a pattern
        [[ $file == $glob ]] && return 0
    done
    return 1
}

# The branch keeps a version of its own, and that version has to sit above
# whatever trunk reached while the branch was away.
semver_max_plus_patch() {
    local branch_v=$1 trunk_v=$2 highest maj min pat
    highest=$(printf '%s\n%s\n' "$branch_v" "$trunk_v" | sort -V | tail -1)
    if [[ $highest == "$branch_v" && $branch_v != "$trunk_v" ]]; then
        printf '%s\n' "$branch_v"
        return
    fi
    IFS=. read -r maj min pat <<<"$trunk_v"
    printf '%s.%s.%s\n' "$maj" "$min" "$((pat + 1))"
}

# conflict_main "<glob> <glob>" <check-fn> <resolve-fn> "$@"
conflict_main() {
    local claims=$1 check_fn=$2 resolve_fn=$3
    shift 3
    case ${1:-} in
    --claims)
        printf '%s\n' $claims
        ;;
    --check)
        [[ -n ${2:-} ]] || refuse "--check needs a file"
        path_matches "$2" $claims || not_mine
        require_conflicted "$2"
        "$check_fn" "$2"
        ;;
    --resolve)
        [[ -n ${2:-} ]] || refuse "--resolve needs a file"
        path_matches "$2" $claims || not_mine
        require_conflicted "$2"
        "$resolve_fn" "$2"
        git add -- "$2"
        ;;
    *)
        printf 'usage: %s --claims | --check <file> | --resolve <file>\n' "${0##*/}" >&2
        exit 64
        ;;
    esac
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `bats bin/conflict/test/lib.bats`
Expected: 5 passing.

- [ ] **Step 5: Write the README**

Create `bin/conflict/README.md`:

```markdown
# bin/conflict

One script per recurring rebase conflict shape. Each answers three verbs:

    bin/conflict/<name> --claims            the path globs it owns
    bin/conflict/<name> --check   <file>    0 = I can resolve this, 1 = not mine
    bin/conflict/<name> --resolve <file>    resolve in place and stage it
                                            0 = done, 2 = refuse (reason on stderr)

`--claims` is answerable without a conflict, which is what lets a tool triage a
rebase before starting it. `--check` and `--resolve` need a file with all three
index stages present, so they are only valid mid-rebase.

**A resolver refuses rather than guesses.** Exit 2 is a normal outcome and means
a person should look. No resolver runs a build; regeneration is deferred to the
end of the rebase and declared in `.wt-sync.yaml`.

During a rebase git swaps ours and theirs — stage 2 is trunk, stage 3 is the
commit being replayed. `lib.sh` exposes `stage_trunk` and `stage_branch` so no
script has to remember that.

Run the tests with `bats bin/conflict/test/`.
```

- [ ] **Step 6: Commit**

```bash
git add bin/conflict/
git commit -m "feat(conflict): add the resolver contract and its test harness"
```

---

### Task 2: `openapi-version` — the `openapi:` block in `application.yaml`

**Repo:** `server`

**Files:**
- Create: `bin/conflict/openapi-version`
- Create: `bin/conflict/test/openapi-version.bats`

**Interfaces:**
- Consumes: `lib.sh` from Task 1 — `stage_base`, `stage_trunk`, `stage_branch`, `semver_max_plus_patch`, `refuse`, `conflict_main`.
- Produces: an executable claiming `accessmanagement/src/main/resources/application.yaml`. Task 5 calls it from `bump_openapi.sh`.

- [ ] **Step 1: Write the failing test**

Create `bin/conflict/test/openapi-version.bats`:

```bash
#!/usr/bin/env bats

load helper

R="${BATS_TEST_DIRNAME}/../openapi-version"
Y=accessmanagement/src/main/resources/application.yaml

yaml() { # yaml <main-version> [extra-line]
    printf 'server:\n  port: 8080\nopenapi:\n  main:\n    name: Backend API\n    version: %s\n    public-only: false\n  remote:\n    name: Remote API\n    version: 1.5.1\n%s' "$1" "${2:-}"
}

@test "claims the application.yaml path" {
    run "$R" --claims
    [[ "$output" == *"application.yaml"* ]]
}

@test "exits 1 for a file it does not own" {
    make_conflict other.yaml "a\n" "b\n" "c\n"
    run "$R" --check other.yaml
    [ "$status" -eq 1 ]
}

@test "resolves a version-only conflict by lifting above trunk" {
    make_conflict "$Y" "$(yaml 2.38.0)" "$(yaml 2.38.5)" "$(yaml 2.38.3)"
    run "$R" --resolve "$Y"
    [ "$status" -eq 0 ]
    grep -q 'version: 2.38.6' "$Y"
    run grep -c '<<<<<<<' "$Y"
    [ "$output" = "0" ]
    run git diff --cached --name-only
    [[ "$output" == *"application.yaml"* ]]
}

@test "keeps a branch version already above trunk" {
    make_conflict "$Y" "$(yaml 2.38.0)" "$(yaml 2.38.5)" "$(yaml 2.39.0)"
    run "$R" --resolve "$Y"
    [ "$status" -eq 0 ]
    grep -q 'version: 2.39.0' "$Y"
}

@test "refuses when the two sides differ by more than a version line" {
    make_conflict "$Y" "$(yaml 2.38.0)" "$(yaml 2.38.5 '  extra: trunk\n')" \
                       "$(yaml 2.38.3 '  extra: branch\n')"
    run "$R" --resolve "$Y"
    [ "$status" -eq 2 ]
    [[ "$output" == *"more than"* ]]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bats bin/conflict/test/openapi-version.bats`
Expected: FAIL — `openapi-version` does not exist.

- [ ] **Step 3: Write the implementation**

Create `bin/conflict/openapi-version`:

```bash
#!/usr/bin/env bash
# Resolve a conflict in the openapi: block of application.yaml.
#
# The whole file is merged with git's own three-way merge, which leaves markers
# only where the two sides genuinely disagree. Every remaining conflict must be
# a lone version: line — anything else is a real disagreement about
# configuration and is refused.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"

CLAIMS='accessmanagement/src/main/resources/application.yaml'

# Merge the three stages, then walk the result collapsing version-only
# conflicts. Prints the resolved file on stdout; exits 2 if anything else
# conflicted.
merged() {
    local file=$1 base trunk branch out
    base=$(mktemp) trunk=$(mktemp) branch=$(mktemp) out=$(mktemp)
    # shellcheck disable=SC2064  # expand the paths now, not at trap time
    trap "rm -f '$base' '$trunk' '$branch' '$out'" RETURN

    stage_base   "$file" > "$base"
    stage_trunk  "$file" > "$trunk"
    stage_branch "$file" > "$branch"

    git merge-file -p --diff3 \
        -L branch -L base -L trunk \
        "$branch" "$base" "$trunk" > "$out" || true

    awk '
    /^<<<<<<< branch$/ { inconf = 1; nb = nt = 0; delete B; delete T; side = "B"; next }
    inconf && /^\|\|\|\|\|\|\| base$/ { side = "X"; next }
    inconf && /^=======$/             { side = "T"; next }
    inconf && /^>>>>>>> trunk$/ {
        # Both sides must be exactly one line, and both must be version lines.
        if (nb != 1 || nt != 1) { bad = 1; exit }
        if (B[1] !~ /^[[:space:]]*version:[[:space:]]/) { bad = 1; exit }
        if (T[1] !~ /^[[:space:]]*version:[[:space:]]/) { bad = 1; exit }
        bv = B[1]; tv = T[1]
        sub(/^[[:space:]]*version:[[:space:]]*/, "", bv)
        sub(/^[[:space:]]*version:[[:space:]]*/, "", tv)
        indent = B[1]; sub(/version:.*/, "", indent)
        printf "%%s\n", indent "version: " resolve(bv, tv)
        inconf = 0; next
    }
    inconf { if (side == "B") B[++nb] = $0; else if (side == "T") T[++nt] = $0; next }
    { print }
    END { if (bad) exit 3 }

    function resolve(b, t,   cmd, line) {
        cmd = RESOLVER " " b " " t
        cmd | getline line
        close(cmd)
        return line
    }
    ' RESOLVER="$DIR/semver-lift" "$out" || refuse \
        "the two sides of the openapi: block differ by more than a version line"
}

check_it()   { merged "$1" >/dev/null; }
resolve_it() { local f=$1 tmp; tmp=$(mktemp); merged "$f" > "$tmp"; mv "$tmp" "$f"; }

conflict_main "$CLAIMS" check_it resolve_it "$@"
```

Create `bin/conflict/semver-lift` — awk cannot call a bash function, so the rule gets a one-line front end:

```bash
#!/usr/bin/env bash
# semver-lift <branch-version> <trunk-version> -> the version the branch takes.
set -euo pipefail
DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"
semver_max_plus_patch "$1" "$2"
```

- [ ] **Step 4: Make both executable and run the tests**

```bash
chmod +x bin/conflict/openapi-version bin/conflict/semver-lift
bats bin/conflict/test/openapi-version.bats
```

Expected: 5 passing.

- [ ] **Step 5: Verify against a live branch**

`feat_wt/state_stats` conflicts on this file against `origin/development`. Prove the resolver handles the real thing, in a scratch worktree so nothing real moves:

```bash
git worktree add -q /tmp/wtv feat_wt/state_stats
git -C /tmp/wtv rebase --no-gpg-sign origin/development || true
git -C /tmp/wtv status --short | grep '^UU' || echo "no conflict at this step"
# from the worktree, with the resolver from this branch:
(cd /tmp/wtv && "$OLDPWD"/bin/conflict/openapi-version --resolve \
    accessmanagement/src/main/resources/application.yaml && \
    grep -n 'version:' accessmanagement/src/main/resources/application.yaml | head -4)
git -C /tmp/wtv rebase --abort || true
git worktree remove --force /tmp/wtv
```

Expected: the `openapi.main.version` line resolves to one patch above `development`'s current 2.38.2, with no markers left.

- [ ] **Step 6: Commit**

```bash
git add bin/conflict/openapi-version bin/conflict/semver-lift bin/conflict/test/openapi-version.bats
git commit -m "feat(conflict): resolve openapi version conflicts in application.yaml"
```

---

### Task 3: `openapi-spec` — the generated spec JSONs

**Repo:** `server`

**Files:**
- Create: `bin/conflict/openapi-spec`
- Create: `bin/conflict/merge3.jq`
- Create: `bin/conflict/test/openapi-spec.bats`

**Interfaces:**
- Consumes: `lib.sh` from Task 1; `semver_max_plus_patch` for `info.version`.
- Produces: an executable claiming `etc/openapi/apidocs/*.json`. Task 5 lists it in `.wt-sync.yaml`.

**A refusal here is load-bearing.** Under the revised spec, a resolver refusing
is one of the three signals that classify a branch `divergent` — a branch where
the ground has moved and a rebase is the wrong frame entirely. So the refusal
message must name *what* collided (the conflicting keys), not merely that it
gave up. `feat_wt/spring-boot-4-jackson-3` is the case: 13 conflicting paths and
28 conflicting schema keys, because the framework upgrade rewrote the
generator's own output.

**Why this shape:** the spec is a ~950 KB generated document. Merging it key by key resolves the real cases in milliseconds; regenerating it costs 1–2 minutes of Gradle and would run once per conflicting commit. Measured across the five conflicting server branches, **four collide on nothing but `info.version`**; the fifth (`feat_wt/spring-boot-4-jackson-3`, a framework upgrade that rewrites the generator's output) collides on 28 real keys and must be refused.

The merged sections are `paths`, `components.schemas`, and `tags` (keyed by `.name`), plus `info.version` by rule. Everything else must be identical between base and branch — verified true for all four resolvable branches, where the remainder hashes identically across base, branch and trunk.

- [ ] **Step 1: Write the failing test**

Create `bin/conflict/test/openapi-spec.bats`:

```bash
#!/usr/bin/env bats

load helper

R="${BATS_TEST_DIRNAME}/../openapi-spec"
F=etc/openapi/apidocs/openapi_v3.json

# spec <version> <paths-json> <schemas-json> <tags-json>
spec() {
    jq -nc --arg v "$1" --argjson p "$2" --argjson s "$3" --argjson t "$4" \
      '{openapi:"3.1.0", info:{title:"T", version:$v}, tags:$t,
        paths:$p, components:{schemas:$s}}'
}

@test "merges disjoint paths, schemas and tags and lifts the version" {
    make_conflict "$F" \
      "$(spec 2.38.0 '{"/a":{"get":{}}}' '{"A":{}}' '[{"name":"TA"}]')" \
      "$(spec 2.38.5 '{"/a":{"get":{}},"/t":{"get":{}}}' '{"A":{},"T":{}}' '[{"name":"TA"},{"name":"TT"}]')" \
      "$(spec 2.38.3 '{"/a":{"get":{}},"/b":{"get":{}}}' '{"A":{},"B":{}}' '[{"name":"TA"},{"name":"TB"}]')"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run jq -r '.paths|keys|join(",")' "$F";             [ "$output" = "/a,/b,/t" ]
    run jq -r '.components.schemas|keys|join(",")' "$F"; [ "$output" = "A,B,T" ]
    run jq -r '[.tags[].name]|sort|join(",")' "$F";      [ "$output" = "TA,TB,TT" ]
    run jq -r '.info.version' "$F";                      [ "$output" = "2.38.6" ]
}

@test "refuses when both sides change the same path" {
    make_conflict "$F" \
      "$(spec 2.38.0 '{"/a":{"get":{}}}' '{}' '[]')" \
      "$(spec 2.38.5 '{"/a":{"get":{"x":1}}}' '{}' '[]')" \
      "$(spec 2.38.3 '{"/a":{"get":{"x":2}}}' '{}' '[]')"
    run "$R" --resolve "$F"
    [ "$status" -eq 2 ]
    [[ "$output" == *"/a"* ]]
}

@test "refuses when the branch changes anything outside the merged sections" {
    make_conflict "$F" \
      "$(spec 2.38.0 '{}' '{}' '[]')" \
      "$(spec 2.38.5 '{"/t":{"get":{}}}' '{}' '[]')" \
      "$(spec 2.38.3 '{}' '{}' '[]' | jq -c '.info.title="Changed"')"
    run "$R" --resolve "$F"
    [ "$status" -eq 2 ]
    [[ "$output" == *"outside"* ]]
}

@test "stages the file it resolved" {
    make_conflict "$F" \
      "$(spec 2.38.0 '{}' '{}' '[]')" \
      "$(spec 2.38.5 '{"/t":{"get":{}}}' '{}' '[]')" \
      "$(spec 2.38.3 '{"/b":{"get":{}}}' '{}' '[]')"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run git diff --cached --name-only
    [[ "$output" == *"openapi_v3.json"* ]]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bats bin/conflict/test/openapi-spec.bats`
Expected: FAIL — `openapi-spec` does not exist.

- [ ] **Step 3: Write the merge program**

Create `bin/conflict/merge3.jq`:

```jq
# Three-way merge over the top-level keys of one object.
# Emits {conflict: [key, ...]} when both sides changed a key differently,
# otherwise {result: {...}}. A key deleted on one side and untouched on the
# other is dropped, which is what with_entries filters out.
def merge3($b; $o; $t):
  ( [ ($b | keys_unsorted[]), ($o | keys_unsorted[]), ($t | keys_unsorted[]) ] | unique ) as $all
  | [ $all[]
      | select( ($o[.] != $b[.]) and ($t[.] != $b[.]) and ($o[.] != $t[.]) ) ] as $conflicts
  | if ($conflicts | length) > 0
    then { conflict: $conflicts }
    else { result: ( reduce $all[] as $k ({};
                       . + { ($k): (if $o[$k] != $b[$k] then $o[$k] else $t[$k] end) } )
                   | with_entries(select(.value != null)) ) }
    end;

# Tags are a list of objects, not a map. Key them by name for the merge and
# turn them back afterwards.
def by_name: (. // []) | map({key: .name, value: .}) | from_entries;
def to_tags: [ .[] ];
```

- [ ] **Step 4: Write the resolver**

Create `bin/conflict/openapi-spec`:

```bash
#!/usr/bin/env bash
# Resolve a conflict in a generated OpenAPI spec by merging its top-level
# sections key by key.
#
# This never regenerates. `./gradlew webapp:generateOpenApi` produces the
# authoritative bytes once, at the end of the rebase, declared as a deferred
# step in .wt-sync.yaml. springdoc emits paths and schemas in registration
# order rather than sorted, so a structural merge cannot reproduce the
# generator byte for byte — it exists to let the replay continue, and CI's
# `git diff --exit-code` over etc/openapi/apidocs remains the backstop.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"

CLAIMS='etc/openapi/apidocs/*.json'

# Everything the merge does not own must be identical on base and branch.
# Anything else means the branch edited part of the document this resolver has
# no rule for, and guessing there is exactly what it must not do.
REMAINDER='del(.paths) | del(.components.schemas) | del(.tags) | .info.version = "X"'

spec_merge() {
    local file=$1 base trunk branch section conflicts result
    base=$(mktemp) trunk=$(mktemp) branch=$(mktemp)
    # shellcheck disable=SC2064
    trap "rm -f '$base' '$trunk' '$branch'" RETURN

    stage_base   "$file" > "$base"
    stage_trunk  "$file" > "$trunk"
    stage_branch "$file" > "$branch"

    if [[ $(jq -S "$REMAINDER" "$base" | shasum) != $(jq -S "$REMAINDER" "$branch" | shasum) ]]; then
        refuse "$file: the branch changed the document outside paths, schemas, tags and version"
    fi

    for section in '.paths' '.components.schemas'; do
        conflicts=$(jq -n -f <(cat "$DIR/merge3.jq"; echo "
            merge3(\$b[0]$section // {}; \$t[0]$section // {}; \$br[0]$section // {})
            | .conflict // empty | join(\", \")") \
            --slurpfile b "$base" --slurpfile t "$trunk" --slurpfile br "$branch" -r)
        [[ -z $conflicts ]] || refuse "$file: both sides changed ${section#.}: $conflicts"
    done

    conflicts=$(jq -n -f <(cat "$DIR/merge3.jq"; echo '
        merge3($b[0].tags|by_name; $t[0].tags|by_name; $br[0].tags|by_name)
        | .conflict // empty | join(", ")') \
        --slurpfile b "$base" --slurpfile t "$trunk" --slurpfile br "$branch" -r)
    [[ -z $conflicts ]] || refuse "$file: both sides changed tags: $conflicts"

    local branch_v trunk_v new_v
    branch_v=$(jq -r '.info.version' "$branch")
    trunk_v=$(jq -r '.info.version' "$trunk")
    new_v=$(semver_max_plus_patch "$branch_v" "$trunk_v")

    # Trunk is the skeleton: it carries the newest generator output for every
    # section this resolver does not merge, and the guard above proved the
    # branch did not touch any of them.
    jq -f <(cat "$DIR/merge3.jq"; echo '
          .paths              = (merge3($b[0].paths // {}; $t[0].paths // {}; $br[0].paths // {}) | .result)
        | .components.schemas = (merge3($b[0].components.schemas // {}; $t[0].components.schemas // {}; $br[0].components.schemas // {}) | .result)
        | .tags               = (merge3($b[0].tags|by_name; $t[0].tags|by_name; $br[0].tags|by_name) | .result | to_tags)
        | .info.version       = $newv') \
        --slurpfile b "$base" --slurpfile t "$trunk" --slurpfile br "$branch" \
        --arg newv "$new_v" "$trunk"
}

check_it()   { spec_merge "$1" >/dev/null; }
resolve_it() { local f=$1 tmp; tmp=$(mktemp); spec_merge "$f" > "$tmp"; mv "$tmp" "$f"; }

conflict_main "$CLAIMS" check_it resolve_it "$@"
```

- [ ] **Step 5: Make executable and run the tests**

```bash
chmod +x bin/conflict/openapi-spec
bats bin/conflict/test/openapi-spec.bats
```

Expected: 4 passing.

- [ ] **Step 6: Verify against all five live branches**

This is the task's real acceptance test. The merge logic was prototyped against these exact revisions and must reproduce: four resolve, one refuses.

```bash
for br in feat_wt/webkey feat_wt/state_stats feat_wt/sync_skipped axis_acc feat_wt/spring-boot-4-jackson-3; do
  base=$(git merge-base "$br" origin/development)
  for s in b:"$base" t:origin/development r:"$br"; do
    git show "${s#*:}:etc/openapi/apidocs/openapi_v3.json" > "/tmp/spec.${s%%:*}.json"
  done
  n=$(jq -n -f <(cat bin/conflict/merge3.jq; echo 'merge3($b[0].paths;$t[0].paths;$r[0].paths)|.conflict//[]|length') \
        --slurpfile b /tmp/spec.b.json --slurpfile t /tmp/spec.t.json --slurpfile r /tmp/spec.r.json)
  printf '%-34s path conflicts: %s\n' "$br" "$n"
done
```

Expected: `0` for the first four, `13` for `feat_wt/spring-boot-4-jackson-3`.

- [ ] **Step 7: Commit**

```bash
git add bin/conflict/openapi-spec bin/conflict/merge3.jq bin/conflict/test/openapi-spec.bats
git commit -m "feat(conflict): merge generated openapi specs without regenerating"
```

---

### Task 4: `gradle-includes` — the `include` list in `settings.gradle`

**Repo:** `server`

**Files:**
- Create: `bin/conflict/gradle-includes`
- Create: `bin/conflict/test/gradle-includes.bats`

**Interfaces:**
- Consumes: `lib.sh` from Task 1.
- Produces: an executable claiming `settings.gradle`.

**Why:** both `feat_wt/webkey` and `axis_acc` conflict here, and in both cases each side only *adds* module names to one inline list — trunk adds `pins`, `webkey` adds `bankid-client`, `axis_acc` adds six. A union preserving trunk's order then appending the branch's new entries is the whole rule.

- [ ] **Step 1: Write the failing test**

Create `bin/conflict/test/gradle-includes.bats`:

```bash
#!/usr/bin/env bats

load helper

R="${BATS_TEST_DIRNAME}/../gradle-includes"
F=settings.gradle

inc() { printf "rootProject.name = 'server'\ninclude %s\n" "$1"; }

@test "unions both sides, trunk order first" {
    make_conflict "$F" \
      "$(inc "'a', 'b', 'c'")" \
      "$(inc "'a', 'pins', 'b', 'c'")" \
      "$(inc "'a', 'b', 'bankid', 'c'")"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run grep -c '<<<<<<<' "$F"; [ "$output" = "0" ]
    run sed -n 's/^include //p' "$F"
    [ "$output" = "'a', 'pins', 'b', 'c', 'bankid'" ]
}

@test "refuses when a side removes a module" {
    make_conflict "$F" \
      "$(inc "'a', 'b', 'c'")" \
      "$(inc "'a', 'pins', 'b', 'c'")" \
      "$(inc "'a', 'c'")"
    run "$R" --resolve "$F"
    [ "$status" -eq 2 ]
    [[ "$output" == *"removes"* ]]
}

@test "refuses when the conflict is not on an include line" {
    make_conflict "$F" \
      "rootProject.name = 'a'\n" "rootProject.name = 'b'\n" "rootProject.name = 'c'\n"
    run "$R" --resolve "$F"
    [ "$status" -eq 2 ]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bats bin/conflict/test/gradle-includes.bats`
Expected: FAIL — script does not exist.

- [ ] **Step 3: Write the implementation**

Create `bin/conflict/gradle-includes`:

```bash
#!/usr/bin/env bash
# Resolve a conflict on settings.gradle's include list.
#
# Both sides only ever add modules, so the resolution is the union: trunk's
# order first, then whatever the branch added. A side that *removes* a module
# is a real decision and is refused.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"

CLAIMS='settings.gradle'

modules() { sed -n "s/^include //p" "$1" | tr ',' '\n' | sed "s/[[:space:]]//g; s/'//g" | grep -v '^$'; }

union_line() {
    local file=$1 base trunk branch
    base=$(mktemp) trunk=$(mktemp) branch=$(mktemp)
    # shellcheck disable=SC2064
    trap "rm -f '$base' '$trunk' '$branch'" RETURN

    stage_base   "$file" > "$base"
    stage_trunk  "$file" > "$trunk"
    stage_branch "$file" > "$branch"

    for f in "$base" "$trunk" "$branch"; do
        grep -q '^include ' "$f" || refuse "$file: no include line on one side; nothing this script owns"
    done

    local removed
    removed=$(comm -23 <(modules "$base" | sort) <(modules "$branch" | sort) | tr '\n' ' ')
    [[ -z ${removed// /} ]] || refuse "$file: the branch removes $removed; that is a decision, not a merge"
    removed=$(comm -23 <(modules "$base" | sort) <(modules "$trunk" | sort) | tr '\n' ' ')
    [[ -z ${removed// /} ]] || refuse "$file: trunk removes $removed; that is a decision, not a merge"

    # Trunk's order, then the branch's additions in the order it made them.
    local -a out=()
    while read -r m; do out+=("$m"); done < <(modules "$trunk")
    while read -r m; do
        printf '%s\n' "${out[@]}" | grep -qx "$m" || out+=("$m")
    done < <(modules "$branch")

    local joined
    joined=$(printf "'%s', " "${out[@]}"); joined=${joined%, }

    # Take trunk's file and replace its include line; nothing else on it is ours.
    awk -v line="include $joined" '/^include /{print line; next} {print}' "$trunk"
}

check_it()   { union_line "$1" >/dev/null; }
resolve_it() { local f=$1 tmp; tmp=$(mktemp); union_line "$f" > "$tmp"; mv "$tmp" "$f"; }

conflict_main "$CLAIMS" check_it resolve_it "$@"
```

- [ ] **Step 4: Make executable and run the tests**

```bash
chmod +x bin/conflict/gradle-includes
bats bin/conflict/test/gradle-includes.bats
```

Expected: 3 passing.

- [ ] **Step 5: Commit**

```bash
git add bin/conflict/gradle-includes bin/conflict/test/gradle-includes.bats
git commit -m "feat(conflict): union the settings.gradle include list"
```

---

### Task 5: `server/.wt-sync.yaml`, and `bump_openapi.sh` delegating to the resolver

**Repo:** `server`

**Files:**
- Create: `.wt-sync.yaml`
- Modify: `bin/bump_openapi.sh` — replace the conflict-collapsing awk with a call to `bin/conflict/openapi-version`
- Create: `bin/conflict/test/bump-delegation.bats`

**Interfaces:**
- Consumes: `bin/conflict/openapi-version` (Task 2), `openapi-spec` (Task 3), `gradle-includes` (Task 4).
- Produces: `.wt-sync.yaml`, read by `wt sync` from **trunk**, not from the branch being rebased.

**Important:** `bump_openapi.sh` today does two separable things — collapse a conflicted version line, and choose a version interactively or from an argument. Only the first moves. Version *selection* stays in the bump script, because a resolver takes no version argument. Do not "simplify" the script's interactive path away.

- [ ] **Step 1: Write the characterisation test**

Create `bin/conflict/test/bump-delegation.bats`. This pins the behaviour that must survive the refactor:

```bash
#!/usr/bin/env bats

# bump_openapi.sh keeps its own interface. These tests fix the two behaviours
# the delegation must not change: an explicit version still wins, and a
# conflict that is not version-only is still refused rather than collapsed.

setup() {
    REPO=$(mktemp -d)
    cp -R "${BATS_TEST_DIRNAME}/../../.." "$REPO/src" 2>/dev/null || true
    SCRIPT="${BATS_TEST_DIRNAME}/../../bump_openapi.sh"
}

teardown() { rm -rf "$REPO"; }

@test "the non-interactive explicit-version path is still reachable" {
    # The script takes `<version>` and `<api> <version>` with no prompting.
    # Losing that during the refactor would break publish_api_snapshot.sh,
    # which drives it non-interactively.
    grep -qE '^\s*2\)|\$#\s*-eq\s*2|\$#\s*==\s*2' "$SCRIPT"
    grep -q 'generateOpenApi' "$SCRIPT"
}

@test "the resolver is what collapses a conflicted version line" {
    grep -q 'bin/conflict/openapi-version' "$SCRIPT"
}

@test "version selection still lives in the bump script" {
    grep -qE 'major|minor|patch' "$SCRIPT"
}
```

- [ ] **Step 2: Run to verify the delegation tests fail**

Run: `bats bin/conflict/test/bump-delegation.bats`
Expected: the "resolver is what collapses" test FAILS — `bump_openapi.sh` does not mention `bin/conflict/openapi-version` yet.

- [ ] **Step 3: Write `.wt-sync.yaml`**

Create `.wt-sync.yaml` at the repo root:

```yaml
# How wt sync rebases this repository. Read from trunk, never from the branch
# being rebased — these entries name executables.

resolvers:
  - bin/conflict/openapi-version
  - bin/conflict/openapi-spec
  - bin/conflict/gradle-includes

# Deferred work runs once, after the last commit is replayed, and is keyed on
# what the rebase changed rather than on what conflicted. A branch can take
# trunk-side spec changes with no conflict at all and still need the regen.
#
# ./gradlew webapp:generateOpenApi takes 1-2 minutes. Running it per conflicting
# commit would cost six on axis_acc, which touches the spec in four commits.
defer:
  - run: ./gradlew webapp:generateOpenApi
    when: paths-changed(etc/openapi/apidocs/**)
    commit: "chore(api): regenerate the openapi specs after rebase"

# Narrow on purpose. TEST_COMMAND in bin/worktree/worktree.conf is
# `./gradlew test`, which is far too slow to run per rebase. This line is
# lifted from test_pr.yaml: it is CI's own guard and catches the real failure
# mode, a spec that was never regenerated, in about two seconds.
verify:
  - git diff --exit-code etc/openapi/apidocs

# Which paths mean "this branch changes the dependency graph". A branch touching
# any of these is a signal that it may be divergent rather than merely behind —
# trunk's code has been written against a different set of libraries.
# feat_wt/spring-boot-4-jackson-3 touches nine of them; feat_wt/state_stats
# touches none.
dependency_graph:
  - gradle/libs.versions.toml
  - build.gradle
  - "*/build.gradle"
  - gradle.properties
```

- [ ] **Step 4: Delegate from `bump_openapi.sh`**

Read `bin/bump_openapi.sh` first. Replace the block that collapses conflicted version lines (the `flush_conflict` awk function and its caller, around lines 174–245) with a call to the resolver, keeping everything else — API discovery, the interactive major/minor/patch prompt, the explicit-version path, and the `./gradlew webapp:generateOpenApi` run at the end:

```bash
# Where the file is mid-conflict, the resolver owns collapsing it. It applies
# the same rule wt sync does, so a conflict resolved by hand here and one
# resolved during an automated rebase come out identical.
if git ls-files -u -- "$YAML" | grep -q .; then
    if "${BASE_DIR}/bin/conflict/openapi-version" --resolve "$YAML"; then
        echo "Collapsed the conflicted openapi: block via bin/conflict/openapi-version."
    else
        echo "The openapi: block conflicts on more than a version line."
        echo "Resolve it by hand, then re-run."
        exit 1
    fi
fi
```

- [ ] **Step 5: Run every test**

```bash
bats bin/conflict/test/
```

Expected: all tests across all four files pass.

- [ ] **Step 6: Commit**

```bash
git add .wt-sync.yaml bin/bump_openapi.sh bin/conflict/test/bump-delegation.bats
git commit -m "feat(conflict): declare wt sync config and delegate the version collapse"
```

---

### Task 6: `spec-client-version` — the SDK pin in every `package.json`

**Repo:** `accessmanager`

**Files:**
- Create: `bin/conflict/lib.sh` (copy of `server`'s from Task 1, unmodified)
- Create: `bin/conflict/README.md` (copy of `server`'s from Task 1)
- Create: `bin/conflict/spec-client-version`
- Create: `bin/conflict/test/helper.bash` (copy from Task 1)
- Create: `bin/conflict/test/spec-client-version.bats`

**Interfaces:**
- Consumes: `lib.sh` — `stage_base`, `stage_trunk`, `stage_branch`, `refuse`, `conflict_main`.
- Produces: an executable claiming `package.json` and `*/*/package.json`. Task 8 calls it from `bump_axios_client.sh`.

**The rule, and why it is not the obvious one:** on a conflict in the `@telcred/spec-telcredv2-typescript-axios` pin, **keep the branch's pin** and print a note. Do *not* take trunk's because its number is higher. `feat_wt/extract_webaccess` pins `2.36.0-snapshot.20260831123245` and imports `WebKeysApi` in five files; that API comes from server `feat_wt/webkey`, which is unmerged — trunk's spec at 2.38.2 has zero web-key paths while `feat_wt/webkey` has eleven. Moving that client to trunk's `2.37.1` selects a client without the API the branch needs. Moving off a snapshot is `api-bump` step 10, a deliberate act.

- [ ] **Step 1: Copy the shared pieces**

```bash
mkdir -p bin/conflict/test
cp ~/programmering/telcred/server/bin/conflict/lib.sh          bin/conflict/lib.sh
cp ~/programmering/telcred/server/bin/conflict/README.md       bin/conflict/README.md
cp ~/programmering/telcred/server/bin/conflict/test/helper.bash bin/conflict/test/helper.bash
```

`lib.sh` is duplicated deliberately: each repo's `bin/conflict/` has to work from a checkout of that repo alone, with no shared install. If it drifts, that is a bug to fix, not a reason to add a dependency.

- [ ] **Step 2: Write the failing test**

Create `bin/conflict/test/spec-client-version.bats`:

```bash
#!/usr/bin/env bats

load helper

R="${BATS_TEST_DIRNAME}/../spec-client-version"
PKG="@telcred/spec-telcredv2-typescript-axios"
F=apps/access-manager/package.json

pkg() { jq -nc --arg v "$1" --arg p "$PKG" '{name:"app", dependencies:{($p):$v, "react":"19.0.0"}}'; }

@test "keeps the branch snapshot rather than trunk's higher release" {
    make_conflict "$F" "$(pkg 2.30.2)" "$(pkg 2.37.1)" "$(pkg 2.36.0-snapshot.20260831123245)"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run jq -r --arg p "$PKG" '.dependencies[$p]' "$F"
    [ "$output" = "2.36.0-snapshot.20260831123245" ]
    [[ "$output" != *"<<<"* ]]
}

@test "says the branch is still on a snapshot" {
    make_conflict "$F" "$(pkg 2.30.2)" "$(pkg 2.37.1)" "$(pkg 2.36.0-snapshot.20260831123245)"
    run "$R" --resolve "$F"
    [[ "$output" == *"snapshot"* ]]
}

@test "keeps the branch pin when neither side is a snapshot" {
    make_conflict "$F" "$(pkg 2.30.2)" "$(pkg 2.37.1)" "$(pkg 2.36.0)"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run jq -r --arg p "$PKG" '.dependencies[$p]' "$F"
    [ "$output" = "2.36.0" ]
}

@test "refuses when the manifests differ by more than the pin" {
    make_conflict "$F" "$(pkg 2.30.2)" \
      "$(pkg 2.37.1 | jq -c '.dependencies.react="19.1.0"')" \
      "$(pkg 2.36.0 | jq -c '.dependencies.react="19.2.0"')"
    run "$R" --resolve "$F"
    [ "$status" -eq 2 ]
    [[ "$output" == *"more than"* ]]
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `bats bin/conflict/test/spec-client-version.bats`
Expected: FAIL — script does not exist.

- [ ] **Step 4: Write the implementation**

Create `bin/conflict/spec-client-version`:

```bash
#!/usr/bin/env bash
# Resolve a conflict on the generated API client's pin in a package.json.
#
# The branch's pin always wins. A higher version number on trunk is not
# evidence that the branch's own server change has landed: extract_webaccess
# pins a snapshot carrying WebKeysApi, which trunk's higher 2.37.1 release does
# not contain at all. Moving a client off a snapshot is api-bump step 10 and
# stays a deliberate act.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"

CLAIMS='package.json */*/package.json'
PKG='@telcred/spec-telcredv2-typescript-axios'

pinned() { jq -r --arg p "$PKG" '.dependencies[$p] // .devDependencies[$p] // empty' "$1"; }

resolved() {
    local file=$1 base trunk branch bv tv blank blank_t blank_b
    base=$(mktemp) trunk=$(mktemp) branch=$(mktemp)
    # shellcheck disable=SC2064
    trap "rm -f '$base' '$trunk' '$branch'" RETURN

    stage_base   "$file" > "$base"
    stage_trunk  "$file" > "$trunk"
    stage_branch "$file" > "$branch"

    bv=$(pinned "$branch"); tv=$(pinned "$trunk")
    [[ -n $bv && -n $tv ]] || refuse "$file: does not pin $PKG on both sides"

    # With the pin blanked, the two sides must be identical. Anything else is a
    # real manifest disagreement.
    local blank='if .dependencies[$p]    then .dependencies[$p]    = "X" else . end
               | if .devDependencies[$p] then .devDependencies[$p] = "X" else . end'
    blank_t=$(jq -S --arg p "$PKG" "$blank" "$trunk")
    blank_b=$(jq -S --arg p "$PKG" "$blank" "$branch")
    [[ $blank_t == "$blank_b" ]] || refuse "$file: the two sides differ by more than the $PKG pin"

    if [[ $bv == *-snapshot.* ]]; then
        printf '%s still pins the snapshot %s; trunk is on %s. Moving off a snapshot is api-bump step 10.\n' \
            "$file" "$bv" "$tv" >&2
    fi

    printf '%s\n' "$branch"
}

check_it()   { resolved "$1" >/dev/null; }
resolve_it() { local f=$1 src; src=$(resolved "$f"); cp "$src" "$f"; }

conflict_main "$CLAIMS" check_it resolve_it "$@"
```

- [ ] **Step 5: Make executable and run the tests**

```bash
chmod +x bin/conflict/spec-client-version
bats bin/conflict/test/spec-client-version.bats
```

Expected: 4 passing.

- [ ] **Step 6: Verify against the live branch**

```bash
git show "feat_wt/extract_webaccess:apps/access-manager/package.json" \
  | jq -r '.dependencies["@telcred/spec-telcredv2-typescript-axios"]'
git show "origin/main:apps/access-manager/package.json" \
  | jq -r '.dependencies["@telcred/spec-telcredv2-typescript-axios"]'
```

Expected: `2.36.0-snapshot.20260831123245` and `2.37.1`. The resolver must keep the first.

- [ ] **Step 7: Commit**

```bash
git add bin/conflict/
git commit -m "feat(conflict): keep the branch pin on an sdk client conflict"
```

---

### Task 7: `pnpm-lock` — never merge the lockfile

**Repo:** `accessmanager`

**Files:**
- Create: `bin/conflict/pnpm-lock`
- Create: `bin/conflict/test/pnpm-lock.bats`

**Interfaces:**
- Consumes: `lib.sh` from Task 6.
- Produces: an executable claiming `pnpm-lock.yaml`. It takes trunk's lockfile and leaves the deferred `pnpm install` to reconcile it; it never merges lock content.

- [ ] **Step 1: Write the failing test**

Create `bin/conflict/test/pnpm-lock.bats`:

```bash
#!/usr/bin/env bats

load helper

R="${BATS_TEST_DIRNAME}/../pnpm-lock"
F=pnpm-lock.yaml

@test "takes trunk's lockfile wholesale" {
    make_conflict "$F" "lockfileVersion: 9.0\nbase\n" "lockfileVersion: 9.0\ntrunk\n" "lockfileVersion: 9.0\nbranch\n"
    run "$R" --resolve "$F"
    [ "$status" -eq 0 ]
    run cat "$F"
    [[ "$output" == *"trunk"* ]]
    [[ "$output" != *"branch"* ]]
    [[ "$output" != *"<<<"* ]]
}

@test "stages the file and says an install is still owed" {
    make_conflict "$F" "a\n" "b\n" "c\n"
    run "$R" --resolve "$F"
    [[ "$output" == *"pnpm install"* ]]
    run git diff --cached --name-only
    [[ "$output" == *"pnpm-lock.yaml"* ]]
}

@test "claims only the lockfile" {
    run "$R" --claims
    [ "$output" = "pnpm-lock.yaml" ]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bats bin/conflict/test/pnpm-lock.bats`
Expected: FAIL — script does not exist.

- [ ] **Step 3: Write the implementation**

Create `bin/conflict/pnpm-lock`:

```bash
#!/usr/bin/env bash
# Resolve a pnpm-lock.yaml conflict by taking trunk's copy.
#
# A lockfile is generated, and merging one by hand produces a file that
# resolves to a tree nobody has ever installed. Trunk's copy is the newer and
# more widely tested of the two; the deferred `pnpm install` declared in
# .wt-sync.yaml reconciles it against the merged manifests once, at the end of
# the rebase.
set -euo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$DIR/lib.sh"

CLAIMS='pnpm-lock.yaml'

check_it() { stage_trunk "$1" >/dev/null; }

resolve_it() {
    local f=$1 tmp
    tmp=$(mktemp)
    stage_trunk "$f" > "$tmp"
    mv "$tmp" "$f"
    printf '%s: took trunk. A deferred `pnpm install` still owes the reconciliation.\n' "$f" >&2
}

conflict_main "$CLAIMS" check_it resolve_it "$@"
```

- [ ] **Step 4: Make executable and run the tests**

```bash
chmod +x bin/conflict/pnpm-lock
bats bin/conflict/test/pnpm-lock.bats
```

Expected: 3 passing.

- [ ] **Step 5: Commit**

```bash
git add bin/conflict/pnpm-lock bin/conflict/test/pnpm-lock.bats
git commit -m "feat(conflict): take trunk's lockfile and defer the install"
```

---

### Task 8: `accessmanager/.wt-sync.yaml`, and `bump_axios_client.sh` delegating

**Repo:** `accessmanager`

**Files:**
- Create: `.wt-sync.yaml`
- Modify: `bin/bump_axios_client.sh` — its marker check and pin collapse call the resolver
- Create: `bin/conflict/test/bump-delegation.bats`

**Interfaces:**
- Consumes: `bin/conflict/spec-client-version` (Task 6), `pnpm-lock` (Task 7).
- Produces: `.wt-sync.yaml`, read by `wt sync` from trunk.

**Two things must not change.** `bump_axios_client.sh` updates **all five** manifests in one pass and then runs `pnpm i` once — a per-file resolver would lose that grouping, so the script keeps the loop and calls the resolver per file inside it. And the script's version *selection* (tag listing, `latest`, an explicit argument) stays where it is; the resolver takes no version.

- [ ] **Step 1: Write the characterisation test**

Create `bin/conflict/test/bump-delegation.bats`:

```bash
#!/usr/bin/env bats

SCRIPT="${BATS_TEST_DIRNAME}/../../bump_axios_client.sh"

@test "the resolver is what collapses a pin conflict" {
    grep -q 'bin/conflict/spec-client-version' "$SCRIPT"
}

@test "all manifests are still updated in one pass before a single install" {
    grep -q 'PKG_FILES' "$SCRIPT"
    run bash -c "grep -c 'pnpm i' '$SCRIPT'"
    [ "$output" -ge 1 ]
}

@test "version selection still lives in the bump script" {
    grep -qE 'latest|refs/tags|NEWV' "$SCRIPT"
}
```

- [ ] **Step 2: Run to verify the first test fails**

Run: `bats bin/conflict/test/bump-delegation.bats`
Expected: the "resolver is what collapses" test FAILS.

- [ ] **Step 3: Write `.wt-sync.yaml`**

Create `.wt-sync.yaml` at the repo root:

```yaml
# How wt sync rebases this repository. Read from trunk, never from the branch
# being rebased — these entries name executables.

resolvers:
  - bin/conflict/spec-client-version
  - bin/conflict/pnpm-lock

# Keyed on what the rebase changed, not on what conflicted. feat_wt/arch and
# april-fools take trunk-side lockfile changes with no lock conflict at all,
# and a defer keyed on conflicts would leave node_modules stale.
#
# This install cannot use --frozen-lockfile, unlike the one in
# bin/worktree/worktree.conf: the lockfile is the thing being reconciled.
defer:
  - run: pnpm install
    when: paths-changed(pnpm-lock.yaml, '**/package.json')
    commit: "chore(deps): reconcile the lockfile after rebase"
  - run: pnpm run generate-git-info
    when: head-changed
    commit: "chore(build): regenerate git info after rebase"

verify:
  - git diff --exit-code pnpm-lock.yaml

# See server/.wt-sync.yaml. A pin change is routine; anything else in a manifest
# means the branch is building against different libraries than trunk.
dependency_graph:
  - pnpm-lock.yaml
  - "**/package.json"
```

`generate-git-info` embeds the HEAD hash, branch and timestamp, so every rebase invalidates it. It is keyed on `head-changed` because that is literally when it goes stale.

- [ ] **Step 4: Delegate from `bump_axios_client.sh`**

Read `bin/bump_axios_client.sh` first — the relevant part is the marker check and `pnpm i` block around lines 165–196. Inside the existing `for f in "${PKG_FILES[@]}"` loop, replace the hand-rolled marker refusal with the resolver, and leave the single `pnpm i` after the loop exactly where it is:

```bash
# A file still carrying stages is a conflict, and the resolver owns it. Doing
# it here means a conflict collapsed by hand and one collapsed during an
# automated rebase come out identical.
if git ls-files -u -- "$f" | grep -q .; then
    if ! "${BASE_DIR}/bin/conflict/spec-client-version" --resolve "$f"; then
        echo "$f conflicts by more than the $PKG pin. Resolve it by hand, then re-run."
        exit 1
    fi
fi
```

- [ ] **Step 5: Run every test**

```bash
bats bin/conflict/test/
```

Expected: all tests across all three files pass.

- [ ] **Step 6: Commit**

```bash
git add .wt-sync.yaml bin/bump_axios_client.sh bin/conflict/test/bump-delegation.bats
git commit -m "feat(conflict): declare wt sync config and delegate the pin collapse"
```

---

## What this plan leaves for the next one

`wt sync` itself — triage over `git merge-tree`, the classification table
including the `divergent` class and `wt sync campaign`, rebase execution with `--no-update-refs --no-gpg-sign --rerere-autoupdate`, safety refs and retention, the worktree lock, ordered stack rebasing (`perf_wt/pruning_keyset_index` → `feat_wt/pruning_cron` is live today), the plan file, the read-only table, the fzf picker, `wt sync keep`, and the negotiation protocol. Then the `wt-sync` skill and the `api-bump` amendment.

Nothing in that list changes the resolver contract, which is why the resolvers can land and be used by hand first.
