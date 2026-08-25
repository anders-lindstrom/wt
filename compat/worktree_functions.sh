# Compatibility layer: the function contract that Herdr's skills and the
# lindstrom.worktree-setup plugin source by name. Each function is a thin
# wrapper over wt, so the contract survives the implementation moving out of
# bash. Nothing downstream had to be edited to keep working.
#
# A repository's bin/worktree_functions.sh becomes a two-line shim sourcing
# this file.

_wt_require() {
    command -v wt >/dev/null 2>&1 && return 0
    echo "worktree tooling requires 'wt' on PATH — see https://github.com/anders-lindstrom/wt" >&2
    return 1
}

# Sets REPO_NAME, MAIN_BRANCH, WORKTREE_*, DEVELOPER_CONFIG_*, BUILD_INIT_*,
# REQUIRED_BINS, TEST_COMMAND, RUN_TESTS_BEFORE_REMOVE and AWS_SETUP_ENABLED,
# exactly as the old function did.
load_worktree_config() {
    _wt_require || return 1
    eval "$(wt config --shell)" || return 1
}

get_worktree_path()      { _wt_require && wt path "$1"; }
worktree_branch_name()   { _wt_require && wt branch "$1/$2"; }
strip_worktree_prefix()  { _wt_require && wt branch-strip "$1"; }

# The branch actually checked out at a path, which is not derivable from the
# name once the type can vary.
worktree_branch_at() {
    git -C "$1" rev-parse --abbrev-ref HEAD 2>/dev/null
}
