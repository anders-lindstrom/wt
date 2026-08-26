# Repository Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all seven repositories onto `wt`, replacing ~1,580 lines of duplicated bash each with configuration plus one-line shims — **without moving a single worktree or touching a single branch.**

**Architecture:** Each repo keeps `bin/worktree/worktree.conf`, gains an optional `bin/worktree/provision.sh`, and has its scripts replaced by shims that `exec wt`. Claude Code hook registrations point at `wt hook claude-*`. `.superset/config.json` is never edited — the shims keep it working.

**Tech Stack:** bash, git worktrees, the installed `wt`.

**Spec:** `docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md`
**Predecessors:** the three `wt` plans, all complete.

## Global Constraints — read before touching a repository

- **No worktree is moved. No branch is created, renamed or deleted in a target repo** beyond the migration branch itself. `wt migrate` is not run. The ~40 existing worktrees stay exactly where they are; they resolve through git regardless of shape, and `wt doctor` lists them as candidates, not defects.
- **No main checkout is disturbed.** Every repo's migration happens in a *new worktree* cut from that repo's trunk. This is not optional: `server` is sitting on `launch` with **21 stashes**, and `personal-v` and `residential` each have an uncommitted file. Checking out a branch in their main checkouts would disturb real work.
- **Nothing is pushed.** Each migration ends as a commit on a branch, for review.
- **`.superset/config.json` is never edited.** It runs `./bin/worktree/setup.sh`, which the shim preserves.
- The acceptance gate for every repo is a **config-contract diff**: the variables the old `load_worktree_config` produced must equal what `wt config --shell` produces. Any difference is a regression and stops that repo.
- Commits use Conventional Commits, no AI attribution.

### Per-repo inputs, surveyed 2026-08-26

| repo | trunk | on branch | dirty | stashes | provision.sh | claude hooks | retired keys |
|---|---|---|---|---|---|---|---|
| infrastructure | main | main | 0 | 0 | yes (aws identity only) | yes | AWS_SETUP_ENABLED |
| server | development | **launch** | 0 | **21** | yes (+ decrypt.sh) | yes | AWS_SETUP_ENABLED |
| accessmanager | main | main | 0 | 4 | yes (+ decrypt.sh) | yes | AWS_SETUP_ENABLED, REPO_NAME |
| personal-v | main | main | **1** | 2 | no | yes | AWS_SETUP_ENABLED |
| residential | main | main | **1** | 2 | no | yes | AWS_SETUP_ENABLED |
| longhaul | master | master | 0 | 0 | no | no | AWS_SETUP_ENABLED, WORKTREE_LAYOUT |
| recipus | master | master | 0 | 0 | no | no | AWS_SETUP_ENABLED, WORKTREE_LAYOUT |

`infrastructure` has no `etc/encrypted/bin/decrypt.sh`, so its provisioning step is
the AWS identity check alone.

---

### Task 1: `wt checkout` — a worktree over an existing branch

`checkout.sh` cannot become a shim until `wt` can do what it does: put a worktree
on a branch that already exists, for reviewing a PR or picking up someone's work.
It depends on seven helpers the compat layer does not provide, so leaving it
behind would break it.

**Files:**
- Create: `internal/commands/checkout.go`, `cmd/wt/checkout.go`
- Modify: `internal/repo/repo.go`
- Test: `internal/commands/checkout_test.go`

