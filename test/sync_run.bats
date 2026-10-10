#!/usr/bin/env bats

load helpers
bats_require_minimum_version 1.5.0

# A repo whose trunk declares v.txt owned-line max-plus-patch, with a
# worktree one commit ahead of trunk on v.txt and trunk itself bumped past
# it: `sync` sees a recipe, `run` clears it, `undo` puts it back.
setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    mkdir -p "$BATS_TEST_TMPDIR/roots"
    # Canonicalise: on macOS /var is a symlink to /private/var, so git reports
    # a different path than the one bats handed us and every comparison fails.
    WT_ROOTS="$(cd "$BATS_TEST_TMPDIR/roots" && pwd -P)"
    export WT_ROOTS
    REPO="$WT_ROOTS/demo"
    make_repo "$REPO"
    printf 'conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '"'"'^\\d'"'"'\n    rule: max-plus-patch\n' > "$REPO/.wt-sync.yaml"
    printf '1.0.0\n' > "$REPO/v.txt"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm declare
    cd "$REPO"
    wt new bump >/dev/null
    BUMP="$WT_ROOTS/demo_wt/feat_wt/bump"
    printf '1.0.1\n' > "$BUMP/v.txt"
    git -C "$BUMP" commit -qam bump
    printf '1.0.5\n' > "$REPO/v.txt"
    git -C "$REPO" commit -qam "trunk bump"
    # The repository is its own origin, so origin/main exists for --no-fetch.
    git -C "$REPO" remote add origin "$REPO"
    git -C "$REPO" fetch -q origin
    cd "$REPO"
}

@test "sync rebase rebases a recipe worktree, sync goes quiet, undo puts it back" {
    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"against origin/main "* ]]
    [[ "$output" != *"fetched"* ]]
    [[ "$output" == *"bump"*"recipe"* ]]

    run wt sync --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"as last fetched"* ]]

    run wt sync rebase bump --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"(as last fetched"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    run wt sync undo bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"→"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"recipe"* ]]
}

# With nothing named, --rebase takes the ready group; with no terminal there is
# nobody to ask. Once bump is on trunk there is nothing left to take.
@test "sync --rebase with nothing named takes every ready worktree" {
    # bats gives the run no terminal: a bulk run nobody confirmed moves nothing.
    run wt sync --rebase --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"every ready worktree: bump"* ]]
    [[ "$output" == *"Pass --yes to rebase bump."* ]]
    [[ "$output" != *"rebased 1 commit"* ]]

    run wt sync --rebase --no-fetch --yes
    [ "$status" -eq 0 ]
    [[ "$output" == *"rebased 1 commit"* ]]

    run wt sync rebase --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"nothing is ready to rebase"* ]]
    [[ "$output" != *"left as they are"* ]]
}

# The same flow with the fetch left in. The demo repo is its own origin, so
# `git fetch origin main` is a local, deterministic no-op that still proves
# the fetch path runs and does not change the outcome.
@test "sync rebase fetches trunk first and rebases the same worktree" {
    run wt sync rebase bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"onto origin/main"*"(fetched)"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]
    # bats gives the run no terminal: nothing is asked or pushed, the command is printed.
    [[ "$output" == *"push: git -C"*"--force-with-lease --force-if-includes"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    run wt sync undo bump
    [ "$status" -eq 0 ]
    [[ "$output" == *"→"* ]]
}

# The same run and undo spelled as flags at the end of the wt sync line, with
# the verb's own flags reaching it: --push pushes without asking, --force
# rewinds a branch that moved after the run.
@test "sync <work> --rebase and --undo are the verbs spelled on wt sync" {
    run wt sync bump --rebase --no-fetch --push
    [ "$status" -eq 0 ]
    [[ "$output" == *"(as last fetched"* ]]
    [[ "$output" == *"rebased 1 commit"* ]]
    [[ "$output" == *"✓ pushed feat_wt/bump"*"→"* ]]
    [[ "$output" != *"push: git -C"* ]]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]

    echo more > "$BUMP/more.txt"
    git -C "$BUMP" add more.txt
    git -C "$BUMP" commit -qm "moved since the run"
    run wt sync bump --undo
    [ "$status" -eq 1 ]
    [[ "$output" == *"has moved since that run"*"--force"* ]]

    run wt sync bump --undo --force
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump  "*"→"*"kept under refs/wt-sync/feat_wt/bump/"* ]]

    run wt sync bump --push
    [ "$status" -eq 1 ]
    [[ "$output" == "wt: --push needs --rebase or --resume" ]]
}

