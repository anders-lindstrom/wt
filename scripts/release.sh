#!/usr/bin/env bash
# Release wt: tag the next v* version on HEAD and push the tag. The push is
# the release: .github/workflows/release.yml publishes the binaries, and mise
# installs them (github:anders-lindstrom/wt).
#
#   scripts/release.sh patch|minor|major
#
# land-branch runs it in the main checkout after a merge, as
# ["scripts/release.sh", "{bump}"]. A rerun is safe: a HEAD that already
# carries a v* tag gets that tag pushed if it is not yet, and no second one.
set -euo pipefail

case ${1:-} in
patch | minor | major) bump=$1 ;;
*)
    echo "usage: scripts/release.sh patch|minor|major" >&2
    exit 2
    ;;
esac

if [ -n "$(git status --porcelain)" ]; then
    echo "release: the checkout has uncommitted changes" >&2
    exit 1
fi

git fetch -q --tags origin
head=$(git rev-parse HEAD)
if ! git branch -r --contains "$head" | grep -q .; then
    echo "release: HEAD $head is not on origin; push the merge first" >&2
    exit 1
fi

if tag=$(git describe --exact-match --match 'v[0-9]*' HEAD 2>/dev/null); then
    echo "release: HEAD is already $tag"
    git push -q origin "refs/tags/$tag"
    exit 0
fi

last=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n 1)
IFS=. read -r major minor patch <<<"${last#v}"
major=${major:-0} minor=${minor:-0} patch=${patch:-0}
case $bump in
patch) patch=$((patch + 1)) ;;
minor) minor=$((minor + 1)) patch=0 ;;
major) major=$((major + 1)) minor=0 patch=0 ;;
esac
tag="v$major.$minor.$patch"

git tag -a "$tag" -m "$tag" "$head"
git push -q origin "refs/tags/$tag"
echo "release: tagged and pushed $tag"
