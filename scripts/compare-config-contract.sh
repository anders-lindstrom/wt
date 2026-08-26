#!/usr/bin/env bash
# Compare a migrated repository's wt config against the captured baseline.
#
#   scripts/compare-config-contract.sh <repo-path> <baseline-dir>
set -euo pipefail
repo="${1:?repo path required}"
base="${2:?baseline directory required}"
name="${3:-$(basename "$repo")}"
bash -c "
  cd '$repo'
  eval \"\$(wt config --shell)\"
  echo \"MAIN_BRANCH=\$MAIN_BRANCH\"
  echo \"WORKTREE_BRANCH_PREFIX=\$WORKTREE_BRANCH_PREFIX\"
  echo \"WORKTREE_TYPE_SUFFIX=\$WORKTREE_TYPE_SUFFIX\"
  echo \"WORKTREE_DEFAULT_TYPE=\$WORKTREE_DEFAULT_TYPE\"
  echo \"WORKTREE_TYPES=\$WORKTREE_TYPES\"
  echo \"REQUIRED_BINS=\${REQUIRED_BINS:-}\"
  echo \"BUILD_INIT_ENABLED=\$BUILD_INIT_ENABLED\"
  echo \"BUILD_INIT_COMMAND=\$BUILD_INIT_COMMAND\"
  echo \"TEST_COMMAND=\$TEST_COMMAND\"
  echo \"RUN_TESTS_BEFORE_REMOVE=\$RUN_TESTS_BEFORE_REMOVE\"
  echo \"REPO_NAME=\$REPO_NAME\"
  echo \"DEVELOPER_CONFIG_DIRS=\${DEVELOPER_CONFIG_DIRS[*]}\"
  echo \"DEVELOPER_CONFIG_FILES=\${DEVELOPER_CONFIG_FILES[*]:-}\"
" | sort > "/tmp/$name.after"
if diff -u "$base/$name.before" "/tmp/$name.after"; then
  echo "✓ $name: config contract unchanged"
else
  echo "✗ $name: CONFIG CONTRACT CHANGED — stop and investigate" >&2
  exit 1
fi
