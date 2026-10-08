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

# The stand-in codex of the last test, should the test stop before killing it.
teardown() {
    [ -z "${CODEX_PID:-}" ] || kill "$CODEX_PID" 2>/dev/null || true
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
    [[ "$output" == *"  sync    recipe · wt sync rebase bump"* ]]
    [[ "$output" == *"simulated against origin/main as last fetched"* ]]

    cd "$REPO"
    run wt status .
    [ "$status" -ne 0 ]
    [[ "$output" == *"the main checkout is not a worktree"* ]]
}

# A sleep that calls itself codex and stands in the worktree: the real ps,
# and the real lsof or /proc, are asked what it is and where. With no session
# log to read, an interactive codex is not known to be idle, so it is busy.
@test "a codex working in a worktree is a busy session, listed and refused under" {
    export CODEX_HOME="$BATS_TEST_TMPDIR/codex-home"
    (cd "$BUMP" && exec -a codex sleep 300) &
    CODEX_PID=$!
    # The subshell has to reach the worktree and become codex first.
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        run wt status bump --json
        [[ "$output" == *'"kind": "codex"'* ]] && break
        sleep 0.2
    done
    [ "$status" -eq 0 ]
    [[ "$output" == *'"name": "codex"'* ]]
    [[ "$output" == *'"kind": "codex"'* ]]
    [[ "$output" == *'"state": "busy"'* ]]
    [[ "$output" == *'"sessionsError": null'* ]]

    run wt up bump
    kill "$CODEX_PID"
    [ "$status" -ne 0 ]
    [[ "$output" == *"codex"* ]]
    [[ "$output" == *"busy in it"* ]]
}
