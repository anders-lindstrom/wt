# wt

Git worktree tooling: one implementation, configured per repository.

wt makes, finds, keeps up to date and cleans up git worktrees. By default, every
worktree wt makes lives in one folder beside the repository, at a path that is
its branch name, and is provisioned when it is made: developer config copied
in, caches cloned, the repository's own setup step and build run. Each
repository says how it works in a small committed file; one with no file works
too.

```
<parent>/<repo>_wt/<type>_wt/<work>          branch: <type>_wt/<work>

code/
├─ myrepo/                                    ← the repository
└─ myrepo_wt/
   ├─ feat_wt/api-tidy/                       branch: feat_wt/api-tidy
   └─ fix_wt/login-crash/                     branch: fix_wt/login-crash
```

Why the layout has this shape, and the rules a tool building on it follows:
[docs/worktree-conventions.md](docs/worktree-conventions.md).

## Install

A release, with [mise](https://mise.jdx.dev):

```sh
mise use -g github:anders-lindstrom/wt   # the newest release, kept on the PATH
```

Or built from a checkout:

```sh
git clone git@github.com:anders-lindstrom/wt.git && cd wt
./install.sh                 # builds to ~/.local/bin, installs to ~/.local/share/wt
```

Then, either way, in your shell rc:

```sh
eval "$(wt shell)"
```

A binary cannot change its caller's directory, so `wt cd`, `wt exec` and
`wt attach` live in that shell layer. It also defines `wt_cd`, `wt_exec`,
`wt_attach`, `wt_dir` (print the path), `wt_ls` (list, or show what a pattern
matches) and `wt_rm_me` (remove the worktree you are in).

## A five-minute tour

The day goes look, work, tidy.

```sh
wt new fix/login-crash    # branch fix_wt/login-crash, its worktree, provisioned
wt cd login-crash         # go there, in this shell
wt list                   # every worktree, with its pull request and Claude session
wt status                 # each worktree's standing against trunk
wt up                     # rebase onto trunk, if it goes through without you
wt remove login-crash     # done with it: a merged branch goes, an unmerged one stays
wt sweep                  # from the main checkout: clear out everything that landed
```

`wt list` prints three names for each worktree, and any of them works wherever a
command takes `<work>`:

```
   WORK         BRANCH               PR            PATH
   (main)       main                 -             ~/src/myrepo
   login-crash  fix_wt/login-crash   #41 ✓ open    ~/src/myrepo_wt/fix_wt/login-crash
   api-tidy     feat_wt/api-tidy     -             ~/src/myrepo_wt/feat_wt/api-tidy
```

`<work>` matches exactly, inside this repository, and is what every command
that changes or deletes takes; `.` is the worktree you are in and `/` the main
checkout. `<pattern>`, which `cd`, `exec`, `attach` and `find` take, is fuzzy and
searches your other repositories too. A bare name takes the repository's default
type, so `wt new thing` creates `feat_wt/thing`.

`wt remove` and `wt sweep` keep every commit: they never remove a checkout with
uncommitted changes, keep an unmerged branch, and pin a merged one under
`refs/wt-swept/<run>/`, where `wt refs restore` can put it back.
[docs/remove.md](docs/remove.md) has every rule.

## Set up a repository

```sh
cd your-repo
wt init          # writes bin/worktree/worktree.conf
wt doctor        # check it
```

`wt init` asks for the three keys a repository actually varies, offering
detected values as the defaults:

```
trunk (MAIN_BRANCH) [main]:
branch prefix [feat_wt]:
build command (blank for none): make build
```

Every other key is written commented out at its default. `--yes`, or no
terminal to answer from, writes the detected values without asking.

A repository with no configuration works too: every command runs on what
`wt init --yes` would write, detected afresh each run and never written down.

## Configuration in brief

- **The repository's** `bin/worktree/worktree.conf` holds trunk, the branch
  prefix and types, the developer config and build caches to copy into a new
  worktree, the build command and required tools, and whether to register
  worktrees with Superset. An optional `bin/worktree/provision.sh` is the
  repository's own setup step.
- **Yours**, `~/.config/wt/config.toml`, holds which integrations wt uses
  (GitHub, Superset), how your branches are named, when local trunk is
  fast-forwarded, what `wt refs sweep` treats as a backup, and your roots and
  profiles, so commands can run across every repository you have. `wt config
  set` writes it.

Every key, and how the two files decide between them:
[docs/configuration.md](docs/configuration.md).

## Integrations

- **GitHub pull requests**, through the `gh` CLI, on by default. `wt pr list`
  shows the open ones, `wt pr checkout [<number>]` makes a worktree on one, and
  `wt list` shows each worktree's pull request and checks. `wt sweep` knows a
  squash-merged branch has landed. wt only reads GitHub. With no number,
  `wt pr checkout` asks which:

  ```
  Open pull requests (34):

     1  #41  open   alice   fix-login          Login fails on Safari          your review
     2  #38  draft  bob     feat_wt/pr-lookup  Look pull requests up by …     has a worktree
     …
    10  #12  open   carol   residential_fixes  Residents keep their doors

    24 more — `all` shows them, or type part of a title, branch or author to narrow the list.

  Which one? [1-34, #<number>, text to filter; empty to cancel]
  ```

- **Superset**, off until `wt config set superset true`: worktrees wt makes show
  up in the app, and removing one deletes its workspace.
- **Claude Code sessions**: `wt list` shows the session in each worktree,
  `wt attach` opens one, and `wt session` lists, messages and stops them.
  `wt remove` and `wt sweep` leave a worktree a session is working in alone.
- **The Claude Code mod** in `mods/wt` puts the worktree's standing above the
  prompt and `wt up` in a dialog: [mods/wt/README.md](mods/wt/README.md).

How each integration behaves, and what happens when one fails:
[docs/worktree-conventions.md](docs/worktree-conventions.md).

## Where to go next

- `wt --help`, and `wt <command> --help` for any command, with worked examples.
- `wt about`: this build and what changed recently.
- [docs/worktree-conventions.md](docs/worktree-conventions.md): the layout, the
  rules for tools building on worktrees, pull requests, Superset and T3 Code.
- [docs/configuration.md](docs/configuration.md): `worktree.conf`, branch names,
  your own settings, roots and profiles.
- [docs/remove.md](docs/remove.md): what `wt remove` does with a branch, and
  every check before it removes.
- [docs/json.md](docs/json.md): `--json` output and its schemas, for tools that
  drive wt.

## Development

```sh
make build
make check      # lint, go test and bats
```

What to run for which change: [docs/testing.md](docs/testing.md). Cutting a
release: [docs/releasing.md](docs/releasing.md).
