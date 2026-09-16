#!/usr/bin/env bats

load helpers

# A repo that is its own origin, with a worktree one commit ahead of trunk
# and trunk moved past it, so status has something to count and wt sync has
# a rebase to simulate.
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
    git -C "$REPO" remote add origin "$REPO"
    git -C "$REPO" fetch -q origin
}

@test "status counts every branch against origin/trunk as last fetched" {
    run wt status
    [ "$status" -eq 0 ]
    [[ "${lines[0]}" == "against origin/main, as last fetched"* ]]
    [[ "$output" == *"feat_wt/bump  clean  1 behind · 1 ahead  $BUMP"* ]]
}

@test "status . is the worktree you stand in, with wt sync's verdict" {
    cd "$BUMP"
    run wt status .
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "bump" ]
    [[ "$output" == *"  trunk   1 behind · 1 ahead of origin/main (as last fetched"* ]]
    [[ "$output" == *"  sync    recipe · wt sync run bump"* ]]
    [[ "$output" == *"simulated against origin/main as last fetched"* ]]

    cd "$REPO"
    run wt status .
    [ "$status" -ne 0 ]
    [[ "$output" == *"the main checkout is not a worktree"* ]]
}
