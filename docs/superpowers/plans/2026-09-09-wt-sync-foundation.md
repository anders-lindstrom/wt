# wt sync Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `wt sync` prints an exact, read-only triage of every worktree — class, behind/ahead, the first commit a rebase would stop at, whether the built-in strategies resolve that stop, and who is working there — computed entirely in the object store.

**Architecture:** A new package `internal/wtsync` holds the per-repo `.wt-sync.yaml` (read from trunk), the conflict strategies as pure functions over the three blobs of a conflict, the commit-by-commit replay simulation over `git merge-tree` + `git commit-tree`, and the classification. `internal/commands/sync.go` renders the table; `cmd/wt/sync.go` wires the verb. Nothing in this plan changes a repository: `run`, safety refs, `undo` and `doctor` are the next plan.

**Tech Stack:** Go 1.26, cobra (already a dependency), `gopkg.in/yaml.v3` (new), git ≥ 2.40 for `merge-tree --write-tree --merge-base`. Tests are `go test` with throwaway repositories built the way `internal/commands/*_test.go` builds them.

**Spec:** `docs/superpowers/specs/2026-09-05-wt-sync-design.md` — §1 (triage, the simulation, `divergent`, agent detection), §2 (strategies and their guarantees), §3 (`.wt-sync.yaml`). The bash reference implementation these strategies port is on `Telcred/server` branch `feat_wt/conflict-resolvers` (`bin/conflict/`, 44 bats cases) and `Telcred/accessmanager` same branch (27 cases); their fixture shapes reappear below as Go tests.

## Global Constraints

- **Read-only.** Nothing in this plan writes to a working tree, an index that a worktree uses, or a ref under `refs/heads`. Every `git status` runs with `--no-optional-locks`, because a plain status may rewrite the index. The only objects created are unreachable blobs, trees and commits from the simulation and the temporary index, which `git gc` reclaims.
- **The config is read from `origin/<trunk>`**, never from a working tree: it names executables (spec §3).
- **A strategy refuses rather than guesses**, and a refusal names what collided (spec §2).
- **Never say "ours" or "theirs".** Stage 2 of a rebase conflict is **trunk**, stage 3 is **the replayed commit**. Types and fields are `Base`, `Trunk`, `Branch`.
- **Paths are repository-root relative**; patterns are matched, never expanded against the disk.
- **No fleet fact in code or tests.** Branch names, counts and versions from the spec are illustrations.
- Commits follow Conventional Commits, imperative, lowercase, under 72 characters, no AI attribution trailer of any kind. Work on `main`, push after every commit (Anders, 2026-09-09).
- `gofmt`, `go vet ./...` and `golangci-lint run ./...` clean before every commit.

## Revision, 2026-09-09

A read-only Codex review (thread `01a08471-2104-7643-8370-cd0521f8648c`) found two compile errors, several tests that would pass while the behaviour was wrong, and one contradiction inside the spec. The rulings are in the SDD ledger; the tasks below carry them. The most consequential: `divergent` counts only an `openapi` refusal at the endpoint (not an owned-line refusal on ordinary config) and needs **both** sides to have moved the dependency graph; `git status` always runs with `--no-optional-locks`; scripts are materialised from trunk, not run from the checkout.

## File Structure

```
internal/wtsync/
  config.go          Config, Rule, Deferred; LoadFromTrunk; glob matching; RuleFor
  config_test.go
  conflict.go        Conflict{Path, Base, Trunk, Branch}; Refusal error; Result
  merge3.go          diff3 merge of three blobs via `git merge-file`; block walker
  merge3_test.go
  rules.go           ValueRule: MaxPlusPatch, KeepBranch, KeepTrunk; semver helpers
  rules_test.go
  owned_line.go      OwnedLine strategy
  owned_line_test.go
  openapi.go         OpenAPI strategy (ordered JSON, key-level three-way merge)
  openapi_test.go
  list_union.go      ListUnion strategy
  list_union_test.go
  take_trunk.go      TakeTrunk strategy (+ its test in strategy_test.go)
  strategy.go        Strategy interface; FromRule(Rule) constructor; strategy_test.go
  script.go          Script strategy: the executable contract, checked through a temporary index
  script_test.go
  replay.go          Replay(onto, branch): the object-store simulation; Endpoint(onto, branch)
  replay_test.go
  agents.go          `claude agents --json` parsing; AgentAt(path)
  agents_test.go
  triage.go          Class, Assessment, Assess(repo, cfg, worktree)
  triage_test.go
internal/commands/sync.go        Sync(ctx, w): the table
internal/commands/sync_test.go
cmd/wt/sync.go                   the cobra verb
```

Each strategy file owns exactly one shape and its tests. `conflict.go` and `merge3.go` are shared by the text-shape strategies; `openapi.go` does not use `merge3.go` at all.

---

### Task 1: `.wt-sync.yaml` read from trunk

**Files:**
- Create: `internal/wtsync/config.go`
- Create: `internal/wtsync/config_test.go`
- Modify: `go.mod` (add `gopkg.in/yaml.v3`)

**Interfaces:**
- Consumes: `internal/git.Run(dir, args...)`.
- Produces:
  - `type Rule struct { Paths []string; Strategy, Line, Rule, Delimiter, Run string }`
  - `type Deferred struct { Run string; Paths []string; Commit string }`
  - `type Config struct { Conflicts []Rule; Defer []Deferred; DependencyGraph []string }`
  - `var ErrNoConfig error`
  - `func LoadFromTrunk(mainRoot, trunk string) (*Config, error)` — reads `origin/<trunk>:.wt-sync.yaml`; `ErrNoConfig` when absent.
  - `func Parse(data []byte) (*Config, error)` — yaml + validation, used by LoadFromTrunk and tests.
  - `func (c *Config) RuleFor(path string) (Rule, bool)` — first rule whose `Paths` match.
  - `func MatchGlob(pattern, path string) bool` — `*` within a segment, `**` any number of segments.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/config_test.go`:

```go
package wtsync

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// repoWithOrigin makes a repository whose origin/main exists, so
// LoadFromTrunk has something to read. The "origin" is a second repository
// on disk; origin/main is fetched from it.
func repoWithOrigin(t *testing.T) (local, origin string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin = filepath.Join(parent, "origin")
	local = filepath.Join(parent, "local")
	gitIn(t, parent, "init", "-q", "-b", "main", "origin")
	gitIn(t, origin, "config", "commit.gpgsign", "false")
	gitIn(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, parent, "clone", "-q", origin, "local")
	gitIn(t, local, "config", "commit.gpgsign", "false")
	return local, origin
}

// commitOnOrigin writes a file on origin's main and fetches it locally.
func commitOnOrigin(t *testing.T, local, origin, path, content string) {
	t.Helper()
	full := filepath.Join(origin, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", path)
	gitIn(t, origin, "commit", "-q", "-m", "add "+path)
	gitIn(t, local, "fetch", "-q", "origin")
}

const serverYAML = `
conflicts:
  - paths: [accessmanagement/src/main/resources/application.yaml]
    strategy: owned-line
    line: '^\s*version:'
    rule: max-plus-patch
  - paths: [etc/openapi/apidocs/*.json]
    strategy: openapi
  - paths: [settings.gradle]
    strategy: list-union
    line: '^include '
defer:
  - run: ./gradlew webapp:generateOpenApi
    paths: [etc/openapi/apidocs/**]
    commit: "chore(api): regenerate the openapi specs after rebase"
dependency_graph:
  - gradle/libs.versions.toml
  - "*/build.gradle"
`

func TestParseReadsEverySection(t *testing.T) {
	cfg, err := Parse([]byte(serverYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Conflicts) != 3 || cfg.Conflicts[0].Strategy != "owned-line" || cfg.Conflicts[0].Rule != "max-plus-patch" {
		t.Errorf("conflicts = %+v", cfg.Conflicts)
	}
	if len(cfg.Defer) != 1 || cfg.Defer[0].Commit == "" || len(cfg.Defer[0].Paths) != 1 {
		t.Errorf("defer = %+v", cfg.Defer)
	}
	if len(cfg.DependencyGraph) != 2 {
		t.Errorf("dependency_graph = %v", cfg.DependencyGraph)
	}
}

func TestParseRejectsUnknownStrategyAndMissingParameters(t *testing.T) {
	cases := map[string]string{
		"unknown strategy":       "conflicts:\n  - paths: [a]\n    strategy: magic\n",
		"owned-line needs line":  "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    rule: keep-branch\n",
		"owned-line needs rule":  "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    line: '^v'\n",
		"unknown rule":           "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    line: '^v'\n    rule: newest\n",
		"list-union needs line":  "conflicts:\n  - paths: [a]\n    strategy: list-union\n",
		"script needs run":       "conflicts:\n  - paths: [a]\n    strategy: script\n",
		"a rule needs paths":     "conflicts:\n  - strategy: take-trunk\n",
		"defer needs run":        "defer:\n  - paths: [a]\n",
		"unknown key is an error": "conflicts:\n  - paths: [a]\n    strategy: take-trunk\n    when: always\n",
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(yaml)); err == nil {
				t.Errorf("Parse accepted %q", yaml)
			}
		})
	}
}

func TestParseDefaultsOpenAPIRuleAndListDelimiter(t *testing.T) {
	cfg, err := Parse([]byte("conflicts:\n  - paths: [a.json]\n    strategy: openapi\n  - paths: [s]\n    strategy: list-union\n    line: '^include '\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Conflicts[0].Rule != "max-plus-patch" {
		t.Errorf("openapi rule = %q, want max-plus-patch", cfg.Conflicts[0].Rule)
	}
	if cfg.Conflicts[1].Delimiter != "," {
		t.Errorf("list-union delimiter = %q, want ,", cfg.Conflicts[1].Delimiter)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"settings.gradle", "settings.gradle", true},
		{"settings.gradle", "sub/settings.gradle", false},
		{"etc/openapi/apidocs/*.json", "etc/openapi/apidocs/openapi_v3.json", true},
		{"etc/openapi/apidocs/*.json", "etc/openapi/apidocs/deep/x.json", false},
		{"apps/*/package.json", "apps/access-manager/package.json", true},
		{"apps/*/package.json", "apps/a/b/package.json", false},
		{"**/package.json", "package.json", true},
		{"**/package.json", "apps/a/b/package.json", true},
		{"etc/openapi/apidocs/**", "etc/openapi/apidocs/openapi_v3.json", true},
		{"*/build.gradle", "pins/build.gradle", true},
		{"*/build.gradle", "build.gradle", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestRuleForTakesTheFirstMatch(t *testing.T) {
	cfg, err := Parse([]byte(serverYAML))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cfg.RuleFor("etc/openapi/apidocs/openapi_remote_v3.json")
	if !ok || r.Strategy != "openapi" {
		t.Errorf("RuleFor spec = %+v, %v", r, ok)
	}
	if _, ok := cfg.RuleFor("src/Main.java"); ok {
		t.Error("an unclaimed path must not match")
	}
}

func TestLoadFromTrunkReadsOriginNotTheWorkingTree(t *testing.T) {
	local, origin := repoWithOrigin(t)
	commitOnOrigin(t, local, origin, ".wt-sync.yaml", "conflicts:\n  - paths: [lock]\n    strategy: take-trunk\n")
	// A different, malicious-looking config in the working tree must be ignored.
	if err := os.WriteFile(filepath.Join(local, ".wt-sync.yaml"), []byte("conflicts:\n  - paths: [x]\n    strategy: script\n    run: rm -rf /\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromTrunk(local, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Conflicts) != 1 || cfg.Conflicts[0].Strategy != "take-trunk" {
		t.Errorf("read the wrong config: %+v", cfg.Conflicts)
	}
}

func TestLoadFromTrunkReportsNoConfig(t *testing.T) {
	local, _ := repoWithOrigin(t)
	if _, err := LoadFromTrunk(local, "main"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("err = %v, want ErrNoConfig", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ 2>&1 | head -5`
Expected: build failure, `undefined: Parse` (the package does not exist yet).

- [ ] **Step 3: Add the yaml dependency and write the implementation**

```bash
go get gopkg.in/yaml.v3@v3.0.1
```

Create `internal/wtsync/config.go`:

```go
// Package wtsync keeps worktrees rebased on trunk: it reads a repository's
// declaration of its conflict shapes, resolves those shapes deterministically,
// and simulates every rebase before anything is touched.
package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/anders-lindstrom/wt/internal/git"
)

// ConfigFile is the declaration at the top of a repository.
const ConfigFile = ".wt-sync.yaml"

// ErrNoConfig means trunk carries no declaration: the repository is reported
// by wt sync and never rebased.
var ErrNoConfig = errors.New("no " + ConfigFile + " on trunk")

// Rule declares that files matching Paths have one conflict shape.
type Rule struct {
	Paths    []string `yaml:"paths"`
	Strategy string   `yaml:"strategy"`
	// Line is the regex of the owned line (owned-line) or the list line
	// (list-union).
	Line string `yaml:"line"`
	// Rule decides the value of an owned line or an OpenAPI version:
	// max-plus-patch, keep-branch or keep-trunk.
	Rule string `yaml:"rule"`
	// Delimiter separates list-union items; "," when omitted.
	Delimiter string `yaml:"delimiter"`
	// Run is the executable of a script strategy, relative to the root.
	Run string `yaml:"run"`
}

// Deferred is work that runs once after the last commit is replayed.
type Deferred struct {
	Run string `yaml:"run"`
	// Paths gate the step on what the rebase changed; empty means always.
	Paths []string `yaml:"paths"`
	// Commit is the message for the step's output, when it changes tracked
	// files; empty means the output is never committed.
	Commit string `yaml:"commit"`
}

// Config is one repository's .wt-sync.yaml.
type Config struct {
	Conflicts       []Rule     `yaml:"conflicts"`
	Defer           []Deferred `yaml:"defer"`
	DependencyGraph []string   `yaml:"dependency_graph"`
}

// Strategies and value rules a declaration may name.
var (
	strategies = map[string]bool{"owned-line": true, "openapi": true, "list-union": true, "take-trunk": true, "script": true}
	valueRules = map[string]bool{"max-plus-patch": true, "keep-branch": true, "keep-trunk": true}
)

// LoadFromTrunk reads the declaration from origin/<trunk>, never from a
// working tree: the file names executables, and a feature branch must not be
// able to change what runs unattended.
func LoadFromTrunk(mainRoot, trunk string) (*Config, error) {
	out, err := git.Run(mainRoot, "show", "origin/"+trunk+":"+ConfigFile)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return nil, ErrNoConfig
		}
		return nil, err
	}
	return Parse([]byte(out))
}

// Parse decodes and validates a declaration, filling in defaults.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file decodes to an empty declaration; io.EOF is not an error.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	for i := range cfg.Conflicts {
		r := &cfg.Conflicts[i]
		if len(r.Paths) == 0 {
			return nil, fmt.Errorf("%s: conflicts[%d] has no paths", ConfigFile, i)
		}
		if !strategies[r.Strategy] {
			return nil, fmt.Errorf("%s: conflicts[%d]: unknown strategy %q", ConfigFile, i, r.Strategy)
		}
		switch r.Strategy {
		case "owned-line":
			if r.Line == "" || r.Rule == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: owned-line needs line and rule", ConfigFile, i)
			}
		case "openapi":
			if r.Rule == "" {
				r.Rule = "max-plus-patch"
			}
		case "list-union":
			if r.Line == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: list-union needs line", ConfigFile, i)
			}
			if r.Delimiter == "" {
				r.Delimiter = ","
			}
		case "script":
			if r.Run == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: script needs run", ConfigFile, i)
			}
		}
		if r.Rule != "" && !valueRules[r.Rule] {
			return nil, fmt.Errorf("%s: conflicts[%d]: unknown rule %q", ConfigFile, i, r.Rule)
		}
	}
	for i, d := range cfg.Defer {
		if d.Run == "" {
			return nil, fmt.Errorf("%s: defer[%d] has no run", ConfigFile, i)
		}
	}
	return &cfg, nil
}

