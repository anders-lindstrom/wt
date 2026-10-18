#!/usr/bin/env bash
# ./install.sh [prefix]              default: ~/.local
# ./install.sh --uninstall [prefix]  remove unchanged files from this installer
# Ownership is recorded by POSIX cksum (checksum and size) in share/wt/.installed.
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
MANIFEST="$SHARE_DIR/.installed"
PENDING="$SHARE_DIR/.installed.pending"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
FILES=(bin/wt share/wt/wt.sh share/wt/worktree_functions.sh)

refuse() { echo "wt install: $*" >&2; exit 1; }
for dir in "$BIN_DIR" "$PREFIX/share" "$SHARE_DIR"; do
    [ ! -L "$dir" ] || refuse "refusing symlink directory $dir"
done
for record in "$MANIFEST" "$PENDING"; do
    [ ! -L "$record" ] || refuse "refusing symlink $record"
    if [ -e "$record" ] && [ ! -f "$record" ]; then
        refuse "install record is not a regular file: $record"
    fi
done

owned() {
    local file=$1 record actual
    [ -f "$PREFIX/$file" ] && [ ! -L "$PREFIX/$file" ] || return 1
    actual=$(cksum < "$PREFIX/$file")
    for record in "$MANIFEST" "$PENDING"; do
        if [ -f "$record" ] && awk -v file="$file" -v sum="$actual" '
            $3 == file && $1 " " $2 == sum {found = 1}
            END {exit !found}' "$record"; then
            return 0
        fi
    done
    return 1
}

legacy_owned() {
    local file=$1 metadata
    [ ! -e "$MANIFEST" ] && [ ! -e "$PENDING" ] && [ -f "$PREFIX/$file" ] && [ ! -L "$PREFIX/$file" ] || return 1
    case "$file" in
        bin/wt)
            command -v go >/dev/null || return 1
            metadata=$(go version -m "$PREFIX/$file" 2>/dev/null) || return 1
            awk '$1 == "path" && $2 == "github.com/anders-lindstrom/wt/cmd/wt" && NF == 2 {found = 1}
                 END {exit !found}' <<< "$metadata"
            ;;
        share/wt/wt.sh|share/wt/worktree_functions.sh) return 0 ;;
        *) return 1 ;;
    esac
}

if $UNINSTALL; then
    [ -f "$MANIFEST" ] || [ -f "$PENDING" ] || refuse "no install record at $MANIFEST; nothing removed"
    kept=false
    for file in "${FILES[@]}"; do
        if [ ! -e "$PREFIX/$file" ] && [ ! -L "$PREFIX/$file" ]; then
            continue
        fi
        if owned "$file"; then
            rm -- "$PREFIX/$file"
            echo "Removed: $PREFIX/$file"
        else
            echo "Kept modified or foreign file: $PREFIX/$file" >&2
            kept=true
        fi
    done
    if $kept; then
        exit 1
    fi
    rm -f -- "$MANIFEST" "$PENDING"
    rmdir "$SHARE_DIR" 2>/dev/null || true
    exit 0
fi

ADOPTED=()
for file in "${FILES[@]}"; do
    if [ -e "$PREFIX/$file" ] || [ -L "$PREFIX/$file" ]; then
        if owned "$file"; then
            continue
        fi
        if legacy_owned "$file"; then
            ADOPTED+=("$file")
        else
            refuse "refusing to overwrite $PREFIX/$file (no matching install checksum or proven legacy owner)"
        fi
    fi
done
command -v go >/dev/null || refuse "go is required to build wt"
BUILD_DIR=$(mktemp -d)
RECORD_TMP=""
TARGET_TMP=""
trap 'rm -rf -- "$BUILD_DIR"; rm -f -- "$RECORD_TMP" "$TARGET_TMP"' EXIT

echo "Building wt..."
( cd "$SRC_DIR" && go build \
    -ldflags "$("$SRC_DIR/scripts/ldflags.sh")" \
    -o "$BUILD_DIR/wt" ./cmd/wt )
install -m 0644 "$SRC_DIR/shell/wt.sh" "$BUILD_DIR/wt.sh"
install -m 0644 "$SRC_DIR/compat/worktree_functions.sh" "$BUILD_DIR/worktree_functions.sh"
for file in "${FILES[@]}"; do
    printf '%s %s\n' "$(cksum < "$BUILD_DIR/${file##*/}")" "$file"
done > "$BUILD_DIR/.installed"
mkdir -p "$BIN_DIR" "$SHARE_DIR"

# Keep the currently owned files in pending too, so repeated interrupted
# attempts still recognize files from the preceding pending upgrade.
RECORD_TMP=$(mktemp "$SHARE_DIR/.installed.pending.XXXXXX")
for file in "${FILES[@]}"; do
    if [ -e "$PREFIX/$file" ] || [ -L "$PREFIX/$file" ]; then
        owned "$file" || legacy_owned "$file" || refuse "file changed while building: $PREFIX/$file"
        printf '%s %s\n' "$(cksum < "$PREFIX/$file")" "$file"
    fi
done > "$RECORD_TMP"
cat "$BUILD_DIR/.installed" >> "$RECORD_TMP"
mv -f -- "$RECORD_TMP" "$PENDING"
RECORD_TMP=""

# An interrupted copy must leave the old complete file in place.
for file in "${FILES[@]}"; do
    mode=0644
    [ "$file" != bin/wt ] || mode=0755
    TARGET_TMP=$(mktemp "$PREFIX/$file.XXXXXX")
    install -m "$mode" "$BUILD_DIR/${file##*/}" "$TARGET_TMP"
    mv -f -- "$TARGET_TMP" "$PREFIX/$file"
    TARGET_TMP=""
done

# On success retain only the new checksums, then promote the record.
RECORD_TMP=$(mktemp "$SHARE_DIR/.installed.pending.XXXXXX")
cat "$BUILD_DIR/.installed" > "$RECORD_TMP"
mv -f -- "$RECORD_TMP" "$PENDING"
RECORD_TMP=""
mv -f -- "$PENDING" "$MANIFEST"
if [ "${#ADOPTED[@]}" -gt 0 ]; then
    for file in "${ADOPTED[@]}"; do
        echo "Adopted legacy file: $PREFIX/$file" >&2
    done
fi

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
