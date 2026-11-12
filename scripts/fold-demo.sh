#!/usr/bin/env bash
# fold-demo.sh — the per-layer fold of a stack (#119), end to end, through
# the real wt: a stack of five layers on one line with a generated spec, a
# fixup, version bumps and trunk moved on; then the look, the fold of the
# fixup in place, the rebase onto trunk, the CI check at every layer tip,
# and the undo. Prints what happens; exits non-zero when a check fails.
set -euo pipefail

usage() {
    cat <<'EOF'
usage: scripts/fold-demo.sh [-h] [--rule <rule>] [--keep]

Builds a throwaway repository shaped like a stack whose every PR is checked
by running a generator and git diff --exit-code gen/, then runs, with a
fake docker on PATH:
  wt sync tmp/fold --stack                  the look
  wt sync rebase tmp/fold --stack --autosquash   fold the fixup in place
  wt sync rebase tmp/fold --stack           onto trunk
  ./gen.sh && git diff --exit-code gen/     at every layer's new tip
  wt sync undo tmp/fold                     put it back

  --rule <rule>  the version rule on v.txt (default max-plus-patch)
  --keep         leave the repository in place and print where it is
  WT=<path>      the wt binary to run (default: built from this checkout)

Exit 0 when every layer tip passes its check and the undo puts the line
back, 1 when one does not, 2 for a usage error.
EOF
}

rule=max-plus-patch keep=
while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --rule) [ $# -ge 2 ] || { echo "fold-demo: --rule needs a value" >&2; exit 2; }; rule=$2; shift 2 ;;
        --keep) keep=1; shift ;;
        *) echo "fold-demo: unknown argument $1 (try -h)" >&2; exit 2 ;;
    esac
done
for tool in git tar; do
    command -v "$tool" >/dev/null || { echo "fold-demo: $tool is not on PATH" >&2; exit 1; }
done
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/wt-fold-demo.XXXXXX")
tmp=$(cd "$tmp" && pwd -P)
[ -n "$keep" ] || trap 'rm -rf "$tmp"' EXIT
if [ -z "${WT:-}" ]; then
    command -v go >/dev/null || { echo "fold-demo: go is not on PATH, and WT names no wt to run" >&2; exit 1; }
    (cd "$here" && go build -o "$tmp/bin/wt" ./cmd/wt)
    WT="$tmp/bin/wt"
fi
mkdir -p "$tmp/bin"
printf '#!/bin/sh\nexit 0\n' > "$tmp/bin/docker"
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" XDG_CONFIG_HOME="$tmp/config" NO_COLOR=1
wt() { "$WT" "$@"; }
say() { printf '\n\033[1m$ %s\033[0m\n' "$*"; }

