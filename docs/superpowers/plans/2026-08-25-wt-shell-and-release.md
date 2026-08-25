# wt Shell Layer, Lint and Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move fuzzy cross-repo worktree resolution into the binary as `wt find`, build the shell layer on top of it, and make the tool lintable, CI-tested, releasable and installable.

**Architecture:** `internal/find` scores candidates with a tiered, testable matcher; `wt find` exposes it; `shell/wt.sh` becomes thin wrappers that only do what a binary cannot — change the caller's directory and drive `fzf`.

**Tech Stack:** Go 1.26, cobra, `bats-core`, golangci-lint, GitHub Actions, GoReleaser.

**Spec:** `docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md`
**Predecessors:** `2026-08-25-wt-foundation.md`, `2026-08-25-wt-commands.md` (both complete)

## Global Constraints

- All prior constraints still apply.
- **`wt find` is repo-first but not repo-only.** The current repository is searched first and wins ties, but only a *strong* hit — exact work name, exact branch, or work-name prefix — ends the search. Anything vaguer also scans `$WT_ROOTS` and competes on merit, so an incidental substring nearby cannot shadow a real match elsewhere.
- Matching tiers, best first: 1 exact work, 2 exact branch, 3 work prefix, 4 work substring, 5 branch-or-path substring, 6 subsequence (pattern ≥3 chars). Case-insensitive, substring not regex.
- `WT_ROOTS` is colon-separated so paths containing spaces survive. Default: `$HOME/programmering/telcred:$HOME/programmering/private:$HOME/Git`.
- **`wt find` prints exactly one path on stdout**, or exits non-zero with candidates on stderr. `--candidates` prints every best-tier match, one per line, for the shell to feed to `fzf`.
- Shell functions must never use `local path` — in zsh `path` is tied to `PATH`.
- Every task ends with a Conventional Commits commit, no AI attribution.

---

### Task 1: Tiered fuzzy matching

**Files:**
- Create: `internal/find/find.go`
- Test: `internal/find/find_test.go`

**Interfaces:**
- Produces:

```go
type Candidate struct {
	Work, Branch, Repo, Path string
	Local bool
}
type Scored struct {
	Candidate
	Tier int
}
func Score(c Candidate, pattern, repoPattern string) (Scored, bool)
func Best(scored []Scored) []Scored
func StrongestTier(scored []Scored) int
```

`Best` returns every candidate sharing the winning `(tier, local)` pair — local
preferred at equal tier — which is exactly the set a picker should offer.

- [ ] **Step 1: Write the failing test**

