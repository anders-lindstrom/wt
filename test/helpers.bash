# Fixtures shared by the bats tests: `load helpers` in the test file.

# The user settings must never be the ones belonging to whoever is running the
# tests: `wt config set` writes a real file, and the superset switch decides
# whether wt talks to the Superset app at all. One directory per bats run,
# inside the tree bats removes afterwards. Sourced from a script instead, the
# script says where its own throwaway tree is — nothing here creates a
# directory under TMPDIR that nobody ever deletes.
WT_TEST_TMPDIR="${WT_TEST_TMPDIR:-${BATS_RUN_TMPDIR:-}}"
if [ -n "$WT_TEST_TMPDIR" ]; then
    export XDG_CONFIG_HOME="$WT_TEST_TMPDIR/wt-user-config"
    mkdir -p "$XDG_CONFIG_HOME"
fi

# make_repo <dir>: a repository wt can work in, on main with a minimal
# worktree.conf and an identity to commit as, with nothing committed yet.
# Signing is off and the hooks are the repository's own, so neither a signing
# key nor a global core.hooksPath on the machine running the tests reaches it.
make_repo() {
    local d=$1
    git init -q -b main "$d"
    git -C "$d" config user.email t@example.com
    git -C "$d" config user.name T
    git -C "$d" config commit.gpgsign false
    git -C "$d" config core.hooksPath "$d/.git/hooks"
    mkdir -p "$d/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$d/bin/worktree/worktree.conf"
}
