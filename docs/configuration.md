# Configuration

wt reads two files. The repository's own, `bin/worktree/worktree.conf`, is
committed and says how that repository works. Yours, `~/.config/wt/config.toml`,
is per person and per machine: which integrations wt uses, how your branches are
named, and where your repositories live. `wt config` prints an overview of the
resolved configuration.

## What a repository keeps

Only configuration, and one-line shims where a repository still carries them:

```
bin/worktree/worktree.conf     how this repository works; wt init writes it
bin/worktree/provision.sh      optional: this repository's own setup step
bin/worktree/*.sh              one-line shims that exec wt
bin/worktree_functions.sh      a shim sourcing ~/.local/share/wt/worktree_functions.sh
```

`install.sh` writes `~/.local/share/wt/worktree_functions.sh`; on a mise install,
`wt shell --compat` prints it.

## `worktree.conf`

Read as a bash subset, or as `worktree.toml` if you prefer. Every key is
validated: an unknown or misspelled key is an error, not silence, and a retired
key is an error that says what became of it. `wt init` writes every key, the
ones it did not ask about commented out at their defaults, so the file is that
repository's reference for what it may set.

| key | type | default |
|---|---|---|
| `MAIN_BRANCH` | string | origin HEAD, else `development`, `main` or `master` |
| `WORKTREE_BRANCH_PREFIX` | string | `feat_wt` |
| `WORKTREE_TYPE_SUFFIX` | string | `_wt` |
| `WORKTREE_BRANCH_SUFFIX` | string | the type suffix |
| `WORKTREE_TYPE_NAMES` | list | empty: each type is called by its own name |
| `WORKTREE_DEFAULT_TYPE` | string | derived from the prefix |
| `WORKTREE_TYPES` | list | Conventional Commits types, `research` and `spike` |
| `DEVELOPER_CONFIG_DIRS` | list | `.cursor .claude .run .vscode .idea` |
| `DEVELOPER_CONFIG_FILES` | list | empty |
| `WORKTREE_CACHE_DIRS` | list | empty |
| `BUILD_INIT_ENABLED` | bool | true when a command is set |
| `BUILD_INIT_COMMAND` | string | required when build init is enabled |
| `REQUIRED_BINS` | list | empty |
| `TEST_COMMAND` | string | required when tests-before-remove is on |
| `RUN_TESTS_BEFORE_REMOVE` | bool | `false` |
| `SUPERSET_REGISTER` | `auto` `on` `off` | `auto` |

