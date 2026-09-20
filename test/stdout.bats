#!/usr/bin/env bats

# The one contract every worktree-creating command has: stdout is the path
# alone, so `cd "$(wt new fix/login-crash)"` works. Everything an integration
# has to say goes to stderr, and `run --separate-stderr` is what tells the two
# apart — a merged run, or `tail -1`, proves nothing.
bats_require_minimum_version 1.5.0

load helpers

setup() {
    FAKEBIN="$BATS_TEST_TMPDIR/bin"
    mkdir -p "$FAKEBIN"
    export PATH="$FAKEBIN:$BATS_TEST_DIRNAME/../bin:$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    REPO="$BATS_TEST_TMPDIR/demo"
    make_repo "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
}

# fake_superset writes a `superset` that knows this repository, has its host
# service running, and adopts whatever branch it is handed. It talks on stderr
# as the real one does; none of that may reach wt's stdout.
fake_superset() {
    cat > "$FAKEBIN/superset" <<GH
#!/bin/sh
echo "superset chatter on stderr" >&2
case "\$1 \$2" in
  'status --json') echo '{"running":true}' ;;
  'projects list') echo '[{"id":"p1","name":"demo","path":"$REPO"}]' ;;
  'ws create') echo '{"workspace":{"id":"w1"},"alreadyExists":false}' ;;
  '--version ') echo 1.29.0 ;;
esac
GH
    chmod +x "$FAKEBIN/superset"
    wt config set superset true > /dev/null
}

# only_path asserts that stdout is exactly one existing worktree path, and
# that the registration was heard on stderr.
only_path() {
    [ "${#lines[@]}" -eq 1 ]
    [ -d "$output" ]
    [[ "$output" != *superset* ]]
    [[ "$stderr" == *"registered with Superset"* ]]
}

@test "wt new writes the path alone to stdout, with Superset talking on stderr" {
    fake_superset
    cd "$REPO"
    run --separate-stderr wt new fix/login-crash --skip-build
    [ "$status" -eq 0 ]
    only_path
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt checkout writes the path alone to stdout" {
    fake_superset
    cd "$REPO"
    git -C "$REPO" branch fix_wt/from-elsewhere
    run --separate-stderr wt checkout fix_wt/from-elsewhere --skip-build
    [ "$status" -eq 0 ]
    only_path
    [[ "$output" == */demo_wt/*/from-elsewhere ]]
}

@test "a Superset that will not answer still leaves stdout the path alone" {
    cat > "$FAKEBIN/superset" <<'GH'
#!/bin/sh
echo 'the host service is not running' >&2
exit 1
GH
    chmod +x "$FAKEBIN/superset"
    wt config set superset true > /dev/null
    cd "$REPO"
    run --separate-stderr wt new fix/login-crash --skip-build
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [ -d "$output" ]
    [[ "$stderr" == *"not registered"* ]]
}

