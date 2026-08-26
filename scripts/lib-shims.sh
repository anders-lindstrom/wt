#!/usr/bin/env bash
# Shared by migrate-repo.sh and the re-slim pass: write the shims and the one
# README that explains them, so the explanation lives in a single file per repo
# rather than repeated in a header on every script.

write_shims() {
    local w
    _shim() {
        local f="$1"; shift
        cat > "$f" <<EOF
#!/usr/bin/env bash
# Shim — see README.md
command -v wt >/dev/null || { echo "wt not installed — see bin/worktree/README.md" >&2; exit 1; }
exec wt $* "\$@"
EOF
        chmod +x "$f"
    }
    _shim bin/worktree/new.sh            new
    _shim bin/worktree/remove.sh         remove
    _shim bin/worktree/remove_me.sh      remove --me
    _shim bin/worktree/list.sh           list
    _shim bin/worktree/status.sh         status
    _shim bin/worktree/setup.sh          setup
    _shim bin/worktree/setup_precheck.sh doctor
    _shim bin/worktree/checkout.sh       checkout

    cat > bin/worktree/switch.sh <<'EOF'
#!/usr/bin/env bash
# Shim — see README.md
command -v wt >/dev/null || { echo "wt not installed — see bin/worktree/README.md" >&2; exit 1; }
cd "$(wt path "$1")" && exec "${SHELL:-/bin/sh}"
EOF
    chmod +x bin/worktree/switch.sh

    cat > bin/worktree_functions.sh <<'EOF'
# Shim — see bin/worktree/README.md
if [[ -f "$HOME/.local/share/wt/worktree_functions.sh" ]]; then
    source "$HOME/.local/share/wt/worktree_functions.sh"
else
    echo "wt not installed — see bin/worktree/README.md" >&2
    return 1 2>/dev/null || exit 1
fi
EOF

    cat > bin/worktree/README.md <<'EOF'
# Worktree tooling

The scripts in this directory are **shims**. The implementation lives in
[`wt`](https://github.com/anders-lindstrom/wt) — one copy, shared by every repo,
so a fix lands once instead of being copied into seven.

```sh
./bin/worktree/new.sh fix/login-crash     # same as: wt new fix/login-crash
wt --help                                 # everything else
```

The shims stay because several things reference these exact paths:
`.superset/config.json`, the Herdr plugin, `.claude` hooks, and habit.

## What this repository owns

| file | |
|---|---|
| `worktree.conf` | how this repo works — main branch, build command, which config to copy into a new worktree, required tools |
| `provision.sh` | optional: this repo's own setup step, run in each new worktree (decrypting secrets, checking a cloud identity) |

Everything else here is a shim, including `../worktree_functions.sh`, which
exists for tools that source it by name.

## Notes

`switch.sh` spawns a *new* shell in the worktree, as it always has. To change
your current shell's directory use the `wt_cd` function that ships with `wt`.

## Install

```sh
git clone git@github.com:anders-lindstrom/wt.git && cd wt && ./install.sh
```

Then run `wt doctor` in this repo if something looks wrong.
EOF
}
