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
trunk (MAIN_BRANCH) [main]:
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

Grouped the way `wt --help` groups them. Every command carries worked
examples: `wt <command> --help`.

**Make a worktree**

| | |
|---|---|
| `wt new <type>/<work>` | create a branch and worktree, then provision it (`--base`, `--no-setup`, `--no-build`, `--no-superset`) |
| `wt checkout <branch> [<work>]` | put a worktree on a branch that already exists; also `wt co` |
| `wt pr checkout [<number>]` | put a worktree on a pull request; with no number, pick one from the open ones, your review queue first |
| `wt pr list` | every open pull request, its state and checks, and the worktree on it |
| `wt pr open [<work>]` | open a worktree's pull request in the browser; with no argument, the one you are in |

**Get to your work**

| | |
|---|---|
| `wt cd [<pattern>]` | cd to a worktree, in this shell; `.` is the one you are in, bare or `/` the main checkout |
| `wt exec <pattern> <cmd>…` | run a command there, in a subshell; your shell stays put |
| `wt list` | every worktree, in any layout; `s` marks Superset's, `!` one nothing owns; a `PR` column when a worktree here has one, asked for by branch and cached for a few minutes (`--no-pr`, `--refresh`; `--all`, `--roots`, `--profile` for many repositories) |
| `wt status [<work>]` | each worktree's branch, whether its checkout is clean, and how far behind and ahead of trunk it is; with a worktree named, that one in full with `wt sync`'s verdict (`--all`, `--roots`, `--profile` for many repositories) |
| `wt find <pattern>` | resolve a worktree by fuzzy name, across repositories (`--candidates`) |
| `wt path <work>` / `wt branch <work>` | where a piece of work lives or would go, and its branch; exact, this repository only, for scripts |
| `wt repos` | every repository wt manages under your roots, with its worktrees and whether wt sync is set up (`--all`, `--roots`, `--profile`, `--paths`) |

**Keep up with trunk**

| | |
|---|---|
| `wt up [<work>]` | bring the worktree you are in onto trunk, only if it goes through without you — conflict-free, or every stop resolved by `.wt-sync.yaml`; otherwise it touches nothing and says why. Also where trunk declares no `.wt-sync.yaml`, conflict-free only. The short form of `wt sync . --run --if-ready` (`--push`, `--no-push`, `--no-fetch`, `--yes`, `--force`/`-f` to go ahead past a Claude session in it) |
| `wt sync` | what rebasing each worktree onto trunk would do, simulated after fetching trunk; changes nothing of yours (`--no-fetch`) |
| `wt sync run [<work>...]` | rebase the named worktrees onto trunk with the declared strategies (`--no-fetch`, `--yes`/`-y`, `--push`, `--no-push`, `--if-ready`); also spelled `wt sync <work>... --run`. With nothing named, every worktree the table calls ready except `recipe?`, asked first — and with no terminal, only with `--yes`. `--if-ready` rebases what is ready and fails if anything was not. The push question defaults to no; `--yes` asks nothing and pushes only with `--push` |
| `wt sync resume <work>` | continue the rebase a run left at a conflict that was yours (`--yes`/`-y`, `--push`, `--no-push`); also spelled `wt sync <work> --resume` |
| `wt sync undo <work>` | put back every ref the last `wt sync run` on this worktree moved, aborting a rebase a run handed over (`--force`/`-f`, `--yes`/`-y`); also spelled `wt sync <work> --undo` |
| `wt sync doctor` | check what a run needs; `--fix` turns on rerere and removes expired locks, `--prune` deletes old safety refs; a `keeper` row says whether one is installed and how its last pass went |
| `wt sync keep once` | one unattended pass: fetch trunk and, when it moved, rebase every ready worktree nobody is in and push what finished (`--no-push`); `keep run` still works; logged to `.git/wt-sync-keep.log`; what the job runs, and what cron runs elsewhere |
| `wt sync keep start` | install a launchd job (macOS) that runs `wt sync keep once` every 30 minutes (`--every`, `--no-push`), pushing the way this shell's git does; `wt sync keep status` for the last pass and the next, `wt sync keep stop` to remove it |

