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
    run --separate-stderr wt new fix/login-crash --no-build
    [ "$status" -eq 0 ]
    only_path
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt checkout writes the path alone to stdout" {
    fake_superset
    cd "$REPO"
    git -C "$REPO" branch fix_wt/from-elsewhere
    run --separate-stderr wt checkout fix_wt/from-elsewhere --no-build
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

# fake_gh answers for one open pull request, #12 on residential_fixes, and
# does `pr checkout` by creating that branch where it is run.
fake_gh() {
    cat > "$FAKEBIN/gh" <<'GH'
#!/bin/sh
pr='{"number":12,"title":"Residents keep their doors","headRefName":"residential_fixes","baseRefName":"main","isDraft":false,"state":"OPEN","reviewDecision":"","isCrossRepository":false,"author":{"login":"someone"},"headRepositoryOwner":{"login":"demo"},"url":"https://github.com/demo/demo/pull/12"}'
case "$1 $2" in
  'auth status') exit 0 ;;
  'pr list') printf '[%s]\n' "$pr" ;;
  'api graphql')
    case "$*" in
      *viewer*) printf '{"data":{"viewer":{"login":"anders"},"repository":{"pullRequests":{"nodes":[%s]}}}}\n' "$pr" ;;
      *) printf '{"data":{"repository":{"b0":{"nodes":[%s]}}}}\n' "$pr" ;;
    esac ;;
  'pr view') [ "$3" = 12 ] && printf '%s\n' "$pr" || { echo 'no pull requests found' >&2; exit 1; } ;;
  'pr checkout') [ "$3" = 12 ] && git checkout -q -b residential_fixes || exit 1 ;;
esac
GH
    chmod +x "$FAKEBIN/gh"
    git -C "$REPO" remote remove origin 2>/dev/null || true
    git -C "$REPO" remote add origin git@github.com:demo/demo.git
}

@test "wt pr checkout writes the path alone to stdout, twice over" {
    fake_superset
    fake_gh
    cd "$REPO"
    run --separate-stderr wt pr checkout 12 --no-build
    [ "$status" -eq 0 ]
    only_path
    [[ "$output" == */demo_wt/feat_wt/pr-12-residential_fixes ]]
    first="$output"

    # The worktree is already there: stdout is still that path and nothing
    # else, so `cd "$(wt pr checkout 12)"` works the second time too.
    run --separate-stderr wt pr checkout 12
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [ "$output" = "$first" ]
    [[ "$stderr" == *"already checked out"* ]]
}

@test "the picker with no terminal writes nothing at all to stdout" {
    fake_gh
    cd "$REPO"
    run --separate-stderr wt pr checkout < /dev/null
    [ "$status" -ne 0 ]
    [ -z "$output" ]
    [[ "$stderr" == *"#12"* ]]
    [[ "$stderr" == *"no terminal to choose in"* ]]
}
