#!/usr/bin/env bash
# Run every example `wt <command> --help` prints, verbatim, against throwaway
# repositories — one per example, so nothing depends on what ran before it.
#
#   make build && scripts/verify-help-examples.sh
#
# The examples are read back out of the built binary, so this cannot drift
# from what help actually says. It ends by naming any printed example it did
# not run, and exits non-zero if one failed or was not run: CI runs it, so a
# new example that is not listed here fails the build. The one hook line
# carrying a literal placeholder path is run with a real path substituted.
#
# Not part of `make check`: it builds a few dozen git repositories and takes
# about a minute.
set -u
SRC=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WT=$SRC/bin/wt
if [ ! -x "$WT" ]; then echo "build it first: make build" >&2; exit 2; fi
BASE=${TMPDIR:-/tmp}/wt-ex.$$
mkdir -p "$BASE"
export PATH="$SRC/bin:$PATH"
n=0; pass=0; fail=0
FAILED=(); RAN=()

# make_repo is the bats fixture's, so the examples run against the repository
# the tests use.
source "$SRC/test/helpers.bash"

make_plain() {
    local d=$1/myrepo
    make_repo "$d"
    echo hello > "$d/README.md"
    printf 'test:\n\t@true\n' > "$d/Makefile"
    git -C "$d" add -A; git -C "$d" commit -qm init
    git -C "$d" branch release-2.1
    git -C "$d" tag v2.1
    git -C "$d" remote add origin "$d"; git -C "$d" fetch -q origin
    echo "$d"
}
# The same repository before it has a worktree.conf, for wt init.
make_noconf() {
    local d=$1/myrepo
    make_repo "$d"
    rm -r "$d/bin"
    echo hello > "$d/README.md"
    git -C "$d" add -A; git -C "$d" commit -qm init
    echo "$d"
}
make_worktrees() {
    local d; d=$(make_plain "$1")
    (cd "$d" && "$WT" new fix/login-crash --no-setup >/dev/null 2>&1)
    (cd "$d" && "$WT" new feat/api-tidy --no-setup >/dev/null 2>&1)
    git -C "$d" worktree add -q -b feat_wt/spare "$1/myrepo-login-crash"
    git -C "$d" worktree add -q -b chore_wt/old "$1/myrepo-old"
    echo dirt > "$1/myrepo_wt/feat_wt/api-tidy/dirt.txt"
    git -C "$1/myrepo_wt/feat_wt/api-tidy" add -A
    echo "$d"
}
make_sync() {
    local d; d=$(make_plain "$1")
    printf 'conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: %s\n    rule: max-plus-patch\n' "'^\\d'" > "$d/.wt-sync.yaml"
    printf '1.0.0\n' > "$d/v.txt"
    git -C "$d" add -A; git -C "$d" -c commit.gpgsign=false commit -qm declare
    local w
    for w in fix/login-crash feat/api-tidy; do (cd "$d" && "$WT" new "$w" --no-setup >/dev/null 2>&1); done
    printf '1.0.1\n' > "$1/myrepo_wt/fix_wt/login-crash/v.txt"
    git -C "$1/myrepo_wt/fix_wt/login-crash" -c commit.gpgsign=false -c user.email=t@e.com -c user.name=T commit -qam bump
    printf '1.0.2\n' > "$1/myrepo_wt/feat_wt/api-tidy/v.txt"
    git -C "$1/myrepo_wt/feat_wt/api-tidy" -c commit.gpgsign=false -c user.email=t@e.com -c user.name=T commit -qam bump
    printf '1.0.5\n' > "$d/v.txt"
    git -C "$d" -c commit.gpgsign=false commit -qam "trunk bump"
    git -C "$d" fetch -q origin
    echo "$d"
}
# make_sync, plus a file nothing in the declaration claims that moved on both
# sides: the run resolves v.txt, stops on a.txt and hands the worktree over.
# The resolution is already staged, so `wt sync resume` has something to
# continue.
make_resume() {
    local d w; d=$(make_sync "$1"); w=$1/myrepo_wt/fix_wt/login-crash
    printf 'branch\n' > "$w/a.txt"
    git -C "$w" add -A
    git -C "$w" -c commit.gpgsign=false -c user.email=t@e.com -c user.name=T commit -qm "a on the branch"
    printf 'trunk\n' > "$d/a.txt"
    git -C "$d" add -A; git -C "$d" -c commit.gpgsign=false commit -qm "a on trunk"
    git -C "$d" fetch -q origin
    (cd "$d" && "$WT" sync run login-crash --yes >/dev/null 2>&1)
    printf 'resolved\n' > "$w/a.txt"
    git -C "$w" add -- a.txt
    echo "$d"
}
# make_worktrees, plus a plain branch trunk already contains, so a sweep has
# a branch to delete, clean merged worktrees to remove (login-crash, spare and
# old are cut from trunk and never committed to) and a dirty merged worktree
# (api-tidy, with dirt staged) to keep and say why.
make_sweep() {
    local d; d=$(make_worktrees "$1")
    git -C "$d" branch done-work
    echo "$d"
}

