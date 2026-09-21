#!/usr/bin/env bats

# `run --separate-stderr` is how the warning tests below tell the two streams
# apart.
bats_require_minimum_version 1.5.0

load helpers

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    # A directory of this test's own, so a set here cannot reach another test.
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    USER_CONFIG="$XDG_CONFIG_HOME/wt/config.toml"
    REPO="$BATS_TEST_TMPDIR/demo"
    make_repo "$REPO"
}

@test "the user settings work from outside any repository" {
    cd "$BATS_TEST_TMPDIR"
    run wt config path
    [ "$status" -eq 0 ]
    [ "$output" = "$USER_CONFIG" ]
    [ ! -e "$USER_CONFIG" ]

    run wt config get superset
    [ "$status" -eq 0 ]
    [ "$output" = "false" ]

    run wt config set superset true
    [ "$status" -eq 0 ]
    [[ "$output" == "superset = true in $USER_CONFIG" ]]

    run wt config get superset
    [ "$output" = "true" ]

    run wt config unset superset
    [ "$status" -eq 0 ]
    run wt config get superset
    [ "$output" = "false" ]
}

@test "wt config prints the user settings and where they came from" {
    cd "$REPO"
    run wt config
    [ "$status" -eq 0 ]
    [[ "$output" == *"superset:    false (default)"* ]]
    [[ "$output" == *"github:      true (default)"* ]]
    [[ "$output" == *"superset mode: off (user config)"* ]]

    wt config set superset true
    run wt config
    [[ "$output" == *"superset:    true (user file)"* ]]
    [[ "$output" == *"superset mode: auto (repo default)"* ]]
}

@test "an invalid key or value is refused and writes nothing" {
    cd "$BATS_TEST_TMPDIR"
    run wt config set superst true
    [ "$status" -eq 1 ]
    [[ "$output" == *'unknown key "superst"'* ]]
    [ ! -e "$USER_CONFIG" ]

    run wt config set superset sometimes
    [ "$status" -eq 1 ]
    [[ "$output" == *"true or false"* ]]
    [ ! -e "$USER_CONFIG" ]
}

@test "wt doctor says Superset is off at user level, without probing" {
    cd "$REPO"
    git commit -qm init --allow-empty
    run wt doctor
    [ "$status" -eq 0 ]
    [[ "$output" == *"off in your wt config; turn it on with \`wt config set superset true\`"* ]]
    [[ "$output" != *"host service"* ]]
}

@test "one bad key in the user file is ignored and the good ones stand" {
    mkdir -p "$(dirname "$USER_CONFIG")"
    printf 'superset = true\nsuperst = true\n' > "$USER_CONFIG"
    cd "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
    wt new fix/login-crash --no-setup > /dev/null 2> /dev/null

    run --separate-stderr wt list
    [ "$status" -eq 0 ]
    [[ "$output" == *"login-crash"* ]]
    [[ "$stderr" == *"wt: ignoring "* ]]
    [[ "$stderr" != *"default settings"* ]]
    [[ "$stderr" == *"$USER_CONFIG"* ]]
    [[ "$stderr" == *'unknown key "superst"'* ]]
    [ "$(printf '%s\n' "$stderr" | wc -l)" -eq 1 ]

    # The key that did parse is still the person's instruction.
    run --separate-stderr wt config get superset
    [ "$status" -eq 0 ]
    [ "$output" = "true" ]

    # doctor still calls it a problem: that is where you go to find out.
    run wt doctor
    [ "$status" -ne 0 ]
    [[ "$output" == *'unknown key "superst"'* ]]

    # `wt config set` is input validation, not this, and still refuses.
    run wt config set superst true
    [ "$status" -ne 0 ]
    [[ "$output" == *'unknown key "superst"'* ]]
}

@test "a user file that does not parse turns every integration off" {
    mkdir -p "$(dirname "$USER_CONFIG")"
    printf 'github = false\nsuperset = \n' > "$USER_CONFIG"
    cd "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
    git -C "$REPO" remote add origin git@github.com:demo/demo.git
    wt new fix/login-crash --no-setup > /dev/null 2> /dev/null

    # A gh that fails the test if it is ever run. wt must not reach it: the
    # person wrote something into that file, and wt does not know what.
    tripwire="$BATS_TEST_TMPDIR/bin"
    mkdir -p "$tripwire"
    printf '#!/bin/sh\ntouch "%s/ran"\nexit 1\n' "$BATS_TEST_TMPDIR" > "$tripwire/gh"
    chmod +x "$tripwire/gh"
    PATH="$tripwire:$PATH" run --separate-stderr wt list --refresh
    [ "$status" -eq 0 ]
    [[ "$output" == *"login-crash"* ]]
    [ ! -e "$BATS_TEST_TMPDIR/ran" ]
    [[ "$stderr" == *"wt: every integration is off for this run"* ]]
    [[ "$stderr" == *"$USER_CONFIG"* ]]
    [ "$(printf '%s\n' "$stderr" | wc -l)" -eq 1 ]

    # `wt config get` still answers, with the file's problem on stderr.
    run --separate-stderr wt config get github
    [ "$status" -eq 0 ]
    [ "$output" = "false" ]
    [[ "$stderr" == *"$USER_CONFIG"* ]]

    # `wt config set` refuses to edit a file it cannot read.
    before=$(cat "$USER_CONFIG")
    run wt config set github true
    [ "$status" -ne 0 ]
    [[ "$output" == *"fix it by hand"* ]]
    [ "$(cat "$USER_CONFIG")" = "$before" ]
}

@test "branch_suffix names your branches and leaves the worktree where it was" {
    cd "$REPO"
    git commit -qm init --allow-empty

    run wt config get branch_suffix
    [ "$status" -eq 0 ]
    [ "$output" = "_wt" ]

    run wt config set branch_suffix ""
    [ "$status" -eq 0 ]
    [[ "$output" == "branch_suffix = \"\" in $USER_CONFIG" ]]

    run wt new fix/login-crash --no-setup
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    [ -d "$BATS_TEST_TMPDIR/demo_wt/fix_wt/login-crash" ]

    run git -C "$BATS_TEST_TMPDIR/demo_wt/fix_wt/login-crash" rev-parse --abbrev-ref HEAD
    [ "$output" = "fix/login-crash" ]

    run wt config
    [[ "$output" == *'branch suffix: "" (user file); worktree paths keep "_wt"'* ]]
}

@test "a repository that names its own branch suffix outranks yours" {
    cd "$REPO"
    printf 'WORKTREE_BRANCH_SUFFIX="_wt"\n' >> bin/worktree/worktree.conf
    git add -A && git commit -qm init

    wt config set branch_suffix ""
    run wt new fix/login-crash --no-setup
    [ "$status" -eq 0 ]

    run git -C "$BATS_TEST_TMPDIR/demo_wt/fix_wt/login-crash" rev-parse --abbrev-ref HEAD
    [ "$output" = "fix_wt/login-crash" ]

    run wt config
    [[ "$output" == *'branch suffix: "_wt" (repo file)'* ]]
}

@test "a branch suffix that a branch cannot carry is refused" {
    cd "$BATS_TEST_TMPDIR"
    run wt config set branch_suffix "wt/"
    [ "$status" -eq 1 ]
    [[ "$output" == *"may not contain a slash"* ]]
    [ ! -e "$USER_CONFIG" ]
}
