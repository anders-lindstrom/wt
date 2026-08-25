# wt Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the `wt` CLI — a cobra command layer plus `list`, `status`, `setup`, `new`, `remove`, `adopt`, `migrate`, `doctor` and the Claude Code hook handlers — reproducing the existing bash behaviour exactly.

**Architecture:** `cmd/wt/` holds one cobra command per file, thin, doing only flag parsing and output. All behaviour lives in `internal/commands`, testable without a process. Git mutation helpers live on `internal/repo.Repo`.

**Tech Stack:** Go 1.26, `spf13/cobra`, `BurntSushi/toml`, `go test`, `bats-core`.

**Spec:** `docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md`
**Predecessor:** `docs/superpowers/plans/2026-08-25-wt-foundation.md` (complete)

## Global Constraints

- Everything from the foundation plan's constraints still applies.
- **Regression rule:** the six safety behaviours of `remove.sh` are load-bearing and each gets its own test:
  1. The branch is read from the worktree, never rebuilt from the work name.
  2. Detached HEAD → remove the worktree, touch no branch.
  3. A branch not matching `<type><suffix>/` is not owned by this tooling → remove the worktree, touch no branch.
  4. A worktree containing `.gitmodules` cannot be removed by `git worktree remove`; use `rm -rf` then `git worktree prune`.
  5. Branch merged into `MAIN_BRANCH` → `git branch -d`.
  6. Branch not merged → rename to strip the type prefix, never delete.
- `setup` must also: init submodules when `.gitmodules` exists, skip files that already exist, `mkdir -p` a config file's parent, and treat a **build-init failure as a warning, not an error**.
- Deliberate behaviour change, recorded here: the old `setup.sh` guessed a project type (flutter/node/rust/go/java) to validate tools ad hoc. `REQUIRED_BINS` already declares that per repo, so `wt` checks `REQUIRED_BINS` instead and drops the guessing.
- Commands print to an injected `io.Writer`, never to `os.Stdout` directly, so every one is testable.
- Every task ends with a Conventional Commits commit, no AI attribution.

---

### Task 1: Cobra command layer

**Files:**
- Create: `cmd/wt/root.go`, `cmd/wt/config.go`, `cmd/wt/resolve.go`
- Modify: `cmd/wt/main.go` (reduce to a `main` that calls `Execute`)
- Test: `cmd/wt/root_test.go`

**Interfaces:**
- Consumes: `commands.Open`, `commands.Config`, `commands.Path`, `commands.Branch`.
- Produces: `newRootCmd() *cobra.Command`; `openContext(cmd *cobra.Command) (*commands.Context, error)` resolving cwd once; `Execute() int` returning the process exit code.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestRootListsSubcommands(t *testing.T) {
	out, err := runCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"config", "path", "branch"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help does not mention %q:\n%s", want, out)
		}
	}
}