**Put worktrees in their place**

| | |
|---|---|
| `wt migrate <work> [<type>/<name>]` | move a worktree where it belongs, renaming or retyping it on the way (`--dry-run`, `--force`/`-f`); also `wt move` |
| `wt adopt <path>` | provision a worktree another tool created (`--relocate`, `--no-build`) |
| `wt setup [<from-dir>]` | provision the worktree you are in (`--no-build`, `--source` to name what ran it) |
| `wt remove <work>` | remove a worktree; delete its branch when merged — on trunk, or as a pull request the cache says landed — keep it when not (`--yes`, `--dry-run`, `.` for the one you are in, `--force`/`-f` for a locked one) |
| `wt sweep` | delete local branches already merged into trunk — or whose pull request GitHub merged — and remove the worktrees on such branches that nothing is using; from the main checkout only (`--no-fetch`, `--yes`, `--dry-run`), or across repositories with `--all`, `--roots`, `--profile` |

**This repository, and this build**

| | |
|---|---|
| `wt init` | create this repository's `worktree.conf` (`--yes` to skip the prompts, `--force`/`-f` to replace one) |
| `wt config [--shell]` | the resolved configuration, typed or eval-able |
| `wt config get`/`set`/`unset`/`path` | your own settings, per machine, from anywhere |
| `wt doctor` | check config, required tools and worktree health, and your roots and profiles; `--all`, `--roots`, `--profile` check every repository, wt sync doctor included where it is set up |
| `wt about` / `wt version` | which build this is and what changed; the version alone, for scripts |
| `wt completion zsh` | shell completion, including live work names |

A bare `<work>` takes the repository's default type, so `wt new thing` creates
`feat_wt/thing`. `<work>` matches exactly, inside this repository, and is what
every command that changes or deletes takes; `<pattern>` (`cd`, `exec`, `find`)
is fuzzy and searches your other repositories too.

### Many repositories

wt knows where your repositories live: your **roots**, set per machine.

```toml
# ~/.config/wt/config.toml
[roots]
work = "~/src/work"        # a folder: every repository one level down
oss = "~/src/oss"
dotfiles = "~/dotfiles"    # a repository: that one, searched no deeper

[profiles]
api = ["~/src/work/api", "~/src/work/billing"]
```

`wt config set root.work ~/src/work` and `wt config set profile.api
"<dir> <dir>"` write them; `WT_ROOTS` overrides the table. Only
repositories with a `bin/worktree` configuration count.

- `wt repos` lists them, and `wt cd <repo>` goes to one.
- `wt sweep --all` and `wt sync --all --run` work across every one, planned a
  few at a time and asked once; `wt sync --all` is the overview of each.
  `--roots work` or `--profile api` narrows the run.
- `wt list`, `wt status` and `wt doctor` take the same flags.
- `wt doctor` checks that every root is there and every profile entry is still a
  repository wt manages, inside a root.

## Shell functions

A binary cannot change its caller's directory. These do:

| | |
|---|---|
| `wt_cd <pattern>` | the same as `wt cd`, if you prefer the underscore form |
| `wt_exec <pattern> <cmd>…` | run a command there, in a subshell; your shell stays put |
| `wt_dir <pattern>` | print the path (stdout is path-only) |
| `wt_ls [pattern]` | list worktrees, or show what a pattern matches |
| `wt_rm_me` | remove the worktree you are standing in |

`wt cd` and `wt exec` are the same functions under a nicer name: `wt` is itself a
shell function that handles those two and passes everything else to the binary,
because a process cannot change its caller's directory.

They are thin wrappers over `wt find`; the matching itself lives in the binary
where it is tested. A pattern of `.` is the worktree you are standing in, and
`/` the repository's main checkout.

## Layout

