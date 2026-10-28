# wt in Claude Code

A Claude Code mod: the worktree the session stands in, as `wt status --json`
reports it.

- One line above the prompt that says the worktree in git's marks:
  `/wt · login ↓4↑2 main · ↑3 origin *`. `↓` is what the other side has that
  the branch lacks, `↑` what the branch has on top, and each count is followed
  by what it is against: trunk, then the branch's own remote. `✓` is nothing
  to fetch in, `*` uncommitted changes, `✗ origin` a remote branch that is
  gone, `↑ origin (rebased)` a rebase not pushed yet, and `REBASE STOPPED` a
  conflict waiting for you. Each part is plain while it is as it should be, in
  the accent colour when `wt up` has something to do about it, and in the
  warning colour when it is wrong (a branch that diverged from its own remote
  or whose remote branch is gone, a rebase stopped, a run that failed). It
  takes no keys, so nothing you type is taken from the prompt, and nothing on
  it moves a branch: it names the command that opens the dialog, and a click on
  `/wt` opens it, and closes it again. The main checkout has no line.
- Nothing under the prompt: Claude Code draws a mod's notice there as a warning
  whatever it says, so the line above is the one place it all shows.
- A small dialog, from `/wt`, that holds the keyboard while it is open: `u`
  runs `wt up`, as `wt up --yes --no-push --json --expect <token>`, so it goes
  ahead only over the plan it showed. After it, `p` closes the dialog and puts
  the lease-protected push wt printed in your prompt for you to run, `z` undoes
  it (`wt sync undo`) and `d` dismisses the result. `c` resumes a rebase that
  stopped at a conflict you have resolved, and `v` runs `wt up` over a branch
  that diverged from its own remote. `f` fetches trunk and `r` reads the
  worktree again. Esc, `q`, `/wt close` and `/wt` again close it. Nothing moves
  a branch while a turn runs, a prompt sent during a move waits for it, and
  nothing else fetches during one.
- The result of a run stays on the line until it has nothing left to say: one
  that left something to push goes once the branch is pushed, any other that
  succeeded goes when you next type a prompt, and one that failed stays until
  `d` in the dialog or `/wt dismiss`.
- `/wt [up|undo|push|resume|fetch|refresh|dismiss|close]` does the same from
  the prompt.
- It stays current while you are in another terminal: every ten seconds it
  reads the branch, trunk and remote refs and runs `wt status` again when one
  has moved, and every three minutes it refreshes in full to catch file edits
  and session changes.
- It never fetches by itself. A count against trunk is as of the last fetch
  anyone made in the repository, and once that is over an hour old it says so:
  `fetched 3h ago`. `wt up` fetches before it rebases.

## Install

From a checkout of the wt repository, which Claude Code then reads in place
(`/reload-plugins` after a pull):

```sh
claude plugin marketplace add ~/path/to/wt
claude plugin install wt@wt
```

or from GitHub: `/plugin install wt --marketplace anders-lindstrom/wt`.

## Options

All off until set: with `/plugin configure`, with
`claude plugin install wt@wt --config gittree=true --config reviewStatus=true`,
or under `pluginConfigs` in a settings file.

| Option | What it adds |
|---|---|
| `gittree` | **gittree ↗** at the right of the line above the prompt, to click, which stays when there is nothing else to show; `g` in the dialog, and `/wt gittree`: it opens the worktree in gittree |
| `reviewStatus` | keeps when this conversation last ran a review skill, how many of your inputs ago, and how your last input began. The line above the prompt says them once they are old: `last input 42 min ago: “now fix the two find…” · reviewed 3h ago, 5 inputs since`, or `not reviewed yet, 9 inputs`. The dialog always has both |
| `inputAfterMinutes` | how old your last input is before the line says it: 5 unless set |
| `reviewAfterMinutes` | how old the last review is, or the conversation with none, before the line says it: 30 unless set |
| `reviewSkills` | which skills count as a review: a regular expression on the skill's name. The default takes every skill with `review` in its name except the one about receiving a review |

## Check it

```sh
claude plugin validate mods/wt
claude plugin test mods/wt
```
