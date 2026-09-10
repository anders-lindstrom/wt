#!/usr/bin/env bats

# A repo whose trunk declares v.txt owned-line max-plus-patch, with a
# worktree one commit ahead of trunk on v.txt and trunk itself bumped past
# it: `sync` sees a recipe, `run` clears it, `undo` puts it back.
setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var, so git reports
    # a different path than the one bats handed us and every comparison fails.
    WT_ROOTS="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    export WT_ROOTS
    REPO="$WT_ROOTS/demo"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$REPO/bin/worktree/worktree.conf"
    printf 'conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '"'"'^\\d'"'"'\n    rule: max-plus-patch\n' > "$REPO/.wt-sync.yaml"
    printf '1.0.0\n' > "$REPO/v.txt"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name T
    # The repository is its own origin, so origin/main exists for --no-fetch;
    # its own hooks dir is pinned so a hooksPath set globally on the
    # developer's machine cannot run against the fixture.
    git -C "$REPO" config core.hooksPath "$REPO/.git/hooks"
    git -C "$REPO" add -A
    git -C "$REPO" -c commit.gpgsign=false commit -qm declare
    cd "$REPO"
    wt new bump >/dev/null
    BUMP="$WT_ROOTS/demo_wt/feat_wt/bump"
    printf '1.0.1\n' > "$BUMP/v.txt"
    git -C "$BUMP" -c commit.gpgsign=false commit -qam bump
    printf '1.0.5\n' > "$REPO/v.txt"
    git -C "$REPO" -c commit.gpgsign=false commit -qam "trunk bump"
    git -C "$REPO" remote add origin "$REPO"
    git -C "$REPO" fetch -q origin
    cd "$REPO"
}

@test "sync run rebases a recipe worktree, sync goes quiet, undo puts it back" {
    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"recipe"* ]]

    run wt sync run bump --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"rebased 1 commit"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    run wt sync undo bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"→"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"recipe"* ]]
}

# The same flow with the fetch left in. The demo repo is its own origin, so
# `git fetch origin main` is a local, deterministic no-op that still proves
# the fetch path runs and does not change the outcome.
@test "sync run fetches trunk first and rebases the same worktree" {
    run wt sync run bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"fetched origin/main"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    run wt sync undo bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"→"* ]]
}

# A handover end to end: a.txt moved on both sides and nothing claims it, so
# the run carries the v.txt stop, stops contested on the a.txt stop and leaves
# the plan; the table reports the handover, and resume finishes the rebase
# once the person's file is staged.
@test "sync run hands a contested stop over and resume finishes it" {
    printf 'branch\n' > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    git -C "$BUMP" -c commit.gpgsign=false commit -qm "a on the branch"
    printf 'trunk\n' > "$REPO/a.txt"
    git -C "$REPO" add a.txt
    git -C "$REPO" -c commit.gpgsign=false commit -qm "a on trunk"
    git -C "$REPO" fetch -q origin

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"contested"*"2/2"*"a.txt✗"* ]]

    run wt sync run bump --no-fetch --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"needs you"* ]]
    GITDIR="$(git -C "$BUMP" rev-parse --absolute-git-dir)"
    [ -d "$GITDIR/rebase-merge" ]
    [ -f "$GITDIR/wt-sync-plan.md" ]
    [ -f "$GITDIR/wt-sync-state.json" ]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"contested"*"wt sync resume, or wt sync undo"* ]]
    [[ "$output" != *"dirty"* ]]

    echo resolved > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    run wt sync resume bump
    [ "$status" -eq 0 ]
    [ ! -d "$GITDIR/rebase-merge" ]
    [ ! -f "$GITDIR/wt-sync-state.json" ]
    [ ! -f "$GITDIR/wt-sync-plan.md" ]
    [ "$(git -C "$BUMP" symbolic-ref HEAD)" = "refs/heads/feat_wt/bump" ]
    [ "$(git -C "$BUMP" show HEAD:a.txt)" = "resolved" ]
    [ "$(git -C "$BUMP" show HEAD:v.txt)" = "1.0.6" ]
    git -C "$BUMP" merge-base --is-ancestor origin/main HEAD

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]
}
