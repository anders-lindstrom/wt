#!/usr/bin/env bats

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    REPO="$BATS_TEST_TMPDIR/demo"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$REPO/bin/worktree/worktree.conf"
    cd "$REPO"
    source "$BATS_TEST_DIRNAME/../compat/worktree_functions.sh"
}

@test "load_worktree_config sets the legacy variables" {
    load_worktree_config
    [ "$REPO_NAME" = "demo" ]
    [ "$MAIN_BRANCH" = "main" ]
    [ "$WORKTREE_BRANCH_PREFIX" = "feat_wt" ]
    [ "$WORKTREE_DEFAULT_TYPE" = "feat" ]
}

@test "DEVELOPER_CONFIG_DIRS arrives as a real array" {
    load_worktree_config
    [ "${#DEVELOPER_CONFIG_DIRS[@]}" -eq 5 ]
    [ "${DEVELOPER_CONFIG_DIRS[0]}" = ".cursor" ]
}

@test "worktree_branch_name matches the old contract" {
    run worktree_branch_name fix login-crash
    [ "$status" -eq 0 ]
    [ "$output" = "fix_wt/login-crash" ]
}

@test "strip_worktree_prefix returns the work name" {
    run strip_worktree_prefix "research_wt/caching"
    [ "$status" -eq 0 ]
    [ "$output" = "caching" ]
}

@test "get_worktree_path returns the canonical location" {
    run get_worktree_path "fix/login-crash"
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}
