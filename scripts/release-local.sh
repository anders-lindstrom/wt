#!/usr/bin/env bash
# Run the tagged release workflow in an isolated, full-history checkout.
set -euo pipefail

help() {
    cat <<'HELP'
Run release.yml locally for an existing v* tag on origin/main.
Requires a clean checkout at the tag, act, Docker and gh authentication.

Usage: scripts/release-local.sh [--dry-run | --plan] <tag>

  --dry-run  Build snapshot archives without publishing; save them in dist/.
  --plan     Validate the real release path with act -n; do not build/publish.
  -h, --help Show this help. Default: publish the release using gh auth token.

Examples:
  scripts/release.sh patch                   # tag and push merged HEAD
  scripts/release-local.sh --dry-run v1.2.3  # build all targets locally
  scripts/release-local.sh --plan v1.2.3     # inspect the publishing path
  scripts/release-local.sh v1.2.3            # publish the pushed tag
HELP
}
fail() { echo "release-local: $*" >&2; exit 1; }
for arg in "$@"; do
    case "$arg" in -h | --help) help; exit 0 ;; esac
done
mode=release
tag=
for arg in "$@"; do
    case "$arg" in
        --dry-run | --plan)
            [ "$mode" = release ] || { help >&2; exit 2; }
            mode=${arg#--} ;;
        v*)
            [ -z "$tag" ] || { help >&2; exit 2; }
            tag=$arg ;;
        *) help >&2; exit 2 ;;
    esac
done
[ -n "$tag" ] || { help >&2; exit 2; }
repo=$(git rev-parse --show-toplevel)
cd "$repo"
[ -z "$(git status --porcelain)" ] || fail 'the checkout has uncommitted changes'
commit=$(git rev-parse --verify "refs/tags/$tag^{commit}" 2>/dev/null) || fail "tag $tag does not exist or name a commit"
[ "$(git rev-parse HEAD)" = "$commit" ] || fail "HEAD is not tag $tag; check out the tag first"
git fetch -q origin '+refs/heads/main:refs/remotes/origin/main'
git merge-base --is-ancestor "$commit" refs/remotes/origin/main || fail "tag $tag is not on origin/main"
command -v act >/dev/null || fail 'act is required'
# Ignore inherited tokens: publication uses the human's gh login.
unset GITHUB_TOKEN GH_TOKEN
if [ "$mode" = release ]; then
    GITHUB_TOKEN=$(gh auth token 2>/dev/null) || fail 'gh auth token is unavailable; run gh auth login'
    [ -n "$GITHUB_TOKEN" ] || fail 'gh auth token is empty; run gh auth login'
    export GITHUB_TOKEN
fi
scratch=$(mktemp -d "${TMPDIR:-/tmp}/release-local.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
git clone -q --no-local --no-checkout "$repo" "$scratch/repo"
git -C "$scratch/repo" checkout -q --detach "$commit"
if [ "$mode" = dry-run ]; then
    grep -Fq "env.ACT && env.LOCAL_RELEASE_DRY_RUN == 'true' && 'release --clean --snapshot'" \
        "$scratch/repo/.github/workflows/release.yml" || fail "tag $tag lacks the local snapshot gate"
fi
git -C "$scratch/repo" remote set-url origin "$(git remote get-url origin)"
printf '{"ref":"refs/tags/%s"}\n' "${tag//\"/\\\"}" > "$scratch/event.json"
# A bind mount retains the complete .git history and lets us collect dist/.
args=(push -j release -W .github/workflows/release.yml
    --eventpath "$scratch/event.json" --bind --rm --pull=false
    -P ubuntu-latest=catthehacker/ubuntu:act-22.04
    --secret-file /dev/null --env-file /dev/null --var-file /dev/null
    --input-file /dev/null
    --env LOCAL_RELEASE_DRY_RUN=false)
case "$mode" in
    release) args+=(-s GITHUB_TOKEN) ;;
    dry-run) args+=(--env LOCAL_RELEASE_DRY_RUN=true) ;;
    plan) args+=(-n) ;;
esac
(cd "$scratch/repo"; act "${args[@]}") >&2
if [ "$mode" = dry-run ]; then
    mkdir -p "$repo/dist"
    destination=$(mktemp -d "$repo/dist/local-release.XXXXXX")
    cp -R "$scratch/repo/dist/." "$destination/"
    printf '%s\n' "$destination"
fi
