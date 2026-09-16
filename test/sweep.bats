#!/usr/bin/env bats

load helpers

# A repo that is its own origin, with a plain branch trunk contains, a clean
# worktree on a branch cut from trunk and never committed to, and a dirty one
# on the same footing: sweep deletes the branch, removes the clean worktree
# with its branch, and keeps the dirty one saying why.
setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var, so git reports
    # a different path than the one bats handed us and every comparison fails.
    WT_ROOTS="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    export WT_ROOTS
    REPO="$WT_ROOTS/demo"
    make_repo "$REPO"
    echo hello > "$REPO/README.md"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm init
    git -C "$REPO" remote add origin "$REPO"
    git -C "$REPO" fetch -q origin
    git -C "$REPO" remote set-head origin main
    git -C "$REPO" branch done-work
    cd "$REPO"
    wt new fix/clean >/dev/null
    wt new fix/dirty >/dev/null
    CLEAN="$WT_ROOTS/demo_wt/fix_wt/clean"
    DIRTY="$WT_ROOTS/demo_wt/fix_wt/dirty"
    echo scratch > "$DIRTY/scratch.txt"
    cd "$REPO"
}

@test "sweep removes a clean merged worktree with its branch and keeps a dirty one" {
    # bats gives the sweep no terminal: the plan is printed and nothing goes.
    run wt sweep --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"Will be removed with its branch, 1 worktree:"* ]]
    [[ "$output" == *"clean  fix_wt/clean  $CLEAN  merged into origin/main"* ]]
    [[ "$output" == *"Will be deleted, 1 branch:"* ]]
    [[ "$output" == *"fix_wt/dirty  dirty; commit or discard the changes, then sweep again"* ]]
    [[ "$output" == *"Pass --yes to sweep 1 branch and 1 worktree."* ]]
    [ -d "$CLEAN" ]

    run wt sweep --no-fetch --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"✓ worktree removed; branch fix_wt/clean was merged into origin/main and has been deleted"* ]]
    [[ "$output" == *"✓ deleted done-work"* ]]
    [ ! -d "$CLEAN" ]
    [ -d "$DIRTY" ]
    ! git -C "$REPO" show-ref --verify --quiet refs/heads/fix_wt/clean
    git -C "$REPO" show-ref --verify --quiet refs/heads/fix_wt/dirty

    # Only the kept worktree is left: nothing to sweep, and the row says why.
    run wt sweep --no-fetch --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"No merged branches or worktrees to sweep."* ]]
    [[ "$output" == *"fix_wt/dirty  dirty"* ]]
}
