#!/usr/bin/env bash
# Print the -ldflags value that stamps a wt binary with its version, build
# date and commit date, which `wt about` reports. `make build` and install.sh
# both build with it, so a binary says the same thing however it was made.
#
#   go build -ldflags "$(scripts/ldflags.sh)" ./cmd/wt
#
# Without git, or outside a repository, the version is dev and the commit
# date is empty; the build date is always stamped.
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/.."
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
commit_date=$(git log -1 --format=%cd --date=format:%Y-%m-%dT%H:%M 2>/dev/null || true)
printf -- '-X main.version=%s -X main.buildDate=%s -X main.commitDate=%s\n' \
    "$version" "$(date +%Y-%m-%dT%H:%M)" "$commit_date"
