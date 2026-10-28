#!/usr/bin/env bats

load helpers

RELEASE_LOCAL="$BATS_TEST_DIRNAME/../scripts/release-local.sh"

setup() {
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
    export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
    export FIXTURE="$BATS_TEST_TMPDIR"
    mkdir "$FIXTURE/tools"
    export PATH="$FIXTURE/tools:$PATH"
    cat > "$FIXTURE/tools/gh" <<'GH'
#!/usr/bin/env bash
[ "$*" = 'auth token' ] || exit 1
printf '%s' "${TEST_TOKEN-fixture-secret}"
GH
    cat > "$FIXTURE/tools/act" <<'ACT'
#!/usr/bin/env bash
set -eu
# The wrapper must run a detached, full clone at the tag, with the real origin.
test "$(git rev-parse HEAD)" = "$(git rev-parse 'refs/tags/v1.0.0^{commit}')"
test "$(git rev-parse --is-shallow-repository)" = false
test "$(git rev-list --count HEAD)" = 2
test "$(git remote get-url origin)" = "$FIXTURE/origin.git"
! git symbolic-ref -q HEAD
printf '%s\n' "$@" > "$FIXTURE/args"
while [ "$#" -gt 0 ]; do
    case "$1" in
        --eventpath) cp "$2" "$FIXTURE/event.json"; shift ;;
        *fixture-secret*) exit 99 ;;
    esac
    shift
done
if [ -n "${GITHUB_TOKEN:-}" ]; then
    test "$GITHUB_TOKEN" = fixture-secret
fi
mkdir dist
echo artifact > dist/archive
exit "${ACT_STATUS:-0}"
ACT
    chmod +x "$FIXTURE/tools/gh" "$FIXTURE/tools/act"
    git init -q --bare "$FIXTURE/origin.git"
    git init -q -b main "$FIXTURE/repo"
    cd "$FIXTURE/repo"
    git remote add origin "$FIXTURE/origin.git"
    mkdir -p .github/workflows
    cp "$BATS_TEST_DIRNAME/../.github/workflows/release.yml" .github/workflows/release.yml
    blob=$(git hash-object -w .github/workflows/release.yml)
    git update-index --add --cacheinfo "100644,$blob,.github/workflows/release.yml"
    git commit -q -m first
    git commit -q --allow-empty -m second
    git tag -a v1.0.0 -m v1.0.0
    git -C "$FIXTURE/origin.git" fetch -q "$FIXTURE/repo" main:main
    git fetch -q origin
}

@test "release-local: help works without a repository or credentials" {
    cd "$FIXTURE"
    run "$RELEASE_LOCAL" --help
    [ "$status" -eq 0 ]
    [[ "$output" == *'Usage:'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: refuses a missing tag" {
    run "$RELEASE_LOCAL" v9.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'does not exist'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: refuses a dirty tree" {
    echo dirty > untracked
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'uncommitted changes'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: refuses a tag outside freshly fetched origin/main" {
    git commit -q --allow-empty -m unpublished
    git tag v2.0.0
    run "$RELEASE_LOCAL" v2.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'not on origin/main'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: refuses HEAD different from the tag" {
    git commit -q --allow-empty -m newer
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'HEAD is not tag'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: refuses an empty or unavailable token" {
    export TEST_TOKEN=''
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'token is empty'* ]]
    printf '#!/usr/bin/env bash\nexit 1\n' > "$FIXTURE/tools/gh"
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *'token is unavailable'* ]]
    [ ! -e "$FIXTURE/args" ]
}

@test "release-local: passes the token by name and builds the exact tag" {
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 0 ]
    [[ "$output" != *fixture-secret* ]]
    [ "$(cat "$FIXTURE/event.json")" = '{"ref":"refs/tags/v1.0.0"}' ]
    [[ "$(cat "$FIXTURE/args")" == *$'-s\nGITHUB_TOKEN'* ]]
    [[ "$(cat "$FIXTURE/args")" != *LOCAL_RELEASE_DRY_RUN=true* ]]
}

@test "release-local: propagates act failure and removes the temporary clone" {
    export ACT_STATUS=42 TMPDIR="$FIXTURE"
    run "$RELEASE_LOCAL" v1.0.0
    [ "$status" -eq 42 ]
    [ -z "$(find "$FIXTURE" -maxdepth 1 -name 'release-local.*' -print)" ]
}

@test "release-local: dry-run preserves artifacts without needing gh credentials" {
    printf '#!/usr/bin/env bash\nexit 1\n' > "$FIXTURE/tools/gh"
    run "$RELEASE_LOCAL" --dry-run v1.0.0
    [ "$status" -eq 0 ]
    [[ "$(cat "$FIXTURE/args")" == *LOCAL_RELEASE_DRY_RUN=true* ]]
    [ "$(cat "$output/archive")" = artifact ]
}

@test "release-local: plan validates the publishing path without credentials" {
    printf '#!/usr/bin/env bash\nexit 1\n' > "$FIXTURE/tools/gh"
    run "$RELEASE_LOCAL" --plan v1.0.0
    [ "$status" -eq 0 ]
    [[ "$(cat "$FIXTURE/args")" == *$'\n-n'* ]]
    [[ "$(cat "$FIXTURE/args")" != *LOCAL_RELEASE_DRY_RUN=true* ]]
}

@test "release-local: dry-run refuses a tag without the snapshot gate" {
    echo 'name: old workflow' > .github/workflows/release.yml
    blob=$(git hash-object -w .github/workflows/release.yml)
    git update-index --cacheinfo "100644,$blob,.github/workflows/release.yml"
    git commit -q -m old-workflow
    git tag v1.0.1
    git -C "$FIXTURE/origin.git" fetch -q "$FIXTURE/repo" main:main
    run "$RELEASE_LOCAL" --dry-run v1.0.1
    [ "$status" -eq 1 ]
    [[ "$output" == *'lacks the local snapshot gate'* ]]
    [ ! -e "$FIXTURE/args" ]
}
