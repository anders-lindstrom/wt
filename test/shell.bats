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

@test "wt attach without a terminal starts nothing and prints the command" {
    fake_claude
    run wt attach login-crash
    [ "$status" -ne 0 ]
    [[ "$output" == *"claude attach 3f9a1c20"* ]]
    [[ "$output" != *"started:"* ]]
}

@test "wt attach --resume refuses a worktree whose session is listed" {
    fake_claude
    run wt attach login-crash --resume
    [ "$status" -ne 0 ]
    [[ "$output" == *"fix the crash"* ]]
    [[ "$output" == *"second time"* ]]
    [[ "$output" != *"started:"* ]]
}

@test "the binary hands the shell layer a verb, an id and a path" {
    fake_claude
    run command wt attach --for-shell login-crash
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "attach" ]
    [ "${lines[1]}" = "3f9a1c20" ]
    [ "${lines[2]}" = "end" ]
    run command wt attach --for-shell / --resume
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "continue" ]
    [ "${lines[1]}" = "$REPO" ]
    [ "${lines[2]}" = "end" ]
}

# on_pty <shell> <file>: runs the file in that shell with a terminal on stdin,
# stdout and stderr, which is the only way wt_attach starts anything.
# util-linux's script takes the command as one string, BSD's as arguments.
on_pty() {
    if script --version >/dev/null 2>&1; then
        script -qec "$1 $2" /dev/null </dev/null
    else
        script -q /dev/null "$1" "$2" </dev/null
    fi
}

# What a person types after --resume, as hostile as it gets, on a terminal.
attach_on_terminal() {
    fake_claude
    cat > "$BATS_TEST_TMPDIR/typed.sh" <<TYPED
export PATH="$PATH" WT_ROOTS="$WT_ROOTS" XDG_CONFIG_HOME="$XDG_CONFIG_HOME"
source "$BATS_TEST_DIRNAME/../shell/wt.sh"
cd "$BATS_TEST_TMPDIR"
[ -t 0 ] && [ -t 1 ] && [ -t 2 ] || echo "not a terminal" > "$BATS_TEST_TMPDIR/claude.started"
wt attach demo/login-crash --resume 'x; touch pwned-1 \$(touch pwned-2) \`touch pwned-3\` *'
TYPED
    on_pty "$1" "$BATS_TEST_TMPDIR/typed.sh"
}

@test "on a terminal wt attach starts claude with what it was handed as arguments" {
    command -v script >/dev/null || skip "no script(1) to make a terminal with"
    run attach_on_terminal bash
    [ "$status" -eq 0 ]
    started=$(cat "$BATS_TEST_TMPDIR/claude.started")
    [ "$started" = '2|--resume|x; touch pwned-1 $(touch pwned-2) `touch pwned-3` *|'"$WT_ROOTS/demo_wt/fix_wt/login-crash|" ]
    run ls "$BATS_TEST_TMPDIR"
    [[ "$output" != *pwned* ]]
}

@test "the same under zsh" {
    command -v script >/dev/null || skip "no script(1) to make a terminal with"
    command -v zsh >/dev/null || skip "no zsh here"
    run attach_on_terminal zsh
    [ "$status" -eq 0 ]
    started=$(cat "$BATS_TEST_TMPDIR/claude.started")
    [ "$started" = '2|--resume|x; touch pwned-1 $(touch pwned-2) `touch pwned-3` *|'"$WT_ROOTS/demo_wt/fix_wt/login-crash|" ]
    run ls "$BATS_TEST_TMPDIR"
    [[ "$output" != *pwned* ]]
}

# claude here is a shell function, as it is for anyone whose claude is wrapped.
@test "the shell layer attaches by id through the shell's own claude" {
    claude() { printf '%s|' "$#" "$@"; }
    run _wt_attach_run "$(printf 'attach\n3f9a1c20\n\nend\n')"
    [ "$status" -eq 0 ]
    [ "$output" = "2|attach|3f9a1c20|" ]
}

@test "the shell layer resumes in the worktree and leaves the shell put" {
    claude() { printf '%s|' "$PWD" "$#" "$@"; }
    before="$PWD"
    there="$WT_ROOTS/demo_wt/fix_wt/login-crash"
    run _wt_attach_run "$(printf 'continue\n\n%s\nend\n' "$there")"
    [ "$status" -eq 0 ]
    [ "$output" = "$there|1|--continue|" ]
    run _wt_attach_run "$(printf 'resume\n3f9a1c20-1111-4222-8333-444455556666\n%s\nend\n' "$there")"
    [ "$output" = "$there|2|--resume|3f9a1c20-1111-4222-8333-444455556666|" ]
    _wt_attach_run "$(printf 'continue\n\n%s\nend\n' "$there")" >/dev/null
    [ "$PWD" = "$before" ]
}

# Whatever reaches the shell layer is an argument. Nothing in it is run.
@test "the shell layer never evaluates what it is handed" {
    claude() { printf '%s|' "$#" "$@"; }
    cd "$BATS_TEST_TMPDIR"
    run _wt_attach_run "$(printf 'attach\n$(touch pwned-1); touch pwned-2 `touch pwned-3`\n\nend\n')"
    [ "$output" = '2|attach|$(touch pwned-1); touch pwned-2 `touch pwned-3`|' ]
    run _wt_attach_run "$(printf 'resume\nx; touch pwned-4\n%s\nend\n' "$BATS_TEST_TMPDIR")"
    [ "$output" = '2|--resume|x; touch pwned-4|' ]
    run _wt_attach_run 'touch pwned-5'
    [ "$output" = 'touch pwned-5' ]
    run ls "$BATS_TEST_TMPDIR"
    [[ "$output" != *pwned* ]]
}

@test "wt attach completion reaches the binary through the wt function" {
    run wt __complete attach ""
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix/login-crash"* ]]
}

@test "the binary alone explains that attach needs the shell function" {
    run command wt attach --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"wt.sh"* ]]
}

# A path is one argument to cd whatever is in it, and arrives whole even when
# it ends in a newline, which \$(…) would otherwise take off.
@test "the shell layer resumes in a path with spaces, a glob and a trailing newline" {
    claude() { printf '%s|' "$PWD" "$#" "$@"; }
    there="$BATS_TEST_TMPDIR/a dir * with [glob]"
    mkdir -p "$there" "$BATS_TEST_TMPDIR/a dir other with g"
    run _wt_attach_run "$(printf 'continue\n\n%s\nend\n' "$there")"
    [ "$status" -eq 0 ]
    [ "$output" = "$there|1|--continue|" ]

    trail="$BATS_TEST_TMPDIR/trail"$'\n'
    mkdir -p "$trail" "$BATS_TEST_TMPDIR/trail"
    run _wt_attach_run "$(printf 'continue\n\n%s\nend\n' "$trail")"
    [ "$status" -eq 0 ]
    [ "$output" = "$trail|1|--continue|" ]
}

# A plan that does not end as the binary ends one is not run.
@test "the shell layer runs nothing from a plan that is cut short" {
    claude() { echo "started: $*"; }
    run _wt_attach_run "$(printf 'attach\n3f9a1c20\n')"
    [[ "$output" != *"started:"* ]]
    run _wt_attach_run "$(printf 'continue\n\n%s\n' "$BATS_TEST_TMPDIR")"
    [[ "$output" != *"started:"* ]]
}