func TestVersion(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("version printed nothing")
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	if _, err := runCmd(t, "wibble"); err == nil {
		t.Error("want an error for an unknown command")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wt/ -v`
Expected: FAIL — `undefined: newRootCmd`.

- [ ] **Step 3: Add cobra**

```bash
cd ~/programmering/private/wt && go get github.com/spf13/cobra@latest
```

- [ ] **Step 4: Write minimal implementation**

`cmd/wt/root.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "wt",
		Short: "Git worktree tooling: one implementation, per-repo configuration",
		Long: "wt manages git worktrees from a single implementation, reading each\n" +
			"repository's own bin/worktree/worktree.conf for how that repo works.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(),
		newConfigCmd(),
		newPathCmd(),
		newBranchCmd(),
		newBranchStripCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the wt version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

// openContext resolves the repository and configuration for the current
// directory. Every subcommand that touches a repo starts here.
func openContext() (*commands.Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return commands.Open(cwd)
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		return 1
	}
	return 0
}
```

`cmd/wt/config.go`:

```go
package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newConfigCmd() *cobra.Command {
	var shell bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the repository's resolved worktree configuration",
		Long: "Print the resolved configuration. With --shell, emit eval-able\n" +
			"assignments using the legacy variable names that the Herdr skills\n" +
			"and plugin expect from load_worktree_config.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.Config(ctx, shell, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&shell, "shell", false, "emit eval-able shell assignments")
	return cmd
}
```

`cmd/wt/resolve.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// completeWork offers the work names of existing worktrees. This is the
// ergonomic point of the tool: the names are never memorable, so the shell
// should supply them.
func completeWork(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, err := openContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, w := range worktrees {
		if w.IsMain || w.Branch == "" {
			continue
		}
		if typ, work, ok := naming.ParseBranch(w.Branch, ctx.Config.TypeSuffix); ok {
			names = append(names, typ+"/"+work)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "path <type>/<work>",
		Short:             "Print the path of a worktree",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out, err := commands.Path(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newBranchCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "branch <type>/<work>",
		Short:             "Print the branch name for a piece of work",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out, err := commands.Branch(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newBranchStripCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "branch-strip <branch>",
		Short:  "Strip the worktree type prefix from a branch name",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // compat surface for strip_worktree_prefix
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), naming.StripPrefix(args[0], ctx.Config.TypeSuffix))
			return nil
		},
	}
}
```

`cmd/wt/main.go` becomes:

```go
// Command wt manages git worktrees from one implementation, configured per repo.
package main

import "os"

func main() { os.Exit(Execute()) }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./... && go build -o bin/wt ./cmd/wt && ./bin/wt --help && bats test/compat.bats`
Expected: PASS, help lists the subcommands, all 5 bats tests still green (the compat layer is unchanged and must stay working).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd
git commit -m "feat(cli): move the command layer to cobra"
```

---

### Task 2: Git mutation helpers

**Files:**
- Modify: `internal/repo/repo.go`
- Test: `internal/repo/mutate_test.go`

**Interfaces:**
- Produces, all on `*Repo`:

```go
func (r *Repo) BranchAt(path string) string          // "" when detached or unreadable
func (r *Repo) BranchExists(name string) bool
func (r *Repo) IsMerged(branch, base string) bool
func (r *Repo) AddWorktree(path, branch, base string) error
func (r *Repo) RemoveWorktree(path string) error
func (r *Repo) Prune() error
func (r *Repo) MoveWorktree(from, to string) error
func (r *Repo) DeleteBranch(name string) error       // safe delete, -d
func (r *Repo) RenameBranch(from, to string) error
func (r *Repo) HasSubmodules(path string) bool
```

- [ ] **Step 1: Write the failing test**

```go
package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveWorktree(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(r.Parent, "demo_wt", "fix_wt", "thing2")

	if err := r.AddWorktree(dst, "fix_wt/thing2", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := r.BranchAt(dst); got != "fix_wt/thing2" {
		t.Errorf("BranchAt = %q", got)
	}
	if !r.BranchExists("fix_wt/thing2") {
		t.Error("branch should exist")
	}
	if err := r.RemoveWorktree(dst); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("worktree directory should be gone")
	}
}

func TestBranchAtDetachedIsEmpty(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(parent, "detached")
	run(t, main, "worktree", "add", "-q", "--detach", dst)
	if got := r.BranchAt(dst); got != "" {
		t.Errorf("BranchAt on detached = %q, want empty", got)
	}
}

func TestIsMerged(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	// feat_wt/thing was branched from main with no new commits: merged.
	if !r.IsMerged("feat_wt/thing", "main") {
		t.Error("an unchanged branch should count as merged")
	}
	run(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	if r.IsMerged("main", "feat_wt/thing") {
		t.Error("main has moved ahead; it is not merged into the branch")
	}
}

func TestRenameAndDeleteBranch(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	run(t, main, "branch", "fix_wt/temp")
	if err := r.RenameBranch("fix_wt/temp", "temp"); err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if r.BranchExists("fix_wt/temp") || !r.BranchExists("temp") {
		t.Error("rename did not take effect")
	}
	if err := r.DeleteBranch("temp"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if r.BranchExists("temp") {
		t.Error("branch should be deleted")
	}
}

func TestHasSubmodules(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	if r.HasSubmodules(main) {
		t.Error("fixture has no submodules")
	}
	if err := os.WriteFile(filepath.Join(main, ".gitmodules"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.HasSubmodules(main) {
		t.Error("should detect .gitmodules")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/repo/ -run 'TestAdd|TestBranchAt|TestIsMerged|TestRename|TestHasSub' -v`
Expected: FAIL — `r.AddWorktree undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/repo/repo.go`:

```go
// BranchAt reports the branch checked out at path, or "" when the worktree is
// detached or unreadable. remove reads the branch from the worktree rather than
// rebuilding it from the work name: once the type varies, the name no longer
// determines the branch, and a reconstructed name may belong to an unrelated
// branch that would then be deleted.
func (r *Repo) BranchAt(path string) string {
	out, err := git.Run(path, "symbolic-ref", "--short", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return out
}

// BranchExists reports whether a local branch of that name exists.
func (r *Repo) BranchExists(name string) bool {
	_, err := git.Run(r.MainRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// IsMerged reports whether branch is fully contained in base.
func (r *Repo) IsMerged(branch, base string) bool {
	out, err := git.Run(r.MainRoot, "branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == branch {
			return true
		}
	}
	return false
}

// AddWorktree creates a worktree at path on a new branch cut from base.
func (r *Repo) AddWorktree(path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git.Run(r.MainRoot, "worktree", "add", "-b", branch, path, base)
	return err
}

// RemoveWorktree deletes a worktree checkout. A worktree containing submodules
// cannot be removed by git worktree remove, so it is deleted directly and the
// registration pruned — the same manual path the bash implementation took.
func (r *Repo) RemoveWorktree(path string) error {
	if r.HasSubmodules(path) {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return r.Prune()
	}
	if _, err := git.Run(r.MainRoot, "worktree", "remove", path); err != nil {
		return fmt.Errorf("%w (uncommitted changes? try removing it by hand)", err)
	}
	return nil
}

// Prune clears stale worktree registrations.
func (r *Repo) Prune() error {
	_, err := git.Run(r.MainRoot, "worktree", "prune")
	return err
}

// MoveWorktree relocates a worktree checkout.
func (r *Repo) MoveWorktree(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	_, err := git.Run(r.MainRoot, "worktree", "move", from, to)
	return err
}

// DeleteBranch deletes a branch, refusing when it is not merged.
func (r *Repo) DeleteBranch(name string) error {
	_, err := git.Run(r.MainRoot, "branch", "-d", name)
	return err
}

// RenameBranch renames a branch.
func (r *Repo) RenameBranch(from, to string) error {
	_, err := git.Run(r.MainRoot, "branch", "-m", from, to)
	return err
}

// HasSubmodules reports whether the checkout at path declares submodules.
func (r *Repo) HasSubmodules(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".gitmodules"))
	return err == nil
}
```

Extend the import block to `"fmt"`, `"os"`, `"path/filepath"`, `"strings"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/repo/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repo
git commit -m "feat(repo): add worktree and branch mutation helpers"
```

---

### Task 3: `wt list` and `wt status`

**Files:**
- Create: `internal/commands/list.go`, `cmd/wt/list.go`
- Test: `internal/commands/list_test.go`

**Interfaces:**
- Produces: `commands.List(ctx *Context, w io.Writer) error` and `commands.Status(ctx *Context, w io.Writer) error`.

`List` shows every worktree in any layout, marking those outside the canonical path so a migration candidate is visible without a separate command.

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// repoWithWorktree builds one repo and adds a worktree at the path returned by
// where(parent), so the caller cannot accidentally compute a path from a
// different repository than the one it mutates.
func repoWithWorktree(t *testing.T, where func(parent string) string) (main, wt string) {
	t.Helper()
	main = fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	wt = where(filepath.Dir(main))
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing", wt)
	return main, wt
}

func TestListShowsWorktreesAndMarksNonCanonical(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo-legacy")
	})

	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatalf("List: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "feat_wt/thing") {
		t.Errorf("branch missing:\n%s", out)
	}
	if !strings.Contains(out, "demo-legacy") {
		t.Errorf("legacy path missing:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Errorf("non-canonical worktree not marked:\n%s", out)
	}
}