```go
package find

import "testing"

func cand(work, branch, repo, path string, local bool) Candidate {
	return Candidate{Work: work, Branch: branch, Repo: repo, Path: path, Local: local}
}

func TestScoreTiers(t *testing.T) {
	c := cand("webkey_infra", "feat_wt/webkey_infra", "infrastructure", "/x/infrastructure_wt/feat_wt/webkey_infra", true)
	for _, tc := range []struct {
		pattern string
		want    int
	}{
		{"webkey_infra", 1},          // exact work
		{"feat_wt/webkey_infra", 2},  // exact branch
		{"webkey", 3},                // work prefix
		{"key_inf", 4},               // work substring
		{"infrastructure_wt", 5},     // path substring
		{"wki", 6},                   // subsequence
	} {
		got, ok := Score(c, tc.pattern, "")
		if !ok || got.Tier != tc.want {
			t.Errorf("Score(%q) tier = %d (ok=%v), want %d", tc.pattern, got.Tier, ok, tc.want)
		}
	}
	if _, ok := Score(c, "zzzz", ""); ok {
		t.Error("a non-matching pattern must not score")
	}
}

// A two-character pattern must not fall through to subsequence matching, or
// almost everything matches almost everything.
func TestShortPatternsDoNotSubsequenceMatch(t *testing.T) {
	c := cand("webkey_infra", "feat_wt/webkey_infra", "r", "/p", true)
	if _, ok := Score(c, "wi", ""); ok {
		t.Error("two-character subsequence should not match")
	}
}

func TestScoreIsCaseInsensitive(t *testing.T) {
	c := cand("WebKey", "feat_wt/WebKey", "r", "/p", true)
	if got, ok := Score(c, "webkey", ""); !ok || got.Tier != 1 {
		t.Errorf("got tier %d ok %v, want exact", got.Tier, ok)
	}
}

func TestRepoPatternFiltersByRepo(t *testing.T) {
	a := cand("arch", "feat_wt/arch", "accessmanager", "/a", false)
	p := cand("arch", "feat_wt/arch", "personal-v", "/p", false)
	if _, ok := Score(a, "arch", "personal"); ok {
		t.Error("accessmanager should be filtered out by repo pattern")
	}
	if _, ok := Score(p, "arch", "personal"); !ok {
		t.Error("personal-v should survive the repo pattern")
	}
}

// The pattern is data, not a regex: dots and brackets are literal.
func TestPatternIsNotARegex(t *testing.T) {
	c := cand("a.b", "feat_wt/a.b", "r", "/p", true)
	if got, ok := Score(c, "a.b", ""); !ok || got.Tier != 1 {
		t.Errorf("literal dot: tier %d ok %v", got.Tier, ok)
	}
	if _, ok := Score(cand("axb", "feat_wt/axb", "r", "/p", true), "a.b", ""); ok {
		t.Error("'.' must not act as a regex wildcard")
	}
}

func TestBestPrefersLowerTierThenLocal(t *testing.T) {
	scored := []Scored{
		{cand("a", "", "r1", "/a", false), 1},
		{cand("b", "", "r2", "/b", true), 1},
		{cand("c", "", "r3", "/c", true), 4},
	}
	best := Best(scored)
	if len(best) != 1 || best[0].Path != "/b" {
		t.Errorf("want the local tier-1 candidate, got %+v", best)
	}
}

func TestBestReturnsEveryTiedCandidate(t *testing.T) {
	scored := []Scored{
		{cand("arch", "", "accessmanager", "/a", false), 1},
		{cand("arch", "", "personal-v", "/p", false), 1},
	}
	if best := Best(scored); len(best) != 2 {
		t.Errorf("want both tied candidates, got %d", len(best))
	}
}

func TestBestOnEmptyIsEmpty(t *testing.T) {
	if best := Best(nil); len(best) != 0 {
		t.Errorf("want empty, got %v", best)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/find/ -v`
Expected: FAIL — `undefined: Score`.

- [ ] **Step 3: Write minimal implementation**

`internal/find/find.go`:

```go
// Package find matches a worktree by a fuzzy pattern.
//
// Matching is tiered rather than one blended score, so the result is
// predictable: a user can reason about why a pattern chose what it chose.
// Comparison is case-insensitive and uses substring containment, never a
// regular expression, so a pattern full of dots and brackets is still just
// text.
package find

import "strings"

// Candidate is one worktree that a pattern may match.
type Candidate struct {
	Work   string
	Branch string
	Repo   string
	Path   string
	Local  bool // in the repository the user is standing in
}

// Scored is a Candidate that matched, with the tier it matched at.
type Scored struct {
	Candidate
	Tier int
}

// Tiers, best first.
const (
	TierExactWork = iota + 1
	TierExactBranch
	TierWorkPrefix
	TierWorkSubstring
	TierBranchOrPath
	TierSubsequence
)

// minSubsequence is the shortest pattern allowed to reach the subsequence tier.
// Below it almost every pattern matches almost every candidate.
const minSubsequence = 3

// Score rates one candidate against a pattern, optionally restricted to repos
// whose name contains repoPattern.
func Score(c Candidate, pattern, repoPattern string) (Scored, bool) {
	p := strings.ToLower(pattern)
	if p == "" {
		return Scored{}, false
	}
	if repoPattern != "" && !strings.Contains(strings.ToLower(c.Repo), strings.ToLower(repoPattern)) {
		return Scored{}, false
	}

	work := strings.ToLower(c.Work)
	branch := strings.ToLower(c.Branch)
	path := strings.ToLower(c.Path)

	var tier int
	switch {
	case work == p:
		tier = TierExactWork
	case branch == p:
		tier = TierExactBranch
	case strings.HasPrefix(work, p):
		tier = TierWorkPrefix
	case strings.Contains(work, p):
		tier = TierWorkSubstring
	case strings.Contains(branch, p), strings.Contains(path, p):
		tier = TierBranchOrPath
	case len(p) >= minSubsequence && isSubsequence(work, p):
		tier = TierSubsequence
	default:
		return Scored{}, false
	}
	return Scored{Candidate: c, Tier: tier}, true
}

// Best returns every candidate sharing the winning (tier, locality) pair. The
// current repository wins ties, which is the whole of "repo-first": it decides
// nothing when another repo matched better.
func Best(scored []Scored) []Scored {
	if len(scored) == 0 {
		return nil
	}
	bestTier, bestLocal := 0, false
	for _, s := range scored {
		better := bestTier == 0 ||
			s.Tier < bestTier ||
			(s.Tier == bestTier && s.Local && !bestLocal)
		if better {
			bestTier, bestLocal = s.Tier, s.Local
		}
	}
	var out []Scored
	for _, s := range scored {
		if s.Tier == bestTier && s.Local == bestLocal {
			out = append(out, s)
		}
	}
	return out
}

// StrongestTier reports the best tier present, or 0 for an empty set.
func StrongestTier(scored []Scored) int {
	best := 0
	for _, s := range scored {
		if best == 0 || s.Tier < best {
			best = s.Tier
		}
	}
	return best
}

func isSubsequence(s, pattern string) bool {
	j := 0
	for i := 0; i < len(s) && j < len(pattern); i++ {
		if s[i] == pattern[j] {
			j++
		}
	}
	return j == len(pattern)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/find/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/find
git commit -m "feat(find): add tiered fuzzy worktree matching"
```

