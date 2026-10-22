#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

setup() {
    export WT_INSTALL_REAL_GO="$(command -v go)"
    export WT_INSTALL_REAL_MV="$(command -v mv)"
    ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
    PREFIX="$BATS_TEST_TMPDIR/prefix with spaces"
    mkdir -p "$BATS_TEST_TMPDIR/fakes"
    cat > "$BATS_TEST_TMPDIR/fakes/go" <<'GO'
#!/bin/sh
if [ "$1" = version ]; then exec "$WT_INSTALL_REAL_GO" "$@"; fi
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then
        shift
        printf '#!/bin/sh\n# github.com/anders-lindstrom/wt\necho %s\n' "${WT_TEST_BUILD_TEXT:-test-build}" > "$1"
        exit 0
    fi
    shift
done
exit 1
GO
    chmod +x "$BATS_TEST_TMPDIR/fakes/go"
    export PATH="$BATS_TEST_TMPDIR/fakes:$PATH"
}

@test "install is re-runnable and uninstalls only what it installed" {
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    [ -x "$PREFIX/bin/wt" ]
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    touch "$PREFIX/share/wt/unrelated" "$PREFIX/share/wt/.installed" "$PREFIX/share/wt/.installed.pending"
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 0 ]
    [ ! -e "$PREFIX/bin/wt" ]
    [ ! -e "$PREFIX/share/wt/wt.sh" ]
    [ ! -e "$PREFIX/share/wt/worktree_functions.sh" ]
    [ -e "$PREFIX/share/wt/unrelated" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
    [ ! -e "$PREFIX/share/wt/.installed.pending" ]
}

@test "install and uninstall refuse a binary wt did not install" {
    mkdir -p "$PREFIX/bin"
    printf foreign > "$PREFIX/bin/wt"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cat "$PREFIX/bin/wt")" = foreign ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cat "$PREFIX/bin/wt")" = foreign ]
}

@test "a foreign Go binary is not overwritten" {
    mkdir -p "$PREFIX/bin"
    printf 'package main\nfunc main() {}\n' > "$BATS_TEST_TMPDIR/foreign.go"
    "$WT_INSTALL_REAL_GO" build -o "$PREFIX/bin/wt" "$BATS_TEST_TMPDIR/foreign.go"
    before=$(cksum < "$PREFIX/bin/wt")
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cksum < "$PREFIX/bin/wt")" = "$before" ]
}

@test "an earlier wt install is upgraded in place" {
    mkdir -p "$PREFIX/bin" "$PREFIX/share/wt"
    "$WT_INSTALL_REAL_GO" build -o "$PREFIX/bin/wt" "$ROOT/cmd/wt"
    cp "$ROOT/shell/wt.sh" "$PREFIX/share/wt/wt.sh"
    cp "$ROOT/compat/worktree_functions.sh" "$PREFIX/share/wt/worktree_functions.sh"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
}

@test "symlink targets are never overwritten or uninstalled" {
    bash "$ROOT/install.sh" "$PREFIX"
    cp "$PREFIX/bin/wt" "$BATS_TEST_TMPDIR/foreign"
    rm "$PREFIX/bin/wt"
    ln -s "$BATS_TEST_TMPDIR/foreign" "$PREFIX/bin/wt"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 1 ]
    [ -L "$PREFIX/bin/wt" ]
    [ -e "$BATS_TEST_TMPDIR/foreign" ]
}

@test "legacy shell symlinks are refused" {
    mkdir -p "$PREFIX/share/wt"
    ln -s "$ROOT/shell/wt.sh" "$PREFIX/share/wt/wt.sh"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ -L "$PREFIX/share/wt/wt.sh" ]
}