`WORKTREE_TYPE_SUFFIX` marks a type in a worktree path and in the branch that
goes with it. `WORKTREE_BRANCH_SUFFIX` overrides it for branches alone, and is
the one string key where an empty value is a value: `WORKTREE_BRANCH_SUFFIX=""`
gives branches with no suffix, and every path stays where it was (see
[Branches without the `_wt`](#branches-without-the-_wt)). Leave it out and the
choice falls to whoever clones the repository. `WORKTREE_BRANCH_PREFIX` follows
the type suffix when it is not set itself, so the two cannot disagree, and it
may be written the way the branches read (`feature_wt`) in a repository that
renames its types.

`WORKTREE_TYPE_NAMES` is `<type>=<name>` pairs, such as `(feat=feature docs=doc)`:
what the branches call a type. It is validated against `WORKTREE_TYPES` and
against itself, since two types that read alike would make a branch name
ambiguous. See [What each type is called](#what-each-type-is-called).

`DEVELOPER_CONFIG_DIRS` and `DEVELOPER_CONFIG_FILES` are the gitignored
developer files `wt setup` copies from the source checkout into a new worktree.

`WORKTREE_CACHE_DIRS` names gitignored build caches, such as `(node_modules
.gradle target)`, that `wt setup` clones from the source checkout before
`provision.sh` and the build run, so both start warm. The clone is copy-on-write
(APFS, Btrfs, XFS) and shares the files' data until one changes. Where the
filesystem cannot clone, the cache is skipped with a line saying why, never
copied. A cache already in the worktree is kept, and `--no-cache` skips them.

`SUPERSET_REGISTER` decides whether a new worktree is also registered as a
Superset workspace: `auto` when Superset can take it, `on` to also have
`wt doctor` count an unusable Superset as a problem, `off` to leave it alone.
Registration never fails a command under any of them, and none of them applies
until your own `superset` setting is `true`: a committed file cannot switch a
desktop integration on for whoever clones the repository. See
[Superset registers both ways](worktree-conventions.md#superset-registers-both-ways).

### A repository with no configuration

Every command also runs in a repository with no `worktree.conf`, on what
`wt init --yes` would write: trunk from origin's HEAD, else the first of
`development`, `main` and `master` that exists here or on origin, the default
prefix, no build command. These are detected on each run and never written to
the repository, which is what lets a harness make worktrees in a clone nobody
set up. `wt new` says so in one line on stderr, `wt config` shows the values as
`detected`, and `wt doctor` notes it without counting it as a problem. Nothing
that runs code is detected: only a `bin/worktree/provision.sh` the repository
carries runs.

When not even trunk can be detected, wt does not take the checked-out branch
for it: commands stop with "cannot tell which branch is trunk", `wt init --yes`
refuses, and `wt init` asks you to name it.

`wt init` rejects an answer that would not validate at the prompt, and warns
when the file would land somewhere git ignores: a repository that ignores `bin/`
for its build output also ignores `bin/worktree/`, so that configuration would
work for you and for nobody who clones the repository.

### `provision.sh`

An optional executable `wt setup` runs after copying developer config and
caches and before build init, with the new worktree as its working directory.
It gets `WT_PORT` (as `wt port` prints it), `WT_BRANCH` and `WT_WORK` in its
environment, and the build command gets the same three. This is where a
repository puts its own step, such as decrypting secrets or checking a cloud
identity.

## Branch names

### Branches without the `_wt`

The `_wt` on a branch is one setting. A repository whose branches should read
`fix/login-crash` says so in its own file:

```sh
WORKTREE_BRANCH_SUFFIX=""
```

and a person who wants that wherever the repository has not decided says so in
theirs, once:

```sh
wt config set branch_suffix ""      # or "-wt", or whatever yours carry
```

The worktrees do not move for it. The folders are wt's layout, and they keep
the suffix `WORKTREE_TYPE_SUFFIX` gives them:

```
code/
├─ myrepo/                                    ← the repository
└─ myrepo_wt/
   ├─ feat_wt/api-tidy/                       branch: feat/api-tidy
   └─ fix_wt/login-crash/                     branch: fix/login-crash
```

The repository outranks the person only where it said something: a committed
file that never mentions the suffix is not a decision, so whoever clones it may
still name their own branches. `wt config` and `wt doctor` print which file
decided.

A suffixed branch says at a glance that wt made it, and `wt remove` renames an
unmerged one out of the prefix to keep its commits (`fix_wt/login-crash` →
`login-crash`). Without a suffix, any `<word>/<name>` branch, such as
`dependabot/npm-x` or an agent's `claude/…`, reads as a worktree branch of type
`<word>`. Nothing breaks, because every lookup goes through `git worktree list`;
only the names change.

### What each type is called

`feat` or `feature`, `docs` or `doc`: the word is a preference, and the type
underneath it is not. Name the ones you want called something else, in the
repository's file or in your own:

```sh
WORKTREE_TYPE_NAMES=(feat=feature docs=doc)      # the repository's naming
wt config set type_names "feat=feature"          # or yours, everywhere
```

```
$ wt new feat/login-crash        # or feature/login-crash: the same work
  ~/src/myrepo_wt/feat_wt/login-crash        branch: feature/login-crash
```

The type is what the folders carry and what every command reasons in; only the
branch reads the word. Renaming one strands no checkout, and a branch named
before the change still resolves. Everything takes both spellings: `wt new`,
`wt migrate`, `wt path`, `wt branch`, the type read out of a bare name
(`wt new feature_dev-123`), the type read off somebody else's pull request
branch, and the completions.

The same two-file rule as the suffix applies: the repository where it named its
types, you everywhere else. A pair for a type a repository does not have does
not apply there. Two names for one type, or a name that is already another
type, would make a branch ambiguous: the repository's file is told so, and your
own pairs are dropped where they would collide.

### The type from the name

The type comes from the spec, as in `wt new fix/login-crash`, or, for a bare
name, from the name itself: `wt new fix_dev-123` creates `fix_wt/dev-123`. A name
whose first word is not a type is left whole and takes the repository's default
type, and an explicit `<type>/<work>` always wins. This lets the type survive a
tool that has nowhere to enter one, such as Superset, which mints every branch
from a single fixed prefix.

## Your own settings

```
$XDG_CONFIG_HOME/wt/config.toml     # or ~/.config/wt/config.toml
```

The file does not have to exist. No file means the built-in defaults, and
`wt config set` is the only thing that creates it.

| key | default | what it decides |
|---|---|---|
| `superset` | `false` | whether wt registers worktrees it creates as Superset workspaces |
| `github` | `true` | whether wt uses the GitHub CLI for branches and pull requests |
| `branch_suffix` | `_wt` | what your branches carry, where a repository does not say |
| `type_names` | empty | what your branches call each type, as `feat=feature` pairs |
| `ff_trunk` | `true` | whether `wt up` and `wt sync rebase` fast-forward local trunk after fetching it, when that is safe |
| `ref_sweep_patterns` | `backup/* safe/* safety/* backup-* safe-* safety-* *-backup *-safe *-safety` | the branch and tag names `wt refs sweep` treats as backups (globs) |
| `ref_sweep_age` | `14d` | how old a backup no other ref contains must be before `wt refs sweep` takes it |
| `root.<name>` | none | a directory of repositories, or one repository, for `--roots <name>` |
| `profile.<name>` | none | a list of repositories for `--profile <name>` |

```
wt config                      # everything, with where each value came from
wt config get superset         # one value alone, for a script
wt config set superset true    # written to your file; validated first
wt config unset superset       # back to the built-in default
wt config path                 # where the file is, whether or not it exists
```

`get`, `set`, `unset` and `path` work from anywhere: these are not repository
settings. An unknown key is an error naming the key. The integrations take
`true` or `false` and nothing else, `branch_suffix` takes what a branch can carry
before its slash (`""` included), and a bad value is refused before anything is
written. `set` edits the file in place, so your comments and ordering survive.
`wt config set --help` lists every setting.

`branch_suffix` and `type_names` are the settings a repository can overrule,
and only by naming its own `WORKTREE_BRANCH_SUFFIX` or `WORKTREE_TYPE_NAMES`.

Your `superset` setting is the master switch. With it `false`, wt never runs the
Superset CLI, whatever a repository's `SUPERSET_REGISTER` says; with it `true`,
that key can still decline. `wt config` shows the resolution:

```
user config:   /Users/you/.config/wt/config.toml (no file yet)
  superset:      false (default)
  github:        true (default)
  branch_suffix: "_wt" (default)
  type_names:    [] (default)
  ff_trunk:      true (default)
  ref_sweep_patterns: ["backup/*", "safe/*", …] (default)
  ref_sweep_age: "14d" (default)
superset mode: off (user config)
```

A file wt cannot parse turns every integration off for that run, and a file
with one bad key keeps its good keys; either way the command runs and says so in
one line. [Not applicable, and broken](worktree-conventions.md#not-applicable-and-broken)
has the detail.

### Roots and profiles

wt knows where your repositories live: your roots, set per machine.

```toml
# ~/.config/wt/config.toml
[roots]
work = "~/src/work"        # a folder: every repository one level down
oss = "~/src/oss"
dotfiles = "~/dotfiles"    # a repository: that one, searched no deeper

[profiles]
api = ["~/src/work/api", "~/src/work/billing"]
```

`wt config set root.work ~/src/work` and `wt config set profile.api "<dir> <dir>"`
write them, and `WT_ROOTS`, when set, stands in for every root. Only
repositories with a `bin/worktree` configuration count.

- `wt repos` lists them, and `wt cd <repo>` goes to one.
- `wt sweep --all` and `wt sync --all --rebase` work across every one, planned a
  few at a time and asked once; `wt sync --all` is the overview of each.
  `--roots work` or `--profile api` narrows the run.
- `wt list`, `wt status` and `wt doctor` take the same flags.
- `wt doctor` checks that every root is there and every profile entry is still a
  repository wt manages, inside a root.