---

### Task 2: `wt find` — collecting candidates across repos

**Files:**
- Create: `internal/commands/find.go`, `cmd/wt/find.go`
- Test: `internal/commands/find_test.go`

**Interfaces:**
- Produces:

```go
func Roots() []string                                   // $WT_ROOTS, colon-separated, with the default
func Find(ctx *Context, pattern string) ([]find.Scored, error)
```

`ctx` may be nil when the caller is not inside a repository; the search then
goes straight to the roots.

- [ ] **Step 1: Write the failing test**

```go
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootsDefaultsAndSplitsOnColon(t *testing.T) {
	t.Setenv("WT_ROOTS", "/a:/b with space:/c")
	got := Roots()
	if len(got) != 3 || got[1] != "/b with space" {
		t.Errorf("got %q", got)
	}
	t.Setenv("WT_ROOTS", "")
	if len(Roots()) == 0 {
		t.Error("want a default when WT_ROOTS is unset")
	}
}

// A strong local hit ends the search; a weak one must not, or an incidental
// substring nearby shadows a real match in another repo.
func TestFindStrongLocalHitWins(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	var buf strings.Builder
	_ = buf
	if _, err := New(ctx, "feat/opensearch_ism", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	got, err := Find(ctx, "opensearch_ism")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0].Tier != 1 {
		t.Fatalf("want one exact local hit, got %+v", got)
	}
}

func TestFindWeakLocalHitStillScansRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WT_ROOTS", root)

	// Local repo: "opensearch" contains "arch" only as a substring.
	local := committedRepoIn(t, root, "local", minimalConf)
	lctx, _ := Open(local)
	if _, err := New(lctx, "feat/opensearch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	// Another repo under the roots holds the real thing.
	other := committedRepoIn(t, root, "other", minimalConf)
	octx, _ := Open(other)
	if _, err := New(octx, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}

	got, err := Find(lctx, "arch")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one winner, got %d: %+v", len(got), got)
	}
	if got[0].Work != "arch" || got[0].Repo != "other" {
		t.Errorf("the exact match in another repo must win, got %+v", got[0])
	}
}

func TestFindReportsEveryTiedCandidate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WT_ROOTS", root)
	for _, name := range []string{"one", "two"} {
		r := committedRepoIn(t, root, name, minimalConf)
		c, _ := Open(r)
		if _, err := New(c, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Find(nil, "arch")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want both repos' arch, got %d: %+v", len(got), got)
	}
}

func TestFindNoMatch(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	got, err := Find(nil, "definitely-nothing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want no matches, got %+v", got)
	}
}

// committedRepoIn builds a repo with one commit inside a chosen parent, so a
// test can control which root the scan will find it under.
func committedRepoIn(t *testing.T, parent, name, conf string) string {
	t.Helper()
	main := filepath.Join(parent, name)
	gitIn(t, parent, "init", "-q", "-b", "main", name)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	dir := filepath.Join(main, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-qm", "init")
	return main
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands/ -run TestFind -v`
Expected: FAIL — `undefined: Roots`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/find.go`:

```go
package commands

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/find"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// defaultRoots is where repositories live on this machine when WT_ROOTS says
// nothing. Colon-separated, so a path containing spaces survives.
const defaultRoots = "programmering/telcred:programmering/private:Git"

