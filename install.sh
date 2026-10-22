#!/usr/bin/env bash
# ./install.sh [prefix]              default: ~/.local
# ./install.sh --uninstall [prefix]  remove the files this installer put there
# Re-runnable. A file is overwritten or removed only when it is recognisably
# wt's own: the binary carries wt's module path, and each shell file opens with
# its own first line. Anything else at those paths is left alone.
set -euo pipefail

UNINSTALL=false
if [ "${1:-}" = --uninstall ]; then
    UNINSTALL=true
    shift
fi
if [ "$#" -gt 1 ] || [[ "${1:-}" = -* ]]; then
    echo "Usage: ./install.sh [--uninstall] [prefix]" >&2
    exit 2
fi
PREFIX="${1:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share/wt"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
FILES=(bin/wt share/wt/wt.sh share/wt/worktree_functions.sh)

refuse() { echo "wt install: $*" >&2; exit 1; }
for dir in "$BIN_DIR" "$PREFIX/share" "$SHARE_DIR"; do
    [ ! -L "$dir" ] || refuse "refusing symlink directory $dir"
done

# ours says whether the file at $PREFIX/$1 is one wt installed.
ours() {
    local file=$1
    [ -f "$PREFIX/$file" ] && [ ! -L "$PREFIX/$file" ] || return 1
    case "$file" in
        bin/wt) grep -aqF 'github.com/anders-lindstrom/wt' "$PREFIX/$file" ;;
        share/wt/wt.sh) [ "$(head -n 1 "$PREFIX/$file")" = "# Shell layer for wt." ] ;;
        share/wt/worktree_functions.sh)
            head -n 1 "$PREFIX/$file" | grep -q '^# Compatibility layer: the function contract' ;;
    esac
}

exists() { [ -e "$PREFIX/$1" ] || [ -L "$PREFIX/$1" ]; }

if $UNINSTALL; then
    kept=false
    for file in "${FILES[@]}"; do
        exists "$file" || continue
        if ours "$file"; then
            rm -- "$PREFIX/$file"
            echo "Removed: $PREFIX/$file"
        else
            echo "Kept, not installed by wt: $PREFIX/$file" >&2
            kept=true
        fi
    done
    # Records an earlier installer left beside the files.
    rm -f -- "$SHARE_DIR/.installed" "$SHARE_DIR/.installed.pending"
    rmdir "$SHARE_DIR" 2>/dev/null || true
    ! $kept
    exit
fi

for file in "${FILES[@]}"; do
    if exists "$file" && ! ours "$file"; then
        refuse "refusing to overwrite $PREFIX/$file: wt did not install it"
    fi
done
command -v go >/dev/null || refuse "go is required to build wt"
BUILD_DIR=$(mktemp -d)
TARGET_TMP=""
trap 'rm -rf -- "$BUILD_DIR"; rm -f -- "$TARGET_TMP"' EXIT

echo "Building wt..."
( cd "$SRC_DIR" && go build \
    -ldflags "$("$SRC_DIR/scripts/ldflags.sh")" \
    -o "$BUILD_DIR/wt" ./cmd/wt )
install -m 0644 "$SRC_DIR/shell/wt.sh" "$BUILD_DIR/wt.sh"
install -m 0644 "$SRC_DIR/compat/worktree_functions.sh" "$BUILD_DIR/worktree_functions.sh"
mkdir -p "$BIN_DIR" "$SHARE_DIR"

# An interrupted copy must leave the old complete file in place.
for file in "${FILES[@]}"; do
    mode=0644
    [ "$file" != bin/wt ] || mode=0755
    TARGET_TMP=$(mktemp "$PREFIX/$file.XXXXXX")
    install -m "$mode" "$BUILD_DIR/${file##*/}" "$TARGET_TMP"
    mv -f -- "$TARGET_TMP" "$PREFIX/$file"
    TARGET_TMP=""
done

echo
echo "Installed:"
for file in "${FILES[@]}"; do echo "  $PREFIX/$file"; done
echo
echo "Add to your shell rc:"
echo "  eval \"\$(wt shell)\""
echo
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Note: $BIN_DIR is not on your PATH." ;;
esac
