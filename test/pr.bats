#!/usr/bin/env bats

# `run --separate-stderr` is how these tests check that a warning goes to
# stderr and nowhere near stdout.
bats_require_minimum_version 1.5.0

load helpers

setup() {
    # A bin of this test's own, first, so the fake gh is the one wt finds and
    # the developer's real one is never asked anything.
    FAKEBIN="$BATS_TEST_TMPDIR/bin"
    mkdir -p "$FAKEBIN"
    # Every invocation of the fake appends its argv here, which is how these
    # tests see whether gh ran at all.
    GH_ARGV="$BATS_TEST_TMPDIR/gh-argv"
    : > "$GH_ARGV"
    export PATH="$FAKEBIN:$BATS_TEST_DIRNAME/../bin:$PATH"
    export XDG_CONFIG_HOME="$BATS_TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME"
    REPO="$BATS_TEST_TMPDIR/demo"
    make_repo "$REPO"
    git -C "$REPO" commit -q --allow-empty -m init
}

# gh_graphql_shim answers `gh api graphql`, which is how wt asks about branches
# by name: alias by alias from head_nodes, so a branch's answer depends on the
# branch asked about and nothing else. The picker's query is told apart by its
# `viewer` field and answered from open_prs.
gh_graphql_shim() {
    cat <<'GH'
graphql() {
  case "$*" in
    *viewer*)
      printf '{"data":{"viewer":{"login":"anders"},"repository":{"pullRequests":{"nodes":[%s]}}}}\n' "$(open_prs)"
      return ;;
  esac
  printf '{"data":{"repository":{'
  first=1
  for a in "$@"; do
    case "$a" in
      b[0-9]*=*)
        [ $first -eq 1 ] || printf ','
        first=0
        printf '"%s":{"nodes":[%s]}' "${a%%=*}" "$(head_nodes "${a#*=}")"
        ;;
    esac
  done
  printf '}}}\n'
}
GH
}

# fake_gh writes a gh answering for one open pull request, #12 on
# residential_fixes, and doing `pr checkout` by creating that branch where it
# is run. Nothing here reaches github.com.
fake_gh() {
    cat > "$FAKEBIN/gh" <<GH
#!/bin/sh
printf '%s\n' "\$*" >> "$GH_ARGV"
GH
    cat >> "$FAKEBIN/gh" <<'GH'
pr='{"number":12,"title":"Residents keep their doors","headRefName":"residential_fixes","baseRefName":"main","isDraft":false,"state":"OPEN","reviewDecision":"","isCrossRepository":false,"author":{"login":"someone"},"headRepositoryOwner":{"login":"demo"},"url":"https://github.com/demo/myrepo/pull/12"}'
head_nodes() { case "$1" in residential_fixes) printf '%s' "$pr" ;; esac; }
open_prs() { printf '%s' "$pr"; }
GH
    gh_graphql_shim >> "$FAKEBIN/gh"
    cat >> "$FAKEBIN/gh" <<'GH'
case "$1 $2" in
  'auth status') exit 0 ;;
  '--version ') echo 'gh version 2.100.0 (2026-09-03)' ;;
  'api graphql') graphql "$@" ;;
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

@test "the PR column is cached, and --refresh asks again" {
    fake_gh
    cd "$REPO"
    wt pr checkout 12 >/dev/null
    wt list >/dev/null
    before=$(wc -l < "$GH_ARGV")

    # The second listing is answered from the cache: no gh at all.
    run wt list
    [ "$status" -eq 0 ]
    [[ "$output" == *"#12 open"* ]]
    [ "$(wc -l < "$GH_ARGV")" -eq "$before" ]

    run wt list --refresh
    [ "$status" -eq 0 ]
    [[ "$output" == *"#12 open"* ]]
    [ "$(wc -l < "$GH_ARGV")" -gt "$before" ]

    # --no-pr asks nothing, cache or no cache.
    before=$(wc -l < "$GH_ARGV")
    run wt list --no-pr
    [ "$status" -eq 0 ]
    [[ "$output" != *"#12"* ]]
    [ "$(wc -l < "$GH_ARGV")" -eq "$before" ]
}

@test "a gh that fails warns on stderr and leaves stdout alone" {
    fake_gh
    cd "$REPO"
    wt pr checkout 12 >/dev/null
    wt list --no-pr > "$BATS_TEST_TMPDIR/plain"

    # A gh that is there, logged in, and cannot answer.
    cat > "$FAKEBIN/gh" <<'GH'
#!/bin/sh
case "$1 $2" in
  'auth status') exit 0 ;;
  'api graphql') echo 'dial tcp: no route to host' >&2; exit 1 ;;
esac
GH
    chmod +x "$FAKEBIN/gh"

    run --separate-stderr wt list --refresh
    [ "$status" -eq 0 ]
    [ "$output" = "$(cat "$BATS_TEST_TMPDIR/plain")" ]
    [[ "$stderr" == *"wt: no pull requests shown"* ]]

    # A gh that was never logged in is an ordinary machine: nothing to say.
    cat > "$FAKEBIN/gh" <<'GH'
#!/bin/sh
echo 'To get started with GitHub CLI, please run:  gh auth login' >&2
exit 1
GH
    chmod +x "$FAKEBIN/gh"

    run --separate-stderr wt list --refresh
    [ "$status" -eq 0 ]
    [ "$output" = "$(cat "$BATS_TEST_TMPDIR/plain")" ]
    [ -z "$stderr" ]
}

@test "wt pr open hands the number to gh --web" {
    fake_gh
    cd "$REPO"
    wtpath=$(wt pr checkout 12 | tail -1)
    : > "$GH_ARGV"

    cd "$wtpath"
    run wt pr open
    [ "$status" -eq 0 ]
    [[ "$output" == *"Opening #12 open · Residents keep their doors"* ]]
    grep -q -- 'pr view 12 --web' "$GH_ARGV"
}