// Roots returns the directories to scan for other repositories.
func Roots() []string {
	raw := os.Getenv("WT_ROOTS")
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		var out []string
		for _, rel := range strings.Split(defaultRoots, ":") {
			out = append(out, filepath.Join(home, rel))
		}
		return out
	}
	var out []string
	for _, p := range strings.Split(raw, ":") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Find resolves a fuzzy pattern to the best-matching worktrees.
//
// The current repository is searched first and wins ties, but only a strong hit
// — exact work name, exact branch, or a work-name prefix — ends the search
// there. A vaguer local hit also scans the roots and competes on merit, because
// otherwise an incidental substring beats a real match next door: standing in
// infrastructure, "arch" matches opensearch_ism and would quietly shadow the
// feat_wt/arch worktrees that actually exist elsewhere.
//
// ctx may be nil when the caller is not inside a repository.
func Find(ctx *Context, pattern string) ([]find.Scored, error) {
	var scored []find.Scored

	if ctx != nil {
		local, err := candidates(ctx.Repo, ctx.Config.TypeSuffix, true)
		if err != nil {
			return nil, err
		}
		scored = scoreAll(local, pattern)
		if t := find.StrongestTier(scored); t > 0 && t <= find.TierWorkPrefix {
			return find.Best(scored), nil
		}
	}

	seen := map[string]bool{}
	for _, s := range scored {
		seen[s.Path] = true
	}
	for _, r := range scanRoots(Roots()) {
		if ctx != nil && r.MainRoot == ctx.Repo.MainRoot {
			continue
		}
		cands, err := candidates(r, suffixFor(r), false)
		if err != nil {
			continue
		}
		for _, s := range scoreAll(cands, pattern) {
			if !seen[s.Path] {
				seen[s.Path] = true
				scored = append(scored, s)
			}
		}
	}
	return find.Best(scored), nil
}

// suffixFor reads another repository's type suffix, falling back to the default
// when it has no readable configuration — a repo wt does not manage still has
// worktrees worth finding.
func suffixFor(r *repo.Repo) string {
	c, err := loadFor(r)
	if err != nil {
		return "_wt"
	}
	return c.TypeSuffix
}

func candidates(r *repo.Repo, suffix string, local bool) ([]find.Candidate, error) {
	worktrees, err := r.Worktrees()
	if err != nil {
		return nil, err
	}
	var out []find.Candidate
	for _, wt := range worktrees {
		work := r.Name
		if !wt.IsMain {
			work = naming.StripPrefix(wt.Branch, suffix)
			if work == "" {
				work = filepath.Base(wt.Path)
			}
		}
		out = append(out, find.Candidate{
			Work:   work,
			Branch: wt.Branch,
			Repo:   r.Name,
			Path:   wt.Path,
			Local:  local,
		})
	}
	return out, nil
}

func scoreAll(cands []find.Candidate, pattern string) []find.Scored {
	// A pattern containing "/" is tried whole first, so a full branch name
	// works; only if nothing matches is it split into repo/work.
	var out []find.Scored
	for _, c := range cands {
		if s, ok := find.Score(c, pattern, ""); ok {
			out = append(out, s)
		}
	}
	if len(out) > 0 || !strings.Contains(pattern, "/") {
		return out
	}
	repoPat, workPat := pattern[:strings.LastIndex(pattern, "/")], pattern[strings.LastIndex(pattern, "/")+1:]
	for _, c := range cands {
		if s, ok := find.Score(c, workPat, repoPat); ok {
			out = append(out, s)
		}
	}
	return out
}