# The handover of the test below, resumed with the flag spelling.
@test "sync <work> --resume is sync resume <work>" {
    printf 'branch\n' > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    git -C "$BUMP" commit -qm "a on the branch"
    printf 'trunk\n' > "$REPO/a.txt"
    git -C "$REPO" add a.txt
    git -C "$REPO" commit -qm "a on trunk"
    git -C "$REPO" fetch -q origin

    run wt sync bump --rebase --no-fetch --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"needs you"* ]]
    GITDIR="$(git -C "$BUMP" rev-parse --absolute-git-dir)"
    [ -f "$GITDIR/wt-sync-plan.md" ]

    echo resolved > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    run wt sync bump --resume --no-push
    [ "$status" -eq 0 ]
    [[ "$output" == *"push: git -C"*"--force-with-lease --force-if-includes"* ]]
    [ ! -d "$GITDIR/rebase-merge" ]
    [ ! -f "$GITDIR/wt-sync-plan.md" ]
    [ "$(git -C "$BUMP" symbolic-ref HEAD)" = "refs/heads/feat_wt/bump" ]
}

# A handover end to end: a.txt moved on both sides and nothing claims it, so
# the run carries the v.txt stop, stops contested on the a.txt stop and leaves
# the plan; the table reports the handover, and resume finishes the rebase
# once the person's file is staged.
@test "sync rebase hands a contested stop over and resume finishes it" {
    printf 'branch\n' > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    git -C "$BUMP" commit -qm "a on the branch"
    printf 'trunk\n' > "$REPO/a.txt"
    git -C "$REPO" add a.txt
    git -C "$REPO" commit -qm "a on trunk"
    git -C "$REPO" fetch -q origin

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"contested"*"2/2"*"a.txt✗"* ]]

    run wt sync rebase bump --no-fetch --yes
    [ "$status" -ne 0 ]
    [[ "$output" == *"needs you"* ]]
    GITDIR="$(git -C "$BUMP" rev-parse --absolute-git-dir)"
    [ -d "$GITDIR/rebase-merge" ]
    [ -f "$GITDIR/wt-sync-plan.md" ]
    [ -f "$GITDIR/wt-sync-state.json" ]

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" == *"bump"*"contested"*"wt sync resume, or wt sync undo"* ]]
    [[ "$output" != *"dirty"* ]]

    echo resolved > "$BUMP/a.txt"
    git -C "$BUMP" add a.txt
    run wt sync resume bump
    [ "$status" -eq 0 ]
    [ ! -d "$GITDIR/rebase-merge" ]
    [ ! -f "$GITDIR/wt-sync-state.json" ]
    [ ! -f "$GITDIR/wt-sync-plan.md" ]
    [ "$(git -C "$BUMP" symbolic-ref HEAD)" = "refs/heads/feat_wt/bump" ]
    [ "$(git -C "$BUMP" show HEAD:a.txt)" = "resolved" ]
    [ "$(git -C "$BUMP" show HEAD:v.txt)" = "1.0.6" ]
    git -C "$BUMP" merge-base --is-ancestor origin/main HEAD

    run wt sync
    [ "$status" -eq 0 ]
    [[ "$output" != *"bump"* ]]
}

# --json: the overview as one object on stdout, nothing else there, and the
# worktree filed where the table files it.
@test "sync --json prints the overview as one object on stdout" {
    run --separate-stderr wt sync --no-fetch --json
    [ "$status" -eq 0 ]
    [[ "$output" == "{"*"}" ]]
    [[ "$output" == *'"command": "sync"'* ]]
    [[ "$output" == *'"work": "bump"'*'"group": "ready"'*'"class": "recipe"'* ]]
    [[ "$output" == *'"token": "1:'* ]]
    # The fixture declares no deferred step: an empty array, never absent.
    [[ "$output" == *'"deferredDeclared": []'* ]]

    run wt sync bump --json
    [ "$status" -ne 0 ]
    [[ "$output" == *"name none"* ]]
}