// RuleFor returns the first rule claiming path.
func (c *Config) RuleFor(path string) (Rule, bool) {
	for _, r := range c.Conflicts {
		for _, p := range r.Paths {
			if MatchGlob(p, path) {
				return r, true
			}
		}
	}
	return Rule{}, false
}

// MatchGlob matches a root-relative path against a pattern where `*` spans
// one path segment and `**` spans any number, including none. Patterns are
// matched, never expanded against the disk.
func MatchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if !matchSegment(pat[0], segs[0]) {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}

// matchSegment matches one segment where `*` spans any characters.
func matchSegment(pat, seg string) bool {
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return pat == seg
	}
	if !strings.HasPrefix(seg, parts[0]) {
		return false
	}
	seg = seg[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(seg, part)
		if i < 0 {
			return false
		}
		seg = seg[i+len(part):]
	}
	return strings.HasSuffix(seg, parts[len(parts)-1])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'Parse|MatchGlob|RuleFor|LoadFromTrunk' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: every test `--- PASS`, then `ok`. If `TestParseRejectsUnknownStrategyAndMissingParameters/unknown key is an error` fails, `KnownFields(true)` is not in effect; keep it, that is the guard against a typo silently disabling a rule.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add go.mod go.sum internal/wtsync/config.go internal/wtsync/config_test.go
git commit -m "feat(sync): read a repository's conflict declaration from trunk"
git push origin main
```

---

### Task 2: Conflicts, refusals, and the diff3 block walker

**Files:**
- Create: `internal/wtsync/conflict.go`
- Create: `internal/wtsync/merge3.go`
- Create: `internal/wtsync/merge3_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Conflict struct { Path string; Base, Trunk, Branch []byte }`
  - `type Refusal struct { Path, Reason string }` implementing `error`; `func Refuse(path, format string, a ...any) error`; `func IsRefusal(err error) bool`
  - `type Block struct { Branch, Base, Trunk []string }` — one conflict region
  - `type Segment struct { Lines []string; Block *Block }` — text or a block
  - `func Merge3(c Conflict) ([]Segment, error)` — runs `git merge-file -p --diff3`, splits into segments
  - `func Render(segs []Segment, collapse func(Block) ([]string, error)) ([]byte, error)` — re-emits the file with every block replaced by `collapse`'s lines; the file ends with a newline iff the branch blob did

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/merge3_test.go`:

```go
package wtsync

import (
	"errors"
	"strings"
	"testing"
)

func conflict(base, trunk, branch string) Conflict {
	return Conflict{Path: "f.txt", Base: []byte(base), Trunk: []byte(trunk), Branch: []byte(branch)}
}

func TestMerge3SplitsTextAndBlocks(t *testing.T) {
	segs, err := Merge3(conflict("keep\nbase\ntail\n", "keep\ntrunk\ntail\n", "keep\nbranch\ntail\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3: %+v", len(segs), segs)
	}
	if segs[0].Block != nil || strings.Join(segs[0].Lines, "|") != "keep" {
		t.Errorf("first segment = %+v", segs[0])
	}
	b := segs[1].Block
	if b == nil || strings.Join(b.Branch, "|") != "branch" || strings.Join(b.Base, "|") != "base" || strings.Join(b.Trunk, "|") != "trunk" {
		t.Errorf("block = %+v", b)
	}
	if segs[2].Block != nil || strings.Join(segs[2].Lines, "|") != "tail" {
		t.Errorf("last segment = %+v", segs[2])
	}
}

func TestMerge3KeepsOneSidedChangesOutsideBlocks(t *testing.T) {
	segs, err := Merge3(conflict("a\nv1\n", "a\nv2\ntrunk-added\n", "a\nv3\n"))
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, s := range segs {
		if s.Block == nil {
			text = append(text, s.Lines...)
		}
	}
	joined := strings.Join(text, "|")
	if !strings.Contains(joined, "a") {
		t.Errorf("text segments = %q", joined)
	}
}

func TestMerge3ReportsNoBlocksWhenGitMergesCleanly(t *testing.T) {
	segs, err := Merge3(conflict("a\nb\n", "a2\nb\n", "a\nb2\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		if s.Block != nil {
			t.Fatalf("unexpected block %+v", s.Block)
		}
	}
}

func TestRenderReplacesBlocksAndPreservesTheEnding(t *testing.T) {
	c := conflict("x\nbase\n", "x\ntrunk\n", "x\nbranch\n")
	segs, err := Merge3(c)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(segs, func(b Block) ([]string, error) { return []string{"resolved"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "x\nresolved\n" {
		t.Errorf("out = %q", out)
	}
	// no trailing newline on the branch side -> none in the output
	c2 := conflict("x\nbase", "x\ntrunk", "x\nbranch")
	segs2, _ := Merge3(c2)
	out2, _ := Render(segs2, func(b Block) ([]string, error) { return []string{"r"}, nil })
	if string(out2) != "x\nr" {
		t.Errorf("out2 = %q", out2)
	}
}

func TestRenderPropagatesARefusal(t *testing.T) {
	segs, _ := Merge3(conflict("base\n", "trunk\n", "branch\n"))
	_, err := Render(segs, func(b Block) ([]string, error) { return nil, Refuse("f.txt", "not mine: %d lines", len(b.Branch)) })
	var r *Refusal
	if !errors.As(err, &r) || r.Path != "f.txt" || !strings.Contains(r.Reason, "not mine: 1 lines") {
		t.Errorf("err = %v", err)
	}
	if !IsRefusal(err) {
		t.Error("IsRefusal should be true")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'Merge3|Render' 2>&1 | head -3`
Expected: `undefined: Merge3` (and the others).

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/conflict.go`:

```go
package wtsync

import (
	"errors"
	"fmt"
)

// Conflict is one file that both sides changed, as the three blobs git holds
// for it. Trunk is the side being rebased onto (stage 2 during a rebase) and
// Branch is the commit being replayed (stage 3); the ambiguous words "ours"
// and "theirs" are not used anywhere in this package.
//
// Incomplete is set when the conflict does not carry three regular blobs — a
// side deleted or renamed the file, or an entry is a submodule or symlink.
// No strategy owns such a conflict; it is a person's call.
type Conflict struct {
	Path       string
	Base       []byte
	Trunk      []byte
	Branch     []byte
	Incomplete string
}

// Refusal is a strategy declining a conflict it does not own completely. It
// is a normal outcome, not a failure: the file is left exactly as it was and
// a person looks. Reason names what collided.
type Refusal struct {
	Path   string
	Reason string
}

func (r *Refusal) Error() string { return r.Path + ": " + r.Reason }

// Refuse builds a Refusal.
func Refuse(path, format string, a ...any) error {
	return &Refusal{Path: path, Reason: fmt.Sprintf(format, a...)}
}

// IsRefusal reports whether err is a strategy refusing, as opposed to
// something going wrong.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}
```

Create `internal/wtsync/merge3.go`:

```go
package wtsync

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Block is one region git could not merge: the branch's lines, the base's,
// and trunk's.
type Block struct {
	Branch []string
	Base   []string
	Trunk  []string
}

// Segment is a run of merged text, or one conflict block.
type Segment struct {
	Lines []string
	Block *Block
}

const (
	markBranch = "<<<<<<< branch"
	markBase   = "||||||| base"
	markMid    = "======="
	markTrunk  = ">>>>>>> trunk"
)

