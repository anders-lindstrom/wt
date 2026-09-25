# Worktree conventions

For agents and scripts that do more with worktrees than `wt new` and `wt list`:
moving, removing or scanning them, or building a tool, hook or skill on top of them.
The commands are documented in `wt <command> --help`. This page covers why the layout
is what it is, and the rules that follow from it.

## Why one convention

Many tools on this machine create or read worktrees: `wt`, Claude Code
(`EnterWorktree`, subagent isolation), Codex, Superset, Herdr, IDEs, git GUIs,
terminal tab titles, the Telcred repo-overview dashboard, and agents running shell
commands. Left to their defaults, each puts checkouts somewhere different:
`.claude/worktrees/`, `.worktrees/`, Herdr's and Superset's own directories,
`../<repo>-<work>`. The result is work nobody can find: a branch in progress that no
dashboard shows, a dev server running from a folder nobody remembers, a checkout
deleted with commits that were never merged.

One layout fixes that for both kinds of reader:

- **Tools** know where to look and where to create, so a worktree one tool made is
  found, provisioned, synced and cleaned up by the others.
- **The human** sees every piece of work on a repo in one folder beside it, in any
  tool that shows folders or git state, and can read from a path alone which repo it
  belongs to, what kind of work it is, and which branch it is on.

## The layout

```
<parent>/<repo>/                           the main checkout
<parent>/<repo>_wt/<type>_wt/<work>/       a worktree, on branch <type>_wt/<work>
```

