# What's new

Newest first, each heading stamped with the local date and time of the change
(several land per day). `wt about` prints the newest 5 entries, or every entry from
the last 3 days if that is more, so keep each entry to a few lines a human would
want read out to them; the full story is in git history and in
`docs/superpowers/plans/`.

## 2026-09-13 16:25 — wt sync says what puts a worktree back, and names a rebase finished by hand

- A handed-over row keeps its stop, subject and file: `at 7/23 "…"  x.ts✗  waits
  on you: wt sync resume, or wt sync undo`; `wt sync <work>` names the plan file.
- The needs-you line says resolve, `git add`, then resume, or undo. The plan file
  says `<<<<<<< HEAD` is trunk, lists each never-hand-merge path once, and counts
  `24 merges landed (145 commits)`.
- A handover you finished with `git rebase --continue` is called that by the
  table, `run`, `doctor` and `undo`; `resume` runs what is left.

## 2026-09-13 15:48 — wt sync undo no longer throws away work that is not the run's

- A rebase you started yourself after aborting the run's is refused, not
  aborted, with or without `--force`; finish or abort it yourself first.
- A commit you made inside the handed-over rebase is named and refused, not
  aborted away. `resume` keeps it; `undo --force` keeps it under a safety ref.
- A forced undo that stops partway no longer leaves a safety ref for a branch
  it never moved, which made every later undo say "already at".
## 2026-09-13 08:28 — worktree.toml rejects a misspelled key, as worktree.conf does

- A key wt does not read in `worktree.toml` used to pass in silence, so a
  typo like `developer_config_file` quietly did nothing. It is now the same
  error `worktree.conf` gives: `unknown key "developer_config_file"`. A
  retired key gets the same sentence saying what replaced it. The repository's
  own `worktree.toml` and every real `worktree.conf` still load clean.

## 2026-09-13 08:27 — wt about names the commit on a make build too

- A binary from `make build` used to say `wt dev` and nothing about when it
  was built; only `install.sh` stamped the commit and dates. Both now build
  with the same `scripts/ldflags.sh`, so `wt about` reports the commit, the
  build time and the commit time whichever way the binary was made. `dev` is
  still what a build without git says.

## 2026-09-12 03:05 — wt sync sees a dependency-graph overlap it used to miss

- A changed line beginning `-- ` or `++ ` is a change like any other. `wt sync`
  had been skipping those as though they were a diff's `---`/`+++` file
  headers, so an edit to a dependency-graph file could pass as "only the
  version line moved" and the overlap with trunk went unsaid. It now reads the
  hunks properly. Expect a worktree or two to start reporting that both sides
  changed the graph — it was always true, and it is worth your eye.

## 2026-09-12 02:18 — wt sync run still reports what did not finish when a push goes wrong

- A run that pushed a branch and then could not read its new tip stopped
  there, and the report it had been building — everything refused, restored,
  failed or left needing you — was never printed. That push is now named as
  one more thing that did not come off, and the run finishes its report.

## 2026-09-11 22:56 — wt sync: every git call has a deadline, and interrupted handovers say how to get back

- `git merge-file`, the `git archive` and `tar` behind a conflict script, and
  `wt sync doctor`'s trunk lookups get the same deadline as every other git in
  `wt sync`. One that runs out says it timed out, not that a script is missing
  or trunk does not resolve.
- Ctrl-C while `wt sync run` or `resume` writes the plan names the worktree and
  what puts it back, instead of only `interrupted`. A plan `resume` cannot write
  says the worktree is left mid-rebase and `git rebase --abort` puts it back.

## 2026-09-11 22:34 — wt sweep fits the terminal

- On a terminal, `wt sweep` cuts long commit subjects so each row of its plan
  fits the width, as `wt list` and `wt status` do. Piped, they stay whole.

## 2026-09-11 22:29 — wt sweep says what holds a merged branch

- A merged branch held by a bisect or rebase is listed as `held by the rebase
  in <path>; finish or abort it there`, not sent to `wt remove`, which would
  find nothing to remove. A plain checkout's line says `wt remove` deletes it.
- A branch kept at the last moment says why: it moved, was checked out, or
  trunk no longer contains it.

## 2026-09-11 22:23 — wt remove deletes a branch merged on origin

- Behaviour change: `wt remove` now deletes a branch that `origin/main`
  contains, as last fetched, instead of renaming it and leaving it behind for
  `wt sweep`. That is what a pull request merged on GitHub looks like while the
  main checkout's `main` is behind.
- It still never fetches, and deletes the branch only at the commit the plan
  showed: a branch that moves in the meantime is kept.

## 2026-09-11 07:40 — wt sync fetches trunk before it looks

- `wt sync` and `wt sync <work>` fetch trunk first, so the header names the
  current `origin/main` and its SHA instead of warning `(not fetched)`. The
  fetch moves only that remote-tracking ref.