// scanRoots finds repositories one level under each root. A linked worktree
// that happens to sit at that depth reports its whole repository, so results
// are deduplicated by main-worktree path.
func scanRoots(roots []string) []*repo.Repo {
	seen := map[string]bool{}
	var out []*repo.Repo
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				continue
			}
			r, err := repo.Discover(dir)
			if err != nil || seen[r.MainRoot] {
				continue
			}
			seen[r.MainRoot] = true
			out = append(out, r)
		}
	}
	return out
}
```

Add to `internal/commands/context.go`:

```go
// loadFor loads another repository's configuration without building a Context.
func loadFor(r *repo.Repo) (*config.Config, error) {
	return config.Load(r.MainRoot, r.DetectMainBranch())
}
```

`cmd/wt/find.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newFindCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "find <pattern>",
		Short: "Resolve a worktree by fuzzy name, across repositories",
		Long: "Resolve a fuzzy pattern to a worktree path. The current repository is\n" +
			"searched first and wins ties, but a weak local match still lets other\n" +
			"repositories under $WT_ROOTS compete.\n\n" +
			"Prints one path. When several candidates tie, exits non-zero and lists\n" +
			"them on stderr, so nothing runs in a worktree you did not mean.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Not being in a repository is fine: the search falls back to roots.
			ctx, _ := openContext()
			matches, err := commands.Find(ctx, args[0])
			if err != nil {
				return err
			}
			switch {
			case len(matches) == 0:
				return fmt.Errorf("no worktree matches %q", args[0])
			case all:
				for _, m := range matches {
					fmt.Fprintln(cmd.OutOrStdout(), m.Path)
				}
				return nil
			case len(matches) == 1:
				fmt.Fprintln(cmd.OutOrStdout(), matches[0].Path)
				return nil
			}
			fmt.Fprintf(os.Stderr, "wt: %q is ambiguous:\n", args[0])
			for _, m := range matches {
				fmt.Fprintf(os.Stderr, "  %-24s %-28s %s\n", m.Work, m.Repo, m.Path)
			}
			return fmt.Errorf("%d candidates", len(matches))
		},
	}
	cmd.Flags().BoolVar(&all, "candidates", false, "print every tied candidate, one per line")
	return cmd
}
```

Register `newFindCmd()` in `newRootCmd` and add `"github.com/anders-lindstrom/wt/internal/config"` plus `"github.com/anders-lindstrom/wt/internal/repo"` to `context.go`'s imports if absent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal cmd
git commit -m "feat(find): resolve worktrees by fuzzy name across repositories"
```

---

### Task 3: The shell layer

**Files:**
- Create: `shell/wt.sh`, `test/shell.bats`
- Test: `test/shell.bats`

**Interfaces:**
- Produces the shell functions a binary cannot provide: `wt_cd`, `wt_exec`,
  `wt_dir`, `wt_ls`, `wt_rm_me`, and a `_wt_pick` helper that drives `fzf`.

Every one is a thin wrapper over `wt find`. The fuzzy logic itself lives in the
binary and is tested there; this file exists only for the two things a separate
process cannot do — change the caller's directory, and open an interactive
picker.

- [ ] **Step 1: Write the failing test**

`test/shell.bats`:

```bash
#!/usr/bin/env bats

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    export WT_ROOTS="$BATS_TEST_TMPDIR/roots"
    mkdir -p "$WT_ROOTS"
    REPO="$WT_ROOTS/demo"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$REPO/bin/worktree/worktree.conf"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name T
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm init
    cd "$REPO"
    wt new fix/login-crash >/dev/null
    source "$BATS_TEST_DIRNAME/../shell/wt.sh"
}

@test "wt_dir prints the path and nothing else" {
    run wt_dir login-crash
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 1 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt_cd changes the calling shell's directory" {
    wt_cd login-crash
    [[ "$PWD" == */demo_wt/fix_wt/login-crash ]]
}

@test "wt_exec runs in the worktree and leaves the shell put" {
    before="$PWD"
    run wt_exec login-crash pwd
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
    [ "$PWD" = "$before" ]
}

@test "wt_exec propagates the command's exit code" {
    run wt_exec login-crash sh -c "exit 7"
    [ "$status" -eq 7 ]
}

@test "wt_exec preserves argument quoting" {
    run wt_exec login-crash sh -c 'printf "%s\n" "one two"'
    [ "$output" = "one two" ]
}

@test "wt_dir fails on no match" {
    run wt_dir zzz-nothing
    [ "$status" -ne 0 ]
}

@test "wt_ls lists worktrees" {
    run wt_ls
    [ "$status" -eq 0 ]
    [[ "$output" == *"fix_wt/login-crash"* ]]
}

@test "wt_rm_me refuses to run in the main checkout" {
    cd "$REPO"
    run wt_rm_me
    [ "$status" -ne 0 ]
    [[ "$output" == *"main checkout"* ]]
}
```

