#!/usr/bin/env bash
# Capture what a repository's OLD load_worktree_config produces, so a migration
# can be proved not to have changed it. Run before migrating.
#
#   scripts/capture-config-contract.sh <repo-path> <out-dir>
set -euo pipefail
repo="${1:?repo path required}"
out="${2:?output directory required}"
name=$(basename "$repo")
mkdir -p "$out"
# BASE_DIR is what load_worktree_config resolves worktree.conf against. The
# repo's own scripts set it from git rev-parse; without it the function silently
# returns pure defaults, and a baseline of defaults would prove nothing.
bash -c "
  cd '$repo'
  BASE_DIR=\$(git rev-parse --show-toplevel)
  export BASE_DIR
  source ./bin/env_check_functions.sh
  source ./bin/worktree_functions.sh
  load_worktree_config
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
" | sort > "$out/$name.before"
echo "captured $out/$name.before"
