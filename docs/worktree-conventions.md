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
| A type: `feat`, `fix`, `research`, `spike`, … | Folder listings and `git branch` read as a list of work grouped by kind. |
| The `_wt` suffix on the type | Marks a branch as made for a worktree. `wt remove` uses it: a merged branch is deleted, and an unmerged one `wt` made is renamed out of the prefix so its commits survive. |

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
8. **Rebase onto trunk with `wt sync`.** `wt sync` alone shows what would happen and
   changes nothing; `wt sync run <work>` does it, `wt sync --run` does every worktree
   the table calls ready, and `wt sync undo <work>` puts a run back. Each verb is
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
the PATH, a GitHub remote and gh logged in to its host; `wt pr` says which of
those failed and `wt doctor` reports them all. `wt list`'s `PR` column is one
bounded `gh` call for the whole listing and degrades to silence, so a listing
offline is the listing you always had.

## More

- `wt <command> --help`: every command, with worked examples.
- [README](../README.md): install, setting up a repository, the full command list.