```
<parent>/<repo>_wt/<type>_wt/<work>          branch: <type>_wt/<work>

code/
├─ myrepo/                                    ← the repo
└─ myrepo_wt/
   ├─ feat_wt/api-tidy/                       branch: feat_wt/api-tidy
   └─ fix_wt/login-crash/                     branch: fix_wt/login-crash
```

The path tail below `<repo>_wt/` is character-for-character the branch name, so
the two convert with no rules to remember. Why the layout is this shape, and the
rules a tool building on it follows: [docs/worktree-conventions.md](docs/worktree-conventions.md).

### Branches without the `_wt`

The `_wt` on a branch is one setting. A repository whose branches should read
`fix/login-crash` says so in its own file:

```sh
WORKTREE_BRANCH_SUFFIX=""
```

and a person who wants that wherever the repository has not decided says so in
theirs, once, for every repository they work in:

```sh
wt config set branch_suffix ""      # or "-wt", or whatever yours carry
```

**The worktrees do not move for it.** The folders are wt's layout, not a name
anybody types, and they keep the suffix `WORKTREE_TYPE_SUFFIX` gives them:

```
code/
├─ myrepo/                                    ← the repo
└─ myrepo_wt/
   ├─ feat_wt/api-tidy/                       branch: feat/api-tidy
   └─ fix_wt/login-crash/                     branch: fix/login-crash
```

The repository outranks the person, but only where it said something: a
committed file that never mentions the suffix is not a decision, so whoever
clones it may still name their own branches. `wt config` and `wt doctor` both
print which file decided.

What the suffix buys, and what dropping it costs: a suffixed branch says at a
glance that wt made it, and `wt remove` renames an unmerged one out of the
prefix to keep its commits (`fix_wt/login-crash` → `login-crash`). Without one,
any `<word>/<name>` branch — `dependabot/npm-x`, an agent's own `claude/…` —
reads as a worktree branch of type `<word>`. Nothing breaks: every lookup still
goes through `git worktree list`, so only the names change.

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

The type is the identity — a fix is a fix whether its branches say `fix`,
`fix_wt` or `bugfix` — so it is what the folders carry and what every command
reasons in. Only the branch reads the word, which is why renaming one strands
no checkout, and why a branch named before the change still resolves.

Everything takes both spellings: `wt new`, `wt migrate`, `wt path`, `wt branch`,
the type read out of a bare name (`wt new feature_dev-123`), the type read off
somebody else's pull request branch, and the completions, which offer the words
you would type. The same two-file rule as the suffix applies — the repository
where it named its types, you everywhere else — and a pair for a type a
repository does not have is a name for somewhere else, so it quietly does not
apply there.

Two names for one type, or a name that is already another type, would make a
branch ambiguous; the repository's file is told so, and your own pairs are
dropped where they would collide.

The type comes from the spec — `wt new fix/login-crash` — or, for a bare name,
is read out of the name itself: `wt new fix_dev-123` creates `fix_wt/dev-123`.
A name whose first word is not a type is left whole, and an explicit
`<type>/<work>` always wins. This is what lets the type survive a tool that has
nowhere to enter one; Superset mints every branch from a single fixed prefix.

**Worktrees in other layouts keep working.** Every lookup goes through
`git worktree list`, never the shape of a path, so worktrees made by Superset,
by plain `git worktree add`, or before a repo was migrated all resolve.

### Superset's layout

Superset builds `<parent>/<repo>_wt/<repo>/<type>_wt/<work>` — the canonical
path with the repository name repeated, because it joins its per-project
worktree base directory with `<repo>/<branch>`. No setting on either side
removes that segment.

wt treats it as a layout of its own rather than a fault. `wt setup` provisions
a Superset workspace where it stands and never moves it, `wt doctor` passes it,
and `wt list` marks it `s`. Only `!` — a layout nothing owns, such as a
pre-migration `<repo>-<work>` checkout — is a `wt migrate` candidate, because
Superset stores the absolute path of every workspace and a move leaves that
workspace pointing at nothing. `wt migrate` says so before it moves.

### Worktrees wt makes show up in Superset

