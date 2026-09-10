# What's new

Newest first, each heading stamped with the local date and time of the change
(several land per day). `wt about` prints the top section, so keep each entry to a few
lines a human would want read out to them; the full story is in git history and
in `docs/superpowers/plans/`.

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