repo="$tmp/demo"
git init -q -b main "$repo"
git -C "$repo" config user.email demo@example.com
git -C "$repo" config user.name Demo
git -C "$repo" config commit.gpgsign false
git -C "$repo" config core.hooksPath "$repo/.git/hooks"
mkdir -p "$repo/bin/worktree"
printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$repo/bin/worktree/worktree.conf"
cd "$repo"
cat > gen.sh <<'EOF'
#!/bin/sh
set -e
mkdir -p gen
{
  printf '{\n  "version": "%s",\n  "sources": [\n' "$(head -n 1 v.txt)"
  for f in src/*; do printf '    "%s: %s",\n' "$f" "$(cat "$f")"; done
  printf '    "end"\n  ]\n}\n'
} > gen/spec.json
EOF
chmod +x gen.sh
commit() { git add -A && git commit -q --no-verify -m "$1"; }
put() { mkdir -p "$(dirname "$1")" && printf '%s\n' "$2" > "$1"; }
put v.txt 1.0.0; put src/core.txt core; ./gen.sh; commit base
git checkout -q -b tmp/fold
put src/a.txt a; ./gen.sh; commit "feat(f1): a"
put v.txt 1.1.0; commit "chore(f1): bump to 1.1.0"; git branch f1
put docs/n.txt notes; commit "feat(f2): notes"
put docs/m.txt more; commit "feat(f2): more"; git branch f2
put src/c.txt c; ./gen.sh; commit "feat(f3): c"
put v.txt 1.1.1; commit "chore(f3): bump to 1.1.1"; git branch f3
put docs/d.txt d; commit "feat(f4): docs"; git branch f4
put v.txt 1.1.2; commit "chore(f5): bump to 1.1.2"; git branch f5
put docs/n.txt "notes, fixed"; commit "fixup! feat(f2): notes"
git checkout -q main
cat > .wt-sync.yaml <<EOF
conflicts:
  - paths: [v.txt]
    strategy: owned-line
    line: '^\d'
    rule: $rule
defer:
  - run: ./gen.sh
    paths: [gen/**]
    each: layer
    fold: owner
    needs: [docker]
    commit: "chore: regenerate the spec"
each_commit:
  - run: test ! -e broken.marker
EOF
commit "chore: declare"
put v.txt 1.1.0; commit "chore: bump to 1.1.0"
put src/t.txt trunk; ./gen.sh; commit "feat: trunk's own source"
put db/V2__trunk.sql "create table t (id int);"; commit "feat: trunk's migration"
git remote add origin "$repo"
git fetch -q origin
git worktree add -q "$tmp/f1" f1
git worktree add -q "$tmp/f3" f3
put "$tmp/f3/docs/m.txt" "work in progress"
git worktree add -q "$tmp/tmp_fold" tmp/fold

echo "The stack: five layers on tmp/fold, f1 and f3 own the spec, f5 bumps"
echo "without it, a fixup for f2 on top; f1 in a clean worktree, f3 in a dirty"
echo "one; trunk bumped to 1.1.0, changed a source and added a migration."
git log --format='  %h %s' --reverse main..tmp/fold

say wt sync tmp/fold --stack --no-fetch
wt sync tmp/fold --stack --no-fetch

say wt sync rebase tmp/fold --stack --autosquash --no-fetch --no-push
out=$(wt sync rebase tmp/fold --stack --autosquash --no-fetch --no-push 2>&1) || true
printf '%s\n' "$out"
move=$(printf '%s\n' "$out" | sed -n 's/.*f3 held: .*; to move it later: //p' | head -n 1)
if [ -n "$move" ]; then
    say "$move   # the held layer, moved as an agent would"
    sh -c "$move"
fi

say wt sync rebase tmp/fold --stack --no-fetch --no-push
out=$(wt sync rebase tmp/fold --stack --no-fetch --no-push 2>&1) || true
printf '%s\n' "$out"

say "./gen.sh && git diff --exit-code gen/   # at every layer's new tip, as CI does per PR"
fail=0
for b in f1 f2 f3 f4 f5; do
    tip=$(printf '%s\n' "$out" | sed -n "s/.*✓ $b  [0-9a-f]* → \([0-9a-f]*\) .*/\1/p" | head -n 1)
    [ -n "$tip" ] || tip=$(printf '%s\n' "$out" | sed -n "s/.*⏸ $b held: .* reset --keep \([0-9a-f]*\)$/\1/p" | head -n 1)
    if [ -z "$tip" ]; then
        printf '  ✗ %s: no new tip in the output\n' "$b"; fail=1; continue
    fi
    check="$tmp/check-$b"
    mkdir -p "$check"
    git archive "$tip" | tar -x -C "$check"
    (cd "$check" && git init -q && git add -A && git -c user.email=c@example.com -c user.name=C commit -qm tip && ./gen.sh)
    if git -C "$check" diff --quiet --exit-code gen/; then
        printf '  ✓ %s %s  v%s  gen/ unchanged\n' "$b" "$(git rev-parse --short "$tip")" "$(git show "$tip:v.txt")"
    else
        printf '  ✗ %s %s  gen/ drifts\n' "$b" "$(git rev-parse --short "$tip")"; fail=1
    fi
done
say "git log --format='%h %s' origin/main..tmp/fold   # the owners hold their layer's spec"
git log --format='  %h %s' --reverse origin/main..tmp/fold

line=$(git rev-parse tmp/fold)
say wt sync undo tmp/fold
wt sync undo tmp/fold
if [ "$(git rev-parse tmp/fold)" = "$line" ]; then
    echo "  ✗ the undo left tmp/fold where the rebase put it"; fail=1
fi
[ -z "$keep" ] || echo "kept: $repo"
exit $fail