# check <mode> <cwd-under-case-dir|""> <prereq|""> <example verbatim>
check() {
    local mode=$1 where=$2 prereq=$3 example=$4
    n=$((n+1))
    local dir="$BASE/case$n"; mkdir -p "$dir"
    local repo
    case $mode in
        plain) repo=$(make_plain "$dir");;
        noconf) repo=$(make_noconf "$dir");;
        worktrees) repo=$(make_worktrees "$dir");;
        sync) repo=$(make_sync "$dir");;
        resume) repo=$(make_resume "$dir");;
        sweep) repo=$(make_sweep "$dir");;
        branch) repo=$(make_plain "$dir"); git -C "$repo" branch fix_wt/login-crash;;
    esac
    local cwd=$repo
    [ -n "$where" ] && cwd="$dir/$where"
    RAN+=("$example")
    local out status
    out=$(cd "$cwd" && { [ -n "$prereq" ] && eval "$prereq" >/dev/null 2>&1; eval "$example"; } 2>&1 </dev/null); status=$?
    if [ $status -eq 0 ]; then
        pass=$((pass+1)); printf 'ok   %s\n' "$example"
    else
        fail=$((fail+1)); FAILED+=("$example")
        printf 'FAIL %s\n     exit %d: %s\n' "$example" "$status" "$(echo "$out"|tail -2|tr '\n' '|')"
    fi
}
SHELL_LAYER="source $SRC/shell/wt.sh"

check plain     "" "" 'wt new fix/login-crash'
check worktrees "" "$SHELL_LAYER" 'wt cd login-crash'
check worktrees "" "" 'wt list'
check sync      "" "" 'wt sync'
check worktrees "" "" 'wt remove login-crash'

check plain "" "" 'wt new fix/login-crash'
check plain "" "" 'wt new login-crash'
check plain "" "" 'wt new fix/login-crash --base v2.1'
check plain "" "" 'wt new spike/idea --no-setup'
check plain "" "" 'wt new fix/login-crash --skip-build'

check branch "" "" 'wt checkout fix_wt/login-crash'
check plain  "" "" 'wt checkout release-2.1 rel21'
check plain  "" "" 'wt checkout release-2.1 --no-setup'
check plain  "" "" 'wt checkout release-2.1 --skip-build'

check worktrees "" "$SHELL_LAYER" 'wt cd login'
check worktrees myrepo_wt/fix_wt/login-crash "$SHELL_LAYER" 'wt cd .'
check worktrees "" "$SHELL_LAYER" 'wt cd /'
check worktrees "" "$SHELL_LAYER" 'wt cd'
check worktrees "" "$SHELL_LAYER" 'wt exec login-crash git status'
check worktrees "" "$SHELL_LAYER" 'wt exec login-crash make test'
check worktrees "" "$SHELL_LAYER" 'wt exec / git log --oneline -5'
check worktrees myrepo_wt/fix_wt/login-crash "$SHELL_LAYER" 'wt exec . make test'

check worktrees "" "" 'wt ls'
check worktrees "" "" 'wt list | cat'
check worktrees "" "" 'wt status'
check worktrees "" "" 'wt status | grep dirty'
check sync      "" "" 'wt status login-crash'
check sync      myrepo_wt/fix_wt/login-crash "" 'wt status .'
check worktrees "" "" 'wt find login'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt find .'
check worktrees "" "" 'wt find /'
check worktrees "" "" 'wt find login --candidates'

check worktrees "" "" 'wt migrate login-crash'
check worktrees "" "" 'wt migrate ../myrepo-login-crash'
check worktrees "" "" 'wt migrate fix/login-crash chore/tidy'
check worktrees "" "" 'wt migrate login-crash --dry-run'
check worktrees "" "" 'wt migrate login-crash --force'

check worktrees "" "" 'wt adopt ../myrepo-login-crash'
check worktrees "" "" 'wt adopt ../myrepo-login-crash --relocate'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt adopt . --skip-build'

check worktrees myrepo_wt/fix_wt/login-crash "" 'wt setup'
check worktrees myrepo-login-crash           "" 'wt setup ../myrepo'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt setup --skip-build'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt setup --source superset'

check worktrees "" "" 'wt remove fix/login-crash'
check worktrees "" "" 'wt remove login-crash --yes'
check worktrees "" "" 'wt remove login-crash --force'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt remove --me'
check worktrees myrepo_wt/fix_wt/login-crash "" 'wt remove .'