**Off until you turn it on.** Superset is a per-person choice, so nothing here
happens until:

```
wt config set superset true
```

After that, `wt new`, `wt checkout` and `wt pr checkout` hand the worktree to
Superset once it is
provisioned, so one started from the shell appears in the app beside the ones
started there. Superset adopts the checkout git already has — it creates
nothing and moves nothing, and the workspace points at wt's canonical path.

This needs all three of: the `superset` CLI, on the PATH or at
`~/.superset/bin/superset` where the desktop app installs it; the host service
running, which means the app is open; and this repository already one of
Superset's projects. A machine with no Superset, and a repository Superset does
not track, are ordinary states and pass in silence. A Superset that is there and
would not answer is one line on stderr and nothing else — `wt new` still prints
only the path on stdout and still exits the same way. wt never starts the app
and never creates a project. `wt doctor` reports which of the three it found,
whatever the mode.

Registering a branch that already has a workspace is Superset's own no-op, so
running it twice changes nothing. A *new* workspace makes Superset run the
project's setup step if it has one, which for these repos is
`wt setup --source superset`: a second, idempotent provisioning pass on top of
the one `wt new` just did. `--no-setup` therefore skips registration too.

**Removal is one-way.** wt never deregisters. `superset ws delete` deletes the
checkout off disk, uncommitted work included, so `wt remove` does not call it —
delete the workspace in Superset when you want it gone.

Off again with `wt config set superset false`, which stops wt running Superset
at all. With it on, a repository can still decline with `SUPERSET_REGISTER=off`
in its `worktree.conf`, and one run can with `wt new --no-superset`. A
repository that asks for it with `SUPERSET_REGISTER=on` hears about every way
the registration can stop, this one included, and `wt doctor` counts those as
problems.

## Pull requests

**On by default**, and inert where it cannot apply. wt reads pull requests
through the `gh` CLI and keeps no credentials of its own: whatever gh is logged
in as is what wt sees. Nothing wt does writes to GitHub — the only gh command
it runs that changes anything is `gh pr checkout`, and what that changes is
your own checkout.

```
wt pr list          # every open pull request, and the worktree on it
wt pr checkout 12   # a worktree for that one, provisioned like any other
wt pr checkout      # pick from the open ones
wt pr open          # this worktree's pull request, in the browser
```

`wt pr checkout` makes the worktree at the canonical path and provisions it
through the same step as `wt new`, so everything else — `provision.sh`, the
build, the Superset registration if you have opted into it — happens by its own
rules. The worktree goes on the pull request's **own head branch**, put there
by `gh pr checkout` running inside the new worktree, so a push from it updates
the pull request. That is what makes a pull request from a fork work: gh points
the branch's remote at the fork, which nothing else would get right. Your main
checkout is never switched.

The worktree is named `pr-<number>-<branch>`, under the type its branch
suggests — `feat_wt/pr-12-residential_fixes` — so `wt cd pr-12` finds it. A head
branch that already follows this repository's convention keeps its own name
instead, so a branch wt made lands exactly where `wt new` would have put it. A
pull request whose branch is already in a worktree prints that path and makes
nothing.

Given a number, a merged or closed pull request is checked out too: finished
work is worth re-reading. GitHub deletes the head branch when a pull request
merges, so wt falls back to `refs/pull/<number>/head` — the same commit, on a
branch with no upstream, and it says so.

The picker only offers the open ones, and puts the ones **waiting on your
review** first:

```
Open pull requests (34):

   1  #41  open   alice   fix-login          Login fails on Safari          your review
   2  #38  draft  anders  feat_wt/pr-lookup  Look pull requests up by …     has a worktree
   …
  10  #12  open   someone residential_fixes  Residents keep their doors

  24 more — `all` shows them, or type part of a title, branch or author to narrow the list.

Which one? [1-34, #<number>, text to filter; empty to cancel]
```

