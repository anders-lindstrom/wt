# Shell layer for wt.
#
# These exist only for the things a separate process cannot do: change the
# calling shell's directory, open an interactive picker, and start `claude` the
# way this shell does. All matching logic lives in the binary (`wt find`, `wt
# attach`) where it is tested — this file must never grow a second
# implementation of it.
#
#   wt      cd|exec|attach ...     the subcommands that must run in your shell
#   wt_dir  <pattern>              print a worktree's path
#   wt_cd   <pattern>              cd there, in this shell
#   wt_exec <pattern> <cmd> [...]  run a command there, in a subshell
#   wt_attach [pattern] [session]  open the Claude session there, in this terminal
#   wt_ls   [pattern]              list worktrees, or show what a pattern matches
#   wt_rm_me                       remove the worktree you are standing in
#
# A pattern of "." is the worktree you are standing in and "/" is the main
# checkout, as everywhere in wt; a bare `wt cd` goes to the main checkout.
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

# Opening a session is the shell's to do because `claude` may be a shell
# function that sets a session up, which the binary cannot call. The binary
# finds the session and asks which when there are several; with nobody at a
# terminal it starts nothing and prints the command instead.
wt_attach() {
    _wt_require || return 1
    if ! { [ -t 0 ] && [ -t 1 ] && [ -t 2 ]; }; then
        command wt attach "$@"
        return $?
    fi
    local plan
    plan=$(command wt attach --for-shell "$@") || return $?
    _wt_attach_run "$plan"
}

# _wt_attach_run starts what `wt attach --for-shell` printed: a verb, an id
# and a path, a line each, then a line saying `end`. The id and the path are
# only ever arguments. A session's name is free text and never reaches this
# function; nothing here may be handed to eval. The last line is there
# because $(…) drops trailing newlines: with it, a path that ends in one
# arrives whole. Anything else the binary printed, its --help say, is printed
# as it is.
_wt_attach_run() {
    local nl='
'
    local verb="${1%%"$nl"*}" rest="${1#*"$nl"}"
    local id="${rest%%"$nl"*}" wt_path="${rest#*"$nl"}"
    case "$wt_path" in
        *"$nl"end) wt_path="${wt_path%"$nl"end}" ;;
        *) verb= ;;
    esac
    case "$verb" in
        attach)
            claude attach "$id"
            ;;
        continue)
            ( cd "$wt_path" && claude --continue )
            ;;
        resume)
            ( cd "$wt_path" && claude --resume "$id" )
            ;;
        *)
            [ -z "$1" ] || printf '%s\n' "$1"
            ;;
    esac
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

# `wt cd` and `wt exec` cannot live in the binary: a process cannot change its
# caller's directory. `wt attach` starts claude the way your shell does. This
# wrapper handles those three and passes everything else to the real wt, so
# there is one command to remember rather than two families.
#
# A bare `wt cd`, like `wt cd /`, returns to the repository's main checkout.
wt() {
    case "${1:-}" in
        cd)
            shift
            wt_cd "${1:-/}"
            ;;
        exec)
            shift
            wt_exec "$@"
            ;;
        attach)
            shift
            wt_attach "$@"
            ;;
        *)
            command wt "$@"
            ;;
    esac
}
