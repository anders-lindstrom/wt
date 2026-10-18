#!/usr/bin/env bats
# scripts/release.sh: tag the next v* version on HEAD and push the tag, which
# is what publishes a release. Every case runs on a throwaway repository with a
# bare origin, so nothing reaches GitHub.

RELEASE="$BATS_TEST_DIRNAME/../scripts/release.sh"

setup() {
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
    export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
    git init -q --bare "$BATS_TEST_TMPDIR/origin.git"
    git init -q -b main "$BATS_TEST_TMPDIR/repo"
    cd "$BATS_TEST_TMPDIR/repo"
    git remote add origin "$BATS_TEST_TMPDIR/origin.git"
    git commit -q --allow-empty -m first
    git tag -a v0.2.0 -m v0.2.0
    git commit -q --allow-empty -m second
    git push -q origin main v0.2.0
}

remote_tags() { git ls-remote --tags --refs origin | sed 's|.*refs/tags/||' | sort -V | tr '\n' ' '; }

@test "release: each bump tags the next version on HEAD and pushes it" {
    for c in "patch v0.2.1" "minor v0.3.0" "major v1.0.0"; do
        set -- $c
        run "$RELEASE" "$1"
        [ "$status" -eq 0 ]
        [ "$(git describe --exact-match HEAD)" = "$2" ]
        [ "$(git cat-file -t "$2")" = tag ]
        [[ "$(remote_tags)" == *"$2 "* ]]
        git commit -q --allow-empty -m next && git push -q origin main
    done
}

@test "release: HEAD already tagged is done, with no second tag" {
    run "$RELEASE" patch
    [ "$status" -eq 0 ]
    run "$RELEASE" minor
    [ "$status" -eq 0 ]
    [[ "$output" == *"HEAD is already v0.2.1"* ]]
    [ "$(remote_tags)" = "v0.2.0 v0.2.1 " ]
}

@test "release: a tag made but not pushed is pushed on the rerun" {
    git tag -a v0.2.1 -m v0.2.1
    run "$RELEASE" patch
    [ "$status" -eq 0 ]
    [ "$(remote_tags)" = "v0.2.0 v0.2.1 " ]
}

@test "release: refuses a bad bump, a dirty tree or a HEAD not on origin" {
    run "$RELEASE" huge
    [ "$status" -eq 2 ]
    echo x > dirty
    run "$RELEASE" patch
    [ "$status" -eq 1 ]
    rm dirty
    git commit -q --allow-empty -m local-only
    run "$RELEASE" patch
    [ "$status" -eq 1 ]
    [[ "$output" == *"not on origin"* ]]
    [ "$(remote_tags)" = "v0.2.0 " ]
}
