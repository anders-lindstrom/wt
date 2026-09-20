#!/usr/bin/env bats

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