| Choice | Why |
|---|---|
| Beside the repo, not inside it | Build tools, test runners, file watchers, search and IDE indexing all descend into folders inside a repo, and every repo would have to gitignore it. A sibling folder is outside all of that. |
| One `<repo>_wt/` folder per repo | A repo's worktrees sort next to it and stay out of the parent folder, which in `telcred/` already holds dozens of entries. |
| The path below `<repo>_wt/` is the branch name | Nothing to translate: the path gives the branch and the branch gives the path. |
| A type: `feat`, `fix`, `research`, `spike`, … | Folder listings and `git branch` read as a list of work grouped by kind. The type is the identity every tool reasons in; `WORKTREE_TYPE_NAMES` (or a person's `type_names`) only changes the word its branches carry, and the folders keep the type. |
| The `_wt` suffix on the type | Marks a branch as made for a worktree. `wt remove` uses it: a merged branch is deleted, and an unmerged one `wt` made is renamed out of the prefix so its commits survive. |

A repo that wants plain `fix/login-crash` branches sets
`WORKTREE_BRANCH_SUFFIX=""`, and a person who wants it wherever the repo has
not decided sets `branch_suffix` in their own config. That gives up the last
mark: any `<word>/<name>` branch then reads as a worktree branch of type
`<word>`. It renames nothing on disk — the folders above are the layout, and
they keep their suffix — so every rule below holds unchanged, and the path is
the branch wherever the two suffixes agree, which is the default.

## Provisioning belongs to `wt`

A bare checkout is not usable: it has no gitignored developer config, no secrets, no
installed dependencies and no build. Each repo declares what a worktree needs in
`bin/worktree/worktree.conf`, and `wt new`, `wt setup` and `wt adopt` apply it. A
worktree made any other way starts broken, so other tools hand creation to `wt`, or
run `wt adopt` on what they made. The `bin/worktree/*.sh` scripts some repos still
carry are shims that call `wt`.

## Rules for anything that touches worktrees

1. **Find worktrees through git.** Use `git worktree list --porcelain`, `wt list` or
   `wt find`. The layout says where new worktrees go; existing ones can sit elsewhere
   (see the table below), so the shape of a path proves nothing.
2. **Ask `wt` for paths and branches.** From inside the repo, `wt path <work>` and
   `wt branch <work>`; `wt config --shell` for the repo's settings. Both take any
   name `wt list` prints for a worktree that exists — the work name, the branch or
   the path — or `.` for the one you are in, and answer for that worktree,
   whatever its type. `wt path` prints an
   absolute path; only when nothing exists is it where `wt new` would put the work.
   Scripts that rebuild the convention themselves are how tools drift apart.
3. **Create with `wt new`.** Its only stdout is the path:
   `path="$(wt new fix/login-crash)"`. For a checkout another tool made, run
   `wt adopt <path> --relocate`. Claude Code's `WorktreeCreate` and `WorktreeRemove`
   hook events can be routed through `wt hook claude-create` and
   `wt hook claude-remove`.
4. **Remove with `wt remove`.** Deleting the folder or running `git worktree remove`
   leaves the branch behind with nothing deciding its fate. `wt remove` accepts a work
   name, branch, path or `.`; `--yes` skips the question, and without a terminal it
   never asks. Merged branches left without a worktree are for `wt sweep`.
5. **Move with `wt migrate`.** The move is git's own, so uncommitted work goes with
   it, but anything holding the old absolute path does not follow: IDE projects,
   running dev servers, terminal sessions, Herdr and Superset workspaces. Reopen those
   at the new path. Like `wt new`, its only stdout is the final path.
6. **Leave a working session alone.** `wt remove` stops at a git lock whose holder is
   still running, and `wt migrate` stops when `claude agents` reports a session in the
   worktree. `--force` is for when you know that session is finished with it.
7. **Ask before `wt init`.** A repo without `bin/worktree/worktree.conf` is not set up
   for `wt`. `wt init` adds that file to the repo, which is the owner's decision.
8. **`wt sync` and `wt up` are opt-in.** An ordinary rebase onto trunk is plain git
   (`git fetch && git rebase origin/<trunk>`); reach for `wt sync` when asked, or to
   finish a run that stopped. `wt sync` alone shows what would happen and changes
   nothing; `wt sync run <work>` does it, `wt sync --run` does every worktree the table
   calls ready except `recipe?`, and `wt sync undo <work>` puts a run back. Without a
   terminal, a run with nothing named rebases nothing unless `--yes` says so, and
   nothing is pushed without `--push` or `--yes`. Each verb is
   also a flag on the line you just recalled: `wt sync <work> --run`,
   `--resume`, `--undo`. `wt sync keep start` does the ready ones on a timer,
   leaving alone any worktree a session is in, and the table says when it last did.
9. **Clean up merged branches and their worktrees with `wt sweep`.** From the main
   checkout, `wt sweep` fetches origin, lists the local branches trunk already
   contains, and deletes them after one question (`--yes` for scripts; without a
   terminal it changes nothing). A merged branch whose worktree nothing is using —
   nothing uncommitted, no held lock, no agent session in it — goes with its
   worktree, the way `wt remove` takes it; the plan shows every directory that will
   go before asking. Any other worktree is kept, and its row says why: dirty, a
   session in it, a held lock, or a bisect or rebase that holds the branch. It never
   deletes trunk or a long-lived branch. Don't bulk-delete with
   `git branch --merged | xargs git branch -D`: it compares with whatever the
   checkout has and takes long-lived branches with it.
10. **Find repositories with `wt repos`.** Anything that walks every repository
    reads `wt repos --paths` rather than globbing folders: it knows the person's
    roots (the `[roots]` table of their wt config, or `WT_ROOTS`), treats a root
    that is itself a repository as that one repository, skips `<repo>_wt/` folders
    and checkouts wt does not manage, and names each repository once. Commands
    that act across repositories take `--all`, `--roots <name>` or `--profile
    <name>` instead of a loop.

## Layouts `wt list` recognises

| Mark | Layout | What to do |
|---|---|---|
| none | `<repo>_wt/<type>_wt/<work>` | Nothing. |
| `s` | Superset's: `<repo>_wt/<repo>/<type>_wt/<work>` | Leave it where it is. Superset stores the absolute path of every workspace, so a move leaves its workspace pointing at nothing. `wt setup` provisions it in place. |
| `!` | Anything else: a legacy `../<repo>-<work>` checkout, one inside the repo, or plain `git worktree add` somewhere | `wt migrate <work, branch or path>` moves it into place and renames the branch to match. |

A worktree's name comes from its branch where the branch follows the
convention, and from its directory where it does not — which is the case for
every worktree on somebody else's branch, made by `wt checkout <branch>` or by
`wt pr checkout`. Those are at a canonical path and are marked as such; the
directory wt gave them is the name they go by in `wt list`, in completion and
as an argument to every command that takes one.

## Superset registers both ways

Superset's own workspaces reach wt through the project's setup step, which runs
`wt setup --source superset` in the workspace Superset built.

It goes the other way too, once you have opted in with `wt config set superset
true`: `wt new`, `wt checkout` and `wt pr checkout` hand the worktree they just
made to Superset,
so a worktree started from the shell shows up in the app beside the ones
started there. wt runs `superset ws create --local --project <id> --branch
<branch> --skip-branch-prefix`, which makes no checkout of its own — Superset
adopts the one git already has for that branch, at the path wt chose.

It is inactive unless everything is in place: the `superset` CLI on the PATH or
at `~/.superset/bin/superset`, the desktop app's host service running, and this
repository one of Superset's projects. wt never starts the host service and
never creates a project. A machine without Superset, and a repository Superset
does not track, are ordinary states and pass in silence; a Superset that is
there and would not answer is a line. `SUPERSET_REGISTER=on` is a repository
asking for registration, so under it every way the registration can stop is a
line, marked `!`, and `wt doctor` counts it as a problem. `wt doctor` names the
state whatever the mode. Registering the same branch twice is Superset's own
no-op, so nothing duplicates.

**Nothing is deregistered.** `wt remove` and `wt sweep` never call Superset.
`superset ws delete` deletes the checkout off disk along with the row,
uncommitted work included, so wt does not call it. Remove the workspace in
Superset when you want it gone; after `wt remove` it stays behind, pointing at
a directory that is no longer there.

**The user switch is the master, and it is off by default.** `superset` in
`$XDG_CONFIG_HOME/wt/config.toml` (or `~/.config/wt/config.toml`) decides
whether wt runs the Superset CLI at all: with it false wt starts no subprocess
and says nothing, whatever a repository asks for. `worktree.conf` is committed,
and a committed file must not switch on a desktop integration for whoever
clones the repository.

With it on, a repository can still decline with `SUPERSET_REGISTER=off` in
`worktree.conf`, and one run can with `wt new --no-superset`. `wt config` prints
the resolution and which file decided it; `wt doctor` reports which of the three
states the machine is in.

## Pull requests come in through `gh`

`wt pr checkout <n>` makes the worktree and then runs `gh pr checkout` **inside
it**, never in the main checkout. wt creates the worktree detached and with no
files (`git worktree add --detach --no-checkout`), and gh switches it to the
pull request's own head branch, which populates the tree once rather than
twice. Provisioning then runs through the same step as `wt new`.

The branch is gh's to set up: for a pull request from a fork gh points
`branch.<name>.remote` and `.pushRemote` at the fork's URL, so a push from the
worktree updates the pull request. Nothing wt could do with `git worktree add`
alone would get that right. gh also renames a fork branch
that is called after trunk to `<owner>/<branch>`, and wt matches worktrees to
pull requests under both spellings, so a fork's `main` is never mistaken for
your own.

A merged pull request usually has no head branch left, so `gh pr checkout`
cannot find it. wt then fetches `refs/pull/<number>/head` into the branch
itself and says the branch has no upstream. Only for a pull request that is not
open: for an open one, a gh that fails is a failure.

The worktree is named `pr-<number>-<branch>`, under the type the head branch
suggests, unless the head branch already follows this repository's convention
— then it keeps its own type and name, and lands where `wt new` would have put
it. A gh that fails leaves nothing behind: the detached worktree is removed.

wt only ever reads GitHub. Nothing creates, merges, comments on or closes a
pull request, and wt stores no credentials — `gh auth login` is yours to run.
The integration needs `github = true` in your settings (the default), `gh` on
the PATH, a GitHub remote and gh logged in to its host. The first three are
checked before a command does anything; the login is not, because the real call
reports it and a `gh auth status` of its own would cost a process for an answer
wt is about to get anyway. `wt pr` says which of the four failed and
`wt doctor`, whose job is to report the state, checks all four explicitly.
`wt pr open` opens a worktree's pull request through `gh pr view --web`.

The repository asked about is the one **gh** resolves as its base, not always
`origin`: `remote.<name>.gh-resolved` where `gh repo set-default` wrote one, and
otherwise gh's own ordering, `upstream` before `github` before `origin`. wt acts
on gh's answers — it fetches `refs/pull/<n>/head` from this remote and keys the
cache on it — so asking about a different repository than gh would is a bug
waiting to happen on any fork.

### Asking by branch

wt asks GitHub about **the branches its worktrees are on**, by name: one
`gh api graphql` carrying an aliased `pullRequests(headRefName:)` per branch,
all states, five per branch ordered by `UPDATED_AT`, fifty branches to a call.
A listing by recency could not answer this — in a repository with two hundred
open pull requests, a worktree on an older one simply showed nothing. Branch
names travel as GraphQL variables, never spliced into the query text.

Where a branch has more than one pull request, this repository's own beats a
fork's (a bare branch name here is this repository's branch), then an open one
beats a finished one, then the most recently updated.

