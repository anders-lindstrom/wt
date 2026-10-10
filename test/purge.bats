#!/usr/bin/env bats

load helpers
bats_require_minimum_version 1.5.0

# A repo with one worktree moved aside into a folder, the way
# wt remove --move-to leaves it.
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
    wt remove aside --yes --move-to "$ROOT/trash/aside" >/dev/null
    Q="$ROOT/trash/aside"
}

@test "purge with no terminal and no --yes deletes nothing" {
    run wt purge "$Q"
    [ "$status" -ne 0 ]
    [[ "$output" == *"pass --yes"* ]]
    [ -d "$Q/checkout" ]
    [ -n "$(git -C "$REPO" for-each-ref refs/wt-quarantine/)" ]
}

@test "purge --yes deletes the folder and the pins, from anywhere" {
    cd "$ROOT"
    run wt purge "$Q" --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"✓ purged $Q"* ]]
    [ ! -e "$Q" ]
    [ -z "$(git -C "$REPO" for-each-ref refs/wt-quarantine/)" ]
}

@test "purge --json prints the plan and deletes nothing" {
    run --separate-stderr wt purge "$Q" --json
    [ "$status" -eq 0 ]
    [[ "$output" == *'"command": "quarantine purge"'* ]]
    [[ "$output" == *'"state": "quarantined"'* ]]
    [[ "$output" == *'"token": "1:'* ]]
    [ -d "$Q/checkout" ]
}

@test "purge refuses a folder no worktree was moved to" {
    mkdir -p "$ROOT/plain"
    echo keep > "$ROOT/plain/keep.txt"
    run wt purge "$ROOT/plain" --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"no recovery.json"* ]]
    [ -f "$ROOT/plain/keep.txt" ]
}

@test "purge of a run id points to wt refs purge, and wt refs purge of a folder back" {
    run wt purge 20260930T091500Z-3f2a --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"wt refs purge 20260930T091500Z-3f2a"* ]]

    run wt refs purge "$Q" --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"wt purge $Q"* ]]
    [ -d "$Q/checkout" ]
}

# wt purge was wt quarantine purge.
@test "quarantine purge is an unknown command, and deletes nothing" {
    refs=$(git -C "$REPO" for-each-ref)

    for flags in "" "--yes" "--yes --json"; do
        run --separate-stderr wt quarantine purge "$Q" $flags
        [ "$status" -ne 0 ]
        [[ "$output" == "" ]]
        [[ "$stderr" == *'unknown command "quarantine"'* ]]
    done

    [ -d "$Q/checkout" ]
    [ -f "$Q/recovery.json" ]
    [ "$(git -C "$REPO" for-each-ref)" = "$refs" ]
}

# --move-to on wt remove and wt sweep was --quarantine.
@test "--quarantine is an unknown flag on remove and sweep, and moves nothing" {
    wt new fix/again >/dev/null
    refs=$(git -C "$REPO" for-each-ref)

    for line in "remove again --yes --quarantine $ROOT/trash/again" \
        "rm again --yes --quarantine=$ROOT/trash/again" \
        "sweep --no-fetch --yes --quarantine $ROOT/trash/sw" \
        "sweep --no-fetch --yes --quarantine=$ROOT/trash/sw"; do
        run wt $line
        [ "$status" -ne 0 ]
        [[ "$output" == *"unknown flag: --quarantine"* ]]
    done

    [ -d "$ROOT/demo_wt/fix_wt/again" ]
    [ ! -e "$ROOT/trash/again" ]
    [ ! -e "$ROOT/trash/sw" ]
    [ "$(git -C "$REPO" for-each-ref)" = "$refs" ]
}
