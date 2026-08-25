# Extracting worktree tooling into `wt`

**Date:** 2026-08-25
**Status:** Approved design, not yet implemented

## Problem

Seven repositories each carry a private copy of the same git-worktree tooling:

- `telcred/` — accessmanager, infrastructure, personal-v, residential, server
- `private/` — longhaul, recipus

Each copy is ~1,580 lines across 13 files (`bin/worktree/*.sh`,
`bin/worktree_functions.sh`, `bin/hooks/worktree-{create,remove}.sh`).
**11,075 lines total, of which ~9,500 are redundant duplicates.**

### The copies have drifted

Eleven of the thirteen files exist in two to four different variants. Only
`remove_me_function.sh` and `hooks/worktree-remove.sh` are identical everywhere.

| file | variants | file | variants |
|---|---|---|---|
| `worktree/setup.sh` | 4 | `worktree/status.sh` | 3 |
| `worktree_functions.sh` | 3 | `worktree/remove.sh` | 3 |
| `worktree/list.sh` | 3 | `worktree/setup_precheck.sh` | 3 |
| `hooks/worktree-create.sh` | 3 | `new.sh`/`switch.sh`/`checkout.sh`/`remove_me.sh` | 2 each |

### The evidence that settles it

**The same bug was found and fixed twice, independently, in two repos, with two
incompatible fixes — and neither reached the other five.**

The bug: a repo whose `WORKTREE_BRANCH_PREFIX` does not end in `_wt` derives a
default type that is absent from `WORKTREE_TYPES`, so *every* create fails with
"unknown worktree type".

- **server** (2026-07-31) appends the derived type to `WORKTREE_TYPES`.
- **accessmanager** (2026-08-17) falls back to `feat` and prints a warning.

Three further artifacts of the same disease:

- accessmanager quoted `source "${BASE_DIR}/..."` in four scripts, fixing paths
  containing spaces. **Stranded** — no other repo has it.
- accessmanager's `setup.sh` grew a `--skip-build` flag, exactly what CI and
  automated provisioning want. **Stranded.**
- infrastructure's `setup.sh` is **missing the `DEVELOPER_CONFIG_FILES` block
  entirely**. Setting that key in its `worktree.conf` today is silently ignored
  — a latent bug invisible until someone tries to use it.

### What is not the problem

`worktree.conf` is already a good per-repo config surface, and the four
`setup.sh` variants are **not** legitimate per-repo provisioning — real
provisioning is already externalised into config keys. The variants are drift.
The disease is *copying*, not bash.

### Secondary problems

- **Four path shapes in the wild.** `../<repo>-<work>` (flat, the Telcred
  default), `../<repo>_wt/<work>` (nested, longhaul/recipus),
  `../<repo>_wt/<repo>/<type>_wt/<work>` (the majority of the ~67 worktrees
  actually on disk, produced by neither code path — agent-invented, ~June 2026),
  and the agent-owned `~/.superset/worktrees/<repo>/<name>` and
  `<repo>/.claude/worktrees/<name>`. One worktree is nested *inside* its own
  main checkout.
- **Superset provisions nothing.** It creates worktrees in its own directory and
  never runs the repo's setup, so they lack gitignored config, decrypted secrets
  and installed dependencies.
- **Telcred concepts leak into a generic tool.** `AWS_SETUP_ENABLED` is a config
  key in repos that have nothing to do with AWS.
- **The built-in defaults are server-shaped.** `MAIN_BRANCH` defaults to
  `development` and `TEST_COMMAND` to `./gradlew test` — wrong for six of seven
  repos, which only works because all six override them explicitly.

## Goals

1. One implementation. Drift becomes structurally impossible.
2. Per-repo behaviour stays declared in the repo, as config.
3. Works for manual use and for every agent: Claude Code, Herdr (skills *and*
   plugin), Superset.
4. Installable by colleagues later without restructuring.
5. Misconfiguration produces an error, not silence.

## Non-goals

- Migrating the ~67 existing worktrees. They keep working as they are.
- Changing what provisioning *does* for any repo.
- Replacing Herdr or Superset.

## Decisions

| decision | choice | rationale |
|---|---|---|
| implementation | **Go binary `wt`** | typed config, real validation, testable, single-file distribution |
| tool home | **new standalone repo `anders-lindstrom/wt`** | longhaul and recipus are not Telcred, so a Telcred-owned repo is the wrong home; can go public later |
| repo surface | **`worktree.conf` + thin executable shims** | preserves `.claude/settings.json` paths, the Herdr plugin's `-x` checks, the Herdr skills' detection rule, and muscle memory |
| config format | **parse the existing bash subset; also accept TOML** | all 7 repos migrate with zero config edits; TOML conversion becomes optional and per-repo |
| worktree path | **`<parent>/<repo>_wt/<type>_wt/<work>`** | groups per repo, keeps checkouts adjacent to the repo, and the path tail is character-for-character the branch |
| audience | just Anders now, colleagues later | design for distribution, install for one |

## Architecture

Three layers, none of which can absorb another:

