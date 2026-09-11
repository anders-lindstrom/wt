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
2. **Ask `wt` for paths and branches.** From inside the repo, `wt path <type>/<work>`
   and `wt branch <type>/<work>`; `wt config --shell` for the repo's settings.
   `wt path` prints an absolute path, and when that work already has a worktree it
   prints where that worktree is. Scripts that rebuild the convention themselves are
   how tools drift apart.
3. **Create with `wt new`.** Its only stdout is the path:
   `path="$(wt new fix/login-crash)"`. For a checkout another tool made, run
   `wt adopt <path> --relocate`. Claude Code's `WorktreeCreate` and `WorktreeRemove`
   hook events can be routed through `wt hook claude-create` and
   `wt hook claude-remove`.
4. **Remove with `wt remove`.** Deleting the folder or running `git worktree remove`
   leaves the branch behind with nothing deciding its fate. `wt remove` accepts a work
   name, branch or path; `--yes` skips the question, and without a terminal it never
   asks.
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
   changes nothing; `wt sync run <work>` does it, and `wt sync undo <work>` puts it
   back.

## Layouts `wt list` recognises

| Mark | Layout | What to do |
|---|---|---|
| none | `<repo>_wt/<type>_wt/<work>` | Nothing. |
| `s` | Superset's: `<repo>_wt/<repo>/<type>_wt/<work>` | Leave it where it is. Superset stores the absolute path of every workspace, so a move leaves its workspace pointing at nothing. `wt setup` provisions it in place. |
| `!` | Anything else: a legacy `../<repo>-<work>` checkout, one inside the repo, or plain `git worktree add` somewhere | `wt migrate <work, branch or path>` moves it into place and renames the branch to match. |

## More

- `wt <command> --help`: every command, with worked examples.
- [README](../README.md): install, setting up a repository, the full command list.