The answers live in the main checkout's git dir (`wt-pr-cache.json`, beside the
keeper's state), one entry per branch with its own fetched-at, keyed on the
repository gh resolved. `wt list` and `wt status` read them; five minutes is how
long an answer about an open pull request stands. Only the branches the cache
cannot answer are fetched, so a branch made since the last listing does not wait
the five minutes out — and a branch that has no pull request is remembered as
such, or every listing would ask again. A file that is missing, stale, corrupt
or unwritable is simply a miss: `wt list` is never slower or less reliable for
having one, and `wt doctor` is where a cache nothing can write becomes visible.

`wt list --refresh` asks about them all again. The commands that write the file
are the ones that just read GitHub: `wt list` and `wt status` for the branches
they had to fetch, `wt pr open` and `wt sweep` for the branches they touch.
`wt pr list` and the checkout picker ask a different question and leave the file
alone. A `wt list` column served from the cache says how old the oldest answer
in it is, once that is a minute or more.

### What sweep reads it for

A squash or rebase merge rewrites the commits, so git sees the branch as
unmerged for ever. The pull request is what answers that, so sweep reads it:
every row that has one names it, and a worktree whose pull request landed is
swept. Landed is three things, all required — GitHub says **merged**, the pull
request's **base is trunk**, and the local branch is at **exactly** the commit
the pull request carried. A stacked pull request merged into its parent branch
is merged with nothing having reached trunk, and deleting that branch would
throw the work away; a commit made after the merge takes the branch off that
commit and keeps it. An answer with no base recorded is an unanswered question,
not a yes. Sweep's own checks come first, unchanged.