| layer | contents | why it must be separate |
|---|---|---|
| `wt` (Go binary) | all mechanism: create, remove, list, status, setup, adopt, path/branch resolution, config parsing and validation | the part that drifted; needs types and tests |
| `shell/wt.sh` | `wt_cd`, `wt_exec`, `wt_dir`, `wt_ls`, `wt_rm_me` | **a binary cannot change the calling shell's directory.** This is precisely why `remove_me_function.sh` exists in every repo today |
| `compat/worktree_functions.sh` | `load_worktree_config`, `get_worktree_path`, `worktree_branch_name`, `worktree_branch_at`, `strip_worktree_prefix` as thin wrappers over `wt path` / `wt branch` / `wt config --shell` | three Herdr skills and the Herdr plugin `source` this file by name; keeping it means **zero skill edits** |

The shell layer absorbs the `wt_exec` / `wt_dir` / `wt_cd` / `wt_ls` functions
currently living in `~/dotfiles/.config/worktree/scripts.sh`. They move here so
they ship with the tool that owns the concept and are available to colleagues;
`.zshrc` sources them from the install location instead.

Their fuzzy cross-repo resolution is retained as-is: current repo first, widening
to `$WT_ROOTS` only when the local match is weak, ambiguity resolved through
`fzf` when interactive and by failing with the candidate list when not. This is
the one place where behaviour deliberately spans repos; every `wt` subcommand is
scoped to a single repository.

### Resolution is always via git, never via path shape

Every lookup goes through `git worktree list --porcelain`. This is why all four
existing path shapes keep working with no migration — already demonstrated by
the dotfiles helpers, which resolve all four today.

## The repo contract

### `bin/worktree/worktree.conf` — unchanged path and name

Keeping the exact filename means nothing that references it has to move.

**Schema.** Every key is typed and validated; an unknown or misspelled key is an
**error**, not silence. This is what catches the infrastructure class of bug.

| key | type | default | notes |
|---|---|---|---|
| `MAIN_BRANCH` | string | *detected from origin HEAD* | no more `development` default |
| `WORKTREE_BRANCH_PREFIX` | string | `feat_wt` | |
| `WORKTREE_TYPE_SUFFIX` | string | `_wt` | |
| `WORKTREE_DEFAULT_TYPE` | string | derived from prefix | see "Resolving the rival fixes" |
| `WORKTREE_TYPES` | list | Conventional Commits + `research` `spike` | |
| `DEVELOPER_CONFIG_DIRS` | list | `.cursor .claude .run .vscode .idea` | copied into a new worktree |
| `DEVELOPER_CONFIG_FILES` | list | empty | copied into a new worktree |
| `BUILD_INIT_ENABLED` | bool | `true` | |
| `BUILD_INIT_COMMAND` | string | *required if enabled* | no more `./gradlew` default |
| `REQUIRED_BINS` | list | empty | checked by `wt doctor` |
| `TEST_COMMAND` | string | *required if tests-before-remove* | no more `./gradlew` default |
| `RUN_TESTS_BEFORE_REMOVE` | bool | `false` | |

**Retired keys:**

- `REPO_NAME` — always derivable from the main worktree. Only accessmanager sets
  it, redundantly.
- `WORKTREE_LAYOUT` — the tool now owns the path shape.
- `AWS_SETUP_ENABLED` — see below.

### `bin/worktree/provision.sh` — the repo's own setup step

`AWS_SETUP_ENABLED` never really meant "AWS". It verified an identity and ran
`etc/encrypted/bin/decrypt.sh local`. That is "this repo has a secret-decryption
step", which becomes an **optional executable `bin/worktree/provision.sh`**
invoked by `wt setup` after config copying and before build init.

Telcred repos put their AWS check and decrypt call in it. longhaul and recipus
simply do not have the file. The Telcred-specific concept leaves the generic
tool and becomes what it always was: repo-declared behaviour.

### Shims

`bin/worktree/{new,remove,remove_me,list,status,switch,checkout,setup,setup_precheck}.sh`
each become a one-line executable that `exec`s the corresponding `wt`
subcommand. **~10 lines per repo instead of ~1,580.** They must stay executable
at exactly these paths, because the Herdr plugin tests them with `-x`.

`bin/worktree_functions.sh` becomes a two-line shim sourcing the installed
compat layer.

`bin/hooks/worktree-{create,remove}.sh` are **deleted outright**; the hook
registration in `.claude/settings.json` points at `wt hook claude-create`
directly.

## Path and branch conventions

```
<parent>/<repo>_wt/<type>_wt/<work>          branch: <type>_wt/<work>

programmering/telcred/
├─ infrastructure/                            ← the repo
├─ infrastructure_wt/
│  └─ feat_wt/webkey_infra/                   branch: feat_wt/webkey_infra
├─ server/
└─ server_wt/
   ├─ feat_wt/scim_usersharing/               branch: feat_wt/scim_usersharing
   └─ fix_wt/login-crash/                     branch: fix_wt/login-crash
```

