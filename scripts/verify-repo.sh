#!/usr/bin/env bash
# Verify one migrated repository. Exits non-zero on any failure.
#   scripts/verify-repo.sh <migration-worktree> <repo-name> <baseline-dir>
set -euo pipefail
wtdir="${1:?worktree required}"; name="${2:?name required}"; base="${3:?baseline required}"
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
cd "$wtdir"

"$here/compare-config-contract.sh" "$PWD" "$base" "$name"

fail=0
for f in new.sh remove.sh setup.sh list.sh status.sh switch.sh checkout.sh setup_precheck.sh remove_me.sh; do
    test -x "bin/worktree/$f" || { echo "  ✗ not executable: $f"; fail=1; }
done
[ $fail -eq 0 ] && echo "  ✓ nine shims executable"

bash -c 'source ./bin/worktree_functions.sh && load_worktree_config \
  && echo "  ✓ herdr contract: REPO_NAME=$REPO_NAME MAIN_BRANCH=$MAIN_BRANCH dirs=${#DEVELOPER_CONFIG_DIRS[@]} AWS=$AWS_SETUP_ENABLED"' || fail=1

git status --porcelain .superset/ 2>/dev/null | grep -q . && { echo "  ✗ .superset edited"; fail=1; } || echo "  ✓ .superset untouched"

diff <(grep -v wt-migration "/tmp/$name.worktrees.before") \
     <(git worktree list | grep -v wt-migration) >/dev/null \
  && echo "  ✓ no worktree moved" || { echo "  ✗ WORKTREES CHANGED"; fail=1; }

exit $fail