func TestListMarksCanonicalWorktreeCleanly(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "feat_wt/thing") && strings.Contains(line, "!") {
			t.Errorf("canonical worktree wrongly marked: %q", line)
		}
	}
}

func TestStatusReportsCleanliness(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Status(ctx, &buf); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(buf.String(), "clean") {
		t.Errorf("want a cleanliness report:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run 'TestList|TestStatus' -v`
Expected: FAIL — `undefined: List`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/list.go`:

```go
package commands

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// List prints every worktree of the repository, in whatever layout it is in.
// A worktree outside the canonical path is marked "!", because worktrees made
// by other tools — Superset's shape, or anything created before migration —
// stay valid but are candidates for `wt migrate`.
func List(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\tWORK\tBRANCH\tPATH")
	for _, wt := range worktrees {
		work, branch := "(main)", wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !wt.IsMain {
			if typ, name, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix); ok {
				work = name
				if wt.Path != naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, name, ctx.Config.TypeSuffix) {
					mark = "!"
				}
			} else {
				work = "-"
				mark = "!"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mark, work, branch, wt.Path)
	}
	return tw.Flush()
}

// Status prints each worktree's branch and whether its checkout is clean.
func Status(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tSTATE\tPATH")
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		state := "clean"
		if out, err := git.Run(wt.Path, "status", "--porcelain"); err != nil {
			state = "unreadable"
		} else if out != "" {
			state = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", branch, state, wt.Path)
	}
	return tw.Flush()
}
```

`cmd/wt/list.go`:

```go
package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.List(ctx, cmd.OutOrStdout())
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show each worktree's branch and whether it is clean",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.Status(ctx, cmd.OutOrStdout())
		},
	}
}
```

Register both in `newRootCmd`'s `AddCommand` call.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(list): add wt list and wt status"
```

---

### Task 4: `wt setup`

**Files:**
- Create: `internal/commands/setup.go`, `cmd/wt/setup.go`
- Test: `internal/commands/setup_test.go`

**Interfaces:**
- Produces:

```go
type SetupOptions struct {
	Source    string // worktree to copy developer config from
	SkipBuild bool
}
func Setup(ctx *Context, target string, opts SetupOptions, w io.Writer) error
```

Order, matching the bash exactly: config dirs → config files → `provision.sh` → submodules → build init.

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCopiesConfigDirsAndFiles(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"+
		"DEVELOPER_CONFIG_DIRS=(.vscode)\nDEVELOPER_CONFIG_FILES=(\"nested/app.env\")\n")
	mustMkdir(t, filepath.Join(main, ".vscode"))
	mustWrite(t, filepath.Join(main, ".vscode", "settings.json"), "{}")
	mustMkdir(t, filepath.Join(main, "nested"))
	mustWrite(t, filepath.Join(main, "nested", "app.env"), "K=V")

	target := t.TempDir()
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".vscode", "settings.json")); err != nil {
		t.Errorf("config dir not copied: %v", err)
	}
	// The parent directory of a nested config file must be created.
	if got := mustRead(t, filepath.Join(target, "nested", "app.env")); got != "K=V" {
		t.Errorf("config file not copied: %q", got)
	}
}

func TestSetupNeverOverwritesExistingFiles(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"+
		"DEVELOPER_CONFIG_FILES=(\"app.env\")\n")
	mustWrite(t, filepath.Join(main, "app.env"), "FROM=source")
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "app.env"), "MINE=keep")

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(target, "app.env")); got != "MINE=keep" {
		t.Errorf("existing file was overwritten: %q", got)
	}
}

func TestSetupRunsProvisionScript(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	mustWrite(t, filepath.Join(main, "bin", "worktree", "provision.sh"),
		"#!/bin/sh\necho provisioned > provisioned.txt\n")
	if err := os.Chmod(filepath.Join(main, "bin", "worktree", "provision.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// provision.sh runs with the target worktree as its working directory.
	if got := mustRead(t, filepath.Join(target, "provisioned.txt")); !strings.Contains(got, "provisioned") {
		t.Errorf("provision.sh did not run in the target: %q", got)
	}
}

// A failing build is a warning in the bash implementation. Setup must not
// return an error for it, or every agent that provisions would start failing.
func TestSetupBuildFailureIsAWarningNotAnError(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"exit 3\"\n")
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("build failure should not be fatal: %v", err)
	}
	if !strings.Contains(buf.String(), "Warning") {
		t.Errorf("want a warning in the output:\n%s", buf.String())
	}
}

