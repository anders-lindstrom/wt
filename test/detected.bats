#!/usr/bin/env bats

# A repository nobody ran `wt init` in: a clone of someone else's project, or
# one a harness makes worktrees in. wt runs there on what `wt init --yes` would
# write, held in memory, and leaves the repository as it found it.
bats_require_minimum_version 1.5.0

load helpers

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    local src="$BATS_TEST_TMPDIR/upstream"
    make_repo "$src"
    rm -r "$src/bin"
    echo hello > "$src/README.md"
    git -C "$src" add -A
    git -C "$src" commit -q -m init
    REPO="$BATS_TEST_TMPDIR/demo"
    git clone -q "$src" "$REPO"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name T
    git -C "$REPO" config commit.gpgsign false
}

# untouched asserts the main checkout still has no configuration and nothing
# else changed in it.
untouched() {
    [ ! -e "$REPO/bin" ]
    [ -z "$(git -C "$REPO" status --porcelain)" ]
}

@test "the Claude Code hooks create and remove a worktree with no config file" {
    cd "$REPO"
    run --separate-stderr wt hook claude-create <<< '{"name":"fix/login-crash"}'
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    [ -d "$output" ]
    [ "$(grep -c 'detected defaults' <<< "$stderr")" -eq 1 ]
    [[ "$stderr" == *"trunk main"* ]]
    untouched

    local path=$output
    run --separate-stderr wt hook claude-remove <<< "{\"path\":\"$path\"}"
    [ "$status" -eq 0 ]
    [ -z "$output" ]
    [ ! -e "$path" ]
    untouched
}

@test "wt new prints the path alone, and one line on stderr about detection" {
    cd "$REPO"
    run --separate-stderr wt new fix/login-crash
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [ -d "$output" ]
    [ "$(grep -c 'detected' <<< "$stderr")" -eq 1 ]
    untouched
}

@test "the commands that read the configuration run on the detected one" {
    cd "$REPO"
    wt new fix/login-crash > /dev/null 2>&1
    git -C "$REPO" branch feat_wt/elsewhere
    run wt checkout feat_wt/elsewhere
    [ "$status" -eq 0 ]
    for c in "list" "status login-crash" "status login-crash --json" \
        "setup $REPO/../demo_wt/fix_wt/login-crash" "sweep --dry-run" \
        "sync" "sync --json" "sync login-crash" "sync keep" \
        "up login-crash --yes --no-push" "remove login-crash --dry-run"; do
        run wt $c
        echo "wt $c: $output"
        [ "$status" -eq 0 ]
    done
    # These refuse for want of a .wt-sync.yaml, which is a declaration of its
    # own and not wt's configuration: they get past loading it.
    for c in "sync run login-crash --yes --no-push" "sync doctor"; do
        run wt $c
        echo "wt $c: $output"
        [[ "$output" == *".wt-sync.yaml"* || "$output" == *"declaration"* ]]
        [[ "$output" != *"worktree.conf"* ]]
    done
    run wt remove elsewhere --yes
    [ "$status" -eq 0 ]
    untouched
}

@test "wt config and wt doctor say the values are detected" {
    cd "$REPO"
    run wt config
    [ "$status" -eq 0 ]
    [[ "$output" == *'configuration: detected (no config file; `wt init` writes one)'* ]]
    run wt doctor
    [[ "$output" == *"  - no config file; trunk main"* ]]
    [[ "$output" != *"  ! no bin/worktree"* ]]
}

@test "wt init still writes the configuration down" {
    cd "$REPO"
    run wt init --yes
    [ "$status" -eq 0 ]
    [ -f "$REPO/bin/worktree/worktree.conf" ]
    run wt config
    [[ "$output" != *detected* ]]
}

@test "a trunk that cannot be detected keeps the error, and names wt init" {
    cd "$REPO"
    git checkout -q --detach
    git branch -q -m main other
    git remote remove origin
    run wt new fix/login-crash
    [ "$status" -ne 0 ]
    [[ "$output" == *"no trunk to detect"* ]]
    [[ "$output" == *"wt init"* ]]
}