The path tail below `<repo>_wt/` is character-for-character the branch name, so
path and branch convert to each other with no rules to remember, and
`git worktree list` reads identically to the directory tree.

This changes today's convention, where the type lives only in the branch and the
directory is bare `<work>`. Directories become self-describing.

## Command surface

All subcommands operate on the repository containing the current directory,
resolved through git, so they work from the main checkout or any worktree of it.
`wt new` accepts a bare `<work>` as well, taking the repo's
`WORKTREE_DEFAULT_TYPE` — every existing invocation stays valid.

```
wt new <type>/<work>     create branch + worktree + provision
wt list                  every worktree, any shape
wt status                per-worktree git state
wt remove <work>         merge-checked removal (delete branch if merged,
                         rename out of <type>_wt/ if not)
wt remove --me           remove the worktree you are standing in
wt setup <src>           provision a worktree  [--skip-build]
wt adopt <path>          provision a worktree someone else created,
                         optionally relocating it  [--relocate]
wt migrate <work>        relocate an existing worktree to the canonical shape
wt path <work>           absolute path
wt branch <work>         branch name
wt doctor                config + environment + worktree health
wt config [--shell]      typed dump, or eval-able shell assignments
wt hook claude-create    Claude Code WorktreeCreate handler (JSON on stdin)
wt hook claude-remove    Claude Code WorktreeRemove handler
```

`remove --me` folds `remove_me.sh` in as a flag. `wt_rm_me` in the shell layer
handles cd-ing out of the doomed directory first.

`wt doctor` replaces `setup_precheck.sh` and additionally reports:
unprovisioned worktrees, worktrees nested inside a main checkout (infrastructure
currently has one), prunable entries, missing `REQUIRED_BINS`, and config errors.

## Agent integration

Five consumers, not four:

| consumer | today | after |
|---|---|---|
| manual | 7 copies of `bin/worktree/*.sh` | shims → `wt` |
| Claude Code | `bash ./bin/hooks/worktree-create.sh`, 3 variants | `wt hook claude-create` in `.claude/settings.json`; per-repo hook scripts deleted |
| Herdr skills | `source bin/worktree_functions.sh` | same path, now a shim → compat layer. **No skill edits** |
| Herdr plugin `lindstrom.worktree-setup` | `-x` checks on `bin/worktree/{new,remove,setup}.sh`, sources `worktree_functions.sh` | unchanged — the shims satisfy it exactly |
| Superset | bypasses everything; leaves unprovisioned checkouts | `wt adopt` |

**Superset cannot be integrated at creation time.** Its hook directory contains
only per-agent notification hooks (copilot, cursor, gemini, opencode); there is
no worktree lifecycle event to register against. `wt adopt` is therefore the
deliberate answer, not a workaround: any worktree created by any tool, now or
in future, can be provisioned after the fact by one command. It also covers
Claude Code's detached `.claude/worktrees/` checkouts.

## Resolving the rival fixes

Both existing fixes are replaced by a third, better answer: **validate at config
load and fail with a clear message naming `WORKTREE_DEFAULT_TYPE` as the
remedy.**

server's version silently accepts any type, which makes `WORKTREE_TYPES`
meaningless. accessmanager's warns and guesses. With typed validation neither is
needed — and **all seven repos currently set `WORKTREE_BRANCH_PREFIX="feat_wt"`,
which is valid, so no repo breaks.**

The three stranded fixes (path quoting, `--skip-build`, the missing
`DEVELOPER_CONFIG_FILES` block) simply become the single implementation's
behaviour.

## Testing

1. **Golden tests first.** Capture the current scripts' observable behaviour on
   throwaway repos *before* writing Go, then assert `wt` reproduces it. This is
   what makes a rewrite defensible rather than hopeful.
2. **Config conformance.** All seven real `worktree.conf` files are parsed in
   CI and asserted against their expected typed config, so the bash-subset
   parser is proven against actual data rather than invented samples.
3. **Integration tests** on throwaway git repos: create → setup → list → adopt →
   migrate → remove.
4. **Shell layer tests** under `bats`, since `wt_cd` and `wt_rm_me` cannot be
   tested from Go.

## Rollout

1. Build and test `wt`. **No repo changes at all.**
2. Pilot in **infrastructure** — no colleagues, smallest blast radius, and its
   `DEVELOPER_CONFIG_FILES` is currently dead, so the fix is observable.
3. Roll to the remaining six: shims in, script bodies deleted, hook
   registrations repointed, `provision.sh` extracted where `AWS_SETUP_ENABLED`
   was true.
4. Move the shell functions out of `~/dotfiles/.config/worktree/scripts.sh` and
   source them from the install location.
5. Retire the worktree responsibility of the `tc-ops:sync-scripts` skill, which
   exists largely to paper over this duplication.

## Consequences

- ~9,500 lines of duplicated shell deleted.
- A fix lands once and reaches all seven repos.
- Misconfiguration fails loudly at load rather than silently at runtime.
- Superset-created worktrees become provisionable.
- New dependency: repos require `wt` on PATH. Shims must fail with an
  installation hint rather than "command not found".