**Interfaces:**
- Produces: `repo.AddExistingWorktree(path, branch string) error`;
  `commands.Checkout(ctx *Context, branch, work string, opts NewOptions, w io.Writer) (string, error)`;
  `commands.WorkNameFromBranch(branch, suffix string) string`.

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkNameFromBranch(t *testing.T) {
	for in, want := range map[string]string{
		"feature/pr-123":     "feature-pr-123",
		"fix_wt/login-crash": "login-crash",
		"main":               "main",
		"a//b":               "a-b",
		"--weird--":          "weird",
	} {
		if got := WorkNameFromBranch(in, "_wt"); got != want {
			t.Errorf("WorkNameFromBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckoutPutsAWorktreeOnAnExistingBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "feature/pr-123")

	var buf bytes.Buffer
	path, err := Checkout(ctx, "feature/pr-123", "", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "feature-pr-123")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got := ctx.Repo.BranchAt(path); got != "feature/pr-123" {
		t.Errorf("branch = %q, want the existing branch untouched", got)
	}
}

func TestCheckoutHonoursAnExplicitWorkName(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "feature/pr-123")

	var buf bytes.Buffer
	path, err := Checkout(ctx, "feature/pr-123", "review", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "review" {
		t.Errorf("path = %q, want it to end in review", path)
	}
}

// Checkout must never create a branch: it exists to work on one that is there.
func TestCheckoutRefusesAMissingBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := Checkout(ctx, "no-such-branch", "", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Fatal("want a refusal for a branch that does not exist")
	}
	if ctx.Repo.BranchExists("no-such-branch") {
		t.Error("checkout must not create the branch")
	}
}

func TestCheckoutRefusesAnOccupiedPath(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "dup")
	var buf bytes.Buffer
	if _, err := Checkout(ctx, "dup", "", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkout(ctx, "dup", "", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want a refusal when the worktree already exists")
	}
	_ = os.Stat
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run 'TestCheckout|TestWorkName' -v`
Expected: FAIL — `undefined: WorkNameFromBranch`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/repo/repo.go`:

```go
// AddExistingWorktree puts a worktree at path on a branch that already exists.
// Unlike AddWorktree it never creates a branch.
func (r *Repo) AddExistingWorktree(path, branch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git.Run(r.MainRoot, "worktree", "add", path, branch)
	return err
}
```

`internal/commands/checkout.go`:

```go
package commands

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

var unsafeInWorkName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// WorkNameFromBranch derives a directory-safe work name from a branch name.
// A worktree branch loses its type prefix — fix_wt/login-crash becomes
// login-crash rather than fix_wt-login-crash — and anything else has unsafe
// runs collapsed to a single dash.
func WorkNameFromBranch(branch, suffix string) string {
	work := naming.StripPrefix(branch, suffix)
	work = unsafeInWorkName.ReplaceAllString(work, "-")
	work = strings.Trim(work, "-")
	return work
}

// Checkout puts a worktree on an existing branch, for reviewing a pull request
// or picking up work that already has a branch. It never creates a branch: if
// the branch is not there, that is a mistake worth reporting rather than
// quietly inventing one.
func Checkout(ctx *Context, branch, work string, opts NewOptions, w io.Writer) (string, error) {
	if branch == "" {
		return "", fmt.Errorf("no branch given")
	}
	if !ctx.Repo.BranchExists(branch) {
		return "", fmt.Errorf("branch %s does not exist; use `wt new` to create one", branch)
	}
	if work == "" {
		work = WorkNameFromBranch(branch, ctx.Config.TypeSuffix)
		if work == "" {
			return "", fmt.Errorf("could not derive a work name from branch %q", branch)
		}
		if work != branch {
			fmt.Fprintf(w, "Using work name %q for branch %s\n", work, branch)
		}
	}

	path := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name,
		ctx.Config.DefaultType, work, ctx.Config.TypeSuffix)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}

	fmt.Fprintf(w, "Checking out %s at %s\n", branch, path)
	if err := ctx.Repo.AddExistingWorktree(path, branch); err != nil {
		return "", err
	}
	if opts.NoSetup {
		return path, nil
	}
	if err := Setup(ctx, path, SetupOptions{
		Source:    ctx.Repo.MainRoot,
		SkipBuild: opts.SkipBuild,
	}, w); err != nil {
		return path, err
	}
	return path, nil
}
```

`cmd/wt/checkout.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newCheckoutCmd() *cobra.Command {
	var opts commands.NewOptions
	cmd := &cobra.Command{
		Use:   "checkout <branch> [work-name]",
		Short: "Put a worktree on an existing branch",
		Long: "Create a worktree for a branch that already exists — reviewing a pull\n" +
			"request, or picking up work someone else started. Never creates a\n" +
			"branch; use `wt new` for that.\n\n" +
			"Without a work name one is derived from the branch.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			work := ""
			if len(args) == 2 {
				work = args[1]
			}
			path, err := commands.Checkout(ctx, args[0], work, opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.SkipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().BoolVar(&opts.NoSetup, "no-setup", false, "create the worktree without provisioning it")
	return cmd
}
```

Register `newCheckoutCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests, lint and the shell suites**

Run: `go test ./... && golangci-lint run && go build -o bin/wt ./cmd/wt && bats test/ && ./install.sh`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal cmd && git commit -m "feat(checkout): put a worktree on an existing branch"
```

---

### Task 2: Capture the config contract of all seven repos, before anything changes

The acceptance gate for each migration is that the variables `load_worktree_config`
produced are exactly what `wt config --shell` produces. That baseline can only be
taken while the old implementation is still in place, so it is taken now, for all
seven, in one go.

**Files:**
- Create: `scripts/capture-config-contract.sh`, `scripts/compare-config-contract.sh`

- [ ] **Step 1: Write the capture script**

`scripts/capture-config-contract.sh`:

```bash
#!/usr/bin/env bash
# Capture what a repository's OLD load_worktree_config produces, so a migration
# can be proved not to have changed it. Run before migrating.
#
#   scripts/capture-config-contract.sh <repo-path> <out-dir>
set -euo pipefail

repo="${1:?repo path required}"
out="${2:?output directory required}"
name=$(basename "$repo")
mkdir -p "$out"

bash -c "
  cd '$repo'
  source ./bin/env_check_functions.sh
  source ./bin/worktree_functions.sh
  load_worktree_config
  {
    echo \"MAIN_BRANCH=\$MAIN_BRANCH\"
    echo \"WORKTREE_BRANCH_PREFIX=\$WORKTREE_BRANCH_PREFIX\"
    echo \"WORKTREE_TYPE_SUFFIX=\$WORKTREE_TYPE_SUFFIX\"
    echo \"WORKTREE_DEFAULT_TYPE=\$WORKTREE_DEFAULT_TYPE\"
    echo \"WORKTREE_TYPES=\$WORKTREE_TYPES\"
    echo \"REQUIRED_BINS=\${REQUIRED_BINS:-}\"
    echo \"BUILD_INIT_ENABLED=\$BUILD_INIT_ENABLED\"
    echo \"BUILD_INIT_COMMAND=\$BUILD_INIT_COMMAND\"
    echo \"TEST_COMMAND=\$TEST_COMMAND\"
    echo \"RUN_TESTS_BEFORE_REMOVE=\$RUN_TESTS_BEFORE_REMOVE\"
    echo \"REPO_NAME=\$REPO_NAME\"
    echo \"AWS_SETUP_ENABLED=\$AWS_SETUP_ENABLED\"
    echo \"DEVELOPER_CONFIG_DIRS=\${DEVELOPER_CONFIG_DIRS[*]}\"
    echo \"DEVELOPER_CONFIG_FILES=\${DEVELOPER_CONFIG_FILES[*]:-}\"
  }
" | sort > "$out/$name.before"

echo "captured $out/$name.before"
```

`scripts/compare-config-contract.sh`:

```bash
#!/usr/bin/env bash
# Compare a migrated repository's wt config against the captured baseline.
#
#   scripts/compare-config-contract.sh <repo-path> <baseline-dir>
set -euo pipefail

repo="${1:?repo path required}"
base="${2:?baseline directory required}"
name=$(basename "$repo")

bash -c "
  cd '$repo'
  eval \"\$(wt config --shell)\"
  {
    echo \"MAIN_BRANCH=\$MAIN_BRANCH\"
    echo \"WORKTREE_BRANCH_PREFIX=\$WORKTREE_BRANCH_PREFIX\"
    echo \"WORKTREE_TYPE_SUFFIX=\$WORKTREE_TYPE_SUFFIX\"
    echo \"WORKTREE_DEFAULT_TYPE=\$WORKTREE_DEFAULT_TYPE\"
    echo \"WORKTREE_TYPES=\$WORKTREE_TYPES\"
    echo \"REQUIRED_BINS=\${REQUIRED_BINS:-}\"
    echo \"BUILD_INIT_ENABLED=\$BUILD_INIT_ENABLED\"
    echo \"BUILD_INIT_COMMAND=\$BUILD_INIT_COMMAND\"
    echo \"TEST_COMMAND=\$TEST_COMMAND\"
    echo \"RUN_TESTS_BEFORE_REMOVE=\$RUN_TESTS_BEFORE_REMOVE\"
    echo \"REPO_NAME=\$REPO_NAME\"
    echo \"AWS_SETUP_ENABLED=\$AWS_SETUP_ENABLED\"
    echo \"DEVELOPER_CONFIG_DIRS=\${DEVELOPER_CONFIG_DIRS[*]}\"
    echo \"DEVELOPER_CONFIG_FILES=\${DEVELOPER_CONFIG_FILES[*]:-}\"
  }
" | sort > "/tmp/$name.after"

if diff -u "$base/$name.before" "/tmp/$name.after"; then
  echo "✓ $name: config contract unchanged"
else
  echo "✗ $name: CONFIG CONTRACT CHANGED — stop and investigate" >&2
  exit 1
fi
```

- [ ] **Step 2: Capture all seven baselines**

```bash
cd ~/programmering/private/wt
chmod +x scripts/*.sh
BASE=~/programmering/private/wt/.migration-baseline
for r in telcred/infrastructure telcred/server telcred/accessmanager \
         telcred/personal-v telcred/residential private/longhaul private/recipus; do
  scripts/capture-config-contract.sh ~/programmering/$r "$BASE"
done
ls "$BASE"
```

Expected: seven `.before` files.

- [ ] **Step 3: Commit the scripts**

The baselines are throwaway; the scripts are not.

```bash
printf '.migration-baseline/\n' >> .gitignore
git add scripts .gitignore
git commit -m "build: add config-contract capture and compare for the repo migration"
```

---

### Task 3: Pilot — migrate `infrastructure`, then STOP for review

infrastructure is the pilot: on `main`, clean, no stashes, no colleagues, and its
`DEVELOPER_CONFIG_FILES` is currently dead so the fix is observable.

**Files (in a new worktree, never the main checkout):**
- Modify: `bin/worktree/worktree.conf`, `.claude/settings.json`
- Create: `bin/worktree/provision.sh`, nine shims, `bin/worktree_functions.sh`
- Delete: nine script bodies, `bin/worktree/remove_me_function.sh`, `bin/hooks/worktree-{create,remove}.sh`

- [ ] **Step 1: Create the migration worktree from trunk**

The main checkout is not touched — this is the rule for every repo, and the
reason it is a rule is `server`, which sits on `launch` with 21 stashes.

```bash
cd ~/programmering/telcred/infrastructure
git worktree add -b chore_wt/wt-migration \
    ../infrastructure_wt/chore_wt/wt-migration main
cd ../infrastructure_wt/chore_wt/wt-migration
git status --porcelain    # must be empty
```

- [ ] **Step 2: Write the shims**

```bash
write_shim() {   # write_shim <file> <wt-args...>
    local f="$1"; shift
    cat > "$f" <<EOF
#!/usr/bin/env bash
# Shim. The implementation lives in github.com/anders-lindstrom/wt.
# This file exists so muscle memory, .superset/config.json, the Herdr plugin's
# executability checks and .claude hooks keep working unchanged.
set -euo pipefail
command -v wt >/dev/null 2>&1 || {
    echo "worktree tooling requires 'wt' on PATH — see https://github.com/anders-lindstrom/wt" >&2
    exit 1
}
exec wt $* "\$@"
EOF
    chmod +x "$f"
}

cd bin/worktree
write_shim new.sh            new
write_shim remove.sh         remove
write_shim remove_me.sh      remove --me
write_shim list.sh           list
write_shim status.sh         status
write_shim setup.sh          setup
write_shim setup_precheck.sh doctor
write_shim checkout.sh       checkout
cd ../..

# switch.sh spawned a shell in the worktree; a script cannot cd its caller, so
# it keeps doing exactly that.
cat > bin/worktree/switch.sh <<'EOF'
#!/usr/bin/env bash
# Shim. See github.com/anders-lindstrom/wt.
# Spawns a shell in the worktree, as this script always has. For changing the
# directory of your *current* shell, use the wt_cd function instead.
set -euo pipefail
command -v wt >/dev/null 2>&1 || {
    echo "worktree tooling requires 'wt' on PATH — see https://github.com/anders-lindstrom/wt" >&2
    exit 1
}
cd "$(wt path "$1")" && exec "${SHELL:-/bin/sh}"
EOF
chmod +x bin/worktree/switch.sh

# The library the Herdr skills and plugin source by name.
cat > bin/worktree_functions.sh <<'EOF'
# Shim. The worktree function contract lives in
# github.com/anders-lindstrom/wt/compat/worktree_functions.sh.
# Sourced by the Herdr skills and the lindstrom.worktree-setup plugin.
if [[ -f "$HOME/.local/share/wt/worktree_functions.sh" ]]; then
    source "$HOME/.local/share/wt/worktree_functions.sh"
else
    echo "worktree tooling requires wt — see https://github.com/anders-lindstrom/wt" >&2
    return 1 2>/dev/null || exit 1
fi
EOF

# Superseded by wt_rm_me in the shell layer.
git rm -q bin/worktree/remove_me_function.sh
# Claude Code now calls wt directly.
git rm -q bin/hooks/worktree-create.sh bin/hooks/worktree-remove.sh
```

- [ ] **Step 3: Extract `provision.sh`**

infrastructure has no `etc/encrypted/bin/decrypt.sh`, so its step is the AWS
identity check alone. This is what `AWS_SETUP_ENABLED=true` did.

```bash
cat > bin/worktree/provision.sh <<'EOF'
#!/usr/bin/env bash
# This repository's own worktree setup step, run by `wt setup` with the new
# worktree as its working directory.
#
# Replaces the AWS_SETUP_ENABLED flag: the behaviour belongs to the repo, not
# to the generic tool.
set -euo pipefail

identity=$(aws sts get-caller-identity --query 'Arn' --output text 2>/dev/null || echo "unknown")
if [[ "$identity" == "unknown" ]]; then
    echo "Error: no usable AWS credentials; run your AWS login first" >&2
    exit 1
fi
echo "AWS access confirmed: $identity"
EOF
chmod +x bin/worktree/provision.sh
```

- [ ] **Step 4: Clean the retired keys from `worktree.conf`**

```bash
sed -i '' '/^AWS_SETUP_ENABLED=/d; /^REPO_NAME=/d; /^WORKTREE_LAYOUT=/d' bin/worktree/worktree.conf
# Remove the now-orphaned comment blocks that introduced them.
grep -n "AWS setup\|Repository name" bin/worktree/worktree.conf || true
```

Delete any comment lines left describing a key that no longer exists.

- [ ] **Step 5: Repoint the Claude Code hooks**

```bash
python3 - <<'PY'
import json, pathlib
p = pathlib.Path(".claude/settings.json")
cfg = json.loads(p.read_text())
for event, cmd in (("WorktreeCreate", "wt hook claude-create"),
                   ("WorktreeRemove", "wt hook claude-remove")):
    for entry in cfg.get("hooks", {}).get(event, []):
        for hook in entry.get("hooks", []):
            if hook.get("type") == "command":
                hook["command"] = cmd
p.write_text(json.dumps(cfg, indent=2) + "\n")
PY
git diff --stat .claude/settings.json
```

If the hooks live at the top level rather than under a `hooks` key, adjust the
path — print the file first and match its actual shape.

- [ ] **Step 6: Verify — the acceptance gate**

```bash
WTREPO=~/programmering/private/wt
BASE=$WTREPO/.migration-baseline

# 1. Config contract is unchanged.
$WTREPO/scripts/compare-config-contract.sh "$PWD" "$BASE"

# 2. Shims are executable at the paths four consumers hard-code.
for f in new.sh remove.sh setup.sh list.sh status.sh switch.sh checkout.sh setup_precheck.sh remove_me.sh; do
    test -x "bin/worktree/$f" || echo "NOT EXECUTABLE: $f"
done

# 3. The Herdr plugin's own checks.
test -x bin/worktree/setup.sh && test -x bin/worktree/new.sh && test -x bin/worktree/remove.sh \
    && echo "herdr plugin checks satisfied"

# 4. The Herdr skills' contract, sourced by name.
bash -c 'source ./bin/worktree_functions.sh && load_worktree_config \
    && echo "REPO_NAME=$REPO_NAME MAIN_BRANCH=$MAIN_BRANCH dirs=${#DEVELOPER_CONFIG_DIRS[@]}" \
    && get_worktree_path opensearch_ism'

# 5. wt itself is happy, and no worktree has moved.
wt doctor || true          # non-canonical worktrees are expected and are NOT errors to fix
wt list

# 6. .superset/config.json untouched.
git diff --stat .superset/config.json | grep . && echo "SUPERSET EDITED — WRONG" || echo "superset untouched ✓"
```

Every one must pass. The config-contract diff is the one that stops the
migration if it fails.

- [ ] **Step 7: Confirm no worktree or branch moved**

```bash
git worktree list          # compare against the list from before; must be identical except this migration worktree
git branch --list | wc -l  # unchanged except chore_wt/wt-migration
```

- [ ] **Step 8: Commit, do not push**

```bash
git add -A
git commit -m "refactor(worktree): move worktree tooling to wt

Replaces ~1,580 lines of duplicated bash with configuration and one-line
shims. The shims keep every consumer working unedited: .superset/config.json,
the Herdr plugin's executability checks, the Herdr skills' sourced function
contract, and muscle memory.

AWS_SETUP_ENABLED becomes bin/worktree/provision.sh, so the step belongs to
this repository rather than to the generic tool. The Claude Code hooks now
call wt directly and the per-repo hook scripts are gone.

No worktree has moved and no branch has changed."
```

- [ ] **Step 9: STOP. Report and wait.**

Present the diffstat, the verification output, and the line count removed. Do
not touch another repository until the human has looked at this one.

---

### Task 4: The remaining six

Only after the pilot is approved. Repeat Task 3 exactly, per the table in
Global Constraints, in this order — least to most consequential:

1. `recipus` and `longhaul` — no Claude hooks, no provision.sh; also drop `WORKTREE_LAYOUT`
2. `residential` and `personal-v` — hooks, no provision.sh
3. `accessmanager` — hooks, provision.sh with `decrypt.sh`, and `REPO_NAME` to drop
4. `server` — last, because of the 21 stashes and the `launch` branch

For the three repos with `decrypt.sh`, `provision.sh` carries the decrypt too:

```bash
cat > bin/worktree/provision.sh <<'EOF'
#!/usr/bin/env bash
# This repository's own worktree setup step, run by `wt setup` with the new
# worktree as its working directory.
set -euo pipefail

identity=$(aws sts get-caller-identity --query 'Arn' --output text 2>/dev/null || echo "unknown")
if [[ "$identity" == "unknown" ]]; then
    echo "Error: no usable AWS credentials; run your AWS login first" >&2
    exit 1
fi
echo "AWS access confirmed: $identity"

if [[ -f etc/encrypted/bin/decrypt.sh ]]; then
    echo "Decrypting local secrets..."
    if ! etc/encrypted/bin/decrypt.sh local; then
        echo "Error: failed to decrypt local secrets." >&2
        echo "  Check AWS permissions, region and KMS key access." >&2
        exit 1
    fi
fi
EOF
chmod +x bin/worktree/provision.sh
```

Run the full Task 3 verification for each. A failing config-contract diff stops
that repo and nothing else proceeds until it is understood.

---

### Task 5: Retire the sync-scripts worktree duty

`tc-ops:sync-scripts` exists largely to copy these scripts between repos. With
one implementation there is nothing left to sync.

- [ ] **Step 1: Read the skill and find its worktree responsibility**

```bash
grep -rn "worktree" ~/.claude/skills/tc-ops/sync-scripts/ 2>/dev/null \
  || find ~/.claude -path "*sync-scripts*" -name "*.md"
```

- [ ] **Step 2: Remove the worktree scope, keep the rest**

Edit the skill so it no longer claims to synchronise `bin/worktree`, and add a
line pointing at `wt` instead. Leave its other responsibilities alone.

- [ ] **Step 3: Commit**

```bash
git -C <skill repo> add -A
git -C <skill repo> commit -m "docs(sync-scripts): drop worktree scripts, now owned by wt"
```

---

## What this plan delivers

Seven repositories carrying configuration and ~10 lines of shim instead of
~1,580 lines of drifting bash, with every consumer — manual use, Superset,
Herdr's plugin, Herdr's skills, Claude Code — working unedited or repointed,
and **not one worktree moved or branch touched.**