Answer with a row number, `#<number>`, or any text: the list narrows and asks
again, and text that leaves one pull request picks it. Without a terminal to pick
in, the whole list goes to stderr and wt asks for a number rather than
choosing — so a script or an agent never lands in a worktree it did not name.
stdout stays the path alone, so `cd "$(wt pr checkout 12)"` works.

`wt list` shows a `PR` column when any worktree here is on a pull request:

```
   WORK                       BRANCH             PR          PATH
   (main)                     master             -           ~/src/fd
   pr-2137-argument-sanitize  argument-sanitize  #2137 open  ~/src/fd_wt/feat_wt/pr-2137-…
```

wt asks GitHub **about those branches by name** — one `gh api graphql` under a
two-second deadline, with a query per branch — so a pull request is found
however old it is and however many have been opened since. Each branch's answer
is then kept in this repository's `.git/` for five minutes, so the next
`wt list` pays nothing at all. Measured on a repository with three worktrees:

| | |
|---|---|
| first listing, GitHub asked | **0.6s** |
| listing from the cache | **0.02s** |
| `wt list --no-pr` | **0.02s** |

Only the branches the cache cannot answer are asked about, so a worktree made
since the last listing shows its pull request at once rather than waiting the
five minutes out. `wt list --refresh` asks about them all again, and `--no-pr`
skips the whole thing. `wt status <work>` reads the same answers and prints the
pull request as one of its facts; `wt pr open` and `wt sweep` refresh what they
touch. A cache file that is corrupt, missing or unwritable is a cache miss and
nothing more: `wt list` never fails or waits over it.

A column served from the cache says how old it is, under the table beside the
layout legend:

```
   pull requests as of 3m ago — `wt list --refresh` asks GitHub again
```

Under a minute there is no line: the listing asked GitHub itself. A cache wt
cannot write means every listing pays the call again, silently — `wt doctor`
is where that shows up.

A pull request merged into something other than trunk says where —
`#31 merged into feat_wt/its-parent` — because a stacked pull request merged
into its parent has landed nothing on trunk.

The column is not printed at all when no worktree here has a pull request, and
when GitHub is out of play `wt list` is byte for byte the listing without it —
see **When something does not work** below.

`wt pr open` opens the pull request whose head branch a worktree is on, through
`gh pr view --web`. With no argument it is the worktree you are standing in.
A worktree with no pull request is one line and a non-zero exit.

**Four things have to be true**, and `wt pr` says which one was not:
`github = true` in your settings; `gh` on the PATH; a GitHub remote on this
repository; and gh logged in to that remote's host. The first three are checked
before anything runs; the login is whatever the real call reports, so no
command pays for a `gh auth status` of its own. `wt doctor` has a `GitHub:`
section reporting all four, and never counts any of it as a problem — a machine
without gh is an ordinary machine.

The repository asked about is the one **gh** resolves as its base: whatever
`gh repo set-default` recorded, and otherwise `upstream` before `github` before
`origin`. On a fork checked out with `origin` pointing at your copy, that is the
parent — which is where the pull requests are.

Off with `wt config set github false`, which stops wt running gh at all.

Not here: creating, merging or commenting on pull requests. wt reads.

### `wt sweep` knows what merged

`wt sweep` deletes what trunk already contains. That question is git's, and git
gets it wrong in one common case: a **squash or rebase merge** rewrites the
commits, so the branch stays unmerged for ever however long ago it landed. The
pull request is the fact that answers it, and sweep reads it. When GitHub
rebased the branch first, so the pull request carried other commits than yours,
`git cherry` finds each of your commits on trunk instead.

```
Will be removed with its branch, 1 worktree:
  login-crash  fix_wt/login-crash  ~/src/repo_wt/fix_wt/login-crash  #34 merged on GitHub (squashed or rebased, so git cannot see it)

Merged, but in use in a worktree, so kept:
  fix_wt/api-tidy  dirty; commit or discard the changes, then sweep again · #35 open
```

Every row that has a pull request names it, including the ones sweep keeps and
the ones whose upstream is gone — usually the whole explanation of why a branch
is sitting there.

