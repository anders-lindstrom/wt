#!/usr/bin/env bats

# wt new and wt checkout for a tool: the plan, the run held to it with
# --expect, and exactly one JSON object on stdout whatever happens.
bats_require_minimum_version 1.5.0

load helpers

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    REPO="$BATS_TEST_TMPDIR/demo"
    make_repo "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
}

token_of() {
    sed -n 's/^  "token": "\(.*\)",$/\1/p'
}

@test "wt new --dry-run --json plans, and --json --expect creates what it planned" {
    cd "$REPO"
    run --separate-stderr wt new fix/login-crash --dry-run --json
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "{" ]
    [ ! -e "$BATS_TEST_TMPDIR/demo_wt/fix_wt/login-crash" ]
    token=$(printf '%s\n' "$output" | token_of)
    [ -n "$token" ]

    run --separate-stderr wt new fix/login-crash --json --expect "$token" --no-build
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "{" ]
    [ "${lines[${#lines[@]}-1]}" = "}" ]
    [[ "$output" == *'"outcome": "created"'* ]]
    [[ "$output" != *Creating* ]]
    [[ "$stderr" == *"Creating fix_wt/login-crash"* ]]
    [ -d "$BATS_TEST_TMPDIR/demo_wt/fix_wt/login-crash" ]
}

@test "wt checkout --json --expect refuses a branch that moved since the plan" {
    cd "$REPO"
    git branch release-2.1
    token=$(wt checkout release-2.1 --dry-run --json | token_of)
    git commit -q --allow-empty -m moved
    git branch -f release-2.1 HEAD
    run --separate-stderr wt checkout release-2.1 --json --expect "$token"
    [ "$status" -ne 0 ]
    [[ "$output" == *'"outcome": "refused"'* ]]
    [[ "$output" == *'"code": "planChanged"'* ]]
    [ ! -e "$BATS_TEST_TMPDIR/demo_wt/feat_wt/release-2.1" ]
}

@test "a SIGTERM during wt new --json writes the one object, stops the script, exits 130" {
    printf '#!/bin/sh\nsleep 300 &\necho $! > "%s/sleeper"\ntouch "%s/started"\nwait\n' "$BATS_TEST_TMPDIR" "$BATS_TEST_TMPDIR" > "$REPO/bin/worktree/provision.sh"
    chmod +x "$REPO/bin/worktree/provision.sh"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm provision
    cd "$REPO"
    wt new fix/slow --json --no-build > "$BATS_TEST_TMPDIR/out.json" 2> "$BATS_TEST_TMPDIR/err.txt" &
    pid=$!
    for _ in $(seq 100); do [ -e "$BATS_TEST_TMPDIR/started" ] && break; sleep 0.1; done
    [ -e "$BATS_TEST_TMPDIR/started" ]
    kill -TERM "$pid"
    # Not `run wait`: bats before 1.11 runs that in a subshell, where the job
    # is not a child and wait answers 255 whatever wt exited with.
    status=0
    wait "$pid" || status=$?
    [ "$status" -eq 130 ]
    out=$(cat "$BATS_TEST_TMPDIR/out.json")
    [[ "$out" == *'"outcome": "interrupted"'* ]]
    [[ "$out" == *'"result": "interrupted"'* ]]
    [ "$(grep -c '"schema": 1' "$BATS_TEST_TMPDIR/out.json")" -eq 1 ]
    # The script's own children go with it: nothing keeps writing to the
    # worktree after the object says it stopped.
    sleep 0.5
    ! kill -0 "$(cat "$BATS_TEST_TMPDIR/sleeper")" 2>/dev/null
}

@test "an empty --expect is refused, not taken as no --expect" {
    cd "$REPO"
    run --separate-stderr wt new fix/x --json --expect ""
    [ "$status" -ne 0 ]
    [[ "$output" == *'"outcome": "refused"'* ]]
    [ ! -e "$BATS_TEST_TMPDIR/demo_wt/fix_wt/x" ]
}

@test "wt new --expect without --json is refused before anything is made" {
    cd "$REPO"
    run wt new fix/x --expect 1:0123abcd
    [ "$status" -ne 0 ]
    [[ "$output" == *"--expect goes with --json"* ]]
    [ ! -e "$BATS_TEST_TMPDIR/demo_wt/fix_wt/x" ]
}
