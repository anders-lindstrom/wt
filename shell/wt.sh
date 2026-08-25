# Shell layer for wt.
#
# These exist only for the two things a separate process cannot do: change the
# calling shell's directory, and open an interactive picker. All matching logic
# lives in the binary (`wt find`) where it is tested — this file must never grow
# a second implementation of it.
#
#   wt_dir  <pattern>              print a worktree's path
#   wt_cd   <pattern>              cd there, in this shell
#   wt_exec <pattern> <cmd> [...]  run a command there, in a subshell
#   wt_ls   [pattern]              list worktrees, or show what a pattern matches
#   wt_rm_me                       remove the worktree you are standing in
#
# Override the search roots with a colon-separated list:
#   export WT_ROOTS="$HOME/work:$HOME/oss"

_wt_require() {
    command -v wt >/dev/null 2>&1 && return 0
    echo "wt is not on PATH — see https://github.com/anders-lindstrom/wt" >&2
    return 1
}

# _wt_resolve prints one path. When several candidates tie it opens fzf if this
# is an interactive terminal, and otherwise fails with the list — so a script or
# an agent never runs in a worktree it did not mean.
_wt_resolve() {
    _wt_require || return 1
    [ -n "$1" ] || { echo "wt: no pattern given" >&2; return 2; }

    local resolved
    if resolved=$(wt find "$1" 2>/dev/null); then
        printf '%s\n' "$resolved"
        return 0
    fi

    local candidates
    candidates=$(wt find --candidates "$1" 2>/dev/null)
    if [ -z "$candidates" ]; then
        wt find "$1" >/dev/null   # re-run for its error message on stderr
        return 1
    fi

    if [ -t 0 ] && [ -t 2 ] && command -v fzf >/dev/null 2>&1; then
        printf '%s\n' "$candidates" |
            fzf --preview-window=hidden --height=40% --reverse --prompt='worktree> '
        return $?
    fi
    echo "wt: '$1' is ambiguous:" >&2
    printf '%s\n' "$candidates" | sed 's/^/  /' >&2
    return 1
}

# NB: the local is wt_path, not path — in zsh `path` is tied to PATH, so
# `local path` inside a function empties PATH for everything it calls.
wt_dir() {
    local wt_path
    wt_path=$(_wt_resolve "$1") || return $?
    printf '%s\n' "$wt_path"
}

wt_cd() {
    local wt_path
    wt_path=$(_wt_resolve "$1") || return $?
    cd "$wt_path"
}

# Runs in a subshell, so your own shell stays where it is and the command's exit
# code is what you get back. The command is executed directly rather than
# through eval, so quoting survives; the tradeoff is that a shell *alias* will
# not expand (a shell function will).
wt_exec() {
    if [ "$#" -lt 2 ]; then
        echo "usage: wt_exec <pattern> <command> [args...]" >&2
        return 2
    fi
    local pattern="$1"; shift
    local wt_path
    wt_path=$(_wt_resolve "$pattern") || return $?
    ( cd "$wt_path" && "$@" )
}

wt_ls() {
    _wt_require || return 1
    if [ -n "$1" ]; then
        wt find --candidates "$1"
        return $?
    fi
    wt list
}

# Removing the worktree you are standing in has to cd out of it first, which is
# why this is a shell function and not a flag alone.
wt_rm_me() {
    _wt_require || return 1
    local main here
    main=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || {
        echo "wt: not in a git repository" >&2
        return 1
    }
    main=${main%/.git}
    here=$(git rev-parse --show-toplevel 2>/dev/null) || return 1
    if [ "$here" = "$main" ]; then
        echo "wt: refusing to remove the main checkout" >&2
        return 1
    fi
    cd "$main" || return 1
    wt remove --me-at "$here"
}
