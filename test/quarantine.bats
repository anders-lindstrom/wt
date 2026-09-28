#!/usr/bin/env bats

load helpers
bats_require_minimum_version 1.5.0

# A repo with one worktree moved aside into a quarantine, the way
# wt remove --quarantine leaves it.
setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var.
    ROOT="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    REPO="$ROOT/demo"
    make_repo "$REPO"
    echo hello > "$REPO/README.md"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm init
    cd "$REPO"
    wt new fix/aside >/dev/null
    mkdir -p "$ROOT/trash"
    wt remove aside --yes --quarantine "$ROOT/trash/aside" >/dev/null
    Q="$ROOT/trash/aside"
}

@test "quarantine purge with no terminal and no --yes deletes nothing" {
    run wt quarantine purge "$Q"
    [ "$status" -ne 0 ]
    [[ "$output" == *"pass --yes"* ]]
    [ -d "$Q/checkout" ]
    [ -n "$(git -C "$REPO" for-each-ref refs/wt-quarantine/)" ]
}

@test "quarantine purge --yes deletes the folder and the pins, from anywhere" {
    cd "$ROOT"
    run wt quarantine purge "$Q" --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"✓ purged $Q"* ]]
    [ ! -e "$Q" ]
    [ -z "$(git -C "$REPO" for-each-ref refs/wt-quarantine/)" ]
}

@test "quarantine purge --json prints the plan and deletes nothing" {
    run --separate-stderr wt quarantine purge "$Q" --json
    [ "$status" -eq 0 ]
    [[ "$output" == *'"state": "quarantined"'* ]]
    [[ "$output" == *'"token": "1:'* ]]
    [ -d "$Q/checkout" ]
}

@test "quarantine purge refuses a folder that is not a quarantine" {
    mkdir -p "$ROOT/plain"
    echo keep > "$ROOT/plain/keep.txt"
    run wt quarantine purge "$ROOT/plain" --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"no recovery.json"* ]]
    [ -f "$ROOT/plain/keep.txt" ]
}