When GitHub rebased the branch before merging, the pull request carried the
rebased commits and the local branch still has the old ones, so the pull request
cannot vouch for it. `git cherry` can: when every commit on the branch is on trunk
as the same change, the branch counts as merged, in `wt remove` and in sweep for a
branch with a worktree or a gone upstream. A merge commit or an empty commit on
the branch keeps it, because git cherry cannot find either.

`wt remove` acts on the same fact, and reads it from the cache file rather than
from GitHub. It runs from git hooks, so it starts no process and makes no
network call — and it does not need to. A merge is permanent and the commit it
carried never moves again, so a recorded merge is not something the five-minute
TTL has any bearing on: that TTL is about how fresh the state of an *open* pull
request is. With nothing recorded for the branch, remove keeps the branch, as
it does without GitHub.

### Not applicable, and broken

Anything that touches an integration follows one rule. When the integration
cannot apply — switched off, the tool absent, this repository not on GitHub, no
login, this repository not a Superset project — wt is **silent** and behaves
exactly as it does without the integration. When the integration is set up and
the thing it was supposed to do failed anyway, wt says so on **stderr**: stdout
and the exit code are untouched.

The two spell that line differently, because they answer different questions.
GitHub is asked in passing by commands whose subject is something else, so its
failure is one `wt: <what is missing>: <gh's line>` per command per integration,
through `Context.Warnf`. Superset is a step `wt new` carries out, so it reports
that step's outcome where it happens, one line per worktree: `-` for a remark,
`!` where `SUPERSET_REGISTER=on` asked for it, `✓` for a workspace that was
made.

wt's own settings fail **closed**. A `~/.config/wt/config.toml` wt cannot parse
turns every integration off for that run and says so in one line: a file the
person wrote and wt cannot read is no licence to fall back to a default that
switches one on. A file that parses with one bad key keeps its good keys and the
line names the part being ignored. Either way the command runs and `wt doctor`
reports it as a problem. `wt config set` refuses to edit a file it cannot parse;
`wt config get` answers with the value wt is acting on and puts the file's
problem on stderr.

## More

- `wt <command> --help`: every command, with worked examples.
- [README](../README.md): install, setting up a repository, the full command list.
