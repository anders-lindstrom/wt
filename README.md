# wt

Git worktree tooling: one implementation, per-repo configuration.

Replaces the ~1,580-line copy of worktree scripts that seven repositories each
carry privately, and which has drifted into 2–4 variants of nearly every file.

- `wt` — the CLI (create, remove, list, setup, adopt, doctor)
- `shell/wt.sh` — `wt_cd`, `wt_exec`, `wt_dir`, `wt_ls`, `wt_rm_me`, for the
  things a binary cannot do to your shell
- `compat/worktree_functions.sh` — preserves the function contract that the
  Herdr skills and plugin source by name

Repositories keep only `bin/worktree/worktree.conf`, an optional
`bin/worktree/provision.sh`, and one-line shims.

**Status:** design approved, implementation not started. See
[the design spec](docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md).
