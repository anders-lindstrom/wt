# Fixtures shared by the bats tests: `load helpers` in the test file.

# make_repo <dir>: a repository wt can work in, on main with a minimal
# worktree.conf and an identity to commit as, with nothing committed yet.
# Signing is off and the hooks are the repository's own, so neither a signing
# key nor a global core.hooksPath on the machine running the tests reaches it.
# SUPERSET_REGISTER=off for the same reason: a fixture repository must not
# reach the Superset app of whoever is running the tests.
make_repo() {
    local d=$1
    git init -q -b main "$d"
    git -C "$d" config user.email t@example.com
    git -C "$d" config user.name T
    git -C "$d" config commit.gpgsign false
    git -C "$d" config core.hooksPath "$d/.git/hooks"
    mkdir -p "$d/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\nSUPERSET_REGISTER=off\n' > "$d/bin/worktree/worktree.conf"
}
