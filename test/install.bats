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
        printf '#!/bin/sh\necho %s\n' "${WT_TEST_BUILD_TEXT:-test-build}" > "$1"
        exit 0
    fi
    shift
done
exit 1
GO
    chmod +x "$BATS_TEST_TMPDIR/fakes/go"
    export PATH="$BATS_TEST_TMPDIR/fakes:$PATH"
}

@test "install records ownership, upgrades and uninstalls only installed files" {
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    [ -x "$PREFIX/bin/wt" ]
    [ -f "$PREFIX/share/wt/.installed" ]
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    touch "$PREFIX/share/wt/unrelated"
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 0 ]
    [ ! -e "$PREFIX/bin/wt" ]
    [ ! -e "$PREFIX/share/wt/wt.sh" ]
    [ ! -e "$PREFIX/share/wt/worktree_functions.sh" ]
    [ -e "$PREFIX/share/wt/unrelated" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
}

@test "install and uninstall refuse an unrecorded binary" {
    mkdir -p "$PREFIX/bin"
    printf foreign > "$PREFIX/bin/wt"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cat "$PREFIX/bin/wt")" = foreign ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cat "$PREFIX/bin/wt")" = foreign ]
}

@test "replaced files are preserved on upgrade and uninstall" {
    bash "$ROOT/install.sh" "$PREFIX"
    printf foreign > "$PREFIX/bin/wt"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cat "$PREFIX/bin/wt")" = foreign ]
    [ -e "$PREFIX/share/wt/.installed" ]
    [ ! -e "$PREFIX/share/wt/wt.sh" ]
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

@test "a proven legacy installation is adopted and recorded" {
    mkdir -p "$PREFIX/bin" "$PREFIX/share/wt"
    "$WT_INSTALL_REAL_GO" build -o "$PREFIX/bin/wt" "$ROOT/cmd/wt"
    cp "$ROOT/shell/wt.sh" "$PREFIX/share/wt/wt.sh"
    cp "$ROOT/compat/worktree_functions.sh" "$PREFIX/share/wt/worktree_functions.sh"
    run --separate-stderr bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    [ -f "$PREFIX/share/wt/.installed" ]
    [ "$(wc -l < "$PREFIX/share/wt/.installed" | tr -d ' ')" = 3 ]
    for file in bin/wt share/wt/wt.sh share/wt/worktree_functions.sh; do
        [[ "$stderr" == *"Adopted legacy file: $PREFIX/$file"* ]]
        [[ "$output" != *"Adopted legacy file:"* ]]
    done
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
}

@test "a foreign Go binary is not adopted" {
    mkdir -p "$PREFIX/bin"
    printf 'package main\nfunc main() {}\n' > "$BATS_TEST_TMPDIR/foreign.go"
    "$WT_INSTALL_REAL_GO" build -o "$PREFIX/bin/wt" "$BATS_TEST_TMPDIR/foreign.go"
    before=$(cksum < "$PREFIX/bin/wt")
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ "$(cksum < "$PREFIX/bin/wt")" = "$before" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
}

@test "a legacy binary cannot be adopted without go" {
    mkdir -p "$PREFIX/bin"
    "$WT_INSTALL_REAL_GO" build -o "$PREFIX/bin/wt" "$ROOT/cmd/wt"
    mkdir -p "$BATS_TEST_TMPDIR/no-go"
    ln -s "$(command -v dirname)" "$BATS_TEST_TMPDIR/no-go/dirname"
    run env PATH="$BATS_TEST_TMPDIR/no-go" /bin/bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [[ "$output" == *"refusing to overwrite"* ]]
    [ ! -e "$PREFIX/share/wt/.installed" ]
}

@test "legacy shell symlinks are refused" {
    mkdir -p "$PREFIX/share/wt"
    ln -s "$ROOT/shell/wt.sh" "$PREFIX/share/wt/wt.sh"
    run bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 1 ]
    [ -L "$PREFIX/share/wt/wt.sh" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
}

# Kill only this installer, immediately after the binary's atomic rename.
interrupt_after_binary() {
    cat > "$BATS_TEST_TMPDIR/fakes/mv" <<'MV'
#!/bin/sh
"$WT_INSTALL_REAL_MV" "$@" || exit $?
for destination; do :; done
if [ "${WT_TEST_INTERRUPT_INSTALL:-}" = 1 ] && [ "$destination" = "$WT_TEST_INSTALL_DEST/bin/wt" ]; then
    kill -KILL "$PPID"
fi
MV
    chmod +x "$BATS_TEST_TMPDIR/fakes/mv"
}

@test "repeated interrupted upgrades can be completed and then uninstalled" {
    bash "$ROOT/install.sh" "$PREFIX"
    before=$(cksum < "$PREFIX/bin/wt")
    interrupt_after_binary
    for build in upgrade retry; do
        run env TMPDIR="$BATS_TEST_TMPDIR" WT_TEST_BUILD_TEXT="$build" \
            WT_TEST_INTERRUPT_INSTALL=1 WT_TEST_INSTALL_DEST="$PREFIX" \
            bash "$ROOT/install.sh" "$PREFIX"
        [ "$status" -eq 137 ]
        [ -f "$PREFIX/share/wt/.installed.pending" ]
        [ "$(cksum < "$PREFIX/bin/wt")" != "$before" ]
    done
    run env WT_TEST_BUILD_TEXT=recovered bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 0 ]
    [ ! -e "$PREFIX/share/wt/.installed.pending" ]
    [ "$(wc -l < "$PREFIX/share/wt/.installed" | tr -d ' ')" = 3 ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 0 ]
    [ ! -e "$PREFIX/bin/wt" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
}

@test "an interrupted upgrade can be uninstalled without finishing" {
    bash "$ROOT/install.sh" "$PREFIX"
    interrupt_after_binary
    run env TMPDIR="$BATS_TEST_TMPDIR" WT_TEST_BUILD_TEXT=upgrade \
        WT_TEST_INTERRUPT_INSTALL=1 WT_TEST_INSTALL_DEST="$PREFIX" \
        bash "$ROOT/install.sh" "$PREFIX"
    [ "$status" -eq 137 ]
    run bash "$ROOT/install.sh" --uninstall "$PREFIX"
    [ "$status" -eq 0 ]
    [ ! -e "$PREFIX/bin/wt" ]
    [ ! -e "$PREFIX/share/wt/wt.sh" ]
    [ ! -e "$PREFIX/share/wt/worktree_functions.sh" ]
    [ ! -e "$PREFIX/share/wt/.installed" ]
    [ ! -e "$PREFIX/share/wt/.installed.pending" ]
}