Acting on one is narrower than printing it. A worktree is swept on the strength
of its pull request only when three things hold: GitHub says **merged**, the
pull request's base is **trunk**, and the local branch is at **exactly** the
commit the pull request carried. A stacked pull request merged into its parent
branch is merged without anything having reached trunk, so it never makes a
branch deletable. One commit made locally after the merge is work that went
nowhere and takes the branch off that commit, so sweep leaves it alone. Every
check sweep already made applies first: uncommitted changes, a held lock, an
agent session in the directory. Sweep fetches its pull requests fresh, and with
GitHub off or unreachable it does exactly what it did before.

`wt remove <work>` acts on the same fact, from the file `wt list` and `wt sweep`
leave behind. It starts no `gh` and makes no network call — it runs from git
hooks — but it does not have to: a merge is permanent and the commit it carried
never moves, so a recorded merge cannot go stale, whatever the five-minute TTL
says about open ones. With nothing recorded for the branch, remove keeps the
branch, as it does without GitHub.

### When something does not work

wt distinguishes **not applicable** from **broken**, and only says something
about the second.

Not applicable is silence: `github = false`, no `gh`, a repository with no
GitHub remote, a gh that was never logged in. Nothing was set up, there is
nothing to report, and `wt list` prints what it always printed.

Broken is one line on **stderr**: gh is there, the repository is on GitHub, and
the call failed or ran past its deadline anyway.

```
$ wt list
wt: no pull requests shown: gh api graphql did not answer within 2s
   WORK         BRANCH                PATH
   login-crash  fix_wt/login-crash    ~/src/repo_wt/fix_wt/login-crash
```

stdout is unchanged, the exit code is unchanged, and each integration says it
at most once per command. Superset follows the same rule, in its own shape: a
registration reports its outcome as it happens, one `-`, `!` or `✓` line on
stderr per worktree, and says nothing where the integration does not apply.

Your own `~/.config/wt/config.toml` splits the same way, and it fails **closed**.
A file wt cannot parse at all turns every integration off for that run — a file
you wrote and wt cannot read is no licence to fall back to a default that
switches one on:

```
$ wt list
wt: every integration is off for this run: /Users/you/.config/wt/config.toml: toml: line 2 (last key "superset"): expected value but found '\n' instead
```

A file that parses with one bad key keeps its good keys, and the line names the
part being ignored:

```
$ wt list
wt: ignoring unknown key "githb" in /Users/you/.config/wt/config.toml (wt's settings are: superset github)
```

Either way the command runs, and `wt doctor` counts it as a problem — that is
where you go to read it in full. `wt config get` still answers, with the file's
problem on stderr, because a script asking what a setting is should get the
value wt is itself acting on. `wt config set` refuses to edit a file it cannot
parse, and both still refuse an unknown key outright: that is input validation,
not a file wt could not read.

## Moving a worktree

`wt migrate` takes a worktree the way `wt list` prints it — the work name, the
branch or the path — and puts it at the path this layout gives it. A second
argument changes the type, the name, or both, and the branch is renamed to
match, because below `<repo>_wt/` the path *is* the branch:

```
wt migrate login-crash                             # just fit it to the layout
wt migrate ../myrepo-api-tidy                      # by path
wt migrate fix/flaky-test fix/slow-test            # rename as it moves
wt migrate stats chore/stats                       # keep the name, change the type
```

With no second argument the branch decides: one already in the convention
keeps its name, `fix/flaky-test` is missing only the type suffix, and a bare
`api-tidy` is a name under the repository's default type. A branch that says
neither is asked about rather than guessed at.

