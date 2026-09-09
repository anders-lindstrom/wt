# What's new

Newest first. `wt about` prints the top section, so keep each entry to a few
lines a human would want read out to them; the full story is in git history and
in `docs/superpowers/plans/`.

## 2026-09-09 — wt sync run, undo and doctor

- `wt sync run <work>...` rebases worktrees onto trunk with the strategies the
  repository declares, behind a lock and a safety ref under refs/wt-sync.
- Restores are verified, and a step that cannot finish now is deferred rather
  than half-applied; a stack rebases onto its own rebased parent.
- `wt sync undo <work>` puts the refs back, and refuses to discard later work.
- `wt sync doctor` checks what a run needs. Ctrl-C releases the locks and kills
  the running git step.