@test "wt pr open on a worktree with no pull request says so" {
    fake_gh
    cd "$REPO"
    wt new fix/login-crash --no-setup >/dev/null
    : > "$GH_ARGV"

    run wt pr open login-crash
    [ "$status" -ne 0 ]
    [[ "$output" == *"no pull request in demo/demo has fix_wt/login-crash as its head branch"* ]]
    # GitHub was asked about this branch and said there is none; that is what
    # makes the absence of --web mean something.
    grep -q -- 'b0=fix_wt/login-crash' "$GH_ARGV"
    ! grep -q -- '--web' "$GH_ARGV"
}

# fake_gh_merged answers with one merged pull request, #34, whose head is
# branch at tip and whose base is trunk — a squash or rebase merge, which
# leaves the branch looking unmerged to git for ever.
fake_gh_merged() {
    local branch=$1 tip=$2 base=${3:-main}
    cat > "$FAKEBIN/gh" <<GH
#!/bin/sh
printf '%s\n' "\$*" >> "$GH_ARGV"
pr='{"number":34,"title":"Login crash","headRefName":"$branch","baseRefName":"$base","headRefOid":"$tip","isDraft":false,"state":"MERGED","reviewDecision":"","isCrossRepository":false,"author":{"login":"someone"},"headRepositoryOwner":{"login":"demo"},"url":"https://github.com/demo/demo/pull/34"}'
head_nodes() { case "\$1" in "$branch") printf '%s' "\$pr" ;; esac; }
open_prs() { :; }
GH
    gh_graphql_shim >> "$FAKEBIN/gh"
    cat >> "$FAKEBIN/gh" <<'GH'
case "$1 $2" in
  'auth status') exit 0 ;;
  '--version ') echo 'gh version 2.100.0 (2026-09-03)' ;;
  'api graphql') graphql "$@" ;;
  'pr list') printf '[%s]\n' "$pr" ;;
esac
GH
    chmod +x "$FAKEBIN/gh"
    git -C "$REPO" remote remove origin 2>/dev/null || true
    git -C "$REPO" remote add origin git@github.com:demo/demo.git
}

@test "sweep names the pull request, and sweeps a squash-merged worktree" {
    cd "$REPO"
    wtpath=$(wt new fix/login-crash --no-setup | tail -1)
    git -C "$wtpath" commit -q --allow-empty -m "the work that was squashed"
    tip=$(git -C "$wtpath" rev-parse HEAD)
    fake_gh_merged fix_wt/login-crash "$tip"

    run wt sweep --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"#34 merged on GitHub (squashed or rebased, so git cannot see it)"* ]]

    run wt sweep --no-fetch --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"branch fix_wt/login-crash was merged as #34 and has been deleted"* ]]
    [ ! -d "$wtpath" ]
    run git -C "$REPO" rev-parse --verify --quiet refs/heads/fix_wt/login-crash
    [ "$status" -ne 0 ]
}

@test "a commit made after the merge keeps the worktree" {
    cd "$REPO"
    wtpath=$(wt new fix/login-crash --no-setup | tail -1)
    git -C "$wtpath" commit -q --allow-empty -m "the work that was squashed"
    tip=$(git -C "$wtpath" rev-parse HEAD)
    git -C "$wtpath" commit -q --allow-empty -m "and then some more"
    fake_gh_merged fix_wt/login-crash "$tip"

    run wt sweep --no-fetch --yes
    [ "$status" -eq 0 ]
    [[ "$output" != *"#34 merged on GitHub"* ]]
    [ -d "$wtpath" ]
}

@test "wt remove deletes a squash-merged branch from the cache, with no gh at all" {
    cd "$REPO"
    wtpath=$(wt new fix/login-crash --no-setup | tail -1)
    git -C "$wtpath" commit -q --allow-empty -m "the work that was squashed"
    tip=$(git -C "$wtpath" rev-parse HEAD)
    fake_gh_merged fix_wt/login-crash "$tip"
    # One sweep plan is enough to leave the answer in the cache; nothing is
    # removed, because there is no terminal to ask.
    wt sweep --no-fetch >/dev/null
    [ -f "$REPO/.git/wt-pr-cache.json" ]

    # From here on gh does not exist. wt remove runs from git hooks, so it may
    # start no process and touch no network: the cache is all it has.
    (
        no_gh
        run wt remove login-crash --yes
        [ "$status" -eq 0 ]
        [[ "$output" == *"branch fix_wt/login-crash was merged as #34 and has been deleted"* ]]
    )
    [ ! -d "$wtpath" ]
    run git -C "$REPO" rev-parse --verify --quiet refs/heads/fix_wt/login-crash
    [ "$status" -ne 0 ]
}

@test "wt remove keeps a branch whose pull request merged into its parent" {
    cd "$REPO"
    wtpath=$(wt new fix/login-crash --no-setup | tail -1)
    git -C "$wtpath" commit -q --allow-empty -m "the work that was squashed"
    tip=$(git -C "$wtpath" rev-parse HEAD)
    # Merged, but into the branch it was stacked on: nothing reached trunk.
    fake_gh_merged fix_wt/login-crash "$tip" feat_wt/the-parent
    wt sweep --no-fetch >/dev/null

    (
        no_gh
        run wt remove login-crash --yes
        [ "$status" -eq 0 ]
        [[ "$output" == *"branch kept as login-crash"* ]]
    )
    run git -C "$REPO" rev-parse --verify --quiet refs/heads/login-crash
    [ "$status" -eq 0 ]
}