func TestSetupSkipBuild(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"touch built.txt\"\n")
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main, SkipBuild: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "built.txt")); err == nil {
		t.Error("build ran despite --skip-build")
	}
}

// helpers
func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run TestSetup -v`
Expected: FAIL — `undefined: Setup`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/setup.go`:

```go
package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/git"
)

// SetupOptions controls provisioning.
type SetupOptions struct {
	Source    string
	SkipBuild bool
}

// Setup provisions a worktree: developer config, the repo's own provision.sh,
// submodules, then build initialisation. The order matches the bash
// implementation this replaces.
//
// A build-init failure is reported as a warning and does not fail the command,
// because every agent that provisions a worktree would otherwise start failing
// on a transient dependency problem.
func Setup(ctx *Context, target string, opts SetupOptions, w io.Writer) error {
	src := opts.Source
	if src == "" {
		src = ctx.Repo.MainRoot
	}

	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(w, " - source %s not found; skipping config sync\n", src)
	} else {
		copyConfigDirs(ctx, src, target, w)
		copyConfigFiles(ctx, src, target, w)
	}

	if err := runProvision(ctx, target, w); err != nil {
		return err
	}
	initSubmodules(target, w)
	runBuildInit(ctx, target, opts, w)

	fmt.Fprintln(w, "✓ Worktree setup complete")
	return nil
}

func copyConfigDirs(ctx *Context, src, target string, w io.Writer) {
	for _, d := range ctx.Config.DeveloperConfigDirs {
		from, to := filepath.Join(src, d), filepath.Join(target, d)
		if _, err := os.Stat(from); err != nil {
			fmt.Fprintf(w, " - %s not in source, skipping\n", d)
			continue
		}
		if _, err := os.Stat(to); err == nil {
			fmt.Fprintf(w, " - %s already exists, skipping\n", d)
			continue
		}
		if err := copyTree(from, to); err != nil {
			fmt.Fprintf(w, " ! failed to copy %s: %v\n", d, err)
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", d)
	}
}

func copyConfigFiles(ctx *Context, src, target string, w io.Writer) {
	for _, f := range ctx.Config.DeveloperConfigFiles {
		from, to := filepath.Join(src, f), filepath.Join(target, f)
		if st, err := os.Stat(from); err != nil || st.IsDir() {
			fmt.Fprintf(w, " - %s not in source, skipping\n", f)
			continue
		}
		if _, err := os.Stat(to); err == nil {
			fmt.Fprintf(w, " - %s already exists, skipping\n", f)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			fmt.Fprintf(w, " ! failed to create %s: %v\n", filepath.Dir(to), err)
			continue
		}
		if err := copyFile(from, to); err != nil {
			fmt.Fprintf(w, " ! failed to copy %s: %v\n", f, err)
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", f)
	}
}

// runProvision executes the repository's own setup step, if it declares one.
// This is what AWS_SETUP_ENABLED became: repo-declared behaviour rather than a
// Telcred-shaped flag in a generic tool. Its failure IS fatal — a worktree
// without decrypted secrets is not usable.
func runProvision(ctx *Context, target string, w io.Writer) error {
	if !ctx.HasProvisionScript() {
		return nil
	}
	script := filepath.Join(ctx.Repo.MainRoot, "bin", "worktree", "provision.sh")
	fmt.Fprintln(w, "Running bin/worktree/provision.sh...")
	cmd := exec.Command(script)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("provision.sh failed: %w", err)
	}
	fmt.Fprintln(w, "✓ provision.sh complete")
	return nil
}

func initSubmodules(target string, w io.Writer) {
	if _, err := os.Stat(filepath.Join(target, ".gitmodules")); err != nil {
		return
	}
	fmt.Fprintln(w, "Initializing git submodules...")
	if _, err := git.Run(target, "submodule", "update", "--init", "--recursive"); err != nil {
		fmt.Fprintf(w, " ! Warning: failed to initialise submodules: %v\n", err)
		return
	}
	fmt.Fprintln(w, "✓ submodules initialised")
}

func runBuildInit(ctx *Context, target string, opts SetupOptions, w io.Writer) {
	switch {
	case opts.SkipBuild:
		fmt.Fprintln(w, "⏭ build initialisation skipped (--skip-build)")
		return
	case !ctx.Config.BuildInitEnabled:
		fmt.Fprintln(w, "- build initialisation disabled in configuration")
		return
	}
	fmt.Fprintf(w, "Running: %s\n", ctx.Config.BuildInitCommand)
	cmd := exec.Command("sh", "-c", ctx.Config.BuildInitCommand)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, " ! Warning: build initialisation failed: %v\n", err)
		fmt.Fprintf(w, "   Try running it by hand: %s\n", ctx.Config.BuildInitCommand)
		return
	}
	fmt.Fprintln(w, "✓ build dependencies downloaded")
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, st.Mode().Perm())
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		}
		return copyFile(path, dst)
	})
}
```

`cmd/wt/setup.go`:

```go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSetupCmd() *cobra.Command {
	var skipBuild bool
	cmd := &cobra.Command{
		Use:   "setup <source-dir>",
		Short: "Provision the current worktree from a source checkout",
		Long: "Copy developer config from <source-dir>, run the repository's\n" +
			"bin/worktree/provision.sh if it has one, initialise submodules and\n" +
			"run build initialisation.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.SetupOptions{SkipBuild: skipBuild}
			if len(args) == 1 {
				opts.Source = args[0]
			}
			target, err := os.Getwd()
			if err != nil {
				return err
			}
			return commands.Setup(ctx, target, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip build initialisation")
	return cmd
}
```

Register `newSetupCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(setup): port worktree provisioning to go"
```

