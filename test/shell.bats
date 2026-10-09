#!/usr/bin/env bats

load helpers

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var, so git reports a
    # different path than the one bats handed us and every comparison fails.
    WT_ROOTS="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    export WT_ROOTS
    REPO="$WT_ROOTS/demo"
    make_repo "$REPO"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm init
    cd "$REPO"
    wt new fix/login-crash >/dev/null
    source "$BATS_TEST_DIRNAME/../shell/wt.sh"
}

@test "wt_dir prints the path and nothing else" {
    run wt_dir login-crash
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt_cd changes the calling shell's directory" {
    wt_cd login-crash
    [[ "$PWD" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt_exec runs in the worktree and leaves the shell put" {
    before="$PWD"
    run wt_exec login-crash pwd
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    [ "$PWD" = "$before" ]
}

@test "wt_exec propagates the command's exit code" {
    run wt_exec login-crash sh -c "exit 7"
    [ "$status" -eq 7 ]
}

@test "wt_exec preserves argument quoting" {
    run wt_exec login-crash sh -c 'printf "%s\n" "one two"'
    [ "$output" = "one two" ]
}

@test "wt_dir fails on no match" {
    run wt_dir zzz-nothing
    [ "$status" -ne 0 ]
}

@test "wt_ls lists worktrees" {
    run wt_ls
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix_wt/login-crash"* ]]
}

@test "wt_rm_me refuses to run in the main checkout" {
    cd "$REPO"
    run wt_rm_me
    [ "$status" -ne 0 ]
    [[ "$output" == *"main checkout"* ]]
}

@test "wt_rm_me removes the worktree it stands in and returns you to the repo" {
    wt_cd login-crash
    wt_rm_me
    [ "$PWD" = "$REPO" ]
    [ ! -d "$REPO/../demo_wt/fix_wt/login-crash" ]
}

# The completion script calls `wt __complete ...`, which the sourced function
# must pass through to the binary, or `wt exec <tab>` completes nothing.
@test "wt exec completion reaches the binary through the wt function" {
    run wt __complete exec ""
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix/login-crash"* ]]
    [[ "$output" == *$'\n:4\n'* ]]
    run wt __complete exec login-crash ""
    [ "$status" -eq 0 ]
    [[ "$output" == ':0'$'\n'* ]]
    run wt __complete cd ""
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix/login-crash"* ]]
}

@test "wt cd changes the calling shell's directory" {
    wt cd login-crash
    [[ "$PWD" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt cd / returns to the repository's main checkout" {
    wt cd login-crash
    wt cd /
    [ "$PWD" = "$REPO" ]
}

@test "wt cd with no argument also returns to the main checkout" {
    wt cd login-crash
    wt cd
    [ "$PWD" = "$REPO" ]
}

@test "wt cd . from a subdirectory lands at that worktree's root" {
    wt cd login-crash
    mkdir -p src/deep
    cd src/deep
    wt cd .
    [[ "$PWD" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt cd . from inside the main checkout lands at its root" {
    mkdir -p "$REPO/sub"
    cd "$REPO/sub"
    wt cd .
    [ "$PWD" = "$REPO" ]
}

@test "wt exec . runs at the root of the worktree you are in" {
    wt cd login-crash
    mkdir -p src/deep
    cd src/deep
    run wt exec . pwd
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    run wt exec / pwd
    [ "$status" -eq 0 ]
    [ "$output" = "$REPO" ]
}

@test "wt exec runs in a worktree and leaves the shell put" {
    before="$PWD"
    run wt exec login-crash pwd
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    [ "$PWD" = "$before" ]
}

@test "wt passes every other subcommand to the binary" {
    run wt list
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix_wt/login-crash"* ]]
}

@test "wt --help still reaches the binary" {
    run wt --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"worktree"* ]]
}

@test "the binary alone explains that cd needs the shell function" {
    run command wt cd foo
    [ "$status" -ne 0 ]
    [[ "$output" == *"wt.sh"* ]]
}

# A claude that lists one background session in the login-crash worktree. The
# binary asks this one; nothing here reaches the machine's own claude. Asked
# to start anything, it writes how it was called to claude.started instead:
# the number of arguments, each of them, and where it stood.
fake_claude() {
    mkdir -p "$BATS_TEST_TMPDIR/fakebin"
    cat > "$BATS_TEST_TMPDIR/fakebin/claude" <<CLAUDE
#!/bin/sh
[ "\$1 \$2" = "agents --json" ] || { printf '%s|' "\$#" "\$@" "\$PWD" > "$BATS_TEST_TMPDIR/claude.started"; echo "started: \$*"; exit 0; }
printf '[{"id":"3f9a1c20","sessionId":"3f9a1c20-1111-4222-8333-444455556666","name":"fix the crash","kind":"background","state":"blocked","cwd":"%s"}]\n' "$WT_ROOTS/demo_wt/fix_wt/login-crash"
CLAUDE
    chmod +x "$BATS_TEST_TMPDIR/fakebin/claude"
    export PATH="$BATS_TEST_TMPDIR/fakebin:$PATH"
}

@test "wt list shows the session in a worktree, and --no-sessions leaves it out" {
    fake_claude
    run wt list
    [ "$status" -eq 0 ]
    [[ "$output" == *"SESSION"* ]]
    [[ "$output" == *"fix the crash · needs input"* ]]
    run wt list --no-sessions
    [ "$status" -eq 0 ]
    [[ "$output" != *"SESSION"* ]]
}