- [ ] **Step 2: Run the suite to verify it fails**

```bash
cd ~/programmering/private/wt && go build -o bin/wt ./cmd/wt && bats test/shell.bats
```
Expected: FAIL — `wt.sh` does not exist.

- [ ] **Step 3: Write minimal implementation**

`shell/wt.sh`:

```bash
# Shell layer for wt.
#
# These exist only for the two things a separate process cannot do: change the
# calling shell's directory, and open an interactive picker. All matching logic
# lives in the binary (`wt find`) where it is tested — this file must never grow
# a second implementation of it.
#
#   wt_dir  <pattern>              print a worktree's path
#   wt_cd   <pattern>              cd there, in this shell
#   wt_exec <pattern> <cmd> [...]  run a command there, in a subshell
#   wt_ls   [pattern]              list worktrees, or show what a pattern matches
#   wt_rm_me                       remove the worktree you are standing in
#
# Override the search roots with a colon-separated list:
#   export WT_ROOTS="$HOME/work:$HOME/oss"

_wt_require() {
    command -v wt >/dev/null 2>&1 && return 0
    echo "wt is not on PATH — see https://github.com/anders-lindstrom/wt" >&2
    return 1
}

# _wt_resolve prints one path. When several candidates tie it opens fzf if this
# is an interactive terminal, and otherwise fails with the list — so a script or
# an agent never runs in a worktree it did not mean.
_wt_resolve() {
    _wt_require || return 1
    [ -n "$1" ] || { echo "wt: no pattern given" >&2; return 2; }

    local resolved
    if resolved=$(wt find "$1" 2>/dev/null); then
        printf '%s\n' "$resolved"
        return 0
    fi

    local candidates
    candidates=$(wt find --candidates "$1" 2>/dev/null)
    if [ -z "$candidates" ]; then
        wt find "$1" >/dev/null   # re-run for its error message on stderr
        return 1
    fi

    if [ -t 0 ] && [ -t 2 ] && command -v fzf >/dev/null 2>&1; then
        printf '%s\n' "$candidates" |
            fzf --preview-window=hidden --height=40% --reverse --prompt='worktree> '
        return $?
    fi
    echo "wt: '$1' is ambiguous:" >&2
    printf '%s\n' "$candidates" | sed 's/^/  /' >&2
    return 1
}

# NB: the local is wt_path, not path — in zsh `path` is tied to PATH, so
# `local path` inside a function empties PATH for everything it calls.
wt_dir() {
    local wt_path
    wt_path=$(_wt_resolve "$1") || return $?
    printf '%s\n' "$wt_path"
}

wt_cd() {
    local wt_path
    wt_path=$(_wt_resolve "$1") || return $?
    cd "$wt_path"
}

# Runs in a subshell, so your own shell stays where it is and the command's exit
# code is what you get back. The command is executed directly rather than
# through eval, so quoting survives; the tradeoff is that a shell *alias* will
# not expand (a shell function will).
wt_exec() {
    if [ "$#" -lt 2 ]; then
        echo "usage: wt_exec <pattern> <command> [args...]" >&2
        return 2
    fi
    local pattern="$1"; shift
    local wt_path
    wt_path=$(_wt_resolve "$pattern") || return $?
    ( cd "$wt_path" && "$@" )
}

wt_ls() {
    _wt_require || return 1
    if [ -n "$1" ]; then
        wt find --candidates "$1"
        return $?
    fi
    wt list
}

# Removing the worktree you are standing in has to cd out of it first, which is
# why this is a shell function and not a flag alone.
wt_rm_me() {
    _wt_require || return 1
    local main
    main=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || {
        echo "wt: not in a git repository" >&2
        return 1
    }
    main=${main%/.git}
    if [ "$(git rev-parse --show-toplevel 2>/dev/null)" = "$main" ]; then
        echo "wt: refusing to remove the main checkout" >&2
        return 1
    fi
    local here
    here=$(git rev-parse --show-toplevel) || return 1
    cd "$main" || return 1
    wt remove --me-at "$here"
}
```