It prints the plan first — where it goes, what the branch becomes, whether the
checkout is dirty — and `--dry-run` stops there. Uncommitted work is no reason
to refuse: the move is git's own, so it travels. A branch that is already
checked out somewhere, a worktree already at the destination or an agent
session living in the directory are reasons, and it says which (`--force`
moves past the session).

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
| `WORKTREE_BRANCH_SUFFIX` | string | the type suffix |
| `WORKTREE_TYPE_NAMES` | list | empty — each type is called by its own name |
| `WORKTREE_DEFAULT_TYPE` | string | derived from the prefix |
| `WORKTREE_TYPES` | list | Conventional Commits + `research` `spike` |
| `DEVELOPER_CONFIG_DIRS` | list | `.cursor .claude .run .vscode .idea` |
| `DEVELOPER_CONFIG_FILES` | list | empty |
| `BUILD_INIT_ENABLED` | bool | true when a command is set |
| `BUILD_INIT_COMMAND` | string | required when build init is enabled |
| `REQUIRED_BINS` | list | empty |
| `TEST_COMMAND` | string | required when tests-before-remove is on |
| `RUN_TESTS_BEFORE_REMOVE` | bool | `false` |
| `SUPERSET_REGISTER` | `auto` `on` `off` | `auto` |

`WORKTREE_TYPE_SUFFIX` marks a type in a worktree path and in the branch that
goes with it; `WORKTREE_BRANCH_SUFFIX` overrides it for branches alone, and is
the one string key where an empty value is a value rather than an absent key:
`WORKTREE_BRANCH_SUFFIX=""` gives branches with no suffix at all, leaving every
path where it was — see [branches without the `_wt`](#branches-without-the-_wt).
Leave it out and the choice falls to whoever clones the repository.
`WORKTREE_BRANCH_PREFIX` follows the type suffix when it is not set itself, so
the two cannot silently disagree, and it may be written the way the branches
read (`feature_wt`) in a repository that renames its types.

`WORKTREE_TYPE_NAMES` is `<type>=<name>` pairs — `(feat=feature docs=doc)` —
saying what the branches call a type. It is validated against
`WORKTREE_TYPES`, and against itself: two types that read alike would make a
branch name ambiguous. See [what each type is
called](#what-each-type-is-called).

`SUPERSET_REGISTER` decides whether a new worktree is also registered as a
Superset workspace: `auto` when Superset can take it, `on` to additionally have
`wt doctor` count an unusable Superset as a problem, `off` to leave it alone.
Registration never fails a command under any of them. It only applies once
[your own settings](#what-you-keep) say `superset = true`; a committed file
cannot switch a desktop integration on for whoever clones the repository.

Retired: `REPO_NAME` (derived), `WORKTREE_LAYOUT` (the tool owns the shape),
`AWS_SETUP_ENABLED` (became `provision.sh`).

### `provision.sh`

An optional executable run by `wt setup` after config copying and before build
init, with the new worktree as its working directory. This is where a repo puts
its own step — decrypting secrets, checking a cloud identity — instead of the
tool carrying a flag for it.

## What you keep

Which integrations wt uses is per person and per machine, so it lives in a
file of your own rather than in any repository:

```
$XDG_CONFIG_HOME/wt/config.toml     # or ~/.config/wt/config.toml
```

**It does not have to exist.** No file means the built-in defaults, and
`wt config set` is the only thing that ever creates it.

| key | default | what it decides |
|---|---|---|
| `superset` | `false` | whether wt registers worktrees it creates as Superset workspaces |
| `github` | `true` | whether wt reads pull requests through the GitHub CLI |
| `branch_suffix` | `_wt` | what your branches carry, where a repository does not say |
| `type_names` | empty | what your branches call each type, as `feat=feature` pairs |

```
wt config                      # everything, with where each value came from
wt config get superset         # one value alone, for a script
wt config set superset true    # written to your file; validated first
wt config unset superset       # back to the built-in default
wt config path                 # where that file is, whether or not it exists
```

`get`, `set`, `unset` and `path` work from anywhere — these are not repository
settings, so they do not need one. An unknown key is an error naming the key,
the way `worktree.conf` treats one; an integration takes `true` or `false` and
nothing else, `branch_suffix` takes what a branch can carry before its slash
(`""` included), and either is refused before anything is written. `set` edits
the file in place, so comments and ordering you put there survive.

`branch_suffix` and `type_names` are the settings a repository can overrule,
and only by naming its own `WORKTREE_BRANCH_SUFFIX` or `WORKTREE_TYPE_NAMES`:
see [branches without the `_wt`](#branches-without-the-_wt) and [what each type
is called](#what-each-type-is-called). A file wt cannot read costs you the
integrations, never these two — a branch has to be called something, so the
repository answers instead.

**Your setting is the master switch.** With `superset = false` wt never runs
the Superset CLI, whatever a repository's `SUPERSET_REGISTER` says; with it
true, that key still gets to decline. `wt config` shows the resolution:

```
user config:   /Users/you/.config/wt/config.toml (no file yet)
  superset:      false (default)
  github:        true (default)
  branch_suffix: "_wt" (default)
  type_names:    [] (default)
superset mode: off (user config)
```

`wt config --shell` is unchanged: it stays the repository's legacy
assignments, which the Herdr skills eval.

## Removing a worktree is careful

**Anything `wt list` prints is a valid argument** — the work name, the branch, or
the path — as is `<type>/<work>`, and `.` for the worktree you are standing in,
as in every command; `/` names the main checkout, which cannot be removed. On a
narrow terminal `wt list` shortens paths with `…` to fit; `wt list | cat` prints
them whole:

```sh
wt remove wt-migration                       # WORK column
wt remove chore_wt/wt-migration              # BRANCH column
wt remove ~/src/repo_wt/chore_wt/wt-migration  # PATH column
wt remove chore/wt-migration                 # <type>/<work>
wt remove .                                  # the one you are in
```

Matching is exact and stays inside this repository. `wt cd` may guess at a name;
a wrong guess there costs a directory change, and here it costs a checkout — so
a work name used under two types must be disambiguated by its type, and there is
no fallback to the repository's default type. (`wt new` still has one: it names
a worktree that does not exist yet.)

**What the removal will do is printed before it does it:**

```
  path    /Users/you/src/repo_wt/chore_wt/wt-migration
  branch  chore_wt/wt-migration — not merged: 2 commits ahead of main
  state   clean

  the checkout will be deleted
  the branch will be kept as "wt-migration" (2 commits ahead of main)

Remove it? [y/N]
```

In a terminal you are asked to confirm; `--yes` skips the question, and a script,
hook or agent with no terminal is never asked. Git refuses to remove a checkout
with uncommitted changes, which is why the plan reports `state` before you
answer rather than after.

**A locked worktree is read, not repeated back at you.** An agent session takes
a git worktree lock on the directory it works in, naming itself and its pid in
the reason. wt reads that reason: if the process has exited the lock is litter,
so the plan says `stale`, releases it and carries on. If it is still running,
removal stops before the question is asked — and `--force` (`-f`) is how you
say you mean it anyway. A lock with no pid in it falls back to the sessions wt
can see. git's own advice, `remove -f -f`, never reaches you: it is not a
command that exists here.

`wt remove` reads the branch **from the worktree**, never rebuilding it from the
name: once the type can vary, a reconstructed name may belong to an unrelated
branch. The plan states where that branch stands — merged, or how many commits
ahead of `origin/<trunk>` it is — for every branch, whoever created it, because
that is the fact the whole decision turns on. (`state` is a separate question:
it is about uncommitted changes in the checkout.)

**A branch merged into trunk is deleted**, whether or not wt created it: merged
means nothing is lost. Merged means reachable from `origin/<trunk>` as last
fetched, or from the local trunk, so a pull request merged on GitHub counts even
while the main checkout's trunk is behind; `wt remove` never fetches, and deletes
the branch only at the commit the plan showed. **An unmerged branch is never deleted** — one
wt made is renamed out of the `<type>_wt/` prefix so the work survives its
worktree, and one wt did not make is left exactly as it is. A detached HEAD has
no branch to touch.

## Development

```sh
make check      # lint + go test + bats
make build
```

## Documentation

- [Design spec](docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md)
- [Implementation plans](docs/superpowers/plans/)
