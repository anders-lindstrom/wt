# wt

Git worktree tooling: **one implementation, per-repo configuration.**

Replaces the ~1,580-line copy of worktree scripts that seven repositories each
carried privately — copies that had drifted into two to four variants of nearly
every file, to the point where the same bug was found and fixed twice, in two
repos, with two incompatible fixes, and neither reached the other five.

## Install

```sh
git clone git@github.com:anders-lindstrom/wt.git && cd wt
./install.sh                 # builds to ~/.local/bin, installs to ~/.local/share/wt
```

Then in your shell rc:

```sh
source ~/.local/share/wt/wt.sh
```

## Commands

| | |
|---|---|
| `wt new <type>/<work>` | create a branch and worktree, then provision it |
| `wt list` | every worktree, in any layout; `!` marks one off the canonical path |
| `wt status` | each worktree's branch and whether it is clean |
| `wt remove <type>/<work>` | remove a worktree; delete its branch only when merged |
| `wt setup <source-dir>` | provision the current worktree |
| `wt adopt <path>` | provision a worktree another tool created (`--relocate` to move it) |
| `wt migrate <type>/<work>` | move a worktree to the canonical path |
| `wt find <pattern>` | resolve a worktree by fuzzy name, across repositories |
| `wt doctor` | check config, required tools and worktree health |
| `wt path` / `wt branch` | resolve one piece of work |
| `wt config [--shell]` | the resolved configuration, typed or eval-able |
| `wt completion zsh` | shell completion, including live work names |

A bare `<work>` takes the repository's default type, so `wt new thing` creates
`feat_wt/thing`.

## Shell functions

A binary cannot change its caller's directory. These do:

| | |
|---|---|
| `wt cd [pattern]` | cd to a worktree, in this shell; bare or `.` returns to the main checkout |
| `wt exec <pattern> <cmd>…` | run a command there, in a subshell |
| `wt_cd <pattern>` | the same as `wt cd`, if you prefer the underscore form |
| `wt_exec <pattern> <cmd>…` | run a command there, in a subshell; your shell stays put |
| `wt_dir <pattern>` | print the path (stdout is path-only) |
| `wt_ls [pattern]` | list worktrees, or show what a pattern matches |
| `wt_rm_me` | remove the worktree you are standing in |

`wt cd` and `wt exec` are the same functions under a nicer name: `wt` is itself a
shell function that handles those two and passes everything else to the binary,
because a process cannot change its caller's directory.

They are thin wrappers over `wt find`; the matching itself lives in the binary
where it is tested. A pattern of `.` means the repository's main checkout.

## Layout

```
<parent>/<repo>_wt/<type>_wt/<work>          branch: <type>_wt/<work>

programmering/telcred/
├─ infrastructure/                            ← the repo
└─ infrastructure_wt/
   ├─ feat_wt/webkey_infra/                   branch: feat_wt/webkey_infra
   └─ fix_wt/login-crash/                     branch: fix_wt/login-crash
```

The path tail below `<repo>_wt/` is character-for-character the branch name, so
the two convert with no rules to remember.

**Worktrees in other layouts keep working.** Every lookup goes through
`git worktree list`, never the shape of a path, so worktrees made by Superset,
by plain `git worktree add`, or before a repo was migrated all resolve. `wt
doctor` lists them and `wt migrate` moves one when you want.

## What a repository keeps

Only configuration and one-line shims:

```
bin/worktree/worktree.conf     how this repo works
bin/worktree/provision.sh      optional: this repo's own setup step
bin/worktree/*.sh              one-line shims that exec wt
bin/worktree_functions.sh      shim sourcing the compat layer
```

### `worktree.conf`

Read as the existing bash subset, or as `worktree.toml` if you prefer. Every key
is validated: **an unknown or misspelled key is an error, not silence.**

| key | type | default |
|---|---|---|
| `MAIN_BRANCH` | string | detected from origin HEAD |
| `WORKTREE_BRANCH_PREFIX` | string | `feat_wt` |
| `WORKTREE_TYPE_SUFFIX` | string | `_wt` |
| `WORKTREE_DEFAULT_TYPE` | string | derived from the prefix |
| `WORKTREE_TYPES` | list | Conventional Commits + `research` `spike` |
| `DEVELOPER_CONFIG_DIRS` | list | `.cursor .claude .run .vscode .idea` |
| `DEVELOPER_CONFIG_FILES` | list | empty |
| `BUILD_INIT_ENABLED` | bool | true when a command is set |
| `BUILD_INIT_COMMAND` | string | required when build init is enabled |
| `REQUIRED_BINS` | list | empty |
| `TEST_COMMAND` | string | required when tests-before-remove is on |
| `RUN_TESTS_BEFORE_REMOVE` | bool | `false` |

Retired: `REPO_NAME` (derived), `WORKTREE_LAYOUT` (the tool owns the shape),
`AWS_SETUP_ENABLED` (became `provision.sh`).

### `provision.sh`

An optional executable run by `wt setup` after config copying and before build
init, with the new worktree as its working directory. This is where a repo puts
its own step — decrypting secrets, checking a cloud identity — instead of the
tool carrying a flag for it.

## Removing a worktree is careful

`wt remove` reads the branch **from the worktree**, never rebuilding it from the
name: once the type can vary, a reconstructed name may belong to an unrelated
branch. It touches no branch at all on a detached HEAD, or on a branch that does
not follow the convention and so belongs to someone else. A merged branch is
deleted; **an unmerged one is renamed out of the prefix, never deleted**, so work
in progress cannot be lost.

## Development

```sh
make check      # lint + go test + bats
make build
```

## Documentation

- [Design spec](docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md)
- [Implementation plans](docs/superpowers/plans/)
