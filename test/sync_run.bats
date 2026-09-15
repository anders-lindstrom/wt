#!/usr/bin/env bats

load helpers

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
    make_repo "$REPO"
    printf 'conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '"'"'^\\d'"'"'\n    rule: max-plus-patch\n' > "$REPO/.wt-sync.yaml"
    printf '1.0.0\n' > "$REPO/v.txt"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm declare
    cd "$REPO"
    wt new bump >/dev/null
    BUMP="$WT_ROOTS/demo_wt/feat_wt/bump"
    printf '1.0.1\n' > "$BUMP/v.txt"
    git -C "$BUMP" commit -qam bump
    printf '1.0.5\n' > "$REPO/v.txt"
    git -C "$REPO" commit -qam "trunk bump"
    # The repository is its own origin, so origin/main exists for --no-fetch.
    git -C "$REPO" remote add origin "$REPO"
    git -C "$REPO" fetch -q origin
    cd "$REPO"
}

@test "sync run rebases a recipe worktree, sync goes quiet, undo puts it back" {
    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"against origin/main "* ]]
    [[ "$output" != *"fetched"* ]]
    [[ "$output" == *"bump"*"recipe"* ]]

    run wt sync --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"as last fetched"* ]]

    run wt sync run bump --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"(as last fetched"* ]]
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
    [[ "$output" == *"onto origin/main"*"(fetched)"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]
    # bats gives the run no terminal: nothing is asked or pushed, the command is printed.
    [[ "$output" == *"push: git -C"*"--force-with-lease --force-if-includes"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    run wt sync undo bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"→"* ]]
}

# The same run and undo spelled as flags at the end of the wt sync line, with
# the verb's own flags reaching it: --push pushes without asking, --force
# rewinds a branch that moved after the run.
@test "sync <work> --run and --undo are the verbs spelled on wt sync" {
    run wt sync bump --run --no-fetch --push
    [ "$status" -eq 0 ]
    [[ "$output" == *"(as last fetched"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]
    [[ "$output" == *"✓ pushed feat_wt/bump"*"→"* ]]
    [[ "$output" != *"push: git -C"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    echo more > "$BUMP/more.txt"
    git -C "$BUMP" add more.txt
    git -C "$BUMP" commit -qm "moved since the run"
    run wt sync bump --undo
    [ "$status" -eq 1 ]
    [[ "$output" == *"has moved since that run"*"--force"* ]]

    run wt sync bump --undo --force
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump  "*"→"*"kept under refs/wt-sync/feat_wt/bump/"* ]]

    run wt sync bump --push
    [ "$status" -eq 1 ]
    [[ "$output" == "wt: --push needs --run or --resume" ]]
}

# The handover of the test below, resumed with the flag spelling.
@test "sync <work> --resume is sync resume <work>" {
    printf 'branch\n' > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    git -C "$BUMP" commit -qm "a on the branch"
    printf 'trunk\n' > "$REPO/a.txt"
    git -C "$REPO" add a.txt
    git -C "$REPO" commit -qm "a on trunk"
    git -C "$REPO" fetch -q origin

    run wt sync bump --run --no-fetch --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"needs you"* ]]
    GITDIR="$(git -C "$BUMP" rev-parse --absolute-git-dir)"
    [ -f "$GITDIR/wt-sync-plan.md" ]

    echo resolved > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    run wt sync bump --resume --no-push
    [ "$status" -eq 0 ]
    [[ "$output" == *"push: git -C"*"--force-with-lease --force-if-includes"* ]]
    [ ! -d "$GITDIR/rebase-merge" ]
    [ ! -f "$GITDIR/wt-sync-plan.md" ]
    [ "$(git -C "$BUMP" symbolic-ref HEAD)" = "refs/heads/feat_wt/bump" ]
}

# A handover end to end: a.txt moved on both sides and nothing claims it, so
# the run carries the v.txt stop, stops contested on the a.txt stop and leaves
# the plan; the table reports the handover, and resume finishes the rebase
# once the person's file is staged.
@test "sync run hands a contested stop over and resume finishes it" {
    printf 'branch\n' > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    git -C "$BUMP" commit -qm "a on the branch"
    printf 'trunk\n' > "$REPO/a.txt"
    git -C "$REPO" add a.txt
    git -C "$REPO" commit -qm "a on trunk"
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
