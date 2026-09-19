#!/usr/bin/env bats

load helpers

setup() {
    # A bin of this test's own, first, so the fake gh is the one wt finds and
    # the developer's real one is never asked anything.
    FAKEBIN="$BATS_TEST_TMPDIR/bin"
    mkdir -p "$FAKEBIN"
    export PATH="$FAKEBIN:$BATS_TEST_DIRNAME/../bin:$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    REPO="$BATS_TEST_TMPDIR/demo"
    make_repo "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
}

# fake_gh writes a gh answering for one open pull request, #12 on
# residential_fixes, and doing `pr checkout` by creating that branch where it
# is run. Nothing here reaches github.com.
fake_gh() {
    cat > "$FAKEBIN/gh" <<'GH'
#!/bin/sh
pr='{"number":12,"title":"Residents keep their doors","headRefName":"residential_fixes","isDraft":false,"state":"OPEN","reviewDecision":"","isCrossRepository":false,"author":{"login":"someone"},"headRepositoryOwner":{"login":"demo"},"url":"https://github.com/demo/myrepo/pull/12"}'
case "$1 $2" in
  'auth status') exit 0 ;;
  '--version ') echo 'gh version 2.100.0 (2026-09-03)' ;;
  'pr list') printf '[%s]\n' "$pr" ;;
  'pr view') [ "$3" = 12 ] && printf '%s\n' "$pr" || { echo 'no pull requests found' >&2; exit 1; } ;;
  'pr checkout') [ "$3" = 12 ] && git checkout -q -b residential_fixes || exit 1 ;;
esac
GH
    chmod +x "$FAKEBIN/gh"
    git -C "$REPO" remote remove origin 2>/dev/null || true
    git -C "$REPO" remote add origin git@github.com:demo/demo.git
}

@test "wt pr checkout makes a worktree on the pull request's branch" {
    fake_gh
    cd "$REPO"
    run wt pr checkout 12
    [ "$status" -eq 0 ]
    wtpath="${lines[${#lines[@]}-1]}"
    [[ "$wtpath" == */demo_wt/feat_wt/pr-12-residential_fixes ]]
    [ -d "$wtpath" ]
    [ "$(git -C "$wtpath" symbolic-ref --short HEAD)" = residential_fixes ]
}

@test "wt list gains a PR column, and wt pr list names the worktree" {
    fake_gh
    cd "$REPO"
    wt pr checkout 12 >/dev/null

    run wt list
    [ "$status" -eq 0 ]
    [[ "$output" == *"PR"* ]]
    [[ "$output" == *"#12 open"* ]]
    [[ "$output" == *"pr-12-residential_fixes"* ]]

    run wt pr list
    [ "$status" -eq 0 ]
    [[ "$output" == *"demo/demo, 1 open pull request"* ]]
    [[ "$output" == *"pr-12-residential_fixes"* ]]
}

@test "a second checkout of the same pull request prints the worktree it has" {
    fake_gh
    cd "$REPO"
    first=$(wt pr checkout 12 | tail -1)
    run wt pr checkout 12
    [ "$status" -eq 0 ]
    [ "$(echo "$output" | tail -1)" = "$first" ]
    [[ "$output" == *"already checked out"* ]]
}

@test "with no terminal the picker prints the list and asks for a number" {
    fake_gh
    cd "$REPO"
    run wt pr checkout < /dev/null
    [ "$status" -ne 0 ]
    [[ "$output" == *"#12"* ]]
    [[ "$output" == *"no terminal to choose in"* ]]
}

# no_gh replaces the PATH with one carrying wt and git and nothing else, so
# the machine's own gh — CI runners have one — cannot answer.
no_gh() {
    local dir="$BATS_TEST_TMPDIR/nogh"
    mkdir -p "$dir"
    ln -sf "$(command -v git)" "$dir/git"
    export PATH="$dir:$BATS_TEST_DIRNAME/../bin"
}

@test "wt pr says which gate stopped it" {
    cd "$REPO"
    git -C "$REPO" remote add origin git@github.com:demo/demo.git

    # No gh on the PATH at all.
    (
        no_gh
        run wt pr list
        [ "$status" -ne 0 ]
        [[ "$output" == *"gh is not on your PATH"* ]]
    )

    # A repository that is not on GitHub.
    fake_gh
    git -C "$REPO" remote set-url origin https://gitlab.com/demo/demo.git
    run wt pr list
    [ "$status" -ne 0 ]
    [[ "$output" == *"has no GitHub remote"* ]]

    # Turned off by the person.
    git -C "$REPO" remote set-url origin git@github.com:demo/demo.git
    wt config set github false
    run wt pr list
    [ "$status" -ne 0 ]
    [[ "$output" == *"GitHub is off in your wt config"* ]]
}

@test "wt list is unchanged when GitHub is not in play" {
    cd "$REPO"
    wt new fix/login-crash --no-setup > /dev/null
    without=$(wt list)

    fake_gh
    wt config set github false
    [ "$(wt list)" = "$without" ]

    # On, but no pull request belongs to any worktree here.
    wt config set github true
    [ "$(wt list)" = "$without" ]
}

@test "wt doctor has a GitHub section that is never a problem" {
    cd "$REPO"
    (
        no_gh
        run wt doctor
        [ "$status" -eq 0 ]
        [[ "$output" == *"GitHub:"* ]]
        [[ "$output" == *"no gh on the PATH"* ]]
        [[ "$output" == *"No problems found."* ]]
    )

    fake_gh
    run wt doctor
    [ "$status" -eq 0 ]
    [[ "$output" == *"✓ demo/demo on github.com"* ]]
    [[ "$output" == *"No problems found."* ]]
}
