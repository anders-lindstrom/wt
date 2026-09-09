# What's new

Newest first. `wt about` prints the top section, so keep each entry to a few
lines a human would want read out to them; the full story is in git history and
in `docs/superpowers/plans/`.

## 2026-09-09 — Superset's layout, and types read from a name

- Superset's `<repo>_wt/<repo>/<type>_wt/<work>` is now a layout wt endorses:
  `doctor` passes it, `list` marks it `s`, and `setup` provisions it where it
  stands. Only `!` — a layout nothing owns — is still a migrate candidate.
- `wt migrate` warns that Superset's workspace holds the old path before it
  moves, not after.
- A bare work name can carry its type: `wt new fix_dev-123` creates
  `fix_wt/dev-123`. An explicit `<type>/<work>` still wins.

## 2026-09-09 — wt sync run, undo and doctor

- `wt sync run <work>...` rebases worktrees onto trunk with the strategies the
  repository declares, behind a lock and a safety ref under refs/wt-sync.
- Restores are verified, and a step that cannot finish now is deferred rather
  than half-applied; a stack rebases onto its own rebased parent.
- `wt sync undo <work>` puts the refs back, and refuses to discard later work.
- `wt sync doctor` checks what a run needs. Ctrl-C releases the locks and kills
  the running git step.
