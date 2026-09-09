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

## Setting up a repository

```sh
cd your-repo
wt init          # writes bin/worktree/worktree.conf
wt doctor        # check it
```

`wt init` asks for the three keys a repository actually varies, offering
detected values as the defaults:

```
main branch [main]:
branch prefix [feat_wt]:
build command (blank for none): make build
```

Every other key is written **commented at its default**, so the file is this
repository's reference for what it may set — uncommenting a line as it stands
changes nothing. An answer that would not validate is rejected at the prompt,
not written and then discovered by the next command:

```
  ! WORKTREE_BRANCH_PREFIX="wip_wt" yields default type "wip", which is not in
    WORKTREE_TYPES; set WORKTREE_DEFAULT_TYPE to choose one of: feat fix docs …
```

With `--yes`, or with no terminal to answer from, the detected values are
written without asking — so a script, a hook or an agent gets the same result
without hanging on a prompt. `--force` replaces a configuration already there;
without it, an existing file is an error rather than something to overwrite.

If the config lands somewhere git ignores — a repo ignoring `bin/` for its
build output also ignores `bin/worktree/` — `wt init` says so, because that
configuration would work for you and for nobody who clones the repo.

## Commands

| | |
|---|---|
| `wt init` | create this repository's `worktree.conf` (`--yes` to skip the prompts) |
| `wt new <type>/<work>` | create a branch and worktree, then provision it |
| `wt list` | every worktree, in any layout; `!` marks one off the canonical path |
| `wt status` | each worktree's branch and whether it is clean |
| `wt sync` | what rebasing each worktree onto trunk would do, simulated; changes nothing |
| `wt remove <work>` | remove a worktree; delete its branch only when merged (`--yes` to skip the prompt) |
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

Only configuration and one-line shims, the first of which `wt init` writes:

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

**Anything `wt list` prints is a valid argument** — the work name, the branch, or
the path — as is `<type>/<work>`:

```sh
wt remove wt-migration                       # WORK column
wt remove chore_wt/wt-migration              # BRANCH column
wt remove ~/src/repo_wt/chore_wt/wt-migration  # PATH column
wt remove chore/wt-migration                 # <type>/<work>
```

Matching is exact and stays inside this repository. `wt cd` may guess at a name;
a wrong guess there costs a directory change, and here it costs a checkout — so
a work name used under two types must be disambiguated by its type, and there is
no fallback to the repository's default type. (`wt new` still has one: it names
a worktree that does not exist yet.)

**What the removal will do is printed before it does it:**

```
  path    /Users/you/src/repo_wt/chore_wt/wt-migration
  branch  chore_wt/wt-migration — not merged into main
  state   clean

  the checkout will be deleted
  the branch will be kept as "wt-migration"

Remove it? [y/N]
```

In a terminal you are asked to confirm; `--yes` skips the question, and a script,
hook or agent with no terminal is never asked. Git refuses to remove a checkout
with uncommitted changes, which is why the plan reports `state` before you
answer rather than after.

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
