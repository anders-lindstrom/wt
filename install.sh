#!/usr/bin/env bash
# Install wt: the binary, the shell layer and the compat layer.
#
#   ./install.sh [prefix]      default prefix: ~/.local
#
# Add to your shell rc:
#   source <prefix>/share/wt/wt.sh
set -euo pipefail

PREFIX="${1:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share/wt"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

command -v go >/dev/null || { echo "go is required to build wt" >&2; exit 1; }

mkdir -p "$BIN_DIR" "$SHARE_DIR"

echo "Building wt..."
( cd "$SRC_DIR" && go build \
    -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o "$BIN_DIR/wt" ./cmd/wt )

install -m 0644 "$SRC_DIR/shell/wt.sh" "$SHARE_DIR/wt.sh"
install -m 0644 "$SRC_DIR/compat/worktree_functions.sh" "$SHARE_DIR/worktree_functions.sh"

echo
echo "Installed:"
echo "  $BIN_DIR/wt"
echo "  $SHARE_DIR/wt.sh"
echo "  $SHARE_DIR/worktree_functions.sh"
echo
echo "Add to your shell rc:"
echo "  source $SHARE_DIR/wt.sh"
echo
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Note: $BIN_DIR is not on your PATH." ;;
esac