// Merge3 merges the three blobs with git's own three-way merge and splits the
// result into text and conflict blocks. git decides what conflicts; this
// package only decides what to do about it.
func Merge3(c Conflict) ([]Segment, error) {
	dir, err := os.MkdirTemp("", "wtsync-merge3-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	names := map[string][]byte{"branch": c.Branch, "base": c.Base, "trunk": c.Trunk}
	for name, data := range names {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command("git", "merge-file", "-p", "--diff3",
		"-L", "branch", "-L", "base", "-L", "trunk",
		filepath.Join(dir, "branch"), filepath.Join(dir, "base"), filepath.Join(dir, "trunk"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// merge-file exits with the number of conflicts, or negative on error.
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() < 0 || exit.ExitCode() > 127 {
			return nil, errors.New(strings.TrimSpace(stderr.String()))
		}
	}
	return split(stdout.String())
}

// split walks merge-file's output. A line that is not inside a block is
// text; the four markers delimit the branch, base and trunk sides.
func split(out string) ([]Segment, error) {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if out == "" {
		lines = nil
	}
	var segs []Segment
	var text []string
	var blk *Block
	side := ""
	flushText := func() {
		if len(text) > 0 {
			segs = append(segs, Segment{Lines: text})
			text = nil
		}
	}
	for _, l := range lines {
		switch {
		case blk == nil && l == markBranch:
			flushText()
			blk, side = &Block{}, "branch"
		case blk != nil && l == markBase:
			side = "base"
		case blk != nil && l == markMid:
			side = "trunk"
		case blk != nil && l == markTrunk:
			segs = append(segs, Segment{Block: blk})
			blk, side = nil, ""
		case blk != nil:
			switch side {
			case "branch":
				blk.Branch = append(blk.Branch, l)
			case "base":
				blk.Base = append(blk.Base, l)
			case "trunk":
				blk.Trunk = append(blk.Trunk, l)
			}
		default:
			text = append(text, l)
		}
	}
	if blk != nil {
		return nil, errors.New("unterminated conflict block in merge output")
	}
	flushText()
	return segs, nil
}

// Render emits the merged file with each block replaced by collapse's lines.
// The output ends with a newline iff the merged text did, which follows the
// branch side: git merge-file takes the ending from the first file given.
func Render(segs []Segment, collapse func(Block) ([]string, error)) ([]byte, error) {
	var out []string
	for _, s := range segs {
		if s.Block == nil {
			out = append(out, s.Lines...)
			continue
		}
		lines, err := collapse(*s.Block)
		if err != nil {
			return nil, err
		}
		out = append(out, lines...)
	}
	return []byte(strings.Join(out, "\n")), nil
}
```

Then make `Render` honour the ending. `Merge3` knows whether the branch blob ended with a newline; carry it on the segments by giving the last text segment a trailing empty line when it did. Replace the tail of `split` and the signature of `Merge3` accordingly:

```go
// in Merge3, replace `return split(stdout.String())` with:
	segs, err := split(stdout.String())
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(stdout.String(), "\n") {
		segs = append(segs, Segment{Lines: []string{""}})
	}
	return segs, nil
```

With that, `strings.Join(out, "\n")` ends in `"\n"` exactly when the merge output did, and the test's second case (no newline on the branch side) produces none.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'Merge3|Render' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. If `TestMerge3ReportsNoBlocksWhenGitMergesCleanly` errors with a negative exit, the `exit.ExitCode()` guard is wrong: `merge-file` returns the conflict count (0 here), and only a true failure is negative.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/conflict.go internal/wtsync/merge3.go internal/wtsync/merge3_test.go
git commit -m "feat(sync): merge three blobs with git and walk the conflict blocks"
git push origin main
```

---

### Task 3: Value rules, the Strategy interface, and `take-trunk`

**Files:**
- Create: `internal/wtsync/rules.go`
- Create: `internal/wtsync/rules_test.go`
- Create: `internal/wtsync/strategy.go`
- Create: `internal/wtsync/take_trunk.go`
- Create: `internal/wtsync/strategy_test.go`

**Interfaces:**
- Consumes: `Conflict`, `Refuse` (Task 2); `Rule` (Task 1).
- Produces:
  - `type ValueRule interface { Apply(branch, trunk string) (string, error) }` — given the branch's and trunk's *line*, return the line the branch keeps.
  - `func RuleNamed(name string) (ValueRule, error)` — `max-plus-patch`, `keep-branch`, `keep-trunk`.
  - `func MaxPlusPatch(branchV, trunkV string) string` — the semver rule on bare versions (spec §2).
  - `var semverRE`, `var exactSemverRE`, `func findSemver(line string) string` — the version token in a line (not followed by `-`, `.` or an alphanumeric), and a bare `X.Y.Z` matcher.
  - `type Strategy interface { Name() string; Resolve(c Conflict) ([]byte, error) }` — resolved bytes, or a `*Refusal`, or another error.
  - `func FromRule(r Rule, root string) (Strategy, error)` — constructs the strategy a rule names. Tasks 4–7 add their cases to its switch; until then unknown strategies return an error.
  - `type TakeTrunk struct{}`

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/rules_test.go`:

```go
package wtsync

import "testing"

func TestMaxPlusPatch(t *testing.T) {
	cases := []struct{ branch, trunk, want string }{
		{"2.39.0", "2.38.5", "2.39.0"}, // branch already above trunk: keep
		{"2.38.3", "2.38.5", "2.38.6"}, // trunk overtook: one patch above trunk
		{"2.38.5", "2.38.5", "2.38.6"}, // equal: the branch still needs its own
		{"1.5.0", "1.5.1", "1.5.2"},
		{"2.38.10", "2.38.9", "2.38.10"}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := MaxPlusPatch(c.branch, c.trunk); got != c.want {
			t.Errorf("MaxPlusPatch(%s, %s) = %s, want %s", c.branch, c.trunk, got, c.want)
		}
	}
}

func TestRuleNamedAppliesToTheVersionInsideALine(t *testing.T) {
	r, err := RuleNamed("max-plus-patch")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Apply("    version: 2.38.3", "    version: 2.38.5")
	if err != nil || got != "    version: 2.38.6" {
		t.Errorf("got %q, %v", got, err)
	}
	// the branch's formatting is kept, only the version changes
	got, err = r.Apply(`    "pkg": "2.38.3",`, `    "pkg": "2.38.5",`)
	if err != nil || got != `    "pkg": "2.38.6",` {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRuleNamedRefusesALineWithoutAWholeVersionToken(t *testing.T) {
	r, _ := RuleNamed("max-plus-patch")
	for _, line := range []string{"    version: SNAPSHOT", "    version: 2.38.3-SNAPSHOT", "    version: 2.38.3.1"} {
		if _, err := r.Apply(line, "    version: 2.38.5"); err == nil {
			t.Errorf("expected an error for %q", line)
		}
	}
}

func TestKeepRules(t *testing.T) {
	kb, _ := RuleNamed("keep-branch")
	kt, _ := RuleNamed("keep-trunk")
	if got, _ := kb.Apply("b", "t"); got != "b" {
		t.Errorf("keep-branch = %q", got)
	}
	if got, _ := kt.Apply("b", "t"); got != "t" {
		t.Errorf("keep-trunk = %q", got)
	}
	if _, err := RuleNamed("newest"); err == nil {
		t.Error("unknown rule must be an error")
	}
}
```

Create `internal/wtsync/strategy_test.go`:

```go
package wtsync

import "testing"

func TestTakeTrunkReturnsTrunksBlobUnchanged(t *testing.T) {
	c := conflict("lockfileVersion: 9.0\nbase\n", "lockfileVersion: 9.0\ntrunk\n", "lockfileVersion: 9.0\nbranch\n")
	out, err := (TakeTrunk{}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(c.Trunk) {
		t.Errorf("out = %q", out)
	}
}

func TestFromRuleBuildsTakeTrunkAndRejectsTheUnknown(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "take-trunk"}, "")
	if err != nil || s.Name() != "take-trunk" {
		t.Errorf("FromRule take-trunk = %v, %v", s, err)
	}
	if _, err := FromRule(Rule{Strategy: "magic"}, ""); err == nil {
		t.Error("unknown strategy must be an error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'MaxPlusPatch|RuleNamed|KeepRules|TakeTrunk|FromRule' 2>&1 | head -3`
Expected: `undefined: MaxPlusPatch` and friends.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/rules.go`:

```go
package wtsync

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// semverRE finds the X.Y.Z inside a line, as a whole token: "2.38.3-SNAPSHOT"
// and "2.38.3.1" are not versions this rule knows, and must refuse rather
// than be lifted to "2.38.6-SNAPSHOT". Group 1 is the version.
var semverRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9.-])(\d+\.\d+\.\d+)(?:[^A-Za-z0-9.-]|$)`)

// exactSemverRE matches a bare X.Y.Z and nothing else.
var exactSemverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// findSemver returns the version token in a line, or "".
func findSemver(line string) string {
	m := semverRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// ValueRule decides which line the branch keeps when both sides changed the
// one line a strategy owns. It sees whole lines so that indentation and
// punctuation around the value survive.
type ValueRule interface {
	Apply(branch, trunk string) (string, error)
}

// RuleNamed returns the rule a declaration names.
func RuleNamed(name string) (ValueRule, error) {
	switch name {
	case "max-plus-patch":
		return maxPlusPatch{}, nil
	case "keep-branch":
		return keepBranch{}, nil
	case "keep-trunk":
		return keepTrunk{}, nil
	}
	return nil, fmt.Errorf("unknown rule %q", name)
}

type keepBranch struct{}
type keepTrunk struct{}
type maxPlusPatch struct{}

func (keepBranch) Apply(branch, _ string) (string, error) { return branch, nil }
func (keepTrunk) Apply(_, trunk string) (string, error)   { return trunk, nil }

// Apply rewrites the version inside the branch's line to MaxPlusPatch of the
// two versions, keeping everything else on the line as the branch wrote it.
func (maxPlusPatch) Apply(branch, trunk string) (string, error) {
	bv := findSemver(branch)
	tv := findSemver(trunk)
	if bv == "" || tv == "" {
		return "", fmt.Errorf("max-plus-patch needs an X.Y.Z version on both sides, got %q and %q",
			strings.TrimSpace(branch), strings.TrimSpace(trunk))
	}
	return strings.Replace(branch, bv, MaxPlusPatch(bv, tv), 1), nil
}

// MaxPlusPatch is the version a branch keeps after trunk moved: the higher of
// the two, and when that is trunk's, one patch above it, because the branch
// still needs a version of its own above everything trunk has published.
func MaxPlusPatch(branchV, trunkV string) string {
	if compareSemver(branchV, trunkV) > 0 {
		return branchV
	}
	maj, min, pat := parseSemver(trunkV)
	return fmt.Sprintf("%d.%d.%d", maj, min, pat+1)
}

func parseSemver(v string) (maj, min, pat int) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) == 3 {
		maj, _ = strconv.Atoi(parts[0])
		min, _ = strconv.Atoi(parts[1])
		pat, _ = strconv.Atoi(parts[2])
	}
	return
}

func compareSemver(a, b string) int {
	am, an, ap := parseSemver(a)
	bm, bn, bp := parseSemver(b)
	switch {
	case am != bm:
		return am - bm
	case an != bn:
		return an - bn
	default:
		return ap - bp
	}
}
```

Create `internal/wtsync/strategy.go`:

```go
package wtsync

import "fmt"

// Strategy resolves one conflict shape. Resolve returns the resolved file, a
// *Refusal when the conflict is not the shape the strategy owns completely,
// or another error when something went wrong. It never guesses.
type Strategy interface {
	Name() string
	Resolve(c Conflict) ([]byte, error)
}

// FromRule builds the strategy a declaration names. root is the repository
// root, needed only by script strategies.
func FromRule(r Rule, root string) (Strategy, error) {
	switch r.Strategy {
	case "take-trunk":
		return TakeTrunk{}, nil
	}
	return nil, fmt.Errorf("unknown strategy %q", r.Strategy)
}
```

Create `internal/wtsync/take_trunk.go`:

```go
package wtsync

// TakeTrunk resolves a generated file by taking trunk's copy wholesale: a
// lockfile merged by hand describes a tree nobody has ever installed, and the
// deferred step declared beside it reconciles trunk's copy against the merged
// manifests once, at the end of the rebase.
type TakeTrunk struct{}

func (TakeTrunk) Name() string { return "take-trunk" }

func (TakeTrunk) Resolve(c Conflict) ([]byte, error) { return c.Trunk, nil }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'MaxPlusPatch|RuleNamed|KeepRules|TakeTrunk|FromRule' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/rules.go internal/wtsync/rules_test.go internal/wtsync/strategy.go internal/wtsync/strategy_test.go internal/wtsync/take_trunk.go
git commit -m "feat(sync): value rules, the strategy interface and take-trunk"
git push origin main
```

---

### Task 4: The `owned-line` strategy

**Files:**
- Create: `internal/wtsync/owned_line.go`
- Create: `internal/wtsync/owned_line_test.go`
- Modify: `internal/wtsync/strategy.go` (add the `owned-line` case to `FromRule`)

**Interfaces:**
- Consumes: `Merge3`, `Render`, `Block`, `Refuse` (Task 2); `ValueRule`, `RuleNamed` (Task 3).
- Produces:
  - `type OwnedLine struct { Line *regexp.Regexp; Rule ValueRule }`
  - `func collapseOwned(b Block, line *regexp.Regexp, rule ValueRule, path string) ([]string, error)` — the block rule of spec §2: exactly one owned line per side; the other lines in the block may differ from the base on **one** side only, and that side's lines are kept.

The shape this owns, from the bash reference (`openapi-version`, `spec-client-version`): git leaves a block whose branch and trunk sides each hold the owned line, sometimes with a neighbouring line one side alone edited (a comment above a version, a dependency next to a pin).

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/owned_line_test.go`:

```go
package wtsync

import (
	"regexp"
	"strings"
	"testing"
)

// yamlDoc is the shape of server's openapi: block. (Not "yaml": that is the
// package's import name.)
func yamlDoc(main string, extra string) string {
	return "server:\n  port: 8080\nopenapi:\n  main:\n    name: Backend API\n    # update when the spec changes\n    version: " + main + "\n    public-only: false\n  remote:\n    name: Remote API\n    version: 1.5.1\n" + extra
}

func ownedVersion(t *testing.T) OwnedLine {
	t.Helper()
	rule, err := RuleNamed("max-plus-patch")
	if err != nil {
		t.Fatal(err)
	}
	return OwnedLine{Line: regexp.MustCompile(`^\s*version:`), Rule: rule}
}

func TestOwnedLineLiftsAVersionOnlyConflictAboveTrunk(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.38.3", "")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\n    version: 2.38.6\n") || strings.Contains(string(out), "<<<<<<<") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(string(out), "    version: 1.5.1\n") {
		t.Error("the remote version, untouched by both sides, must survive")
	}
}

func TestOwnedLineKeepsABranchVersionAlreadyAboveTrunk(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.39.0", "")))
	if err != nil || !strings.Contains(string(out), "version: 2.39.0\n") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestOwnedLineResolvesTwoOwnedLinesInSeparateBlocks(t *testing.T) {
	two := func(m, r string) string { return "openapi:\n  main:\n    version: " + m + "\n  remote:\n    version: " + r + "\n" }
	out, err := ownedVersion(t).Resolve(conflict(two("2.35.0", "1.4.6"), two("2.38.2", "1.5.1"), two("2.36.0", "1.5.0")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != two("2.38.3", "1.5.2") {
		t.Errorf("out = %q", out)
	}
}

func TestOwnedLineKeepsTheRestAsGitMergedIt(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", "  extra: trunk\n"), yamlDoc("2.38.3", "")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "  extra: trunk\n") || !strings.Contains(string(out), "version: 2.38.6\n") {
		t.Errorf("out = %q", out)
	}
}

func TestOwnedLineResolvesABlockGitFoldedAroundTheLine(t *testing.T) {
	withc := func(c, v string) string { return "openapi:\n  main:\n    # " + c + "\n    version: " + v + "\n" }
	// trunk rewrote the comment above the version; the branch bumped the version
	out, err := ownedVersion(t).Resolve(conflict(withc("old", "2.38.0"), withc("new", "2.38.5"), withc("old", "2.38.3")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != withc("new", "2.38.6") {
		t.Errorf("out = %q", out)
	}
	// the branch added a line next to it and trunk only bumped: the branch's line survives
	out, err = ownedVersion(t).Resolve(conflict(
		"k: a\nversion: 1.0.0\n", "k: a\nversion: 1.0.5\n", "k: a\nversion: 1.0.2\nadded: by-branch\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "k: a\nversion: 1.0.6\nadded: by-branch\n" {
		t.Errorf("out = %q", out)
	}
}

func TestOwnedLineRefusesWhenBothSidesChangedOtherLines(t *testing.T) {
	withc := func(c, v string) string { return "openapi:\n  main:\n    # " + c + "\n    version: " + v + "\n" }
	_, err := ownedVersion(t).Resolve(conflict(withc("old", "2.38.0"), withc("trunk", "2.38.5"), withc("branch", "2.38.3")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "more than") {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineRefusesAConflictElsewhere(t *testing.T) {
	_, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", "  extra: trunk\n"), yamlDoc("2.38.3", "  extra: branch\n")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineRefusesAVersionThatIsNotSemver(t *testing.T) {
	_, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.38.3-SNAPSHOT", "")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineKeepsTheBranchPinAndItsFormatting(t *testing.T) {
	pkg := func(v, react string) string {
		return "{\n  \"name\": \"app\",\n  \"dependencies\": {\n    \"@telcred/spec\": \"" + v + "\",\n    \"react\": \"" + react + "\"\n  }\n}\n"
	}
	rule, _ := RuleNamed("keep-branch")
	s := OwnedLine{Line: regexp.MustCompile(`^\s*"@telcred/spec":`), Rule: rule}
	// trunk also bumped react on the adjacent line: git folds it into the block
	out, err := s.Resolve(conflict(pkg("2.30.2", "19.0.0"), pkg("2.37.1", "19.1.0"), pkg("2.36.0-snapshot.20260831123245", "19.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != pkg("2.36.0-snapshot.20260831123245", "19.1.0") {
		t.Errorf("out = %q", out)
	}
	// both sides changed react: refuse
	_, err = s.Resolve(conflict(pkg("2.30.2", "19.0.0"), pkg("2.37.1", "19.1.0"), pkg("2.36.0", "19.2.0")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestFromRuleBuildsOwnedLine(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "owned-line", Line: `^\s*version:`, Rule: "keep-trunk"}, "")
	if err != nil || s.Name() != "owned-line" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
	if _, err := FromRule(Rule{Strategy: "owned-line", Line: `(`, Rule: "keep-trunk"}, ""); err == nil {
		t.Error("a bad regex must be an error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'OwnedLine' 2>&1 | head -3`
Expected: `undefined: OwnedLine`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/owned_line.go`:

```go
package wtsync

import (
	"regexp"
	"strings"
)

// OwnedLine resolves a file where the only thing both sides may change is the
// one line matching Line, whose value Rule decides. git leaves markers only
// where the sides genuinely disagree, so everything else in the file comes
// through as git merged it.
type OwnedLine struct {
	Line *regexp.Regexp
	Rule ValueRule
}

func (OwnedLine) Name() string { return "owned-line" }

func (s OwnedLine) Resolve(c Conflict) ([]byte, error) {
	segs, err := Merge3(c)
	if err != nil {
		return nil, err
	}
	return Render(segs, func(b Block) ([]string, error) {
		return collapseOwned(b, s.Line, s.Rule, c.Path)
	})
}

// collapseOwned resolves one block. Each side must hold exactly one owned
// line. git folds an adjacent edit into the same block, so the block may hold
// other lines too, as long as only one side changed them against the base:
// that side's lines are kept, with the owned line replaced by the rule's
// answer. Both sides changing the other lines is a real disagreement.
func collapseOwned(b Block, line *regexp.Regexp, rule ValueRule, path string) ([]string, error) {
	branchLine, branchRest, ok := splitOwned(b.Branch, line)
	if !ok {
		return nil, Refuse(path, "the two sides differ by more than the owned line")
	}
	trunkLine, trunkRest, ok := splitOwned(b.Trunk, line)
	if !ok {
		return nil, Refuse(path, "the two sides differ by more than the owned line")
	}
	_, baseRest, _ := splitOwned(b.Base, line)

	resolved, err := rule.Apply(branchLine, trunkLine)
	if err != nil {
		return nil, Refuse(path, "%v", err)
	}
	var keep []string
	switch {
	case equalLines(branchRest, baseRest):
		keep = b.Trunk
	case equalLines(trunkRest, baseRest):
		keep = b.Branch
	default:
		return nil, Refuse(path, "the two sides differ by more than the owned line")
	}
	out := make([]string, 0, len(keep))
	for _, l := range keep {
		if line.MatchString(l) {
			out = append(out, resolved)
		} else {
			out = append(out, l)
		}
	}
	return out, nil
}

// splitOwned separates the one owned line from the rest of a side. ok is
// false when the side has no owned line or more than one.
func splitOwned(side []string, line *regexp.Regexp) (owned string, rest []string, ok bool) {
	n := 0
	for _, l := range side {
		if line.MatchString(l) {
			owned = l
			n++
		} else {
			rest = append(rest, l)
		}
	}
	return owned, rest, n == 1
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}
```

Add to `FromRule` in `internal/wtsync/strategy.go`, before the `take-trunk` case:

```go
	case "owned-line":
		re, err := regexp.Compile(r.Line)
		if err != nil {
			return nil, fmt.Errorf("owned-line: bad line regex %q: %w", r.Line, err)
		}
		rule, err := RuleNamed(r.Rule)
		if err != nil {
			return nil, err
		}
		return OwnedLine{Line: re, Rule: rule}, nil
```

and add `"regexp"` to that file's imports.

Also make `Parse` (Task 1, `config.go`) validate what `FromRule` would otherwise reject at first use: compile `Line` for `owned-line` and `list-union` with `regexp.Compile` and return an error naming the rule index on failure; for `script`, require `Run` to be relative, without `..` segments. Add these two cases to `TestParseRejectsUnknownStrategyAndMissingParameters`:

```go
		"bad line regex":          "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    line: '('\n    rule: keep-branch\n",
		"script escapes the root": "conflicts:\n  - paths: [a]\n    strategy: script\n    run: ../evil\n",
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'OwnedLine|Parse' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. `TestOwnedLineResolvesABlockGitFoldedAroundTheLine` is the one that fails if the base comparison is against the wrong side; the second case in it fails if `keep` picks trunk when only the branch added a line.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/owned_line.go internal/wtsync/owned_line_test.go internal/wtsync/strategy.go internal/wtsync/config.go internal/wtsync/config_test.go
git commit -m "feat(sync): the owned-line strategy"
git push origin main
```

---

### Task 5: The `list-union` strategy

**Files:**
- Create: `internal/wtsync/list_union.go`
- Create: `internal/wtsync/list_union_test.go`
- Modify: `internal/wtsync/strategy.go` (add the `list-union` case)

**Interfaces:**
- Consumes: `Merge3`, `Render`, `Block`, `Refuse` (Task 2).
- Produces: `type ListUnion struct { Line *regexp.Regexp; Delimiter string }`.

The shape, from the bash reference (`gradle-includes`): one line holding a delimited list, e.g. `include 'a', 'b'`; both sides add items; git may fold an adjacent added list line into the block. Resolution is the union on one line, trunk's order first, then the branch's additions in the order it made them. A removal on either side, or a block holding anything but list lines, is refused.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/list_union_test.go`:

```go
package wtsync

import (
	"regexp"
	"strings"
	"testing"
)

func inc(list string) string {
	return "plugins {\n    id 'x' version '1.0.0'\n}\n\nrootProject.name = 'server'\ninclude " + list + "\n"
}

func includes() ListUnion {
	return ListUnion{Line: regexp.MustCompile(`^include `), Delimiter: ","}
}

func includeLine(t *testing.T, out []byte) string {
	t.Helper()
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "include ") {
			return strings.TrimPrefix(l, "include ")
		}
	}
	t.Fatalf("no include line in %q", out)
	return ""
}

func TestListUnionUnionsTrunkOrderFirst(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'pins', 'b', 'c'"), inc("'a', 'b', 'bankid', 'c'")))
	if err != nil {
		t.Fatal(err)
	}
	if got := includeLine(t, out); got != "'a', 'pins', 'b', 'c', 'bankid'" {
		t.Errorf("include = %q", got)
	}
	if strings.Contains(string(out), "<<<<<<<") {
		t.Error("markers left behind")
	}
}

func TestListUnionKeepsTheRestOfTheFileFromGit(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a'"), strings.Replace(inc("'a', 'pins'"), "1.0.0", "2.0.0", 1), inc("'a', 'nbix'")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "version '2.0.0'") {
		t.Errorf("trunk's plugin bump lost: %q", out)
	}
}

func TestListUnionRefusesARemovalOnEitherSide(t *testing.T) {
	_, err := includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'pins', 'b', 'c'"), inc("'a', 'c'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "removes 'b'") {
		t.Errorf("branch removal: err = %v", err)
	}
	_, err = includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'c'"), inc("'a', 'b', 'c', 'd'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "removes 'b'") {
		t.Errorf("trunk removal: err = %v", err)
	}
}

func TestListUnionRefusesAConflictOffTheListLine(t *testing.T) {
	_, err := includes().Resolve(conflict("rootProject.name = 'a'\ninclude 'x'\n", "rootProject.name = 'b'\ninclude 'x'\n", "rootProject.name = 'c'\ninclude 'x'\n"))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestListUnionUnionsASecondListLineFoldedIntoTheBlock(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a', 'b'"), inc("'a', 'pins', 'b'"), inc("'a', 'b', 'nbix'")+"include 'nbix-devtools'\n"))
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "include ") {
			for _, it := range strings.Split(strings.TrimPrefix(l, "include "), ",") {
				items = append(items, strings.TrimSpace(it))
			}
		}
	}
	if strings.Join(items, " ") != "'a' 'pins' 'b' 'nbix' 'nbix-devtools'" {
		t.Errorf("items = %v", items)
	}
	if strings.Contains(string(out), "<<<<<<<") {
		t.Error("markers left behind")
	}
}

func TestListUnionRefusesAnItemItCannotParse(t *testing.T) {
	_, err := includes().Resolve(conflict(inc("'a', 'b'"), inc("'a', 'pins', 'b'"), inc("'a', 'b', 'nbix'include 'x'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "cannot parse") {
		t.Errorf("err = %v", err)
	}
}

func TestFromRuleBuildsListUnion(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "list-union", Line: "^include ", Delimiter: ","}, "")
	if err != nil || s.Name() != "list-union" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'ListUnion' 2>&1 | head -3`
Expected: `undefined: ListUnion`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/list_union.go`:

```go
package wtsync

import (
	"regexp"
	"strings"
)

// ListUnion resolves a file whose one contested line is a delimited list that
// both sides only ever add to: the union, trunk's order first, then the
// branch's additions. A removal is a decision, not a merge, and is refused.
type ListUnion struct {
	Line      *regexp.Regexp
	Delimiter string
}

func (ListUnion) Name() string { return "list-union" }

// itemRE is what one list item may look like: a quoted or bare token with no
// whitespace inside. Anything else means the line was not parsed the way it
// was written.
var itemRE = regexp.MustCompile(`^('[^'\s]+'|"[^"\s]+"|[^\s'",]+)$`)

func (s ListUnion) Resolve(c Conflict) ([]byte, error) {
	// Removal is judged on whole files, so an item a side merely moved to
	// another list line still counts as present.
	baseItems, err := s.items(c.Path, strings.Split(string(c.Base), "\n"))
	if err != nil {
		return nil, err
	}
	if len(baseItems) == 0 {
		return nil, Refuse(c.Path, "no list line matching %s", s.Line)
	}
	branchAll, err := s.items(c.Path, strings.Split(string(c.Branch), "\n"))
	if err != nil {
		return nil, err
	}
	trunkAll, err := s.items(c.Path, strings.Split(string(c.Trunk), "\n"))
	if err != nil {
		return nil, err
	}
	for _, it := range baseItems {
		if !contains(branchAll, it) {
			return nil, Refuse(c.Path, "the branch removes %s; that is a decision, not a merge", it)
		}
		if !contains(trunkAll, it) {
			return nil, Refuse(c.Path, "trunk removes %s; that is a decision, not a merge", it)
		}
	}
	segs, err := Merge3(c)
	if err != nil {
		return nil, err
	}
	return Render(segs, func(b Block) ([]string, error) {
		return s.collapse(c.Path, b)
	})
}

// collapse resolves one block, which must consist of list lines only. git
// folds a list line added right after the changed one into the same block,
// so a side may hold more than one; they are unioned onto one line.
func (s ListUnion) collapse(path string, b Block) ([]string, error) {
	if len(b.Branch) == 0 || len(b.Trunk) == 0 {
		return nil, Refuse(path, "the conflict is not on the list line")
	}
	var prefix string
	for _, l := range append(append([]string{}, b.Branch...), b.Trunk...) {
		m := s.Line.FindString(l)
		if m == "" {
			return nil, Refuse(path, "the conflict is not on the list line")
		}
		prefix = m
	}
	branchItems, err := s.items(path, b.Branch)
	if err != nil {
		return nil, err
	}
	trunkItems, err := s.items(path, b.Trunk)
	if err != nil {
		return nil, err
	}
	out := append([]string{}, trunkItems...)
	for _, it := range branchItems {
		if !contains(out, it) {
			out = append(out, it)
		}
	}
	return []string{prefix + strings.Join(out, s.Delimiter+" ")}, nil
}

// items collects the list items of every matching line, validated.
func (s ListUnion) items(path string, lines []string) ([]string, error) {
	var out []string
	for _, l := range lines {
		m := s.Line.FindString(l)
		if m == "" {
			continue
		}
		for _, raw := range strings.Split(strings.TrimPrefix(l, m), s.Delimiter) {
			it := strings.TrimSpace(raw)
			if it == "" {
				continue
			}
			if !itemRE.MatchString(it) {
				return nil, Refuse(path, "cannot parse %s as a list item", it)
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
```

Add to `FromRule`:

```go
	case "list-union":
		re, err := regexp.Compile(r.Line)
		if err != nil {
			return nil, fmt.Errorf("list-union: bad line regex %q: %w", r.Line, err)
		}
		return ListUnion{Line: re, Delimiter: r.Delimiter}, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'ListUnion' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. If the removal test reports the wrong side, check which side `Merge3` labels `Trunk`: the second file given to `merge-file` is the base, the third is trunk.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/list_union.go internal/wtsync/list_union_test.go internal/wtsync/strategy.go
git commit -m "feat(sync): the list-union strategy"
git push origin main
```

---

### Task 6: The `openapi` strategy

**Files:**
- Create: `internal/wtsync/openapi.go`
- Create: `internal/wtsync/openapi_test.go`
- Modify: `internal/wtsync/strategy.go` (add the `openapi` case)

**Interfaces:**
- Consumes: `Conflict`, `Refuse` (Task 2); `ValueRule` for `info.version` — but on bare versions, so it uses `MaxPlusPatch` directly and `RuleNamed` only to validate the name (Task 3).
- Produces: `type OpenAPI struct { Rule string }` (`max-plus-patch`, `keep-branch`, `keep-trunk`).

The shape (spec §2): a springdoc-generated document, ~950 KB, where `paths`, `components.schemas` and `tags` are merged **by key** (tags keyed by `name`), `info.version` decided by rule, and everything else must be identical between base and branch, because there is no rule for it; trunk's copy is then the skeleton. Both sides changing the same key differently is a refusal that names the keys. The generator writes two-space JSON with no trailing newline; the strategy writes the same, so the deferred regeneration diffs as little as possible.

Key order matters for that diff, and `encoding/json` maps lose it, so the document is handled as an ordered map of raw values. Equality of values is structural (order-insensitive), so a key both sides regenerated identically is not a conflict.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/openapi_test.go`:

```go
package wtsync

import (
	"encoding/json"
	"strings"
	"testing"
)

// spec builds a document in the generator's shape.
func spec(version, paths, schemas, tags string) string {
	return `{"openapi":"3.1.0","info":{"title":"T","version":"` + version + `"},"servers":[{"url":"/"}],"tags":` + tags +
		`,"paths":` + paths + `,"components":{"schemas":` + schemas + `,"securitySchemes":{"k":{"type":"apiKey"}}}}`
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, data)
	}
	return v
}

func keys(m any) string {
	obj, _ := m.(map[string]any)
	var out []string
	for k := range obj {
		out = append(out, k)
	}
	sortStrings(out)
	return strings.Join(out, ",")
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func TestOpenAPIMergesDisjointPathsSchemasAndTagsAndLiftsTheVersion(t *testing.T) {
	c := conflict(
		spec("2.38.0", `{"/a":{"get":{}}}`, `{"A":{}}`, `[{"name":"TA"}]`),
		spec("2.38.5", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{"A":{},"T":{}}`, `[{"name":"TA"},{"name":"TT"}]`),
		spec("2.38.3", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{"A":{},"B":{}}`, `[{"name":"TA"},{"name":"TB"}]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, out)
	if keys(v["paths"]) != "/a,/b,/t" {
		t.Errorf("paths = %s", keys(v["paths"]))
	}
	if keys(v["components"].(map[string]any)["schemas"]) != "A,B,T" {
		t.Errorf("schemas = %s", keys(v["components"].(map[string]any)["schemas"]))
	}
	var names []string
	for _, tag := range v["tags"].([]any) {
		names = append(names, tag.(map[string]any)["name"].(string))
	}
	sortStrings(names)
	if strings.Join(names, ",") != "TA,TB,TT" {
		t.Errorf("tags = %v", names)
	}
	if v["info"].(map[string]any)["version"] != "2.38.6" {
		t.Errorf("version = %v", v["info"])
	}
}

func TestOpenAPIKeyChangedIdenticallyOnBothSidesIsNotAConflict(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{"x":1}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{"x":1}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"x": 1`) {
		t.Errorf("out = %s", out)
	}
}

func TestOpenAPIDropsAKeyRemovedOnOneSideAndUntouchedOnTheOther(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}},"/old":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{}},"/old":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if keys(decode(t, out)["paths"]) != "/a,/b" {
		t.Errorf("paths = %s", keys(decode(t, out)["paths"]))
	}
}

func TestOpenAPITakesTrunksOtherSectionsAsTheSkeleton(t *testing.T) {
	trunk := `{"openapi":"3.1.0","info":{"title":"T","version":"2.38.5"},"servers":[{"url":"/new"}],"tags":[],"paths":{"/t":{"get":{}}},"components":{"schemas":{},"securitySchemes":{"k":{"type":"apiKey"},"k2":{"type":"http"}}}}`
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), trunk, spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, out)
	if v["servers"].([]any)[0].(map[string]any)["url"] != "/new" {
		t.Errorf("servers = %v", v["servers"])
	}
	if keys(v["components"].(map[string]any)["securitySchemes"]) != "k,k2" {
		t.Errorf("securitySchemes = %s", keys(v["components"].(map[string]any)["securitySchemes"]))
	}
}

func TestOpenAPIRefusesNamingTheKeysBothSidesChanged(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}}}`, `{"S":{"a":1}}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{"x":1}}}`, `{"S":{"a":2}}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{"x":2}}}`, `{"S":{"a":3}}`, `[]`))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "paths: /a") || !strings.Contains(err.Error(), "schemas: S") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAPIRefusesWhenTheBranchChangedOutsideTheMergedSections(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`),
		strings.Replace(spec("2.38.3", `{}`, `{}`, `[]`), `"title":"T"`, `"title":"Changed"`, 1))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "outside") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAPIWritesTwoSpaceJSONWithNoTrailingNewlineAndTrunksKeyOrder(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`), spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "{\n  \"openapi\": \"3.1.0\",\n  \"info\"") || strings.HasSuffix(s, "\n") || !strings.HasSuffix(s, "}") {
		t.Errorf("format: %q ... %q", s[:40], s[len(s)-20:])
	}
	// trunk's path comes first, the branch's addition after it
	if strings.Index(s, `"/t"`) > strings.Index(s, `"/b"`) {
		t.Error("trunk's keys should come first")
	}
}

func TestOpenAPIRefusesInvalidJSON(t *testing.T) {
	good := spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`)
	for name, bad := range map[string]string{
		"syntax":          `{not json`,
		"truncated":       strings.TrimSuffix(good, "}"),
		"trailing":        good + `{"x":1}`,
		"paths not object": strings.Replace(good, `"paths":{"/t":{"get":{}}}`, `"paths":[1]`, 1),
		"tag without name": strings.Replace(good, `"tags":[]`, `"tags":[{"x":1}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), bad, spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
			if _, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c); !IsRefusal(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestOpenAPIComparesNumbersExactly(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"max":9007199254740992}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"max":9007199254740993}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"max":9007199254740994}}`, `{}`, `[]`))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) {
		t.Errorf("two large integers differing in the last digit must not compare equal: %v", err)
	}
}

func TestFromRuleBuildsOpenAPI(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "openapi", Rule: "max-plus-patch"}, "")
	if err != nil || s.Name() != "openapi" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'OpenAPI' 2>&1 | head -3`
Expected: `undefined: OpenAPI`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/openapi.go`:

```go
package wtsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// OpenAPI resolves a generated OpenAPI document by merging its mutable
// sections key by key. It never regenerates: the deferred step declared beside
// it produces the authoritative bytes once, at the end of the rebase. springdoc
// emits paths and schemas in registration order, so this cannot reproduce the
// generator byte for byte; it exists to let the replay continue.
type OpenAPI struct {
	// Rule decides info.version: max-plus-patch, keep-branch or keep-trunk.
	Rule string
}

func (OpenAPI) Name() string { return "openapi" }

func (s OpenAPI) Resolve(c Conflict) ([]byte, error) {
	base, err := parseDoc(c.Path, "base", c.Base)
	if err != nil {
		return nil, err
	}
	trunk, err := parseDoc(c.Path, "trunk", c.Trunk)
	if err != nil {
		return nil, err
	}
	branch, err := parseDoc(c.Path, "branch", c.Branch)
	if err != nil {
		return nil, err
	}

	// Everything the merge does not own must be identical on base and branch.
	// Anything else means the branch edited part of the document this
	// strategy has no rule for, and guessing there is exactly what it must
	// not do.
	if !reflect.DeepEqual(remainder(base), remainder(branch)) {
		return nil, Refuse(c.Path, "the branch changed the document outside paths, schemas, tags and version")
	}

	sections, err := loadSections(c.Path, base, trunk, branch)
	if err != nil {
		return nil, err
	}
	paths, pathConflicts := merge3Keys(sections.paths[0], sections.paths[1], sections.paths[2])
	schemas, schemaConflicts := merge3Keys(sections.schemas[0], sections.schemas[1], sections.schemas[2])
	tags, tagConflicts := merge3Keys(sections.tags[0], sections.tags[1], sections.tags[2])
	var bad []string
	if len(pathConflicts) > 0 {
		bad = append(bad, "paths: "+strings.Join(pathConflicts, ", "))
	}
	if len(schemaConflicts) > 0 {
		bad = append(bad, "schemas: "+strings.Join(schemaConflicts, ", "))
	}
	if len(tagConflicts) > 0 {
		bad = append(bad, "tags: "+strings.Join(tagConflicts, ", "))
	}
	if len(bad) > 0 {
		return nil, Refuse(c.Path, "both sides changed %s", strings.Join(bad, "; "))
	}

	version, err := s.version(c.Path, branch.version(), trunk.version())
	if err != nil {
		return nil, err
	}

	// Trunk is the skeleton: it carries the newest generator output for every
	// section this strategy does not merge, and the guard above proved the
	// branch did not touch any of them.
	out := trunk
	out.set("paths", paths.raw())
	components, err := trunk.components()
	if err != nil {
		return nil, Refuse(c.Path, "trunk: %v", err)
	}
	components.set("schemas", schemas.raw())
	out.set("components", components.raw())
	out.set("tags", tags.values())
	info, err := out.info()
	if err != nil {
		return nil, Refuse(c.Path, "trunk: %v", err)
	}
	info.set("version", mustRaw(version))
	out.set("info", info.raw())
	return format(out.raw())
}

// merged holds the three mergeable sections of base, trunk and branch, in
// that order.
type merged struct {
	paths, schemas, tags [3]*omap
}

// loadSections parses every section the merge touches, on every side, and
// refuses the file on the first malformed one.
func loadSections(path string, sides ...doc) (merged, error) {
	var m merged
	for i, d := range sides {
		side := [...]string{"base", "trunk", "branch"}[i]
		var err error
		if m.paths[i], err = d.section("paths"); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
		if m.schemas[i], err = d.schemas(); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
		if m.tags[i], err = d.tagsByName(); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
	}
	return m, nil
}

func (s OpenAPI) version(path, branchV, trunkV string) (string, error) {
	switch s.Rule {
	case "keep-branch":
		return branchV, nil
	case "keep-trunk":
		return trunkV, nil
	case "max-plus-patch", "":
		if !exactSemverRE.MatchString(branchV) || !exactSemverRE.MatchString(trunkV) {
			return "", Refuse(path, "info.version is not X.Y.Z on both sides (%q, %q)", branchV, trunkV)
		}
		return MaxPlusPatch(branchV, trunkV), nil
	}
	return "", fmt.Errorf("unknown rule %q", s.Rule)
}

// omap is a JSON object that remembers key order, holding raw values.
type omap struct {
	keys []string
	vals map[string]json.RawMessage
}

func newOmap() *omap { return &omap{vals: map[string]json.RawMessage{}} }

func (o *omap) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *omap) get(k string) json.RawMessage { return o.vals[k] }

func (o *omap) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// values renders the map's values as a JSON array, in key order.
func (o *omap) values() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(o.vals[k])
	}
	b.WriteByte(']')
	return b.Bytes()
}

// parseOmap decodes one JSON object, keeping key order. A null or missing
// object decodes to an empty map.
func parseOmap(data json.RawMessage) (*omap, error) {
	o := newOmap()
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, fmt.Errorf("expected an object, got %v", tok)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	// The object must close, and nothing may follow it: a truncated or
	// trailing-garbage document is not one to rewrite.
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, fmt.Errorf("object not closed")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after the document")
	}
	return o, nil
}

// doc is one OpenAPI document with the accessors the merge needs.
type doc struct{ *omap }

func parseDoc(path, side string, data []byte) (doc, error) {
	o, err := parseOmap(data)
	if err != nil {
		return doc{}, Refuse(path, "%s is not a JSON object: %v", side, err)
	}
	return doc{o}, nil
}

// section parses one object-valued key; a malformed section is an error,
// never an empty map, because an empty map would merge as "everything
// deleted".
func (d doc) section(name string) (*omap, error) {
	o, err := parseOmap(d.get(name))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return o, nil
}

func (d doc) components() (*omap, error) { return d.section("components") }
func (d doc) info() (*omap, error)       { return d.section("info") }

func (d doc) schemas() (*omap, error) {
	c, err := d.components()
	if err != nil {
		return nil, err
	}
	o, err := parseOmap(c.get("schemas"))
	if err != nil {
		return nil, fmt.Errorf("components.schemas: %v", err)
	}
	return o, nil
}

// tagsByName keys the tags array by each tag's name, so it merges like a map.
func (d doc) tagsByName() (*omap, error) {
	o := newOmap()
	var tags []json.RawMessage
	if err := json.Unmarshal(orNull(d.get("tags")), &tags); err != nil {
		return nil, fmt.Errorf("tags: %v", err)
	}
	for _, t := range tags {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(t, &named); err != nil || named.Name == "" {
			return nil, fmt.Errorf("tags: an entry has no name")
		}
		o.set(named.Name, t)
	}
	return o, nil
}

func (d doc) version() string {
	var v struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(orNull(d.get("info")), &v)
	return v.Version
}

// remainder is the document with the merged sections and the version
// blanked, as a structure, for order-insensitive comparison.
func remainder(d doc) any {
	decoded, _ := decodeExact(d.raw())
	v, _ := decoded.(map[string]any)
	delete(v, "paths")
	delete(v, "tags")
	if comp, ok := v["components"].(map[string]any); ok {
		delete(comp, "schemas")
	}
	if info, ok := v["info"].(map[string]any); ok {
		info["version"] = nil
	}
	return v
}

// merge3Keys merges three maps key by key. A key changed on one side only
// takes that side's value; a key removed on one side and untouched on the
// other is dropped; a key both sides changed differently is a conflict. The
// result keeps trunk's key order and appends the branch's additions after.
func merge3Keys(base, trunk, branch *omap) (*omap, []string) {
	out := newOmap()
	var conflicts []string
	seen := map[string]bool{}
	all := append(append([]string{}, trunk.keys...), branch.keys...)
	for _, k := range base.keys {
		all = append(all, k)
	}
	for _, k := range all {
		if seen[k] {
			continue
		}
		seen[k] = true
		b, t, r := base.get(k), trunk.get(k), branch.get(k)
		trunkChanged := !jsonEqual(t, b)
		branchChanged := !jsonEqual(r, b)
		switch {
		case trunkChanged && branchChanged && !jsonEqual(t, r):
			conflicts = append(conflicts, k)
		case trunkChanged:
			if t != nil {
				out.set(k, t)
			}
		default:
			if r != nil {
				out.set(k, r)
			}
		}
	}
	sort.Strings(conflicts)
	return out, conflicts
}

// jsonEqual compares two raw values structurally; nil means absent. Numbers
// are compared as their literal text (UseNumber), not as float64, so two
// large integers that differ in the last digit are not "equal".
func jsonEqual(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, errA := decodeExact(a)
	vb, errB := decodeExact(b)
	if errA != nil || errB != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(va, vb)
}

// decodeExact decodes into generic values with numbers kept as json.Number.
func decodeExact(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func orNull(r json.RawMessage) json.RawMessage {
	if r == nil {
		return json.RawMessage("null")
	}
	return r
}

func mustRaw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// format writes the document the way springdoc does: two-space indentation
// and no newline at the end.
func format(raw json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
```

Add to `FromRule`:

```go
	case "openapi":
		if _, err := RuleNamed(r.Rule); err != nil {
			return nil, err
		}
		return OpenAPI{Rule: r.Rule}, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'OpenAPI' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. `json.Indent` escapes nothing and adds no trailing newline, which the format test checks; if the key-order test fails, `merge3Keys` iterated the base's keys before trunk's.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/openapi.go internal/wtsync/openapi_test.go internal/wtsync/strategy.go
git commit -m "feat(sync): the openapi strategy"
git push origin main
```

---

### Task 7: The `script` escape hatch, checked through a temporary index

**Files:**
- Create: `internal/wtsync/script.go`
- Create: `internal/wtsync/script_test.go`
- Modify: `internal/wtsync/strategy.go` (add the `script` case)

**Interfaces:**
- Consumes: `Conflict`, `Refuse` (Task 2); `git.Run` semantics but with an environment and stdin, so this task adds `func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error)` to `internal/wtsync/script.go` (a thin `exec.Command` wrapper; `internal/git.Run` has neither). It also changes `FromRule` to `FromRule(r Rule, root, trunk string)`.
- Produces:
  - `type Script struct { Root, Trunk, Run string }` — `Root` is the main checkout (where git runs), `Trunk` the ref the script is read from (`origin/<trunk>`), `Run` the path of the executable inside the repository. The script's whole directory is materialised from `Trunk` into a temporary directory with `git archive`, so a script may source siblings, and **nothing is ever run from a working tree** (spec §3).
  - `func (s Script) Resolve(c Conflict) ([]byte, error)` — builds a temporary index holding the three stages of `c.Path`, invokes `<run> --check <path>` with `GIT_INDEX_FILE` pointing at it. Exit 0 means the script would resolve it; the resolved bytes are not available at triage time, so `Resolve` returns `nil, nil` meaning "resolvable, content produced by `--resolve` during a real rebase". This plan only classifies; the next plan's `run` invokes `--resolve` against the real index.

The contract, from spec §2: `--claims` (path globs), `--check <file>` (0 resolvable, 1 not mine), `--resolve <file>` (0 resolved and staged, 2 refused with a reason on stderr). Exit 2 from `--check` is a refusal too.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/script_test.go`:

```go
package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeScript commits an executable to origin's main that answers the
// contract by inspecting the index it was given: it exits 0 when stage 2 of
// the file contains "ok", 2 with a reason otherwise, and 1 for a path
// outside its claim. It is committed, not written into the checkout, because
// scripts are read from trunk.
func fakeScript(t *testing.T, local, origin string) string {
	t.Helper()
	body := `#!/usr/bin/env bash
case $1 in
--claims) echo 'special/*.txt' ;;
--check|--resolve)
  [[ $2 == special/* ]] || exit 1
  stages=$(git ls-files -u -- "$2" | awk '{print $3}' | sort -u | tr -d '\n')
  [[ $stages == 123 ]] || { echo "not three-staged: $stages" >&2; exit 2; }
  if git show ":2:$2" | grep -q ok; then exit 0; fi
  echo "trunk side is not ok" >&2; exit 2 ;;
esac
`
	full := filepath.Join(origin, "bin", "conflict", "special")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "bin/conflict/special")
	gitIn(t, origin, "commit", "-q", "-m", "add the special resolver")
	gitIn(t, local, "fetch", "-q", "origin")
	return "bin/conflict/special"
}

func TestScriptChecksThroughATemporaryIndexWithoutTouchingTheRealOne(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	before := gitIn(t, local, "--no-optional-locks", "status", "--porcelain")

	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("ok\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); err != nil {
		t.Fatalf("expected the script to accept, got %v", err)
	}

	c.Trunk = []byte("nope\n")
	_, err := s.Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "trunk side is not ok") {
		t.Errorf("err = %v", err)
	}

	if after := gitIn(t, local, "--no-optional-locks", "status", "--porcelain"); after != before {
		t.Errorf("the real index changed: %q -> %q", before, after)
	}
	if entries := gitIn(t, local, "ls-files", "-u"); entries != "" {
		t.Errorf("real index has unmerged entries: %s", entries)
	}
}

func TestScriptRunsTrunksCopyNotTheCheckouts(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	// a different, always-accepting script in the working tree must be ignored
	full := filepath.Join(local, run)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("nope\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); !IsRefusal(err) {
		t.Errorf("trunk's script refuses this; the checkout's copy must not have run: %v", err)
	}
}

func TestScriptNotMineIsARefusalThatSaysSo(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	_, err := s.Resolve(Conflict{Path: "other/a.txt", Base: []byte("b"), Trunk: []byte("ok"), Branch: []byte("r")})
	if !IsRefusal(err) || !strings.Contains(err.Error(), "does not claim") {
		t.Errorf("err = %v", err)
	}
}

func TestScriptMissingExecutableIsAnErrorNotARefusal(t *testing.T) {
	local, _ := repoWithOrigin(t)
	s := Script{Root: local, Trunk: "origin/main", Run: "bin/conflict/missing"}
	_, err := s.Resolve(Conflict{Path: "x", Base: []byte("b"), Trunk: []byte("t"), Branch: []byte("r")})
	if err == nil || IsRefusal(err) {
		t.Errorf("err = %v, want a hard error", err)
	}
}

func TestFromRuleBuildsScript(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "script", Run: "bin/conflict/x"}, "/repo", "origin/main")
	if err != nil || s.Name() != "script" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'Script' 2>&1 | head -3`
Expected: `undefined: Script`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/script.go`:

```go
package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Script is the escape hatch: an executable in the repository answering the
// three-verb contract. Its directory is materialised from trunk into a
// temporary directory, never run from a working tree, because a feature
// branch must not be able to change what runs. At triage time it is asked
// --check against a temporary index holding the conflict's three stages: the
// script sees exactly those blobs and nothing else of a rebase (no HEAD, no
// other index entries, mode 100644), which is the contract's limit. Resolve
// therefore returns nil bytes on success: the content is produced by
// --resolve during a real rebase, which the next plan performs. A script is
// trusted code from trunk; nothing here sandboxes it.
type Script struct {
	Root  string // the main checkout, where git runs
	Trunk string // the ref the script is read from, e.g. origin/main
	Run   string // the executable's path inside the repository
}

func (Script) Name() string { return "script" }

func (s Script) Resolve(c Conflict) ([]byte, error) {
	exe, cleanupExe, err := materialise(s.Root, s.Trunk, s.Run)
	if err != nil {
		return nil, err
	}
	defer cleanupExe()
	index, cleanup, err := tempIndex(s.Root, c)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cmd := exec.Command(exe, "--check", c.Path)
	cmd.Dir = s.Root
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return nil, Refuse(c.Path, "%s does not claim it", s.Run)
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		return nil, Refuse(c.Path, "%s", strings.TrimSpace(stderr.String()))
	default:
		return nil, fmt.Errorf("%s --check %s: %v: %s", s.Run, c.Path, err, strings.TrimSpace(stderr.String()))
	}
}

// materialise extracts the directory holding run from trunk into a temporary
// directory with git archive, so the script and any sibling it sources come
// from trunk. It returns the executable's path.
func materialise(root, trunk, run string) (exe string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wtsync-script-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	scriptDir := filepath.Dir(run)
	archive := exec.Command("git", "archive", "--format=tar", trunk, scriptDir)
	archive.Dir = root
	tar := exec.Command("tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		cleanup()
		return "", nil, err
	}
	tar.Stdin = pipe
	var stderr bytes.Buffer
	archive.Stderr, tar.Stderr = &stderr, &stderr
	if err := tar.Start(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := archive.Run(); err != nil {
		_ = tar.Wait()
		cleanup()
		return "", nil, fmt.Errorf("script %s is not on %s: %s", run, trunk, strings.TrimSpace(stderr.String()))
	}
	if err := tar.Wait(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extracting %s from %s: %s", scriptDir, trunk, strings.TrimSpace(stderr.String()))
	}
	exe = filepath.Join(dir, run)
	if info, err := os.Stat(exe); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		cleanup()
		return "", nil, fmt.Errorf("script %s is not an executable on %s", run, trunk)
	}
	return exe, cleanup, nil
}

// tempIndex writes a private index holding the three stages of the conflict
// and nothing else. The blobs are written to the object store, which is the
// only side effect, and one git already tolerates.
func tempIndex(root string, c Conflict) (index string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wtsync-index-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	index = filepath.Join(dir, "index")
	var info strings.Builder
	for stage, data := range map[int][]byte{1: c.Base, 2: c.Trunk, 3: c.Branch} {
		oid, err := hashObject(root, data)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		fmt.Fprintf(&info, "100644 %s %d\t%s\n", oid, stage, c.Path)
	}
	if _, err := gitEnv(root, []string{"GIT_INDEX_FILE=" + index}, strings.NewReader(info.String()), "update-index", "--index-info"); err != nil {
		cleanup()
		return "", nil, err
	}
	return index, cleanup, nil
}

// hashObject writes data to the object store and returns its id.
func hashObject(root string, data []byte) (string, error) {
	return gitEnv(root, nil, bytes.NewReader(data), "hash-object", "-w", "--stdin")
}

// gitEnv runs git in dir with extra environment and optional stdin, returning
// trimmed stdout. internal/git.Run has no place for either.
func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
```

`FromRule` grows a third parameter for this: `func FromRule(r Rule, root, trunk string) (Strategy, error)`. Update its definition and every existing call in tests (`FromRule(Rule{...}, "")` becomes `FromRule(Rule{...}, "", "")`), and add:

```go
	case "script":
		return Script{Root: root, Trunk: trunk, Run: r.Run}, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'Script' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. If the first test's "real index" assertion fails, `GIT_INDEX_FILE` did not reach `update-index`; it must be in the environment of that call, not only of the script.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/script.go internal/wtsync/script_test.go internal/wtsync/strategy.go
git commit -m "feat(sync): the script escape hatch, checked through a temporary index"
git push origin main
```

---

### Task 8: The replay simulation and the endpoint merge

**Files:**
- Create: `internal/wtsync/replay.go`
- Create: `internal/wtsync/replay_test.go`

**Interfaces:**
- Consumes: `Conflict` (Task 2); `gitEnv` (Task 7).
- Produces:
  - `type Stop struct { Index, Total int; Commit, Subject string; Conflicts []Conflict; Messages string }` — the first commit a rebase would stop at, 1-based, with the three blobs of every conflicted file, and merge-tree's own messages for conflicts that carry no three blobs (a rename, a mode change, a submodule).
  - `Conflict` gains `Incomplete string`: non-empty when a side is missing (modify/delete) or an entry is not a regular blob; `Assess` refuses such a conflict before any strategy sees it.
  - `type Replay struct { Commits int; Stop *Stop }` — `Stop == nil` means every commit replays cleanly.
  - `func SimulateRebase(mainRoot, onto, branch string) (Replay, error)` — the object-store simulation of spec §1.
  - `func Endpoint(mainRoot, onto, branch string) ([]Conflict, error)` — the conflicts of merging the two tips; nil when clean.
  - `func BehindAhead(mainRoot, onto, branch string) (behind, ahead int, err error)`.

This is the heart of the trust argument. `git rebase` replays the commits `rev-list --right-only --cherry-pick --no-merges onto...branch` selects, one at a time, onto a moving base. `git merge-tree --write-tree --merge-base=<c>^ <onto> <c>` performs exactly one such cherry-pick in the object store; on success its first line is the merged tree, which `git commit-tree` turns into the next base. On conflict, merge-tree exits 1 and prints the tree, then one line per conflicted entry: `<mode> <oid> <stage>\t<path>`, then a blank line, then messages. Stage 1 is the base, 2 is the side rebased onto (trunk), 3 is the commit being replayed (branch), the same orientation a real rebase gives the index.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/replay_test.go`:

```go
package wtsync

import (
	"os"
	"path/filepath"
	"testing"
)

// linearRepo builds: base -> main moves on (trunk edits) ; feature branches
// from base with the given per-commit edits. Each edit is path -> content.
func linearRepo(t *testing.T, trunkEdits []map[string]string, branchEdits []map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	write := func(edits map[string]string) {
		for p, c := range edits {
			full := filepath.Join(dir, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(map[string]string{"a.txt": "a\n", "b.txt": "b\n", "v.txt": "1.0.0\n"})
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "branch", "feature")
	for i, e := range trunkEdits {
		write(e)
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "trunk "+string(rune('1'+i)))
	}
	gitIn(t, dir, "checkout", "-q", "feature")
	for i, e := range branchEdits {
		write(e)
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "branch "+string(rune('1'+i)))
	}
	gitIn(t, dir, "checkout", "-q", "main")
	return dir
}

func TestSimulateRebaseReplaysDisjointCommitsCleanly(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 2 || r.Stop != nil {
		t.Errorf("replay = %+v", r)
	}
}

func TestSimulateRebaseStopsAtTheFirstConflictingCommitWithItsStages(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"v.txt": "1.0.1\n"}, {"v.txt": "1.0.2\n"}})
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 2 || r.Stop.Total != 3 || r.Stop.Subject != "branch 2" {
		t.Fatalf("stop = %+v", r.Stop)
	}
	if len(r.Stop.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", r.Stop.Conflicts)
	}
	c := r.Stop.Conflicts[0]
	if c.Path != "v.txt" || string(c.Base) != "1.0.0\n" || string(c.Trunk) != "1.0.5\n" || string(c.Branch) != "1.0.1\n" {
		t.Errorf("stages = %q %q %q at %s", c.Base, c.Trunk, c.Branch, c.Path)
	}
}

func TestSimulateRebaseSkipsCommitsAlreadyOnTrunkLikeRebaseDoes(t *testing.T) {
	// the branch cherry-picked a trunk commit: rebase drops it, so must the simulation
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "checkout", "-q", "feature")
	trunkTip := gitIn(t, dir, "rev-parse", "main")
	gitIn(t, dir, "cherry-pick", trunkTip)
	gitIn(t, dir, "checkout", "-q", "main")
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 1 || r.Stop != nil {
		t.Errorf("replay = %+v", r)
	}
}

func TestSimulateRebaseLeavesEveryRefAlone(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2\n"}},
		[]map[string]string{{"v.txt": "3\n"}})
	before := gitIn(t, dir, "for-each-ref")
	if _, err := SimulateRebase(dir, "main", "feature"); err != nil {
		t.Fatal(err)
	}
	if after := gitIn(t, dir, "for-each-ref"); after != before {
		t.Errorf("refs changed:\n%s\n->\n%s", before, after)
	}
	if status := gitIn(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("working tree touched: %s", status)
	}
}

func TestEndpointAndBehindAhead(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "a2\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	conflicts, err := Endpoint(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Path != "v.txt" || string(conflicts[0].Trunk) != "1.0.5\n" {
		t.Errorf("endpoint = %+v", conflicts)
	}
	behind, ahead, err := BehindAhead(dir, "main", "feature")
	if err != nil || behind != 2 || ahead != 1 {
		t.Errorf("behind/ahead = %d/%d, %v", behind, ahead, err)
	}
	clean, err := Endpoint(dir, "main", "main")
	if err != nil || clean != nil {
		t.Errorf("clean endpoint = %+v, %v", clean, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'SimulateRebase|Endpoint' 2>&1 | head -3`
Expected: `undefined: SimulateRebase`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/replay.go`:

```go
package wtsync

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Stop is the first commit at which a rebase would stop, with the three
// blobs of every file it conflicts on.
type Stop struct {
	Index     int // 1-based position among the commits the rebase replays
	Total     int
	Commit    string
	Subject   string
	Conflicts []Conflict
}

// Replay is the outcome of simulating a rebase commit by commit.
type Replay struct {
	Commits int
	Stop    *Stop // nil when every commit replays cleanly
}

// simEnv makes the throwaway commits deterministic and free of any signing
// or identity configuration.
var simEnv = []string{
	"GIT_AUTHOR_NAME=wt sync", "GIT_AUTHOR_EMAIL=wt-sync@localhost",
	"GIT_COMMITTER_NAME=wt sync", "GIT_COMMITTER_EMAIL=wt-sync@localhost",
	"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
}

// SimulateRebase replays branch onto `onto` inside the object store, the way
// `git rebase onto` would, and reports the first commit that conflicts. No
// ref, index or working tree is touched; the only objects created are
// unreachable trees and commits.
func SimulateRebase(mainRoot, onto, branch string) (Replay, error) {
	// The same selection and order the rebase sequencer uses: right side
	// only, patch-equivalent commits dropped, merges flattened, topological.
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--reverse", "--topo-order", "--right-only", "--cherry-pick", "--no-merges", onto+"..."+branch)
	if err != nil {
		return Replay{}, err
	}
	var commits []string
	if out != "" {
		commits = strings.Split(out, "\n")
	}
	base, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", onto+"^{commit}")
	if err != nil {
		return Replay{}, err
	}
	for i, c := range commits {
		tree, conflicts, messages, err := mergeTree(mainRoot, c+"^", base, c)
		if err != nil {
			return Replay{}, err
		}
		if conflicts != nil || messages != "" {
			subject, _ := gitEnv(mainRoot, nil, nil, "log", "-1", "--format=%s", c)
			return Replay{Commits: len(commits), Stop: &Stop{
				Index: i + 1, Total: len(commits), Commit: c, Subject: subject, Conflicts: conflicts, Messages: messages,
			}}, nil
		}
		// A commit whose changes are already present replays to the same
		// tree; rebase drops it, so no simulated commit is made for it.
		if baseTree, _ := gitEnv(mainRoot, nil, nil, "rev-parse", base+"^{tree}"); baseTree == tree {
			continue
		}
		base, err = gitEnv(mainRoot, simEnv, nil, "commit-tree", tree, "-p", base, "-m", "wt sync simulation")
		if err != nil {
			return Replay{}, err
		}
	}
	return Replay{Commits: len(commits)}, nil
}

// Endpoint merges the two tips and returns the conflicts, nil when clean. A
// conflict merge-tree reports only in its messages (no three blobs) comes
// back as one Conflict with Incomplete set and the message as its path
// description.
func Endpoint(mainRoot, onto, branch string) ([]Conflict, error) {
	_, conflicts, messages, err := mergeTree(mainRoot, "", onto, branch)
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 && messages != "" {
		conflicts = []Conflict{{Path: "(see messages)", Incomplete: messages}}
	}
	return conflicts, nil
}

// BehindAhead counts the commits branch lacks from onto, and onto from branch.
func BehindAhead(mainRoot, onto, branch string) (behind, ahead int, err error) {
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--left-right", "--count", onto+"..."+branch)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", out)
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return behind, ahead, nil
}

// mergeTree runs one merge in the object store. mergeBase may be "" to let
// git find it. It returns the merged tree and, on conflict, every conflicted
// file with its blobs (a missing stage or a non-blob entry marks the conflict
// Incomplete) and merge-tree's informational messages.
func mergeTree(mainRoot, mergeBase, onto, commit string) (tree string, conflicts []Conflict, messages string, err error) {
	args := []string{"merge-tree", "--write-tree", "-z"}
	if mergeBase != "" {
		args = append(args, "--merge-base="+mergeBase)
	}
	args = append(args, onto, commit)
	cmd := exec.Command("git", args...)
	cmd.Dir = mainRoot
	out, runErr := cmd.Output()
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
			return "", nil, "", fmt.Errorf("git merge-tree: %v: %s", runErr, stderrOf(runErr))
		}
	}
	// With -z the output is NUL-separated: the tree, then for a conflict one
	// record per index entry ("<mode> <oid> <stage>\t<path>"), then an empty
	// record ending the section, then the informational messages.
	records := strings.Split(string(out), "\x00")
	tree = strings.TrimSpace(records[0])
	if runErr == nil {
		return tree, nil, "", nil
	}
	stages := map[string]*Conflict{}
	seenStage := map[string]map[int]bool{}
	var order []string
	i := 1
	for ; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			break
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(fields[2])
		c, seen := stages[path]
		if !seen {
			c = &Conflict{Path: path}
			stages[path] = c
			seenStage[path] = map[int]bool{}
			order = append(order, path)
		}
		seenStage[path][stage] = true
		if fields[0] != "100644" && fields[0] != "100755" {
			c.Incomplete = "not a regular file (mode " + fields[0] + ")"
			continue
		}
		data, err := catFileRaw(mainRoot, fields[1])
		if err != nil {
			return "", nil, "", err
		}
		switch stage {
		case 1:
			c.Base = data
		case 2:
			c.Trunk = data
		case 3:
			c.Branch = data
		}
	}
	if i+1 < len(records) {
		messages = strings.TrimSpace(strings.Join(records[i+1:], "\n"))
	}
	for _, p := range order {
		c := stages[p]
		if c.Incomplete == "" && !(seenStage[p][1] && seenStage[p][2] && seenStage[p][3]) {
			c.Incomplete = "one side deleted or renamed it"
		}
		conflicts = append(conflicts, *c)
	}
	return tree, conflicts, messages, nil
}

// catFileRaw reads a blob without trimming.
func catFileRaw(mainRoot, oid string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", oid)
	cmd.Dir = mainRoot
	return cmd.Output()
}

func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}
```

`catFileRaw` exists because `gitEnv` trims trailing newlines and a blob's ending matters to every strategy.

A modify/delete conflict has fewer than three stages and a rename or submodule conflict carries no usable blobs; both come back with `Incomplete` set, and Task 10 refuses them before any strategy runs. That is the right answer: a file one side deleted is a person's call.

Add this test to `replay_test.go`:

```go
func TestSimulateRebaseMarksAModifyDeleteConflictIncomplete(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}})
	// trunk deletes a.txt after editing it; the branch edits it: modify/delete
	gitIn(t, dir, "rm", "-q", "a.txt")
	gitIn(t, dir, "commit", "-q", "-m", "trunk drops a")
	gitIn(t, dir, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "branch edits a")
	gitIn(t, dir, "checkout", "-q", "main")
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Conflicts) != 1 || r.Stop.Conflicts[0].Incomplete == "" {
		t.Errorf("stop = %+v", r.Stop)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'SimulateRebase|Endpoint' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. If `TestSimulateRebaseStopsAtTheFirstConflictingCommitWithItsStages` reports stages swapped, the argument order to `merge-tree` is wrong: the first branch is the one rebased onto (stage 2), the second is the commit (stage 3).

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/replay.go internal/wtsync/replay_test.go
git commit -m "feat(sync): simulate a rebase commit by commit in the object store"
git push origin main
```

---

### Task 9: Who is working in a worktree

**Files:**
- Create: `internal/wtsync/agents.go`
- Create: `internal/wtsync/agents_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Agent struct { Name, Cwd, State, Kind string }`
  - `func ParseAgents(data []byte) ([]Agent, error)` — the JSON `claude agents --json` prints; finished sessions (`state: done`) are dropped.
  - `func ListAgents() ([]Agent, error)` — runs the command; a missing `claude` binary yields no agents and no error, because "nobody to ask" is a valid answer (spec §1).
  - `func AgentAt(agents []Agent, path string) *Agent` — the session whose cwd is the worktree or inside it.

Measured on 2026-09-08 (spec §1): the JSON is an array of objects with `cwd`, `id`, `kind` (`interactive`/`background`), `name`, `sessionId`, `startedAt`, `state`; `state` is `null` for interactive sessions and `working`, `blocked` or `done` for background ones, and finished sessions stay in the list.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/agents_test.go`:

```go
package wtsync

import "testing"

const agentsJSON = `[
 {"id":"a1","cwd":"/repo_wt/feat_wt/one","kind":"background","name":"fix it","state":"blocked"},
 {"id":"a2","cwd":"/repo_wt/feat_wt/two","kind":"interactive","name":"two-3a","state":null},
 {"id":"a3","cwd":"/repo_wt/feat_wt/three","kind":"background","name":"finished","state":"done"},
 {"id":"a4","cwd":"/repo_wt/feat_wt/two/sub/dir","kind":"interactive","name":"deep","state":null}
]`

func TestParseAgentsDropsFinishedSessions(t *testing.T) {
	agents, err := ParseAgents([]byte(agentsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("agents = %+v", agents)
	}
	for _, a := range agents {
		if a.State == "done" {
			t.Errorf("finished session kept: %+v", a)
		}
	}
}

func TestAgentAtMatchesTheWorktreeOrADirectoryInsideIt(t *testing.T) {
	agents, _ := ParseAgents([]byte(agentsJSON))
	if a := AgentAt(agents, "/repo_wt/feat_wt/one"); a == nil || a.Name != "fix it" {
		t.Errorf("one = %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/two"); a == nil || a.Name != "two-3a" {
		t.Errorf("two = %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/three"); a != nil {
		t.Errorf("three should be empty, got %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/tw"); a != nil {
		t.Errorf("prefix of a path is not inside it, got %+v", a)
	}
}

func TestParseAgentsRejectsGarbage(t *testing.T) {
	if _, err := ParseAgents([]byte("not json")); err == nil {
		t.Error("expected an error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'Agents|AgentAt' 2>&1 | head -3`
Expected: `undefined: ParseAgents`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/agents.go`:

```go
package wtsync

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Agent is a Claude session, from `claude agents --json`. It is enough to
// answer "is a session living in this worktree"; it says nothing about
// Codex, a dev server or a running test, so the dirty check stays the real
// guard (spec §1).
type Agent struct {
	Name  string `json:"name"`
	Cwd   string `json:"cwd"`
	State string `json:"state"`
	Kind  string `json:"kind"`
}

// ParseAgents decodes the listing and drops finished sessions, which stay in
// the list with state "done" and are nobody.
func ParseAgents(data []byte) ([]Agent, error) {
	var raw []Agent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var live []Agent
	for _, a := range raw {
		if a.State != "done" {
			live = append(live, a)
		}
	}
	return live, nil
}

// ListAgents asks claude for its sessions. No claude on the PATH means no
// sessions, not an error: "nobody to ask" is a normal state.
func ListAgents() ([]Agent, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil
	}
	cmd := exec.Command(exe, "agents", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("claude agents --json failed: " + stderrOf(err))
	}
	return ParseAgents(out)
}

// AgentAt returns a session whose working directory is the worktree at path
// or a directory inside it, preferring the shallowest match.
func AgentAt(agents []Agent, path string) *Agent {
	root := filepath.Clean(path)
	var best *Agent
	for i := range agents {
		cwd := filepath.Clean(agents[i].Cwd)
		if cwd != root && !strings.HasPrefix(cwd, root+string(filepath.Separator)) {
			continue
		}
		if best == nil || len(cwd) < len(filepath.Clean(best.Cwd)) {
			best = &agents[i]
		}
	}
	return best
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'Agents|AgentAt' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/agents.go internal/wtsync/agents_test.go
git commit -m "feat(sync): find the claude session living in a worktree"
git push origin main
```

---

### Task 10: Triage — classifying one worktree

**Files:**
- Create: `internal/wtsync/triage.go`
- Create: `internal/wtsync/triage_test.go`

**Interfaces:**
- Consumes: `Config.RuleFor`, `MatchGlob` (Task 1); `Conflict`, `IsRefusal` (Task 2); `FromRule`, `Strategy` (Tasks 3–7); `SimulateRebase`, `Endpoint`, `BehindAhead`, `gitEnv` (Task 8); `Agent`, `AgentAt` (Task 9); `internal/repo.Worktree`.
- Produces:
  - `type Class int` with `Detached, Current, Stale, Clean, Recipe, Contested, Divergent` and `String()` giving the lowercase names of spec §1.
  - `type FileOutcome struct { Path, Strategy, Note string; Resolved bool }` — one conflicted file at the first stop.
  - `type Assessment struct { Path, Branch string; Class Class; Behind, Ahead int; Dirty bool; Agent *Agent; Replay Replay; Files []FileOutcome; Divergent []string; NoConfig bool; Err error }`
  - `func Assess(mainRoot, onto string, cfg *Config, wt repo.Worktree, agents []Agent) Assessment` — `onto` is the ref to rebase onto (`origin/<trunk>` in production; a plain branch in tests); it is also the ref scripts are read from. `cfg` may be nil: the worktree is still classified, nothing is claimed, and `NoConfig` is set. Anything that goes wrong (a status that fails, a strategy that cannot be built, a missing script, a failed endpoint merge) lands in `Err`; it is never silently degraded.

The classes, from spec §1:

| class | condition |
|---|---|
| `detached` | no branch |
| `current` | behind 0 |
| `stale` | ahead 0 |
| `clean` | every commit replays without conflict |
| `recipe` | the first stop's every file is resolved by a strategy |
| `contested` | some file at the first stop is unclaimed or refused |
| `divergent` | at the **endpoint**, the `openapi` strategy refuses a file it claims (generated output that no longer merges); or **both** trunk and the branch changed a `dependency_graph` path beyond an owned line |

`dirty` (tracked changes only) and the agent are modifiers on the assessment, not classes.

- [ ] **Step 1: Write the failing tests**

Create `internal/wtsync/triage_test.go`:

```go
package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/repo"
)

const triageYAML = `
conflicts:
  - paths: [v.txt]
    strategy: owned-line
    line: '^\d'
    rule: max-plus-patch
dependency_graph:
  - build.gradle
  - v.txt
`

func triageCfg(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(triageYAML))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// featureWorktree adds a worktree for the feature branch and returns it. The
// path is a sibling named after the repository, unique per fixture.
func featureWorktree(t *testing.T, dir string) repo.Worktree {
	t.Helper()
	path := dir + "-wt"
	gitIn(t, dir, "worktree", "add", "-q", path, "feature")
	return repo.Worktree{Path: path, Branch: "feature"}
}

func TestAssessClassifiesCurrentStaleAndDetached(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	wt := featureWorktree(t, dir)
	if a := Assess(dir, "main", triageCfg(t), wt, nil); a.Class != Current || a.Ahead != 1 {
		t.Errorf("current: %+v", a)
	}
	dir2 := linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, nil)
	wt2 := featureWorktree(t, dir2)
	if a := Assess(dir2, "main", triageCfg(t), wt2, nil); a.Class != Stale || a.Behind != 1 {
		t.Errorf("stale: %+v", a)
	}
	if a := Assess(dir2, "main", triageCfg(t), repo.Worktree{Path: wt2.Path, Detached: true}, nil); a.Class != Detached {
		t.Errorf("detached: %+v", a)
	}
}

func TestAssessClassifiesCleanRecipeAndContested(t *testing.T) {
	// clean: disjoint edits
	dir := linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if a := Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil); a.Class != Clean || a.Replay.Commits != 1 {
		t.Errorf("clean: %+v", a)
	}
	// recipe: the only conflict is the owned line, which the strategy resolves
	dir = linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	a := Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class != Recipe || len(a.Files) != 1 || !a.Files[0].Resolved || a.Files[0].Strategy != "owned-line" {
		t.Errorf("recipe: %+v", a)
	}
	// contested: an unclaimed file conflicts
	dir = linearRepo(t, []map[string]string{{"a.txt": "trunk\n"}}, []map[string]string{{"a.txt": "branch\n"}})
	a = Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class != Contested || len(a.Files) != 1 || a.Files[0].Resolved || a.Files[0].Note != "unclaimed" {
		t.Errorf("contested: %+v", a)
	}
}

func TestAssessDivergentWhenTheOpenAPIStrategyRefusesAtTheEndpoint(t *testing.T) {
	cfg, err := Parse([]byte("conflicts:\n  - paths: [spec.json]\n    strategy: openapi\n"))
	if err != nil {
		t.Fatal(err)
	}
	// the first stop resolves (disjoint paths); at the endpoint both sides
	// changed the same path differently, which the strategy refuses
	docWith := func(v, paths string) string {
		return `{"openapi":"3.1.0","info":{"title":"T","version":"` + v + `"},"tags":[],"paths":` + paths + `,"components":{"schemas":{}}}`
	}
	dir := linearRepo(t,
		[]map[string]string{{"spec.json": docWith("1.0.5", `{"/a":{"get":{"x":1}},"/t":{"get":{}}}`)}},
		[]map[string]string{{"spec.json": docWith("1.0.1", `{"/a":{"get":{}},"/b":{"get":{}}}`)}, {"spec.json": docWith("1.0.2", `{"/a":{"get":{"x":2}},"/b":{"get":{}}}`)}})
	gitIn(t, dir, "checkout", "-q", "feature")
	gitIn(t, dir, "checkout", "-q", "main")
	a := Assess(dir, "main", cfg, featureWorktree(t, dir), nil)
	if a.Class != Divergent || len(a.Divergent) != 1 || !strings.Contains(a.Divergent[0], "openapi refuses spec.json") {
		t.Errorf("divergent: %+v", a)
	}
}

func TestAssessAnOwnedLineRefusalIsContestedNotDivergent(t *testing.T) {
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\ntrunk-note\n"}},
		[]map[string]string{{"v.txt": "1.0.1\nbranch-note\n"}})
	a := Assess(dir, "main", cfg, featureWorktree(t, dir), nil)
	if a.Class != Contested || len(a.Divergent) != 0 {
		t.Errorf("an ordinary refusal is contested: %+v", a)
	}
}

func TestAssessDivergentWhenBothSidesChangeTheDependencyGraph(t *testing.T) {
	// both sides touch build.gradle (a dependency_graph path)
	dir := linearRepo(t, []map[string]string{{"build.gradle": "trunk deps\n"}, {"a.txt": "a2\n"}}, []map[string]string{{"build.gradle": "branch deps\n"}})
	a := Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class != Divergent || len(a.Divergent) != 1 || !strings.Contains(a.Divergent[0], "both sides") {
		t.Errorf("divergent: %+v", a)
	}
	// the branch alone changing it is routine
	dir = linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"build.gradle": "deps\n"}})
	a = Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class == Divergent {
		t.Errorf("one side alone is not divergence: %+v", a)
	}
	// an owned line inside a dependency-graph file is routine even when trunk moved the graph
	dir = linearRepo(t, []map[string]string{{"build.gradle": "trunk deps\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	a = Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class == Divergent {
		t.Errorf("a version bump alone is not divergence: %+v", a)
	}
}

func TestAssessRefusesAnIncompleteConflictBeforeAnyStrategy(t *testing.T) {
	dir := linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "rm", "-q", "v.txt")
	gitIn(t, dir, "commit", "-q", "-m", "trunk drops v")
	gitIn(t, dir, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(dir, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "branch bumps v")
	gitIn(t, dir, "checkout", "-q", "main")
	a := Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class != Contested || len(a.Files) != 1 || a.Files[0].Resolved || !strings.Contains(a.Files[0].Note, "deleted") {
		t.Errorf("modify/delete must be a person's call: %+v", a)
	}
}

func TestAssessReportsAMissingScriptAsAnError(t *testing.T) {
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/conflict/missing\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	a := Assess(dir, "main", cfg, featureWorktree(t, dir), nil)
	if a.Err == nil || a.Class != Contested {
		t.Errorf("a missing script is an error, not a silent unclaimed: %+v", a)
	}
}

func TestAssessReportsDirtyTrackedChangesAndTheAgent(t *testing.T) {
	dir := linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	wt := featureWorktree(t, dir)
	if err := os.WriteFile(filepath.Join(wt.Path, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := Assess(dir, "main", triageCfg(t), wt, []Agent{{Name: "busy", Cwd: wt.Path}})
	if a.Dirty {
		t.Error("an untracked file is not dirty")
	}
	if a.Agent == nil || a.Agent.Name != "busy" {
		t.Errorf("agent = %+v", a.Agent)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "b.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := Assess(dir, "main", triageCfg(t), wt, nil); !a.Dirty {
		t.Error("a tracked edit is dirty")
	}
}

func TestAssessWithoutConfigClaimsNothing(t *testing.T) {
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	a := Assess(dir, "main", nil, featureWorktree(t, dir), nil)
	if !a.NoConfig || a.Class != Contested || a.Files[0].Note != "unclaimed" {
		t.Errorf("no config: %+v", a)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wtsync/ -run 'Assess' 2>&1 | head -3`
Expected: `undefined: Assess`.

- [ ] **Step 3: Write the implementation**

Create `internal/wtsync/triage.go`:

```go
package wtsync

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Class is what wt sync would do with a worktree.
type Class int

const (
	Detached  Class = iota // no branch: skipped, reported
	Current                // behind 0: nothing to do
	Stale                  // ahead 0: nothing of its own; a lifecycle question
	Clean                  // every commit replays without conflict
	Recipe                 // the first stop is entirely resolved by strategies
	Contested              // something at the first stop is a person's
	Divergent              // the ground moved: a workstream, not a rebase
)

func (c Class) String() string {
	return [...]string{"detached", "current", "stale", "clean", "recipe", "contested", "divergent"}[c]
}

// FileOutcome is one conflicted file at the first stop.
type FileOutcome struct {
	Path     string
	Strategy string // "" when unclaimed
	Resolved bool
	Note     string // "unclaimed", or the refusal's reason
}

// Assessment is everything wt sync knows about one worktree, computed with
// nothing touched.
type Assessment struct {
	Path      string
	Branch    string
	Class     Class
	Behind    int
	Ahead     int
	Dirty     bool // tracked changes; untracked files never block a rebase
	Agent     *Agent
	Replay    Replay
	Files     []FileOutcome
	Divergent []string // why the class is divergent
	NoConfig  bool
	Err       error
}

// Assess classifies one worktree against onto, the ref it would be rebased
// onto. A nil cfg means the repository declared nothing: it is still
// classified, nothing is claimed, and NoConfig says so.
func Assess(mainRoot, onto string, cfg *Config, wt repo.Worktree, agents []Agent) Assessment {
	a := Assessment{Path: wt.Path, Branch: wt.Branch, NoConfig: cfg == nil, Agent: AgentAt(agents, wt.Path)}
	if wt.Detached || wt.Branch == "" {
		a.Class = Detached
		return a
	}
	// --no-optional-locks: a plain status may refresh and rewrite the index,
	// and this command must not touch a worktree.
	out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		a.Err = fmt.Errorf("status: %w", err)
		return a
	}
	a.Dirty = out != ""
	behind, ahead, err := BehindAhead(mainRoot, onto, wt.Branch)
	if err != nil {
		a.Err = err
		return a
	}
	a.Behind, a.Ahead = behind, ahead
	switch {
	case behind == 0:
		a.Class = Current
		return a
	case ahead == 0:
		a.Class = Stale
		return a
	}

	a.Replay, err = SimulateRebase(mainRoot, onto, wt.Branch)
	if err != nil {
		a.Err = err
		return a
	}
	if a.Replay.Stop == nil {
		a.Class = Clean
	} else {
		a.Class = Recipe
		for _, c := range a.Replay.Stop.Conflicts {
			f, err := tryStrategy(mainRoot, onto, cfg, c)
			if err != nil {
				a.Err = err
			}
			if !f.Resolved {
				a.Class = Contested
			}
			a.Files = append(a.Files, f)
		}
		if a.Replay.Stop.Messages != "" && len(a.Files) == 0 {
			a.Class = Contested
			a.Files = append(a.Files, FileOutcome{Path: "(see messages)", Note: a.Replay.Stop.Messages})
		}
	}

	reasons, err := divergence(mainRoot, onto, cfg, wt.Branch)
	if err != nil && a.Err == nil {
		a.Err = err
	}
	a.Divergent = reasons
	if len(a.Divergent) > 0 {
		a.Class = Divergent
	}
	return a
}

// tryStrategy asks the declared strategy whether it resolves the conflict.
// A conflict without three regular blobs is refused before any strategy sees
// it. The error return is for things going wrong — a strategy that cannot be
// built, a script that cannot run — as opposed to a refusal, which is a
// normal outcome carried in the Note.
func tryStrategy(mainRoot, onto string, cfg *Config, c Conflict) (FileOutcome, error) {
	f := FileOutcome{Path: c.Path, Note: "unclaimed"}
	if c.Incomplete != "" {
		f.Note = c.Incomplete
		return f, nil
	}
	if cfg == nil {
		return f, nil
	}
	rule, ok := cfg.RuleFor(c.Path)
	if !ok {
		return f, nil
	}
	f.Strategy = rule.Strategy
	s, err := FromRule(rule, mainRoot, onto)
	if err != nil {
		f.Note = err.Error()
		return f, fmt.Errorf("%s: %w", c.Path, err)
	}
	if _, err := s.Resolve(c); err != nil {
		var r *Refusal
		if errors.As(err, &r) {
			f.Note = r.Reason
			return f, nil
		}
		f.Note = err.Error()
		return f, fmt.Errorf("%s: %w", c.Path, err)
	}
	f.Resolved, f.Note = true, ""
	return f, nil
}

// divergence returns the reasons a branch is a workstream rather than a
// rebase (spec §1): the openapi strategy refusing at the endpoint — generated
// output that no longer merges — or both sides having changed the dependency
// graph beyond a line a strategy owns. An owned-line refusal on ordinary
// configuration is a normal conflict, not divergence.
func divergence(mainRoot, onto string, cfg *Config, branch string) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	var reasons []string
	conflicts, err := Endpoint(mainRoot, onto, branch)
	if err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	for _, c := range conflicts {
		f, err := tryStrategy(mainRoot, onto, cfg, c)
		if err != nil {
			return nil, err
		}
		if f.Strategy == "openapi" && !f.Resolved {
			reasons = append(reasons, fmt.Sprintf("openapi refuses %s at the endpoint: %s", c.Path, f.Note))
		}
	}
	if len(cfg.DependencyGraph) == 0 {
		return reasons, nil
	}
	base, err := gitEnv(mainRoot, nil, nil, "merge-base", onto, branch)
	if err != nil {
		return nil, fmt.Errorf("merge-base: %w", err)
	}
	branchTouched, err := dependencyChanges(mainRoot, cfg, base, branch)
	if err != nil {
		return nil, err
	}
	trunkTouched, err := dependencyChanges(mainRoot, cfg, base, onto)
	if err != nil {
		return nil, err
	}
	if len(branchTouched) > 0 && len(trunkTouched) > 0 {
		reasons = append(reasons, fmt.Sprintf("both sides changed the dependency graph: branch %s; trunk %s",
			strings.Join(branchTouched, ", "), strings.Join(trunkTouched, ", ")))
	}
	return reasons, nil
}

// dependencyChanges lists the dependency_graph paths changed between base
// and rev beyond lines an owned-line rule declares. Paths are read
// NUL-separated so quoted names are not missed.
func dependencyChanges(mainRoot string, cfg *Config, base, rev string) ([]string, error) {
	changed, err := gitEnv(mainRoot, nil, nil, "diff", "--name-only", "-z", base, rev)
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w", base, rev, err)
	}
	var touched []string
	for _, path := range strings.Split(changed, "\x00") {
		if path == "" || !matchesAny(cfg.DependencyGraph, path) {
			continue
		}
		only, err := onlyOwnedLines(mainRoot, cfg, base, rev, path)
		if err != nil {
			return nil, err
		}
		if !only {
			touched = append(touched, path)
		}
	}
	return touched, nil
}

func matchesAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if MatchGlob(p, path) {
			return true
		}
	}
	return false
}

// onlyOwnedLines reports whether every line changed in path between base
// and rev is one an owned-line rule declares for it: a version bump in a
// manifest is routine, a new dependency is not. A change with no textual
// lines at all (binary, mode only) is not "only owned lines".
func onlyOwnedLines(mainRoot string, cfg *Config, base, rev, path string) (bool, error) {
	rule, ok := cfg.RuleFor(path)
	if !ok || rule.Strategy != "owned-line" {
		return false, nil
	}
	re, err := regexp.Compile(rule.Line)
	if err != nil {
		return false, fmt.Errorf("%s: bad line regex: %w", path, err)
	}
	diff, err := gitEnv(mainRoot, nil, nil, "diff", "-U0", base, rev, "--", path)
	if err != nil {
		return false, fmt.Errorf("diff %s: %w", path, err)
	}
	textual := 0
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			textual++
			if !re.MatchString(l[1:]) {
				return false, nil
			}
		}
	}
	return textual > 0, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wtsync/ -run 'Assess' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: all `--- PASS`. `TestAssessDivergentWhenAStrategyRefusesAtTheEndpoint` is the one to watch: at the first stop `v.txt` is version-only and resolves; at the endpoint both sides changed the line beside it, so the strategy refuses and the class flips.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ ; go vet ./... && golangci-lint run ./...
git add internal/wtsync/triage.go internal/wtsync/triage_test.go
git commit -m "feat(sync): classify a worktree from its simulated rebase"
git push origin main
```

---

### Task 11: `wt sync` — the read-only table

**Files:**
- Create: `internal/commands/sync.go`
- Create: `internal/commands/sync_test.go`
- Create: `cmd/wt/sync.go`
- Modify: `cmd/wt/root.go` (register `newSyncCmd()`)
- Modify: `README.md` (one row in the commands table)

**Interfaces:**
- Consumes: `Context` (`ctx.Repo.MainRoot`, `ctx.Repo.Worktrees()`, `ctx.Config.MainBranch`, `ctx.Config.TypeSuffix`); `naming.ParseBranch`; `wtsync.LoadFromTrunk`, `ErrNoConfig`, `ListAgents`, `Assess`, the `Assessment` fields.
- Produces: `func Sync(ctx *Context, w io.Writer) error`.

The table (spec §7): every worktree except the main checkout, its class, behind/ahead, the first stop, who is in it, and what would need a person. `current` rows are not printed (spec §1). It never changes anything. One `git fetch` per repository would serve every worktree, but this plan does not fetch: the table is computed against whatever `origin/<trunk>` is, and says so in its header; fetching belongs to `run` and `keep`.

- [ ] **Step 1: Write the failing test**

Create `internal/commands/sync_test.go`:

```go
package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// syncRepo builds a repository whose origin is itself, so origin/main
// exists, with a declaration on main and two feature worktrees: one that
// conflicts on the declared owned line, and one cut after trunk's last
// commit, so it is current.
// gitOut runs git and returns its stdout; the package's gitIn returns nothing.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

func syncRepo(t *testing.T) *Context {
	t.Helper()
	main := committedRepo(t, minimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".wt-sync.yaml", "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	write("v.txt", "1.0.0\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err := New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	gitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := New(ctx, "feat/other", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	return ctx
}

func TestSyncPrintsTheTriageAndChangesNothing(t *testing.T) {
	ctx := syncRepo(t)
	before := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/heads")
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "bump") || !strings.Contains(out, "recipe") {
		t.Errorf("expected the bump worktree as recipe:\n%s", out)
	}
	if strings.Contains(out, "other") {
		t.Errorf("a current worktree is not printed:\n%s", out)
	}
	if !strings.Contains(out, "1/1") {
		t.Errorf("expected the first stop 1/1:\n%s", out)
	}
	if after := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/heads"); after != before {
		t.Error("sync changed a ref")
	}
	if !strings.Contains(out, "bump") || !strings.Contains(out, "v.txt✓") {
		t.Errorf("the stop column names the stopping commit and the resolved file:\n%s", out)
	}
}

func TestSyncSaysWhenTrunkDeclaresNothing(t *testing.T) {
	main := committedRepo(t, minimalConf)
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(buf.String(), "no .wt-sync.yaml") {
		t.Errorf("expected the no-config notice:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/commands/ -run 'TestSync' 2>&1 | head -3`
Expected: `undefined: Sync`.

- [ ] **Step 3: Write the implementation**

Create `internal/commands/sync.go`:

```go
package commands

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Sync prints what a rebase onto trunk would do to every worktree, computed
// by simulating each rebase in the object store. It changes nothing; the
// verbs that do are separate commands.
func Sync(ctx *Context, w io.Writer) error {
	trunk := ctx.Config.MainBranch
	onto := "origin/" + trunk
	cfg, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, trunk)
	if err != nil && !errors.Is(err, wtsync.ErrNoConfig) {
		return err
	}
	if cfg == nil {
		fmt.Fprintf(w, "%s declares no %s on %s: reported only, never rebased.\n\n",
			ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	agents, err := wtsync.ListAgents()
	if err != nil {
		fmt.Fprintf(w, "note: %v\n", err)
	}

	fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WORK\tCLASS\tBEHIND\tAHEAD\tSTOP\tWHO\tNOTE")
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		a := wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
		if a.Class == wtsync.Current && a.Err == nil {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			workName(ctx, wt.Branch), a.Class, a.Behind, a.Ahead, stopColumn(a), whoColumn(a), noteColumn(a))
	}
	return tw.Flush()
}

func workName(ctx *Context, branch string) string {
	if branch == "" {
		return "(detached)"
	}
	if _, work, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix); ok {
		return work
	}
	return branch
}

// stopColumn is the first commit the rebase stops at, its subject, and what
// happens to its files: `2/12 "record every sync run" SyncWorker.java✗`.
func stopColumn(a wtsync.Assessment) string {
	if a.Replay.Stop == nil {
		return "-"
	}
	parts := []string{fmt.Sprintf("%d/%d %q", a.Replay.Stop.Index, a.Replay.Stop.Total, truncate(a.Replay.Stop.Subject, 32))}
	for _, f := range a.Files {
		mark := "✗"
		if f.Resolved {
			mark = "✓"
		}
		parts = append(parts, shortPath(f.Path)+mark)
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func whoColumn(a wtsync.Assessment) string {
	if a.Agent == nil {
		return "-"
	}
	return a.Agent.Name
}

func noteColumn(a wtsync.Assessment) string {
	var notes []string
	if a.Err != nil {
		notes = append(notes, "error: "+a.Err.Error())
	}
	if a.Dirty {
		notes = append(notes, "dirty")
	}
	for _, f := range a.Files {
		if !f.Resolved && f.Note != "" && f.Note != "unclaimed" {
			notes = append(notes, shortPath(f.Path)+": "+f.Note)
		}
	}
	notes = append(notes, a.Divergent...)
	if a.NoConfig && a.Class != wtsync.Detached {
		notes = append(notes, "no declaration")
	}
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "; ")
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
```

Create `cmd/wt/sync.go`:

```go
package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Show what rebasing each worktree onto trunk would do",
		Long: "Simulate rebasing every worktree onto origin/<trunk> in the object store\n" +
			"and print the outcome: the class, how far behind, the first commit a\n" +
			"rebase would stop at, and whether the repository's declared strategies\n" +
			"resolve it. Nothing is fetched and nothing is changed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Lenient: a repository without worktree.conf still has worktrees
			// worth reporting on, and the trunk name falls back to origin/HEAD.
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ctx := commands.OpenLenient(cwd, cmd.ErrOrStderr())
			if ctx == nil {
				return errors.New("not inside a git repository")
			}
			return commands.Sync(ctx, cmd.OutOrStdout())
		},
	}
}
```

Register it in `cmd/wt/root.go` after `newStatusCmd(),`:

```go
		newSyncCmd(),
```

Add to the README's commands table, after the `wt status` row:

```markdown
| `wt sync` | what rebasing each worktree onto trunk would do, simulated; changes nothing |
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./... 2>&1 | tail -8`
Expected: every package `ok`. Then run it for real, read-only, in a repository that declares nothing and one that does:

```bash
go build -o bin/wt ./cmd/wt && (cd ~/programmering/private/devports && ~/programmering/private/wt/bin/wt sync)
```

Expected: the no-declaration notice and a table of that repository's worktrees.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ cmd/ ; go vet ./... && golangci-lint run ./...
git add internal/commands/sync.go internal/commands/sync_test.go cmd/wt/sync.go cmd/wt/root.go README.md
git commit -m "feat(sync): wt sync prints a simulated, read-only triage"
git push origin main
```

---

## What this plan leaves for the next one

`wt sync run` with safety refs (`refs/wt-sync/<work>/<epoch>`), `--no-update-refs --no-gpg-sign --rerere-autoupdate`, strategies applied against the real index during the rebase (and `script --resolve`), ordered stacks, deferred steps and their commits, `undo`, and `doctor`. Then the plan file and `resume`; then `watch`, `keep`, the protocol and campaigns, each its own plan. The two draft PRs' `.wt-sync.yaml` files are rewritten to the `conflicts:` shape once `wt sync` reads them, and those PRs shrink to one file each.