---

### Task 5: `wt new`

**Files:**
- Create: `internal/commands/new.go`, `cmd/wt/new.go`
- Test: `internal/commands/new_test.go`

**Interfaces:**
- Produces:

```go
type NewOptions struct {
	Base      string // defaults to the configured MainBranch
	SkipBuild bool
	NoSetup   bool
}
func New(ctx *Context, spec string, opts NewOptions, w io.Writer) (path string, err error)
```

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCreatesCanonicalWorktreeAndBranch(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")

	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "login-crash")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "fix_wt/login-crash" {
		t.Errorf("branch = %q", got)
	}
}

func TestNewBareWorkNameTakesDefaultType(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")

	ctx, _ := Open(main)
	var buf bytes.Buffer
	path, err := New(ctx, "thing", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "feat_wt/thing" {
		t.Errorf("branch = %q, want feat_wt/thing", got)
	}
}

func TestNewRefusesDuplicateWork(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error creating the same work twice")
	}
}

func TestNewRejectsUnknownType(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if _, err := New(ctx, "wibble/thing", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error for an unknown type")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run TestNew -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/new.go`:

```go
package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// NewOptions controls worktree creation.
type NewOptions struct {
	Base      string
	SkipBuild bool
	NoSetup   bool
}

// New creates a branch and its worktree at the canonical path, then provisions
// it. It returns the worktree path so a caller can cd there.
func New(ctx *Context, spec string, opts NewOptions, w io.Writer) (string, error) {
	branch, err := Branch(ctx, spec)
	if err != nil {
		return "", err
	}
	typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType)
	if err != nil {
		return "", err
	}

	if ctx.Repo.BranchExists(branch) {
		return "", fmt.Errorf("branch %s already exists", branch)
	}
	path := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}

	base := opts.Base
	if base == "" {
		base = ctx.Config.MainBranch
	}
	fmt.Fprintf(w, "Creating %s at %s (from %s)\n", branch, path, base)
	if err := ctx.Repo.AddWorktree(path, branch, base); err != nil {
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

`cmd/wt/new.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newNewCmd() *cobra.Command {
	var opts commands.NewOptions
	cmd := &cobra.Command{
		Use:   "new <type>/<work>",
		Short: "Create a worktree and its branch, then provision it",
		Long: "Create <type>_wt/<work> from the repository's main branch, place the\n" +
			"worktree at the canonical path, and provision it. A bare <work> takes\n" +
			"the repository's default type.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// The path alone goes to stdout so `cd "$(wt new ...)"` works.
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch to cut from (default: the configured main branch)")
	cmd.Flags().BoolVar(&opts.SkipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().BoolVar(&opts.NoSetup, "no-setup", false, "create the worktree without provisioning it")
	return cmd
}
```

Register `newNewCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(new): add wt new"
```

---

### Task 6: `wt remove` — the six safety behaviours

**Files:**
- Create: `internal/commands/remove.go`, `cmd/wt/remove.go`
- Test: `internal/commands/remove_test.go`

**Interfaces:**
- Produces:

```go
func Remove(ctx *Context, spec string, w io.Writer) error
func RemoveAt(ctx *Context, path string, w io.Writer) error
```

`RemoveAt` is what `--me` uses once the shell function has cd'd out.

- [ ] **Step 1: Write the failing test — one test per safety behaviour**

```go
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoReady(t *testing.T) (*Context, string) {
	t.Helper()
	main := fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main
}

// 5. A merged branch is deleted.
func TestRemoveDeletesMergedBranch(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/merged", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, "fix/merged", &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/merged") {
		t.Error("merged branch should have been deleted")
	}
}

// 6. An unmerged branch is renamed to strip the prefix, never deleted.
func TestRemoveRenamesUnmergedBranch(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	path, err := New(ctx, "fix/unmerged", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")

	if err := Remove(ctx, "fix/unmerged", &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/unmerged") {
		t.Error("prefixed branch should be gone")
	}
	if !ctx.Repo.BranchExists("unmerged") {
		t.Error("unmerged work must be kept under the stripped name")
	}
}

// 2. A detached worktree has no branch to clean up; touch none.
func TestRemoveDetachedTouchesNoBranch(t *testing.T) {
	ctx, main := repoReady(t)
	dst := filepath.Join(ctx.Repo.Parent, "detached")
	gitIn(t, main, "worktree", "add", "-q", "--detach", dst)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
	if !strings.Contains(buf.String(), "no branch") {
		t.Errorf("want a note about having no branch:\n%s", buf.String())
	}
}

// 3. A branch this tooling does not own is never deleted or renamed.
func TestRemoveLeavesForeignBranchAlone(t *testing.T) {
	ctx, main := repoReady(t)
	gitIn(t, main, "branch", "someones-work")
	dst := filepath.Join(ctx.Repo.Parent, "foreign")
	gitIn(t, main, "worktree", "add", "-q", dst, "someones-work")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("a branch this tooling did not create must survive removal")
	}
}

// 1. The branch is read from the worktree, not rebuilt from the work name.
func TestRemoveReadsBranchFromWorktree(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	path, err := New(ctx, "fix/renamed", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	// Rename the branch by hand: name and branch now disagree.
	gitIn(t, path, "branch", "-m", "fix_wt/renamed", "spike_wt/actually")

	if err := RemoveAt(ctx, path, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if ctx.Repo.BranchExists("spike_wt/actually") {
		t.Error("the branch actually checked out should have been handled")
	}
}

func TestRemoveUnknownWorkIsAnError(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	if err := Remove(ctx, "fix/nope", &buf); err == nil {
		t.Error("want an error removing work that does not exist")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run TestRemove -v`
Expected: FAIL — `undefined: Remove`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/remove.go`:

```go
package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Remove deletes the worktree holding a piece of work, then decides what
// happens to its branch.
func Remove(ctx *Context, spec string, w io.Writer) error {
	path, err := Path(ctx, spec)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no worktree at %s", path)
	}
	return RemoveAt(ctx, path, w)
}

// RemoveAt removes the worktree at path.
//
// The branch is read from the worktree rather than rebuilt from the work name:
// once the type can vary, the name no longer determines the branch, and a
// reconstructed name may belong to an unrelated branch that this would then
// delete. Two cases decline to name a branch at all — a detached HEAD, and a
// branch that does not follow the worktree convention and so belongs to
// someone else. In both, the checkout goes and no branch is touched.
func RemoveAt(ctx *Context, path string, w io.Writer) error {
	branch := ctx.Repo.BranchAt(path)
	switch {
	case branch == "":
		fmt.Fprintf(w, "Note: %s has no branch checked out; removing the worktree only\n", path)
	default:
		if _, _, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix); !ok {
			fmt.Fprintf(w, "Note: %s is not a worktree branch; removing the worktree only\n", branch)
			branch = ""
		}
	}

	fmt.Fprintf(w, "Removing worktree at %s\n", path)
	if err := ctx.Repo.RemoveWorktree(path); err != nil {
		return err
	}

	if branch == "" || !ctx.Repo.BranchExists(branch) {
		fmt.Fprintln(w, "✓ worktree removed")
		return nil
	}

	if ctx.Repo.BranchExists(ctx.Config.MainBranch) &&
		ctx.Repo.IsMerged(branch, ctx.Config.MainBranch) {
		if err := ctx.Repo.DeleteBranch(branch); err != nil {
			return err
		}
		fmt.Fprintf(w, "✓ branch %s was merged into %s and has been deleted\n",
			branch, ctx.Config.MainBranch)
		return nil
	}

	kept := naming.StripPrefix(branch, ctx.Config.TypeSuffix)
	if err := ctx.Repo.RenameBranch(branch, kept); err != nil {
		fmt.Fprintf(w, "✓ worktree removed; keeping branch %s (not merged into %s)\n",
			branch, ctx.Config.MainBranch)
		return nil
	}
	fmt.Fprintf(w, "✓ worktree removed; branch kept as %s (not merged into %s)\n",
		kept, ctx.Config.MainBranch)
	fmt.Fprintf(w, "  delete it later with: git branch -d %s\n", kept)
	return nil
}
```

`cmd/wt/remove.go`:

```go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	cmd := &cobra.Command{
		Use:     "remove <type>/<work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove the worktree and decide what happens to its branch: delete it\n" +
			"when it is merged into the main branch, otherwise rename it out of the\n" +
			"<type>_wt/ prefix so unmerged work is never lost. A branch this tooling\n" +
			"did not create is never touched.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			if me {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				return commands.RemoveAt(ctx, cwd, cmd.OutOrStdout())
			}
			if len(args) != 1 {
				return cmd.Usage()
			}
			return commands.Remove(ctx, args[0], cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	return cmd
}
```

Register `newRemoveCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS, all six safety behaviours covered.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(remove): add wt remove with merge-checked branch handling"
```

---

### Task 7: `wt adopt`, `wt migrate` and `wt doctor`

**Files:**
- Create: `internal/commands/adopt.go`, `internal/commands/doctor.go`, `cmd/wt/adopt.go`, `cmd/wt/doctor.go`
- Test: `internal/commands/adopt_test.go`, `internal/commands/doctor_test.go`

**Interfaces:**
- Produces:

```go
func Adopt(ctx *Context, path string, relocate bool, opts SetupOptions, w io.Writer) (string, error)
func Migrate(ctx *Context, spec string, w io.Writer) (string, error)
func Doctor(ctx *Context, w io.Writer) (problems int, err error)
```

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRelocatesToCanonicalPath(t *testing.T) {
	ctx, main := repoReady(t)
	legacy := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix_wt/legacy", legacy)

	var buf bytes.Buffer
	got, err := Migrate(ctx, "fix/legacy", &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "legacy")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("worktree not at canonical path: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("legacy path should be gone")
	}
}

func TestMigrateIsANoOpWhenAlreadyCanonical(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	path, err := New(ctx, "fix/already", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Migrate(ctx, "fix/already", &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got != path {
		t.Errorf("got %q, want unchanged %q", got, path)
	}
}

func TestAdoptProvisionsAnExternallyCreatedWorktree(t *testing.T) {
	ctx, main := repoReady(t)
	// A worktree made the way Superset makes them: outside the canonical path.
	external := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "outside")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/outside", external)

	var buf bytes.Buffer
	got, err := Adopt(ctx, external, false, SetupOptions{Source: main}, &buf)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got != external {
		t.Errorf("without --relocate the path must not change: %q", got)
	}
}

func TestAdoptRelocates(t *testing.T) {
	ctx, main := repoReady(t)
	external := filepath.Join(ctx.Repo.Parent, "elsewhere")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/moved", external)

	var buf bytes.Buffer
	got, err := Adopt(ctx, external, true, SetupOptions{Source: main}, &buf)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "moved")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAdoptRefusesAPathThatIsNotAWorktree(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	if _, err := Adopt(ctx, t.TempDir(), false, SetupOptions{}, &buf); err == nil {
		t.Error("want an error adopting a directory that is not a worktree of this repo")
	}
}

func TestDoctorReportsNonCanonicalAndNestedWorktrees(t *testing.T) {
	ctx, main := repoReady(t)
	nested := filepath.Join(main, "inside_wt", "feat_wt", "oops")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/oops", nested)

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems == 0 {
		t.Errorf("want problems reported:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "inside the main checkout") {
		t.Errorf("nested worktree not reported:\n%s", buf.String())
	}
}

func TestDoctorIsQuietOnAHealthyRepo(t *testing.T) {
	ctx, _ := repoReady(t)
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("healthy repo reported %d problems:\n%s", problems, buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run 'TestAdopt|TestMigrate|TestDoctor' -v`
Expected: FAIL — `undefined: Migrate`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/adopt.go`:

```go
package commands

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Adopt provisions a worktree somebody else created — plain `git worktree add`,
// a detached agent checkout, or anything made before this repo was migrated.
// With relocate set it is also moved to the canonical path.
func Adopt(ctx *Context, path string, relocate bool, opts SetupOptions, w io.Writer) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	var found *string
	for i := range worktrees {
		if worktrees[i].Path == abs {
			found = &worktrees[i].Path
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("%s is not a worktree of %s", abs, ctx.Repo.Name)
	}

	if relocate {
		moved, err := relocateWorktree(ctx, abs, w)
		if err != nil {
			return "", err
		}
		abs = moved
	}
	if opts.Source == "" {
		opts.Source = ctx.Repo.MainRoot
	}
	if err := Setup(ctx, abs, opts, w); err != nil {
		return abs, err
	}
	return abs, nil
}

// Migrate moves a worktree to the canonical path without reprovisioning it.
func Migrate(ctx *Context, spec string, w io.Writer) (string, error) {
	path, err := Path(ctx, spec)
	if err != nil {
		return "", err
	}
	return relocateWorktree(ctx, path, w)
}

func relocateWorktree(ctx *Context, path string, w io.Writer) (string, error) {
	branch := ctx.Repo.BranchAt(path)
	typ, work, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix)
	if !ok {
		return "", fmt.Errorf("%s is on %q, which is not a worktree branch; nothing to migrate to",
			path, branch)
	}
	want := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
	if want == path {
		fmt.Fprintf(w, "- %s is already at the canonical path\n", path)
		return path, nil
	}
	fmt.Fprintf(w, "Moving %s -> %s\n", path, want)
	if err := ctx.Repo.MoveWorktree(path, want); err != nil {
		return "", err
	}
	return want, nil
}
```

`internal/commands/doctor.go`:

```go
package commands

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Doctor reports configuration and worktree health, returning the number of
// problems found so the caller can set an exit code. It replaces
// setup_precheck.sh and additionally finds worktrees that are misplaced,
// unowned, or nested inside the main checkout.
func Doctor(ctx *Context, w io.Writer) (int, error) {
	problems := 0
	report := func(format string, args ...any) {
		problems++
		fmt.Fprintf(w, "  ! "+format+"\n", args...)
	}

	fmt.Fprintf(w, "Repository: %s (%s)\n", ctx.Repo.Name, ctx.Repo.MainRoot)

	fmt.Fprintln(w, "Required tools:")
	if len(ctx.Config.RequiredBins) == 0 {
		fmt.Fprintln(w, "  - none declared")
	}
	for _, bin := range ctx.Config.RequiredBins {
		if _, err := exec.LookPath(bin); err != nil {
			report("%s is declared in REQUIRED_BINS but not on PATH", bin)
		} else {
			fmt.Fprintf(w, "  ✓ %s\n", bin)
		}
	}

	if !ctx.Repo.BranchExists(ctx.Config.MainBranch) {
		report("MAIN_BRANCH %q does not exist locally", ctx.Config.MainBranch)
	}

	fmt.Fprintln(w, "Worktrees:")
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return problems, err
	}
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		// A worktree inside the main checkout pollutes the parent repo and
		// breaks tooling that walks it.
		if strings.HasPrefix(wt.Path, ctx.Repo.MainRoot+"/") {
			report("%s is inside the main checkout", wt.Path)
			continue
		}
		typ, work, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix)
		if !ok {
			fmt.Fprintf(w, "  - %s is on %q, not managed by wt\n", wt.Path, wt.Branch)
			continue
		}
		want := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
		if wt.Path != want {
			report("%s is not at its canonical path (%s); run: wt migrate %s/%s",
				wt.Path, want, typ, work)
			continue
		}
		fmt.Fprintf(w, "  ✓ %s\n", wt.Path)
	}

	if problems == 0 {
		fmt.Fprintln(w, "No problems found.")
	} else {
		fmt.Fprintf(w, "%d problem(s) found.\n", problems)
	}
	return problems, nil
}
```

`cmd/wt/adopt.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newAdoptCmd() *cobra.Command {
	var relocate, skipBuild bool
	cmd := &cobra.Command{
		Use:   "adopt <path>",
		Short: "Provision a worktree that another tool created",
		Long: "Provision a worktree made outside wt — plain `git worktree add`, an\n" +
			"agent's own checkout, or one created before this repo was migrated.\n" +
			"With --relocate it is also moved to the canonical path.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.Adopt(ctx, args[0], relocate,
				commands.SetupOptions{SkipBuild: skipBuild}, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&relocate, "relocate", false, "also move it to the canonical path")
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip build initialisation")
	return cmd
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "migrate <type>/<work>",
		Short:             "Move a worktree to the canonical path",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.Migrate(ctx, args[0], cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}
```

`cmd/wt/doctor.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, required tools and worktree health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			problems, err := commands.Doctor(ctx, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			return nil
		},
	}
}
```

Register `newAdoptCmd()`, `newMigrateCmd()` and `newDoctorCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(adopt): add wt adopt, migrate and doctor"
```

---

### Task 8: Claude Code hook handlers

**Files:**
- Create: `internal/commands/hook.go`, `cmd/wt/hook.go`
- Test: `internal/commands/hook_test.go`

**Interfaces:**
- Produces:

```go
func HookCreate(ctx *Context, in io.Reader, out, logw io.Writer) error
func HookRemove(ctx *Context, in io.Reader, out, logw io.Writer) error
```

The contract, taken from the existing `bin/hooks/worktree-create.sh`: JSON on
stdin with `{ name, cwd, session_id, hook_event_name }`; the absolute worktree
path is printed to **stdout**; all progress goes to **stderr**.

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestHookCreatePrintsPathOnStdoutOnly(t *testing.T) {
	ctx, _ := repoReady(t)
	var out, logw bytes.Buffer
	in := strings.NewReader(`{"name":"hooked","cwd":"/x","hook_event_name":"WorktreeCreate"}`)
	if err := HookCreate(ctx, in, &out, &logw); err != nil {
		t.Fatalf("HookCreate: %v", err)
	}
	path := strings.TrimSpace(out.String())
	if !strings.HasSuffix(path, "/demo_wt/feat_wt/hooked") {
		t.Errorf("stdout must be the path alone, got %q", path)
	}
	if strings.Count(path, "\n") != 0 {
		t.Errorf("stdout must be a single line, got %q", path)
	}
}

func TestHookCreateRejectsMissingName(t *testing.T) {
	ctx, _ := repoReady(t)
	var out, logw bytes.Buffer
	if err := HookCreate(ctx, strings.NewReader(`{"cwd":"/x"}`), &out, &logw); err == nil {
		t.Error("want an error when name is absent")
	}
}

func TestHookRemoveIsMergeChecked(t *testing.T) {
	ctx, _ := repoReady(t)
	var out, logw bytes.Buffer
	if err := HookCreate(ctx, strings.NewReader(`{"name":"gone"}`), &out, &logw); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(out.String())
	out.Reset()
	if err := HookRemove(ctx, strings.NewReader(`{"path":"`+path+`"}`), &out, &logw); err != nil {
		t.Fatalf("HookRemove: %v", err)
	}
	if ctx.Repo.BranchExists("feat_wt/gone") {
		t.Error("a merged branch should have been deleted by the hook")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run TestHook -v`
Expected: FAIL — `undefined: HookCreate`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/hook.go`:

```go
package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// hookInput is the JSON Claude Code sends on stdin. Unknown fields are ignored
// so a new field in the harness does not break the hook.
type hookInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
	CWD  string `json:"cwd"`
}

// HookCreate handles Claude Code's WorktreeCreate event. The contract is that
// the absolute worktree path is the only thing on stdout; everything else goes
// to stderr, or the harness cannot read the result.
func HookCreate(ctx *Context, in io.Reader, out, logw io.Writer) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return fmt.Errorf("reading hook input: %w", err)
	}
	if input.Name == "" {
		return errors.New("'name' is required in the hook input")
	}
	path, err := New(ctx, input.Name, NewOptions{}, logw)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

// HookRemove handles Claude Code's WorktreeRemove event, going through the same
// merge-checked removal as `wt remove` so the harness cannot lose unmerged work.
func HookRemove(ctx *Context, in io.Reader, out, logw io.Writer) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return fmt.Errorf("reading hook input: %w", err)
	}
	target := input.Path
	if target == "" {
		if input.Name == "" {
			return errors.New("'path' or 'name' is required in the hook input")
		}
		p, err := Path(ctx, input.Name)
		if err != nil {
			return err
		}
		target = p
	}
	return RemoveAt(ctx, target, logw)
}
```

`cmd/wt/hook.go`:

```go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newHookCmd() *cobra.Command {
	hook := &cobra.Command{
		Use:    "hook",
		Short:  "Event handlers for editors and agents",
		Hidden: true,
	}
	hook.AddCommand(
		&cobra.Command{
			Use:   "claude-create",
			Short: "Claude Code WorktreeCreate handler (JSON on stdin)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := openContext()
				if err != nil {
					return err
				}
				return commands.HookCreate(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), os.Stderr)
			},
		},
		&cobra.Command{
			Use:   "claude-remove",
			Short: "Claude Code WorktreeRemove handler (JSON on stdin)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := openContext()
				if err != nil {
					return err
				}
				return commands.HookRemove(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), os.Stderr)
			},
		},
	)
	return hook
}
```

Register `newHookCmd()` in `newRootCmd`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && bats test/compat.bats`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands cmd
git commit -m "feat(hook): add claude code worktree hook handlers"
```

---

## What this plan delivers

A complete `wt` CLI: cobra command layer with dynamic work-name completion, and
`config`, `path`, `branch`, `list`, `status`, `setup`, `new`, `remove`, `adopt`,
`migrate`, `doctor` and the Claude Code hooks — with each of `remove.sh`'s six
safety behaviours pinned by its own test. No repository other than `wt` is
modified.

## Follow-on plan

**Plan 3 — shell layer, CI and repo migration:** `shell/wt.sh` absorbing the
`wt_*` functions from dotfiles plus `wt_rm_me`; golangci-lint and a GitHub
Actions matrix; release binaries; then the seven-repo migration — shims in,
`provision.sh` extracted where `AWS_SETUP_ENABLED` was true, hook registrations
repointed, script bodies deleted, `tc-ops:sync-scripts` worktree duty retired.
