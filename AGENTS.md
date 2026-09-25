# wt

## What's new

Every user-visible change adds one entry to the top of `internal/about/whats-new.md`, newest first, headed `## YYYY-MM-DD HH:MM — <what changed>` in local time: a few lines a person would want read out. On a rebase conflict, keep both entries, newest first. `wt about` shows the newest 5, or all from the last 3 days if more.

## JSON output

`--json` output is a contract with other programs. A change to it updates, in the same commit, the schema in `schema/`, `docs/json.md`, and the schema's version: its `x-version` and its line in `schema/versions.lock` (the test prints the line). Minor for an added field or enum value, patch for wording; anything that breaks a reader — a rename, a removal, a changed type or meaning — is a new major, `*.v2.json`, never an edit to v1.