# --json on a verb: one result object on stdout, the progress on stderr. With
# no terminal a run with nothing named needs --yes; --expect holds the run to
# the overview it was read from.
@test "sync --rebase --json reports the run and holds it to the overview's token" {
    run --separate-stderr wt sync --rebase --no-fetch --json
    [ "$status" -eq 0 ]
    [[ "$output" == "{"*"}" ]]
    [[ "$output" == *'"outcome": "refused"'* ]]
    [[ "$output" == *'"error": "not confirmed: nothing rebased"'* ]]
    [[ "$stderr" == *"Pass --yes to rebase bump."* ]]

    run --separate-stderr wt sync --no-fetch --json
    token=$(printf '%s\n' "$output" | sed -n 's/^  "token": "\(.*\)",$/\1/p')
    [ -n "$token" ]

    run --separate-stderr wt sync rebase --no-fetch --yes --no-push --json --expect 1:0000
    [ "$status" -ne 0 ]
    [[ "$output" == *'"outcome": "refused"'* ]]
    [[ "$output" == *'"worktrees": []'* ]]

    run --separate-stderr wt sync rebase --no-fetch --yes --no-push --json --expect "$token"
    [ "$status" -eq 0 ]
    [[ "$output" == *'"command": "sync rebase"'* ]]
    [[ "$output" == *'"outcome": "done"'* ]]
    [[ "$output" == *'"result": "rebased"'* ]]
    [[ "$output" == *'"pushed": false'* ]]
    [[ "$output" == *'"undoCommand": ['*'"undo",'*'"bump"'* ]]
    [[ "$stderr" == *"rebased 1 commit"* ]]

    run --separate-stderr wt sync undo bump --json
    [ "$status" -eq 0 ]
    [[ "$output" == *'"command": "sync undo"'* ]]
    [[ "$output" == *'"result": "undone"'* ]]

    run --separate-stderr wt sync --all --rebase --json
    [ "$status" -ne 0 ]
    [[ "$output" == "" ]]
}

# wt sync rebase was wt sync run. run is no verb now: it is read as a
# worktree's name, as any other word after wt sync is, and nothing rebases.
@test "sync run is not a verb, and rebases nothing" {
    tip=$(git -C "$BUMP" rev-parse HEAD)

    run --separate-stderr wt sync run bump --if-ready --yes --no-push --no-fetch --json
    [ "$status" -ne 0 ]
    [[ "$output" == "" ]]
    [[ "$stderr" == *"accepts at most 1 arg(s), received 2"* ]]

    run --separate-stderr wt sync run --no-fetch
    [ "$status" -ne 0 ]
    [[ "$output" == "" ]]
    [[ "$stderr" == *'no worktree "run"'* ]]

    for flag in --yes --no-push --json; do
        run --separate-stderr wt sync run --no-fetch "$flag"
        [ "$status" -ne 0 ]
        [[ "$output" == "" ]]
    done

    wt new run >/dev/null
    refs=$(git -C "$REPO" for-each-ref)
    run wt sync run --no-fetch
    [ "$status" -eq 0 ]
    [[ "$output" == *"branch  feat_wt/run"* ]]
    [[ "$output" != *"rebased"* ]]
    [ "$(git -C "$REPO" for-each-ref)" = "$refs" ]
    [ "$(git -C "$BUMP" rev-parse HEAD)" = "$tip" ]
}

# --rebase on wt sync was --run.
@test "sync --run is an unknown flag, and rebases nothing" {
    refs=$(git -C "$REPO" for-each-ref)

    for line in "sync --run" "sync bump --run"; do
        run --separate-stderr wt $line --yes --no-push --no-fetch --json
        [ "$status" -ne 0 ]
        [[ "$output" == "" ]]
        [[ "$stderr" == *"unknown flag: --run"* ]]
    done

    [ "$(git -C "$REPO" for-each-ref)" = "$refs" ]
}
