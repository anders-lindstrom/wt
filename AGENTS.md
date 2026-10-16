# wt

## What's new

Every user-visible change adds one entry to the top of `internal/about/whats-new.md`, newest first, headed `## YYYY-MM-DD HH:MM — <what changed>` in local time: a few lines a person would want read out. On a rebase conflict, keep both entries, newest first. `wt about` shows the newest 5, or all from the last 3 days if more.

## JSON output

`--json` output is a contract with other programs. A change to it updates, in the same commit, the schema in `schema/`, `docs/json.md`, and the schema's version: its `x-version` and its line in `schema/versions.lock` (the test prints the line). Minor for an added field or enum value, patch for wording; anything that breaks a reader — a rename, a removal, a changed type or meaning — is a new major, `*.v2.json`, never an edit to v1.

## Tests

While working, run the package you changed, narrowed with `-run` to the family you touched. Before handing work back, run `make check` once, in the background; never run the full suite in a loop. `make test-unit` takes seconds; `make test-fast` skips the slowest tests but still takes minutes until internal/commands is split. What to run for which change: `docs/testing.md`.