`wt_rm_me` needs a way to name the worktree after cd-ing out, so add a
`--me-at` flag to the remove command in `cmd/wt/remove.go`:

```go
	var meAt string
	// ... inside RunE, before the `me` branch:
			if meAt != "" {
				return commands.RemoveAt(ctx, meAt, cmd.OutOrStdout())
			}
	// ... after the --me flag registration:
	cmd.Flags().StringVar(&meAt, "me-at", "", "remove the worktree at this path (used by wt_rm_me)")
	_ = cmd.Flags().MarkHidden("me-at")
```

- [ ] **Step 4: Run the suite to verify it passes**

```bash
go build -o bin/wt ./cmd/wt && bats test/shell.bats test/compat.bats
```
Expected: PASS, 8 shell tests and 5 compat tests.

- [ ] **Step 5: Commit**

```bash
git add shell test cmd
git commit -m "feat(shell): add the shell layer over wt find"
```

---

### Task 4: Lint and continuous integration

**Files:**
- Create: `.golangci.yml`, `.github/workflows/ci.yml`, `Makefile`

- [ ] **Step 1: Add the lint configuration**

`.golangci.yml`:

```yaml
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - misspell
    - revive
    - bodyclose
    - gosec
  settings:
    gosec:
      excludes:
        # wt exists to run configured git and provisioning commands; G204 flags
        # every one of them and would drown the real findings.
        - G204
formatters:
  enable:
    - gofmt
    - goimports
```

`Makefile`:

```makefile
BIN := bin/wt

.PHONY: build test lint bats check clean

build:
	go build -o $(BIN) ./cmd/wt

test:
	go test ./...

bats: build
	bats test/

lint:
	golangci-lint run

check: lint test bats

clean:
	rm -rf bin
```

- [ ] **Step 2: Run the linter and fix what it reports**

```bash
cd ~/programmering/private/wt
command -v golangci-lint >/dev/null || brew install golangci-lint
golangci-lint run
```

Fix every finding. Expected classes: unchecked `fmt.Fprintf` returns
(`errcheck`), and comment or naming nits from `revive`.

- [ ] **Step 3: Add CI**

`.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      # The test suite shells out to git and commits, so it needs an identity.
      - name: Configure git
        run: |
          git config --global user.email ci@example.com
          git config --global user.name CI
      - run: go build ./...
      - run: go test ./... -race

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  shell:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - name: Configure git
        run: |
          git config --global user.email ci@example.com
          git config --global user.name CI
      - name: Install bats
        run: sudo apt-get update && sudo apt-get install -y bats
      - run: go build -o bin/wt ./cmd/wt
      - run: bats test/
```

- [ ] **Step 4: Verify the suite is green under -race**

Run: `go test ./... -race && golangci-lint run && bats test/`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add .golangci.yml .github Makefile
git commit -m "build: add golangci-lint, make targets and ci"
```

---

### Task 5: Install and release

**Files:**
- Create: `install.sh`, `.goreleaser.yaml`, `.github/workflows/release.yml`
- Modify: `README.md`

- [ ] **Step 1: Write the installer**

`install.sh`:

```bash
#!/usr/bin/env bash
# Install wt: the binary, the shell layer and the compat layer.
#
#   ./install.sh [prefix]      default prefix: ~/.local
#
# Add to your shell rc:
#   source <prefix>/share/wt/wt.sh
set -euo pipefail

PREFIX="${1:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
SHARE_DIR="$PREFIX/share/wt"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

command -v go >/dev/null || { echo "go is required to build wt" >&2; exit 1; }

mkdir -p "$BIN_DIR" "$SHARE_DIR"

echo "Building wt..."
( cd "$SRC_DIR" && go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o "$BIN_DIR/wt" ./cmd/wt )

install -m 0644 "$SRC_DIR/shell/wt.sh" "$SHARE_DIR/wt.sh"
install -m 0644 "$SRC_DIR/compat/worktree_functions.sh" "$SHARE_DIR/worktree_functions.sh"

