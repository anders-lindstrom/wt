# What's new

Newest first, each heading stamped with the local date and time of the change
(several land per day). `wt about` prints the top section, so keep each entry to a few
lines a human would want read out to them; the full story is in git history and
in `docs/superpowers/plans/`.

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