check sweep "" "" 'wt sweep'
check sweep "" "" 'wt sweep --no-fetch'
check sweep "" "" 'wt sweep --yes'

check noconf "" "" 'wt init'
check noconf "" "" 'wt init --yes'
check plain  "" "" 'wt init --force'

check plain "" "" 'wt config'
check plain "" "" 'wt config --shell'
check plain "" "" 'wt doctor'
check worktrees "" "" 'wt doctor; echo $?'
check plain     "" "" 'wt path fix/login-crash'
check worktrees "" "" 'wt path login-crash'
check worktrees "" "" 'wt path fix_wt/login-crash'
check worktrees "" "" 'wt path /'
check plain     "" "" 'wt branch fix/login-crash'
check worktrees "" "" 'wt branch login-crash'
check worktrees "" "" 'wt branch /'
check plain "" "" 'wt about'
check plain "" "" 'wt version'

check sync "" "" 'wt sync --no-fetch'
check sync "" "" 'wt sync login-crash'
check sync "" "" 'wt sync login-crash --run'
check sync "" "" 'wt sync --run'
check sync "" "" 'wt sync run login-crash'
check sync "" "" 'wt sync run login-crash api-tidy'
check sync "" "" 'wt sync run --no-fetch'
check sync "" "" 'wt sync run login-crash api-tidy --yes'
check sync "" "" 'wt sync run login-crash --push'
check sync "" "" 'wt sync login-crash --run --no-push'
check resume "" "" 'wt sync resume login-crash'
check resume "" "" 'wt sync resume fix/login-crash'
check resume "" "" 'wt sync resume login-crash --no-push'
check resume "" "" 'wt sync resume login-crash --yes'
check resume "" "" 'wt sync login-crash --resume --push'
check sync "" 'wt sync run login-crash --yes' 'wt sync undo login-crash'
check sync "" 'wt sync run login-crash --yes' 'wt sync undo login-crash --force'
check sync "" 'wt sync run login-crash --yes' 'wt sync login-crash --undo --force'
check sync "" 'wt sync run login-crash --yes' 'wt sync login-crash --undo --yes'
check sync "" "" 'wt sync doctor'
check sync "" "" 'wt sync doctor --fix'
check sync "" "" 'wt sync doctor --prune'

check plain     "" "" 'wt hook claude-create <<< '"'"'{"name":"fix/login-crash"}'"'"''
check worktrees "" "" 'wt hook claude-remove <<< '"'"'{"name":"login-crash"}'"'"''
# The path form carries a placeholder; a real path is substituted for it, and
# the printed line is recorded as run so the coverage check below accepts it.
check worktrees "" "" 'wt hook claude-remove <<< "{\"path\":\"$PWD/../myrepo-old\"}"'
RAN+=('wt hook claude-remove <<< '"'"'{"path":"/abs/path/to/worktree"}'"'"'')

echo
echo "$pass/$n examples ran clean, $fail failed"
for f in "${FAILED[@]:-}"; do [ -n "$f" ] && echo "  FAILED: $f"; done

# command_paths <path>: <path> and every command under it, one per line, as
# the binary's own completion lists them. It runs from $BASE, outside any
# repository, and keeps only completions with a description, so a work name
# is never taken for a command.
command_paths() {
    local sub
    printf '%s\n' "$1"
    while IFS= read -r sub; do
        case $sub in help|completion) continue;; esac
        command_paths "${1:+$1 }$sub"
    done < <(cd "$BASE" && $WT __complete $1 '' 2>/dev/null | awk -F'\t' 'NF > 1 {print $1}')
}

# Coverage: every example the binary prints must appear above, verbatim. The
# commands are read from the binary too, so a new one is covered without being
# listed here; hook is hidden from completion, so it is named.
echo
missing=0
while IFS= read -r cmdpath; do
    while IFS= read -r line; do
        found=0
        for r in "${RAN[@]}"; do
            [ "$r" = "$line" ] && { found=1; break; }
            case "$r" in "$line"*) found=1; break;; esac
        done
        if [ $found -eq 0 ]; then echo "  NOT VERIFIED: $line"; missing=$((missing+1)); fi
    done < <($WT $cmdpath --help 2>/dev/null | awk '
        /^Examples:/ {f=1; next}
        f && /^[^ ]/ {f=0}
        f && /^  wt / {sub(/^  /, ""); sub(/[[:space:]]*#.*$/, ""); sub(/[[:space:]]*$/, ""); print}')
done < <(command_paths ""; command_paths hook)
echo "$missing printed examples were not run verbatim"
echo "fixtures under $BASE"
[ "$fail" -eq 0 ] && [ "$missing" -eq 0 ]
