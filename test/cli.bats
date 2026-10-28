#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

load helpers

setup() {
    export PATH="$(cd "$BATS_TEST_DIRNAME/../bin" && pwd):$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/config"
    cd "$BATS_TEST_TMPDIR"
}

@test "usage errors exit 2 and runtime failures exit 1" {
    for args in 'new' 'version extra' 'new --bogus' 'wibble' 'remove' 'remove foo --yes --dry-run'; do
        # These fixtures deliberately contain only simple words.
        read -r -a words <<< "$args"
        run --separate-stderr wt "${words[@]}"
        [ "$status" -eq 2 ]
        [ -z "$output" ]
        [[ "$stderr" == *'wt: '* ]]
    done
    run --separate-stderr wt config
    [ "$status" -eq 1 ]
    [ -z "$output" ]
}

@test "version flag matches version command and --help works after --" {
    version=$(wt version)
    run --separate-stderr wt --version
    [ "$status" -eq 0 ]
    [ "$output" = "$version" ]
    [ -z "$stderr" ]
    run --separate-stderr wt new --help
    [ "$status" -eq 0 ]
    [[ "$output" == *'Usage:'* ]]
    [ -z "$stderr" ]
    run --separate-stderr wt new --bogus -- --help
    [ "$status" -eq 2 ]
}
