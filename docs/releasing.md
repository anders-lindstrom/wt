# Releasing wt

A release is a `v*` tag pushed to GitHub. The release workflow,
`.github/workflows/release.yml`, builds darwin and linux binaries for amd64 and
arm64 with GoReleaser and attaches them to the GitHub release, which is what
`mise use -g github:anders-lindstrom/wt` installs.

```sh
scripts/release.sh patch|minor|major
```

tags the next version on a merged HEAD and pushes the tag. Landing a branch
runs it. The workflow publishes only once CI has passed on the tagged commit:
it waits up to an hour for the CI run of that push to main, and fails when CI
failed or never ran on it. A rerun is safe: a HEAD that already carries a `v*` tag gets that tag
pushed if it is not yet, and no second one.

## Without GitHub Actions

When GitHub Actions cannot run, run the same release workflow locally with
`act`, Docker and `gh auth token`:

```sh
scripts/release.sh patch                   # tag and push merged HEAD, as above
scripts/release-local.sh --plan v1.2.3     # act -n on the publishing path
scripts/release-local.sh --dry-run v1.2.3  # build all four archives, publish nothing
scripts/release-local.sh v1.2.3            # publish the pushed tag
```

- Run it from a clean checkout at the tag, and the tag must be on freshly fetched
  `origin/main`. It builds in a temporary clone with full history and tags.
- `--dry-run` builds the four snapshot archives without publishing and prints
  their directory under `dist/`. It refuses tags older than the snapshot gate
  in the workflow.
- Run previews and publishing one at a time: act shares job containers and
  caches.
- The workflow honours snapshot mode only under act; a tag pushed to GitHub
  always publishes once CI has passed on it.
- Under act the workflow does not wait for CI, which may not have run at all:
  check that `make check` passed on the tag before you publish.
- The GoReleaser config needs only `GITHUB_TOKEN`.
- Only a real run checks that the GitHub release is created and the assets
  upload; neither preview mode does.
