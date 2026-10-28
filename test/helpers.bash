# Fixtures shared by the bats tests: `load helpers` in the test file.

# The tests must never see the machine of whoever runs them, as
# internal/gittest.Isolate keeps the Go tests off it: `wt config set` writes a
# real file, the superset switch decides whether wt talks to the Superset app
# at all, and a live claude, a global hooksPath or signing key would make a
# local run differ from CI's. So: HOME, XDG_CONFIG_HOME and git's global config
# are this run's own, git reads no system config and none of the GIT_
# variables the environment carries (an agent launcher's GIT_CONFIG_* and
# GIT_SSH_COMMAND), WT_ROOTS and CODEX_HOME are unset, and the PATH finds no
# claude; a test that wants one puts its own fake first. One directory per bats
# run, inside the tree bats removes afterwards. Sourced from a script instead,
# the script says where its own throwaway tree is: nothing here creates a
# directory under TMPDIR that nobody ever deletes.
WT_TEST_TMPDIR="${WT_TEST_TMPDIR:-${BATS_RUN_TMPDIR:-}}"
if [ -n "$WT_TEST_TMPDIR" ]; then
    while read -r name; do
        unset "$name"
    done < <(compgen -e | grep '^GIT_')
    unset WT_ROOTS CODEX_HOME
    # Go's caches are not settings: a test that builds keeps using the ones
    # it had, rather than downloading every module into the new HOME.
    if command -v go >/dev/null; then
        { read -r GOPATH; read -r GOMODCACHE; read -r GOCACHE; } < <(go env GOPATH GOMODCACHE GOCACHE)
        export GOPATH GOMODCACHE GOCACHE
    fi
    export HOME="$WT_TEST_TMPDIR/home"
    export XDG_CONFIG_HOME="$WT_TEST_TMPDIR/wt-user-config"
    export GIT_CONFIG_GLOBAL="$WT_TEST_TMPDIR/gitconfig" GIT_CONFIG_NOSYSTEM=1
    mkdir -p "$HOME" "$XDG_CONFIG_HOME"
    # Who the tests commit as, wherever they run; no signing and no
    # background maintenance.
    [ -f "$GIT_CONFIG_GLOBAL" ] || printf '%s\n' '[user]' '	name = T' '	email = t@example.com' \
        '[commit]' '	gpgsign = false' '[tag]' '	gpgsign = false' '[maintenance]' '	auto = false' \
        '[init]' '	defaultBranch = main' > "$GIT_CONFIG_GLOBAL"
    # Each PATH directory holding a claude is replaced by one that links to
    # everything else in it, so every other tool, git among them, is still
    # found where it was.
    hide_claude() {
        local dirs d shadow tmp e path=
        IFS=: read -ra dirs <<< "$PATH"
        for d in "${dirs[@]}"; do
            if [ -n "$d" ] && [ -e "$d/claude" ]; then
                d=$(cd "$d" && pwd)
                shadow="$WT_TEST_TMPDIR/path-$(printf %s "$d" | cksum | cut -d' ' -f1)"
                if [ ! -d "$shadow" ]; then
                    tmp=$(mktemp -d "$shadow.XXXXXX")
                    for e in "$d"/* "$d"/.[!.]*; do
                        [ -e "$e" ] || [ -L "$e" ] || continue
                        [ "${e##*/}" = claude ] || ln -s "$e" "$tmp/"
                    done
                    mv "$tmp" "$shadow"
                fi
                d=$shadow
            fi
            path=${path:+$path:}$d
        done
        PATH=$path
    }
    hide_claude
    unset -f hide_claude
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