- `--no-fetch` skips it and says `as last fetched 3h ago`; a fetch that fails
  says why and shows the overview the same way. `wt sync run --no-fetch` uses
  the same words.

## 2026-09-11 07:21 — wt about shows the last few days

- `wt about` lists the newest 5 what's-new entries, or every entry from the
  last 3 days if that is more, instead of only the newest one.

## 2026-09-10 21:59 — an idle session no longer blocks wt sync

- `wt sync run`, `resume` and `undo` go ahead under an idle Claude session:
  shown as `name (idle)`, asked first (`--yes` skips), and told what moved with
  a `wt: …` line after. A busy one is still refused; several show `name +1`.
- The session running the command does not count against its own worktree.
- `claude agents --json` gets 10 s to answer; a timeout is a refusal.

## 2026-09-10 20:48 — wt sweep deletes merged branches

- `wt sweep`, from the main checkout, deletes the local branches origin's
  trunk or the local trunk already contains, after one question (`--yes`
  skips it; without a terminal nothing is deleted).
- Merged branches a worktree still holds are pointed at `wt remove`; branches
  whose upstream is gone before trunk got their commits are listed and kept.

## 2026-09-10 20:39 — wt sync groups worktrees by what to do

- `wt sync` files each worktree under ready, needs you or skipped, one short
  row each, with the stop, collisions and dependency overlap on lines beneath
  and every long list cut to a count.
- `wt sync <work>` prints one worktree in full: every stop and its files,
  every colliding key, every dependency file, one per line under a heading.

## 2026-09-10 19:03 — sync run reads at a glance, and pushes when done

- `wt sync run` and `wt sync resume` mark each line ✓ ✗ ⏭ ⚠, print the undo
  command instead of the safety ref, and end a run of several worktrees with
  a count and a line for each one that did not finish.
- A worktree that finished with nothing owed is pushed at the end with
  `--force-with-lease --force-if-includes`: asked once on a terminal, and
  Enter pushes. `--push` skips the question; `--no-push` prints the command.

## 2026-09-10 15:57 — an empty worktree is no stack parent

- `wt sync run` no longer takes a worktree with no commits of its own, left on
  an older trunk commit, for the parent of every branch cut after it. It pulled
  unrelated worktrees into one stack, and an agent in it refused them all.

## 2026-09-10 12:44 — setup takes --source

- `wt setup --source <name>` says what ran setup, such as `superset`. Setup
  prints it and does nothing else with it yet, so a workspace's setup step can
  pass it now and later behaviour can key on it without a config change.

## 2026-09-10 10:48 — a contested stop is handed to you, and resume finishes it

- `wt sync run` no longer aborts at a conflict that is yours: it leaves the
  rebase in place, what the strategies resolved staged, and a plan file.
- `wt sync resume <work>` checks your work and finishes the rebase. Anything
  unmerged, or a strategy's file hand-merged, is a refusal that changes nothing.
- `wt sync undo <work>` aborts a handed-over rebase and puts the branch back;
  `wt sync` and `wt sync doctor` both name a worktree waiting on you.

## 2026-09-09 23:12 — recipe means every stop resolves

- `wt sync` now replays the whole rebase in the object store, applying the
  declared strategies at each stop. `recipe` means every stop resolves;
  `contested` names the first stop a person owns, wherever it is.
- The first real run had shown the gap: a branch read recipe on its first stop
  and stopped unclaimed at its seventh.

## 2026-09-09 23:01 — migrate takes any worktree, and can rename it

- `wt migrate <worktree> [<type>/<name>]` takes a work name, a branch or a path
  and puts that worktree where the layout says it belongs. Also `wt move`.
- A destination changes the type, the name or both, renaming the branch to
  match; with none, the branch decides.
- It prints the plan first and says what is in the way when it refuses. A dirty
  checkout is not in the way: git's own move carries the work.

## 2026-09-09 21:51 — Superset's layout, and types read from a name

- Superset's `<repo>_wt/<repo>/<type>_wt/<work>` is now a layout wt endorses:
  `doctor` passes it, `list` marks it `s`, and `setup` provisions it where it
  stands. Only `!` — a layout nothing owns — is still a migrate candidate.
- `wt migrate` warns that Superset's workspace holds the old path before it
  moves, not after.
- A bare work name can carry its type: `wt new fix_dev-123` creates
  `fix_wt/dev-123`. An explicit `<type>/<work>` still wins.

## 2026-09-09 20:57 — wt sync run, undo and doctor

- `wt sync run <work>...` rebases worktrees onto trunk with the strategies the
  repository declares, behind a lock and a safety ref under refs/wt-sync.
- Restores are verified, and a step that cannot finish now is deferred rather
  than half-applied; a stack rebases onto its own rebased parent.
- `wt sync undo <work>` puts the refs back, and refuses to discard later work.
- `wt sync doctor` checks what a run needs. Ctrl-C releases the locks and kills
  the running git step.