echo
echo "Installed:"
echo "  $BIN_DIR/wt"
echo "  $SHARE_DIR/wt.sh"
echo "  $SHARE_DIR/worktree_functions.sh"
echo
echo "Add to your shell rc:"
echo "  source $SHARE_DIR/wt.sh"
echo
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Note: $BIN_DIR is not on your PATH." ;;
esac
```

- [ ] **Step 2: Verify the installer end to end**

```bash
cd ~/programmering/private/wt
chmod +x install.sh
rm -rf /tmp/wt-install-test
./install.sh /tmp/wt-install-test
/tmp/wt-install-test/bin/wt version
test -f /tmp/wt-install-test/share/wt/wt.sh && echo "shell layer installed"
test -f /tmp/wt-install-test/share/wt/worktree_functions.sh && echo "compat layer installed"
```
Expected: a version string and both "installed" lines.

- [ ] **Step 3: Add the release configuration**

`.goreleaser.yaml`:

```yaml
version: 2

builds:
  - id: wt
    main: ./cmd/wt
    binary: wt
    env: [CGO_ENABLED=0]
    goos: [darwin, linux]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - formats: [tar.gz]
    # The shell and compat layers ship with the binary: without them the Herdr
    # skills and wt_cd do not work.
    files:
      - shell/wt.sh
      - compat/worktree_functions.sh
      - README.md

checksum:
  name_template: checksums.txt

changelog:
  use: github
  sort: asc
  filters:
    exclude: ["^docs:", "^test:", "^chore:"]
```

`.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 4: Validate the release config without publishing**

```bash
command -v goreleaser >/dev/null || brew install goreleaser
goreleaser check
goreleaser build --snapshot --clean --single-target
```
Expected: `check` passes and a snapshot binary is produced.

- [ ] **Step 5: Write the README**

Replace `README.md` with real usage documentation: what wt is, the install
command, the full command list with one line each, the `worktree.conf` schema
table, the `provision.sh` contract, the shell functions, and a short section on
what a repository keeps after migration.

- [ ] **Step 6: Commit**

```bash
git add install.sh .goreleaser.yaml .github README.md
git commit -m "build: add installer, goreleaser config and release workflow"
```

---

### Task 6: Retire the dotfiles copy

**Files:**
- Modify: `~/dotfiles/.zshrc`
- Delete: `~/dotfiles/.config/worktree/scripts.sh`

The dotfiles copy of the `wt_*` functions is now a second implementation of
logic that lives in the binary. Leaving it would recreate the exact disease this
whole project exists to cure.

- [ ] **Step 1: Install wt properly**

```bash
cd ~/programmering/private/wt && ./install.sh
```

- [ ] **Step 2: Point .zshrc at the installed shell layer**

In `~/dotfiles/.zshrc`, replace

```zsh
source ~/dotfiles/.config/worktree/scripts.sh
```

with

```zsh
# wt worktree tooling (github.com/anders-lindstrom/wt). The shell layer ships
# with the tool rather than living here, so there is only ever one copy.
[[ -f ~/.local/share/wt/wt.sh ]] && source ~/.local/share/wt/wt.sh
```

- [ ] **Step 3: Remove the superseded copy**

```bash
git -C ~/dotfiles rm -q .config/worktree/scripts.sh
rmdir ~/dotfiles/.config/worktree 2>/dev/null || true
```

- [ ] **Step 4: Verify in a fresh login shell**

```bash
env -u GOROOT zsh -i -c 'whence -w wt_cd wt_exec wt_dir wt_ls wt_rm_me; command -v wt'
```
Expected: all five reported as functions, and `wt` on PATH.

- [ ] **Step 5: Commit the dotfiles change**

```bash
cd ~/dotfiles
git add .zshrc .config
git commit -m "refactor(worktree): source the shell layer from the wt install

The wt_* functions moved into github.com/anders-lindstrom/wt, where the
matching logic they wrap is tested. Keeping a copy here would recreate
the duplication the tool exists to remove."
```

---

## What this plan delivers

Fuzzy resolution moved out of shell and into a tested binary; a thin shell layer
for the two things a process cannot do; lint, CI on macOS and Linux, a release
pipeline and an installer; and the dotfiles copy retired so only one
implementation exists.

## Follow-on plan

**Plan 4 — the seven-repo migration:** shims in, `provision.sh` extracted where
`AWS_SETUP_ENABLED` was true, `.claude/settings.json` hooks repointed at
`wt hook claude-*`, script bodies deleted, `.superset/config.json` left
untouched, and the `tc-ops:sync-scripts` worktree duty retired. Piloted in
infrastructure first.
