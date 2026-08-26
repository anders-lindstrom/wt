#!/usr/bin/env bats

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var, so git reports a
    # different path than the one bats handed us and every comparison fails.
    WT_ROOTS="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    export WT_ROOTS
    REPO="$WT_ROOTS/demo"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$REPO/bin/worktree/worktree.conf"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name T
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

@test "wt cd changes the calling shell's directory" {
    wt cd login-crash
    [[ "$PWD" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt cd . returns to the repository's main checkout" {
    wt cd login-crash
    wt cd .
    [ "$PWD" = "$REPO" ]
}

@test "wt cd with no argument also returns to the main checkout" {
    wt cd login-crash
    wt cd
    [ "$PWD" = "$REPO" ]
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
