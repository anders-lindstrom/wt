# wt Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `wt` config, discovery and naming core plus the `wt config`, `wt path` and `wt branch` commands, and the compat layer that makes the three Herdr skills work against it.

**Architecture:** A Go binary reads each repo's existing `bin/worktree/worktree.conf` through a bash-subset parser, validates it into a typed `Config`, and answers path/branch questions. All worktree lookup goes through `git worktree list --porcelain`, never path shape. A generated shell compat layer re-exposes the legacy function contract so nothing downstream is edited.

**Tech Stack:** Go 1.26, `github.com/BurntSushi/toml`, `bats-core` for the shell layer, standard `go test`.

**Spec:** `docs/superpowers/specs/2026-08-25-worktree-tool-extraction-design.md`

## Global Constraints

- Module path: `github.com/anders-lindstrom/wt`. Go 1.26.
- Canonical worktree path: `<parent>/<repo><SUFFIX>/<type><SUFFIX>/<work>`, where `<SUFFIX>` is `WORKTREE_TYPE_SUFFIX` (default `_wt`). The path tail below `<repo><SUFFIX>/` is character-for-character the branch name.
- Branch name: `<type><SUFFIX>/<work>`.
- Every worktree lookup goes through `git worktree list --porcelain`. Never infer from path shape.
- An unknown or misspelled config key is an **error**, never silence.
- Retired input keys: `REPO_NAME`, `AWS_SETUP_ENABLED`, `WORKTREE_LAYOUT`. They must still be **emitted** by `wt config --shell`, because Herdr's skills document them as outputs of `load_worktree_config`.
- No `MAIN_BRANCH` default of `development` and no `./gradlew` defaults. `MAIN_BRANCH` is detected from origin HEAD; `BUILD_INIT_COMMAND` and `TEST_COMMAND` are required only when their feature is enabled.
- All commands operate on the repository containing the current directory.
- Every task ends with a commit using Conventional Commits, no AI attribution.

---

### Task 1: Module skeleton and git wrapper

**Files:**
- Create: `go.mod`, `cmd/wt/main.go`, `internal/git/git.go`
- Test: `internal/git/git_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `git.Run(dir string, args ...string) (string, error)` returning trimmed stdout; `git.Lines(dir string, args ...string) ([]string, error)` splitting on newline with no trailing empty; `git.ErrNotRepo` sentinel error.

- [ ] **Step 1: Write the failing test**

```go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestRunReturnsTrimmedOutput(t *testing.T) {
	dir := newRepo(t)
	got, err := Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "main" {
		t.Errorf("got %q, want %q", got, "main")
	}
}

func TestRunOutsideRepoReturnsErrNotRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "rev-parse", "--show-toplevel"); err != ErrNotRepo {
		t.Errorf("got %v, want ErrNotRepo", err)
	}
}

func TestLinesDropsTrailingBlank(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Lines(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("got %d lines %q, want 1", len(lines), lines)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/git/ -run TestRun -v`
Expected: FAIL — package does not compile, `undefined: Run`.

- [ ] **Step 3: Write minimal implementation**

`internal/git/git.go`:

```go
// Package git is a thin wrapper over the git CLI. Every worktree fact wt
// relies on comes from git itself rather than from the shape of a path.
package git

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// ErrNotRepo is returned when a command runs outside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", ErrNotRepo
		}
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Lines runs git and splits stdout into lines, dropping a trailing blank.
func Lines(dir string, args ...string) ([]string, error) {
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
```

`go.mod`:

```
module github.com/anders-lindstrom/wt

go 1.26
```

`cmd/wt/main.go`:

```go
// Command wt manages git worktrees from one implementation, configured per repo.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wt <command> [args...]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "wt: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd internal
git commit -m "feat(git): add git exec wrapper and cli skeleton"
```

---

### Task 2: Bash-subset config parser

**Files:**
- Create: `internal/config/bashconf.go`, `internal/config/testdata/` (copies of all 7 real `worktree.conf` files)
- Test: `internal/config/bashconf_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Value{Scalar string; List []string; IsList bool}`; `config.ParseBash(r io.Reader) (map[string]Value, error)`.

The grammar is exactly what the seven real files use: an optional shebang, `#` comments (including **commented-out assignments that must not parse**), `KEY=bare`, `KEY="quoted"`, `KEY=(a b c)`, and multi-line arrays closed by `)` on its own line.

- [ ] **Step 1: Copy the real config files in as testdata**

```bash
cd ~/programmering/private/wt
mkdir -p internal/config/testdata
for r in telcred/accessmanager telcred/infrastructure telcred/personal-v \
         telcred/residential telcred/server private/longhaul private/recipus; do
  cp ~/programmering/$r/bin/worktree/worktree.conf \
     internal/config/testdata/$(basename $r).conf
done
ls internal/config/testdata
```

Expected: 7 `.conf` files.

- [ ] **Step 2: Write the failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBashScalars(t *testing.T) {
	in := `#!/usr/bin/env bash
# a comment
# REPO_NAME="commented-out"
AWS_SETUP_ENABLED=true
TEST_COMMAND="./gradlew test"
`
	got, err := ParseBash(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	if _, ok := got["REPO_NAME"]; ok {
		t.Error("commented-out assignment was parsed")
	}
	if got["AWS_SETUP_ENABLED"].Scalar != "true" {
		t.Errorf("bare value: got %q", got["AWS_SETUP_ENABLED"].Scalar)
	}
	if got["TEST_COMMAND"].Scalar != "./gradlew test" {
		t.Errorf("quoted value: got %q", got["TEST_COMMAND"].Scalar)
	}
}

func TestParseBashArrays(t *testing.T) {
	in := `DEVELOPER_CONFIG_DIRS=(.cursor .claude .idea)
DEVELOPER_CONFIG_FILES=(
    "override.properties"
    "common/override.properties"
)
`
	got, err := ParseBash(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	dirs := got["DEVELOPER_CONFIG_DIRS"]
	if !dirs.IsList || len(dirs.List) != 3 || dirs.List[0] != ".cursor" {
		t.Errorf("single-line array: got %+v", dirs)
	}
	files := got["DEVELOPER_CONFIG_FILES"]
	if !files.IsList || len(files.List) != 2 || files.List[1] != "common/override.properties" {
		t.Errorf("multi-line array: got %+v", files)
	}
}

func TestParseBashEmptyArray(t *testing.T) {
	got, err := ParseBash(strings.NewReader("DEVELOPER_CONFIG_FILES=()\n"))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	v := got["DEVELOPER_CONFIG_FILES"]
	if !v.IsList || len(v.List) != 0 {
		t.Errorf("got %+v, want empty list", v)
	}
}

// Conformance: the parser must handle all seven real files, not invented samples.
func TestParseBashRealConfigs(t *testing.T) {
	entries, err := filepath.Glob("testdata/*.conf")
	if err != nil || len(entries) != 7 {
		t.Fatalf("want 7 testdata configs, got %d (%v)", len(entries), err)
	}
	for _, path := range entries {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := ParseBash(f)
			if err != nil {
				t.Fatalf("ParseBash: %v", err)
			}
			if got["WORKTREE_BRANCH_PREFIX"].Scalar != "feat_wt" {
				t.Errorf("WORKTREE_BRANCH_PREFIX = %q, want feat_wt",
					got["WORKTREE_BRANCH_PREFIX"].Scalar)
			}
			if got["MAIN_BRANCH"].Scalar == "" {
				t.Error("MAIN_BRANCH missing")
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: ParseBash`.

- [ ] **Step 4: Write minimal implementation**

`internal/config/bashconf.go`:

```go
// Package config loads and validates a repository's worktree configuration.
package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Value is one parsed assignment. A scalar has IsList false; an array has
// IsList true, which is how an explicitly empty array stays distinguishable
// from an absent key.
type Value struct {
	Scalar string
	List   []string
	IsList bool
}

// ParseBash reads the bash subset the existing worktree.conf files use:
// an optional shebang, # comments, KEY=bare, KEY="quoted", KEY=(a b c),
// and multi-line arrays terminated by ) on its own line.
func ParseBash(r io.Reader) (map[string]Value, error) {
	out := map[string]Value{}
	sc := bufio.NewScanner(r)

	var arrayKey string
	var arrayItems []string

	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())

		if arrayKey != "" {
			if strings.HasPrefix(raw, ")") {
				out[arrayKey] = Value{List: arrayItems, IsList: true}
				arrayKey, arrayItems = "", nil
				continue
			}
			arrayItems = append(arrayItems, splitFields(stripComment(raw))...)
			continue
		}

		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}

		key, rest, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: not an assignment: %q", line, raw)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", line, key)
		}

		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "(") {
			body := strings.TrimPrefix(rest, "(")
			if close := strings.Index(body, ")"); close >= 0 {
				items := splitFields(body[:close])
				if items == nil {
					items = []string{}
				}
				out[key] = Value{List: items, IsList: true}
				continue
			}
			arrayKey = key
			arrayItems = splitFields(stripComment(body))
			if arrayItems == nil {
				arrayItems = []string{}
			}
			continue
		}
		out[key] = Value{Scalar: unquote(stripComment(rest))}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if arrayKey != "" {
		return nil, fmt.Errorf("unterminated array for %q", arrayKey)
	}
	return out, nil
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// stripComment removes a trailing # comment that is not inside quotes.
func stripComment(s string) string {
	inSingle, inDouble := false, false
	for i, r := range s {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func splitFields(s string) []string {
	var items []string
	for _, f := range strings.Fields(s) {
		if f = unquote(f); f != "" {
			items = append(items, f)
		}
	}
	return items
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including 7 subtests under `TestParseBashRealConfigs`.

- [ ] **Step 6: Commit**

```bash
git add internal/config
git commit -m "feat(config): parse the bash subset used by worktree.conf"
```

---

### Task 3: Typed config, defaults and validation

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `ParseBash`, `Value` from Task 2.
- Produces:

```go
type Config struct {
	MainBranch           string
	BranchPrefix         string
	TypeSuffix           string
	DefaultType          string
	Types                []string
	DeveloperConfigDirs  []string
	DeveloperConfigFiles []string
	BuildInitEnabled     bool
	BuildInitCommand     string
	RequiredBins         []string
	TestCommand          string
	RunTestsBeforeRemove bool
}

func FromRaw(raw map[string]Value, mainBranchFallback string) (*Config, error)
```

`FromRaw` returns an error listing **every** problem, not just the first.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"strings"
	"testing"
)

func raw(t *testing.T, s string) map[string]Value {
	t.Helper()
	v, err := ParseBash(strings.NewReader(s))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	return v
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "DEVELOPER_CONFIG_FILE=(.env)\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "DEVELOPER_CONFIG_FILE") {
		t.Fatalf("want error naming the key, got %v", err)
	}
}

func TestRetiredKeysExplainThemselves(t *testing.T) {
	for key, want := range map[string]string{
		"REPO_NAME":         "derived",
		"AWS_SETUP_ENABLED": "provision.sh",
		"WORKTREE_LAYOUT":   "no longer configurable",
	} {
		_, err := FromRaw(raw(t, key+"=x\n"), "main")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error mentioning %q, got %v", key, want, err)
		}
	}
}

func TestMainBranchFallsBackToDetected(t *testing.T) {
	c, err := FromRaw(raw(t, ""), "trunk")
	if err != nil {
		t.Fatalf("FromRaw: %v", err)
	}
	if c.MainBranch != "trunk" {
		t.Errorf("MainBranch = %q, want trunk", c.MainBranch)
	}
}

func TestBuildInitCommandRequiredWhenEnabled(t *testing.T) {
	_, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=true\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "BUILD_INIT_COMMAND") {
		t.Fatalf("want required-when-enabled error, got %v", err)
	}
	if _, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=false\n"), "main"); err != nil {
		t.Errorf("disabled build init should not require a command: %v", err)
	}
}

// The rival-fix resolution: a prefix that names no valid type is an error
// naming WORKTREE_DEFAULT_TYPE as the remedy, rather than a silent guess.
func TestPrefixNamingNoValidTypeIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "WORKTREE_BRANCH_PREFIX=wip\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "WORKTREE_DEFAULT_TYPE") {
		t.Fatalf("want error naming the remedy, got %v", err)
	}
}

func TestAllSevenRealConfigsValidate(t *testing.T) {
	for _, path := range mustGlob(t, "testdata/*.conf") {
		t.Run(path, func(t *testing.T) {
			c, err := FromRaw(mustParseFile(t, path), "main")
			if err != nil {
				t.Fatalf("FromRaw: %v", err)
			}
			if c.DefaultType != "feat" {
				t.Errorf("DefaultType = %q, want feat", c.DefaultType)
			}
		})
	}
}
```

Add the two helpers to `bashconf_test.go`:

```go
func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustParseFile(t *testing.T, path string) map[string]Value {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v, err := ParseBash(f)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestUnknown|TestRetired|TestMain|TestBuild|TestPrefix|TestAllSeven' -v`
Expected: FAIL — `undefined: FromRaw`.

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is a repository's validated worktree configuration.
type Config struct {
	MainBranch           string
	BranchPrefix         string
	TypeSuffix           string
	DefaultType          string
	Types                []string
	DeveloperConfigDirs  []string
	DeveloperConfigFiles []string
	BuildInitEnabled     bool
	BuildInitCommand     string
	RequiredBins         []string
	TestCommand          string
	RunTestsBeforeRemove bool
}

// DefaultTypes is the Conventional Commits set plus the two exploratory kinds
// that produce no feature, so a worktree's type and its commits share one
// vocabulary.
var DefaultTypes = []string{
	"feat", "fix", "docs", "style", "refactor", "perf",
	"test", "build", "ci", "chore", "revert", "research", "spike",
}

// retired maps a removed key to the sentence explaining what replaced it.
// These stay recognised so a stale config gets a real answer instead of
// "unknown key".
var retired = map[string]string{
	"REPO_NAME":         "REPO_NAME is retired; the repository name is derived from the main worktree",
	"AWS_SETUP_ENABLED": "AWS_SETUP_ENABLED is retired; put the step in an executable bin/worktree/provision.sh instead",
	"WORKTREE_LAYOUT":   "WORKTREE_LAYOUT is retired; the worktree path shape is no longer configurable",
}

var known = map[string]bool{
	"MAIN_BRANCH": true, "WORKTREE_BRANCH_PREFIX": true, "WORKTREE_TYPE_SUFFIX": true,
	"WORKTREE_DEFAULT_TYPE": true, "WORKTREE_TYPES": true,
	"DEVELOPER_CONFIG_DIRS": true, "DEVELOPER_CONFIG_FILES": true,
	"BUILD_INIT_ENABLED": true, "BUILD_INIT_COMMAND": true,
	"REQUIRED_BINS": true, "TEST_COMMAND": true, "RUN_TESTS_BEFORE_REMOVE": true,
}

// FromRaw validates parsed assignments into a Config, reporting every problem
// at once rather than stopping at the first.
func FromRaw(r map[string]Value, mainBranchFallback string) (*Config, error) {
	var problems []string

	for key := range r {
		if msg, ok := retired[key]; ok {
			problems = append(problems, msg)
			continue
		}
		if !known[key] {
			problems = append(problems, fmt.Sprintf("unknown key %q", key))
		}
	}

	c := &Config{
		MainBranch:   str(r, "MAIN_BRANCH", mainBranchFallback),
		BranchPrefix: str(r, "WORKTREE_BRANCH_PREFIX", "feat_wt"),
		TypeSuffix:   str(r, "WORKTREE_TYPE_SUFFIX", "_wt"),
		Types:        list(r, "WORKTREE_TYPES", DefaultTypes),
		DeveloperConfigDirs: list(r, "DEVELOPER_CONFIG_DIRS",
			[]string{".cursor", ".claude", ".run", ".vscode", ".idea"}),
		DeveloperConfigFiles: list(r, "DEVELOPER_CONFIG_FILES", nil),
		RequiredBins:         list(r, "REQUIRED_BINS", nil),
		BuildInitCommand:     str(r, "BUILD_INIT_COMMAND", ""),
		TestCommand:          str(r, "TEST_COMMAND", ""),
	}

	// Build init defaults to "on if a command was given". Defaulting it to true
	// while BUILD_INIT_COMMAND has no default would make an empty config invalid.
	var err error
	if c.BuildInitEnabled, err = boolean(r, "BUILD_INIT_ENABLED", c.BuildInitCommand != ""); err != nil {
		problems = append(problems, err.Error())
	}
	if c.RunTestsBeforeRemove, err = boolean(r, "RUN_TESTS_BEFORE_REMOVE", false); err != nil {
		problems = append(problems, err.Error())
	}

	c.DefaultType = str(r, "WORKTREE_DEFAULT_TYPE", "")
	if c.DefaultType == "" {
		c.DefaultType = strings.TrimSuffix(c.BranchPrefix, c.TypeSuffix)
	}
	if !contains(c.Types, c.DefaultType) {
		problems = append(problems, fmt.Sprintf(
			"WORKTREE_BRANCH_PREFIX=%q yields default type %q, which is not in WORKTREE_TYPES; set WORKTREE_DEFAULT_TYPE to choose one of: %s",
			c.BranchPrefix, c.DefaultType, strings.Join(c.Types, " ")))
	}

	if c.BuildInitEnabled && c.BuildInitCommand == "" {
		problems = append(problems, "BUILD_INIT_ENABLED is true but BUILD_INIT_COMMAND is not set")
	}
	if c.RunTestsBeforeRemove && c.TestCommand == "" {
		problems = append(problems, "RUN_TESTS_BEFORE_REMOVE is true but TEST_COMMAND is not set")
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("worktree.conf:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
}

func str(r map[string]Value, key, def string) string {
	if v, ok := r[key]; ok && !v.IsList && v.Scalar != "" {
		return v.Scalar
	}
	return def
}

func list(r map[string]Value, key string, def []string) []string {
	v, ok := r[key]
	if !ok {
		return def
	}
	if v.IsList {
		return v.List
	}
	return strings.Fields(v.Scalar)
}

func boolean(r map[string]Value, key string, def bool) (bool, error) {
	v, ok := r[key]
	if !ok || v.IsList || v.Scalar == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v.Scalar)
	if err != nil {
		return def, fmt.Errorf("%s=%q is not a boolean", key, v.Scalar)
	}
	return b, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests — most pass, the conformance test FAILS**

Run: `go test ./internal/config/ -v`
Expected: every test passes **except** `TestAllSevenRealConfigsValidate`, which
fails with "AWS_SETUP_ENABLED is retired" for all seven, plus "REPO_NAME is
retired" for accessmanager and "WORKTREE_LAYOUT is retired" for longhaul and
recipus. That failure is the validator working:
it is the migration signal Plan 3 acts on. Step 5 moves the fixtures to the
post-migration state.

- [ ] **Step 5: Move the fixtures to their post-migration state**

Strip the two retired keys from the copies in `internal/config/testdata/` and
record why in a `testdata/README.md`:

```bash
cd ~/programmering/private/wt
sed -i '' '/^AWS_SETUP_ENABLED=/d; /^REPO_NAME=/d; /^WORKTREE_LAYOUT=/d' internal/config/testdata/*.conf
cat > internal/config/testdata/README.md <<'EOF'
Copies of the seven real worktree.conf files, used as parser and validator
conformance fixtures.

AWS_SETUP_ENABLED, REPO_NAME and WORKTREE_LAYOUT lines are stripped: all three
are retired keys that the validator now rejects by design. The repos themselves are migrated in
Plan 3; these fixtures represent the post-migration state.
EOF
go test ./internal/config/ -v
```

Expected: PASS, all 7 subtests.

- [ ] **Step 6: Commit**

```bash
git add internal/config
git commit -m "feat(config): validate typed config and reject retired keys"
```

---

### Task 4: TOML config loader and the Load entry point

**Files:**
- Create: `internal/config/load.go`
- Modify: `go.mod` (add `github.com/BurntSushi/toml`)
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: `FromRaw` from Task 3.
- Produces: `config.Load(repoRoot, mainBranchFallback string) (*Config, error)`, preferring `bin/worktree/worktree.toml` when present, else `bin/worktree/worktree.conf`; `config.ErrNoConfig` when neither exists.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsBashConf(t *testing.T) {
	root := t.TempDir()
	writeConf(t, root, "worktree.conf", "MAIN_BRANCH=\"development\"\nBUILD_INIT_ENABLED=false\n")
	c, err := Load(root, "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MainBranch != "development" {
		t.Errorf("MainBranch = %q", c.MainBranch)
	}
}

func TestLoadPrefersToml(t *testing.T) {
	root := t.TempDir()
	writeConf(t, root, "worktree.conf", "MAIN_BRANCH=\"from-conf\"\nBUILD_INIT_ENABLED=false\n")
	writeConf(t, root, "worktree.toml", "main_branch = \"from-toml\"\nbuild_init_enabled = false\n")
	c, err := Load(root, "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MainBranch != "from-toml" {
		t.Errorf("MainBranch = %q, want from-toml", c.MainBranch)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	if _, err := Load(t.TempDir(), "main"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("got %v, want ErrNoConfig", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Add the dependency**

```bash
cd ~/programmering/private/wt
go get github.com/BurntSushi/toml@latest
```

- [ ] **Step 4: Write minimal implementation**

`internal/config/load.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ErrNoConfig means the repository declares no worktree configuration.
var ErrNoConfig = errors.New("no bin/worktree/worktree.conf or worktree.toml")

// tomlConfig mirrors Config with snake_case keys. It exists so a repo can opt
// into a typed format without the bash-subset parser, which stays the default
// so no repo has to convert.
type tomlConfig struct {
	MainBranch           string   `toml:"main_branch"`
	BranchPrefix         string   `toml:"worktree_branch_prefix"`
	TypeSuffix           string   `toml:"worktree_type_suffix"`
	DefaultType          string   `toml:"worktree_default_type"`
	Types                []string `toml:"worktree_types"`
	DeveloperConfigDirs  []string `toml:"developer_config_dirs"`
	DeveloperConfigFiles []string `toml:"developer_config_files"`
	BuildInitEnabled     *bool    `toml:"build_init_enabled"`
	BuildInitCommand     string   `toml:"build_init_command"`
	RequiredBins         []string `toml:"required_bins"`
	TestCommand          string   `toml:"test_command"`
	RunTestsBeforeRemove bool     `toml:"run_tests_before_remove"`
}

// Load reads a repository's worktree configuration, preferring worktree.toml.
func Load(repoRoot, mainBranchFallback string) (*Config, error) {
	dir := filepath.Join(repoRoot, "bin", "worktree")

	tomlPath := filepath.Join(dir, "worktree.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return loadTOML(tomlPath, mainBranchFallback)
	}

	confPath := filepath.Join(dir, "worktree.conf")
	f, err := os.Open(confPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw, err := ParseBash(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", confPath, err)
	}
	return FromRaw(raw, mainBranchFallback)
}

func loadTOML(path, mainBranchFallback string) (*Config, error) {
	var tc tomlConfig
	if _, err := toml.DecodeFile(path, &tc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw := map[string]Value{}
	setStr := func(k, v string) {
		if v != "" {
			raw[k] = Value{Scalar: v}
		}
	}
	setList := func(k string, v []string) {
		if v != nil {
			raw[k] = Value{List: v, IsList: true}
		}
	}
	setStr("MAIN_BRANCH", tc.MainBranch)
	setStr("WORKTREE_BRANCH_PREFIX", tc.BranchPrefix)
	setStr("WORKTREE_TYPE_SUFFIX", tc.TypeSuffix)
	setStr("WORKTREE_DEFAULT_TYPE", tc.DefaultType)
	setStr("BUILD_INIT_COMMAND", tc.BuildInitCommand)
	setStr("TEST_COMMAND", tc.TestCommand)
	setList("WORKTREE_TYPES", tc.Types)
	setList("DEVELOPER_CONFIG_DIRS", tc.DeveloperConfigDirs)
	setList("DEVELOPER_CONFIG_FILES", tc.DeveloperConfigFiles)
	setList("REQUIRED_BINS", tc.RequiredBins)
	if tc.BuildInitEnabled != nil {
		raw["BUILD_INIT_ENABLED"] = Value{Scalar: fmt.Sprint(*tc.BuildInitEnabled)}
	}
	if tc.RunTestsBeforeRemove {
		raw["RUN_TESTS_BEFORE_REMOVE"] = Value{Scalar: "true"}
	}
	return FromRaw(raw, mainBranchFallback)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "feat(config): add toml loader and unified load entry point"
```

---

### Task 5: Repository and worktree discovery

**Files:**
- Create: `internal/repo/repo.go`
- Test: `internal/repo/repo_test.go`

**Interfaces:**
- Consumes: `git.Run`, `git.Lines`, `git.ErrNotRepo` from Task 1.
- Produces:

```go
type Worktree struct {
	Path     string // absolute
	Branch   string // "" when detached
	Detached bool
	Bare     bool
	IsMain   bool
}

type Repo struct {
	Name     string // basename of the main worktree
	MainRoot string // absolute path of the main worktree
	Parent   string // directory containing MainRoot
}

func Discover(cwd string) (*Repo, error)
func (r *Repo) Worktrees() ([]Worktree, error)
func (r *Repo) DetectMainBranch() string
```

`Discover` works from the main checkout or any linked worktree. `DetectMainBranch` reads `origin/HEAD`, falling back to the current branch — this is what replaces the old `development` default.

- [ ] **Step 1: Write the failing test**

```go
package repo

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// fixture builds a repo with one commit and one linked worktree.
func fixture(t *testing.T) (parent, main string) {
	t.Helper()
	parent = t.TempDir()
	main = filepath.Join(parent, "demo")
	run(t, parent, "init", "-q", "-b", "main", "demo")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "config", "user.name", "T")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing",
		filepath.Join(parent, "demo_wt", "feat_wt", "thing"))
	return parent, main
}

func TestDiscoverFromMainCheckout(t *testing.T) {
	parent, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if r.Name != "demo" {
		t.Errorf("Name = %q, want demo", r.Name)
	}
	if r.Parent != parent {
		t.Errorf("Parent = %q, want %q", r.Parent, parent)
	}
}

// Discovery must give the same answer from inside a linked worktree, which is
// where agents and wt_cd usually leave you.
func TestDiscoverFromLinkedWorktree(t *testing.T) {
	parent, main := fixture(t)
	r, err := Discover(filepath.Join(parent, "demo_wt", "feat_wt", "thing"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if r.Name != "demo" || r.MainRoot != main {
		t.Errorf("got %+v, want name demo and MainRoot %q", r, main)
	}
}

func TestWorktreesListsMainFirstAndBranches(t *testing.T) {
	_, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	wts, err := r.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(wts))
	}
	if !wts[0].IsMain || wts[0].Branch != "main" {
		t.Errorf("first = %+v, want main worktree on main", wts[0])
	}
	if wts[1].Branch != "feat_wt/thing" {
		t.Errorf("second branch = %q", wts[1].Branch)
	}
}

func TestDiscoverOutsideRepo(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Error("want error outside a repository")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/repo/ -v`
Expected: FAIL — `undefined: Discover`.

- [ ] **Step 3: Write minimal implementation**

`internal/repo/repo.go`:

```go
// Package repo discovers a repository and its worktrees. Every fact here comes
// from git, never from the shape of a path, which is why worktrees created by
// other tools in other layouts resolve just as well as wt's own.
package repo

import (
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Branch   string
	Detached bool
	Bare     bool
	IsMain   bool
}

// Repo is the repository containing some directory.
type Repo struct {
	Name     string
	MainRoot string
	Parent   string
}

// Discover locates the repository containing cwd. It gives the same answer from
// the main checkout or any linked worktree.
func Discover(cwd string) (*Repo, error) {
	out, err := git.Run(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	main := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			main = rest
			break
		}
	}
	if main == "" {
		return nil, git.ErrNotRepo
	}
	return &Repo{
		Name:     filepath.Base(main),
		MainRoot: main,
		Parent:   filepath.Dir(main),
	}, nil
}

// Worktrees lists every worktree of the repository, main first.
func (r *Repo) Worktrees() ([]Worktree, error) {
	out, err := git.Run(r.MainRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			cur.IsMain = len(list) == 0
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// header noise before the first entry
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(
				strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			cur.Bare = true
		}
	}
	flush()
	return list, nil
}

// DetectMainBranch reads origin/HEAD, falling back to the checked-out branch.
// This replaces the old hardcoded "development" default, which was correct for
// exactly one of the seven repositories.
func (r *Repo) DetectMainBranch() string {
	if out, err := git.Run(r.MainRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, branch, ok := strings.Cut(out, "/"); ok && branch != "" {
			return branch
		}
	}
	// symbolic-ref, not rev-parse --abbrev-ref: the latter fails outright on a
	// repository whose HEAD is unborn, which is exactly the state a freshly
	// initialised repo is in.
	if out, err := git.Run(r.MainRoot, "symbolic-ref", "--short", "HEAD"); err == nil && out != "" {
		return out
	}
	return "main"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repo
git commit -m "feat(repo): discover repositories and worktrees through git"
```

---

### Task 6: Path and branch naming

**Files:**
- Create: `internal/naming/naming.go`
- Test: `internal/naming/naming_test.go`

**Interfaces:**
- Consumes: nothing (pure functions).
- Produces:

```go
func BranchName(typ, work, suffix string) string
func ParseBranch(branch, suffix string) (typ, work string, ok bool)
func StripPrefix(branch, suffix string) string
func WorktreeDir(parent, repoName, typ, work, suffix string) string
func ParseSpec(spec, defaultType string) (typ, work string, err error)
```

`ParseSpec` accepts `fix/login-crash` and a bare `login-crash`, which takes `defaultType` — so every existing invocation stays valid.

- [ ] **Step 1: Write the failing test**

```go
package naming

import (
	"path/filepath"
	"testing"
)

func TestBranchName(t *testing.T) {
	if got := BranchName("fix", "login-crash", "_wt"); got != "fix_wt/login-crash" {
		t.Errorf("got %q", got)
	}
}

func TestParseBranch(t *testing.T) {
	typ, work, ok := ParseBranch("feat_wt/webkey_infra", "_wt")
	if !ok || typ != "feat" || work != "webkey_infra" {
		t.Errorf("got %q %q %v", typ, work, ok)
	}
	if _, _, ok := ParseBranch("main", "_wt"); ok {
		t.Error("plain branch should not parse as a worktree branch")
	}
	if _, _, ok := ParseBranch("feature/x", "_wt"); ok {
		t.Error("a slash alone is not the worktree convention")
	}
}

func TestStripPrefix(t *testing.T) {
	if got := StripPrefix("research_wt/caching", "_wt"); got != "caching" {
		t.Errorf("got %q", got)
	}
	if got := StripPrefix("main", "_wt"); got != "main" {
		t.Errorf("non-worktree branch should pass through, got %q", got)
	}
}

// The path tail below <repo>_wt/ must equal the branch, character for
// character. That equality is the whole point of the layout.
func TestWorktreeDirTailEqualsBranch(t *testing.T) {
	parent := filepath.Join("/tmp", "telcred")
	dir := WorktreeDir(parent, "infrastructure", "feat", "webkey_infra", "_wt")
	want := filepath.Join(parent, "infrastructure_wt", "feat_wt", "webkey_infra")
	if dir != want {
		t.Fatalf("got %q, want %q", dir, want)
	}
	tail, err := filepath.Rel(filepath.Join(parent, "infrastructure_wt"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch := BranchName("feat", "webkey_infra", "_wt"); tail != branch {
		t.Errorf("tail %q != branch %q", tail, branch)
	}
}

func TestParseSpec(t *testing.T) {
	typ, work, err := ParseSpec("fix/login-crash", "feat")
	if err != nil || typ != "fix" || work != "login-crash" {
		t.Errorf("typed spec: got %q %q %v", typ, work, err)
	}
	typ, work, err = ParseSpec("login-crash", "feat")
	if err != nil || typ != "feat" || work != "login-crash" {
		t.Errorf("bare spec should take the default type: got %q %q %v", typ, work, err)
	}
	if _, _, err := ParseSpec("", "feat"); err == nil {
		t.Error("empty spec should error")
	}
	if _, _, err := ParseSpec("a/b/c", "feat"); err == nil {
		t.Error("two slashes should error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/naming/ -v`
Expected: FAIL — `undefined: BranchName`.

- [ ] **Step 3: Write minimal implementation**

`internal/naming/naming.go`:

```go
// Package naming converts between a piece of work, its branch and its path.
//
// The layout is <parent>/<repo><suffix>/<type><suffix>/<work>, so the path tail
// below <repo><suffix>/ is character-for-character the branch name. Path and
// branch therefore convert to each other with no rules to remember, and
// `git worktree list` reads identically to the directory tree.
package naming

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// BranchName builds the branch for a piece of work, e.g. fix_wt/login-crash.
func BranchName(typ, work, suffix string) string {
	return typ + suffix + "/" + work
}

// ParseBranch splits a worktree branch into its type and work name. ok is false
// for any branch that does not follow the convention.
func ParseBranch(branch, suffix string) (typ, work string, ok bool) {
	head, rest, found := strings.Cut(branch, "/")
	if !found || rest == "" {
		return "", "", false
	}
	typ, ok = strings.CutSuffix(head, suffix)
	if !ok || typ == "" {
		return "", "", false
	}
	return typ, rest, true
}

// StripPrefix returns the work name, or the branch unchanged when it does not
// follow the convention.
func StripPrefix(branch, suffix string) string {
	if _, work, ok := ParseBranch(branch, suffix); ok {
		return work
	}
	return branch
}

// WorktreeDir returns the canonical absolute path for a piece of work.
func WorktreeDir(parent, repoName, typ, work, suffix string) string {
	return filepath.Join(parent, repoName+suffix, typ+suffix, work)
}

// ParseSpec reads a "<type>/<work>" argument, or a bare "<work>" which takes
// defaultType so that every pre-existing invocation stays valid.
func ParseSpec(spec, defaultType string) (typ, work string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("no work name given")
	}
	if strings.Count(spec, "/") > 1 {
		return "", "", fmt.Errorf("%q has too many slashes; expected <type>/<work> or <work>", spec)
	}
	if head, rest, found := strings.Cut(spec, "/"); found {
		if head == "" || rest == "" {
			return "", "", fmt.Errorf("%q is not a valid <type>/<work>", spec)
		}
		return head, rest, nil
	}
	return defaultType, spec, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/naming
git commit -m "feat(naming): convert between work, branch and worktree path"
```

---

### Task 7: `wt config`, including the legacy shell emission

**Files:**
- Create: `internal/commands/context.go`, `internal/commands/config.go`
- Modify: `cmd/wt/main.go`
- Test: `internal/commands/config_test.go`

**Interfaces:**
- Consumes: `config.Load`, `repo.Discover`, `repo.DetectMainBranch`.
- Produces:

```go
type Context struct {
	Repo   *repo.Repo
	Config *config.Config
}

func Open(cwd string) (*Context, error)
func Config(ctx *Context, shell bool, w io.Writer) error
```

**`--shell` must emit the legacy variable names**, because Herdr's skills document `load_worktree_config` as producing `REPO_NAME`, `MAIN_BRANCH`, `WORKTREE_BRANCH_PREFIX`, `WORKTREE_TYPES`, `WORKTREE_DEFAULT_TYPE`, `REQUIRED_BINS`, `DEVELOPER_CONFIG_DIRS/FILES`, `BUILD_INIT_COMMAND` and `AWS_SETUP_ENABLED`. `REPO_NAME` is emitted as a derived value, and `AWS_SETUP_ENABLED` reports whether `bin/worktree/provision.sh` exists — both retired as *inputs*, both still produced as *outputs*.

- [ ] **Step 1: Write the failing test**

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

func fixtureRepo(t *testing.T, conf string) string {
	t.Helper()
	parent := t.TempDir()
	main := filepath.Join(parent, "demo")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", "demo"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = parent
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	dir := filepath.Join(main, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return main
}

const minimalConf = "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"

func TestConfigShellEmitsLegacyNames(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatalf("Config: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"REPO_NAME='demo'",
		"MAIN_BRANCH='main'",
		"WORKTREE_BRANCH_PREFIX='feat_wt'",
		"WORKTREE_DEFAULT_TYPE='feat'",
		"AWS_SETUP_ENABLED=false",
		"DEVELOPER_CONFIG_DIRS=(",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// provision.sh is what AWS_SETUP_ENABLED became; the compat output has to
// reflect it so Herdr keeps seeing the flag it documents.
func TestConfigShellReportsProvisionScript(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "AWS_SETUP_ENABLED=true") {
		t.Errorf("want AWS_SETUP_ENABLED=true, got:\n%s", buf.String())
	}
}

func TestConfigShellQuotesSafely(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=true\nBUILD_INIT_COMMAND=\"echo it's fine\"\n")
	ctx, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `BUILD_INIT_COMMAND='echo it'\''s fine'`) {
		t.Errorf("apostrophe not escaped:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/commands/ -v`
Expected: FAIL — `undefined: Open`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/context.go`:

```go
// Package commands implements the wt subcommands.
package commands

import (
	"os"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Context is the repository and configuration a subcommand operates on.
type Context struct {
	Repo   *repo.Repo
	Config *config.Config
}

// Open discovers the repository containing cwd and loads its configuration.
func Open(cwd string) (*Context, error) {
	r, err := repo.Discover(cwd)
	if err != nil {
		return nil, err
	}
	c, err := config.Load(r.MainRoot, r.DetectMainBranch())
	if err != nil {
		return nil, err
	}
	return &Context{Repo: r, Config: c}, nil
}

// HasProvisionScript reports whether the repo declares its own setup step.
func (c *Context) HasProvisionScript() bool {
	info, err := os.Stat(filepath.Join(c.Repo.MainRoot, "bin", "worktree", "provision.sh"))
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
```

`internal/commands/config.go`:

```go
package commands

import (
	"fmt"
	"io"
	"strings"
)

// Config prints the resolved configuration. With shell set, it emits
// eval-able assignments using the *legacy* variable names, because the Herdr
// skills document those as the output of load_worktree_config. REPO_NAME and
// AWS_SETUP_ENABLED are retired as inputs but still produced here: the first is
// derived, the second reports whether bin/worktree/provision.sh exists.
func Config(ctx *Context, shell bool, w io.Writer) error {
	c := ctx.Config
	if !shell {
		fmt.Fprintf(w, "repo:          %s\n", ctx.Repo.Name)
		fmt.Fprintf(w, "main root:     %s\n", ctx.Repo.MainRoot)
		fmt.Fprintf(w, "main branch:   %s\n", c.MainBranch)
		fmt.Fprintf(w, "branch prefix: %s\n", c.BranchPrefix)
		fmt.Fprintf(w, "default type:  %s\n", c.DefaultType)
		fmt.Fprintf(w, "types:         %s\n", strings.Join(c.Types, " "))
		fmt.Fprintf(w, "config dirs:   %s\n", strings.Join(c.DeveloperConfigDirs, " "))
		fmt.Fprintf(w, "config files:  %s\n", strings.Join(c.DeveloperConfigFiles, " "))
		fmt.Fprintf(w, "required bins: %s\n", strings.Join(c.RequiredBins, " "))
		fmt.Fprintf(w, "build init:    %v %s\n", c.BuildInitEnabled, c.BuildInitCommand)
		fmt.Fprintf(w, "provision.sh:  %v\n", ctx.HasProvisionScript())
		return nil
	}

	fmt.Fprintf(w, "REPO_NAME=%s\n", shellQuote(ctx.Repo.Name))
	fmt.Fprintf(w, "MAIN_BRANCH=%s\n", shellQuote(c.MainBranch))
	fmt.Fprintf(w, "WORKTREE_BRANCH_PREFIX=%s\n", shellQuote(c.BranchPrefix))
	fmt.Fprintf(w, "WORKTREE_TYPE_SUFFIX=%s\n", shellQuote(c.TypeSuffix))
	fmt.Fprintf(w, "WORKTREE_DEFAULT_TYPE=%s\n", shellQuote(c.DefaultType))
	fmt.Fprintf(w, "WORKTREE_TYPES=%s\n", shellQuote(strings.Join(c.Types, " ")))
	fmt.Fprintf(w, "REQUIRED_BINS=%s\n", shellQuote(strings.Join(c.RequiredBins, " ")))
	fmt.Fprintf(w, "BUILD_INIT_ENABLED=%v\n", c.BuildInitEnabled)
	fmt.Fprintf(w, "BUILD_INIT_COMMAND=%s\n", shellQuote(c.BuildInitCommand))
	fmt.Fprintf(w, "TEST_COMMAND=%s\n", shellQuote(c.TestCommand))
	fmt.Fprintf(w, "RUN_TESTS_BEFORE_REMOVE=%v\n", c.RunTestsBeforeRemove)
	fmt.Fprintf(w, "AWS_SETUP_ENABLED=%v\n", ctx.HasProvisionScript())
	fmt.Fprintf(w, "DEVELOPER_CONFIG_DIRS=(%s)\n", shellQuoteAll(c.DeveloperConfigDirs))
	fmt.Fprintf(w, "DEVELOPER_CONFIG_FILES=(%s)\n", shellQuoteAll(c.DeveloperConfigFiles))
	return nil
}

// shellQuote single-quotes a value so that eval cannot reinterpret it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteAll(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, shellQuote(s))
	}
	return strings.Join(quoted, " ")
}
```

`cmd/wt/main.go` — replace the `switch` with:

```go
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "config":
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		shell := len(os.Args) > 2 && os.Args[2] == "--shell"
		if err := commands.Config(ctx, shell, os.Stdout); err != nil {
			fail(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "wt: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "wt: %v\n", err)
	os.Exit(1)
}
```

Add `"github.com/anders-lindstrom/wt/internal/commands"` to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v && go run ./cmd/wt config --shell`
Expected: PASS. The `go run` fails with a config error only if run outside a configured repo — that is correct behaviour.

- [ ] **Step 5: Commit**

```bash
git add cmd internal/commands
git commit -m "feat(config): add wt config with legacy shell emission"
```

---

### Task 8: `wt path`, `wt branch`, and the Herdr compat layer

**Files:**
- Create: `internal/commands/resolve.go`, `compat/worktree_functions.sh`, `test/compat.bats`
- Modify: `cmd/wt/main.go`
- Test: `internal/commands/resolve_test.go`, `test/compat.bats`

**Interfaces:**
- Consumes: `Context`, `naming.*`, `repo.Worktrees`.
- Produces: `commands.Path(ctx *Context, spec string) (string, error)` and `commands.Branch(ctx *Context, spec string) (string, error)`.

`Path` returns the path of an **existing** worktree whose branch matches, and otherwise the canonical path the work *would* occupy — which is what `get_worktree_path` did, and what lets `new` and `switch` share one answer.

- [ ] **Step 1: Write the failing Go test**

```go
package commands

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBranchAppliesDefaultType(t *testing.T) {
	ctx, err := Open(fixtureRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Branch(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat_wt/login-crash" {
		t.Errorf("got %q", got)
	}
	if got, _ = Branch(ctx, "fix/login-crash"); got != "fix_wt/login-crash" {
		t.Errorf("explicit type: got %q", got)
	}
}

func TestBranchRejectsUnknownType(t *testing.T) {
	ctx, _ := Open(fixtureRepo(t, minimalConf))
	if _, err := Branch(ctx, "wibble/thing"); err == nil {
		t.Error("want error for a type not in WORKTREE_TYPES")
	}
}

func TestPathForNonexistentWorkIsCanonical(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	ctx, _ := Open(main)
	got, err := Path(ctx, "fix/login-crash")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An existing worktree wins over the canonical path, whatever layout it is in.
// This is what keeps the four legacy path shapes working with no migration.
func TestPathPrefersAnExistingWorktreeInAnyLayout(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(main, "config", "user.email", "t@example.com")
	run(main, "config", "user.name", "T")
	run(main, "commit", "-q", "--allow-empty", "-m", "init")
	legacy := filepath.Join(filepath.Dir(main), "demo-oldshape")
	run(main, "worktree", "add", "-q", "-b", "fix_wt/oldshape", legacy)

	ctx, _ := Open(main)
	got, err := Path(ctx, "fix/oldshape")
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Errorf("got %q, want the existing legacy path %q", got, legacy)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/commands/ -run 'TestBranch|TestPath' -v`
Expected: FAIL — `undefined: Branch`.

- [ ] **Step 3: Write minimal implementation**

`internal/commands/resolve.go`:

```go
package commands

import (
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Branch returns the branch name for a work spec, validating the type against
// the repository's WORKTREE_TYPES.
func Branch(ctx *Context, spec string) (string, error) {
	typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType)
	if err != nil {
		return "", err
	}
	if !typeAllowed(ctx, typ) {
		return "", fmt.Errorf("unknown worktree type %q; expected one of: %s",
			typ, strings.Join(ctx.Config.Types, " "))
	}
	return naming.BranchName(typ, work, ctx.Config.TypeSuffix), nil
}

// Path returns where a piece of work lives. An existing worktree on that branch
// wins regardless of its layout, which is what lets worktrees created by other
// tools in older shapes resolve without migration. Otherwise the canonical path
// is returned, so `new` and `switch` agree on one answer.
func Path(ctx *Context, spec string) (string, error) {
	branch, err := Branch(ctx, spec)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	for _, w := range worktrees {
		if w.Branch == branch {
			return w.Path, nil
		}
	}
	typ, work, _ := naming.ParseSpec(spec, ctx.Config.DefaultType)
	return naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix), nil
}

func typeAllowed(ctx *Context, typ string) bool {
	for _, t := range ctx.Config.Types {
		if t == typ {
			return true
		}
	}
	return false
}
```

Extend the `switch` in `cmd/wt/main.go`:

```go
	case "path", "branch":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: wt %s <type>/<work>\n", os.Args[1])
			os.Exit(2)
		}
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		resolve := commands.Path
		if os.Args[1] == "branch" {
			resolve = commands.Branch
		}
		out, err := resolve(ctx, os.Args[2])
		if err != nil {
			fail(err)
		}
		fmt.Println(out)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Write the compat layer**

`compat/worktree_functions.sh`:

```bash
# Compatibility layer: the function contract that Herdr's skills and the
# lindstrom.worktree-setup plugin source by name. Each function is a thin
# wrapper over wt, so the contract survives the implementation moving out of
# bash. Nothing downstream had to be edited to keep working.
#
# A repository's bin/worktree_functions.sh becomes a two-line shim sourcing
# this file.

_wt_require() {
    command -v wt >/dev/null 2>&1 && return 0
    echo "worktree tooling requires 'wt' on PATH — see https://github.com/anders-lindstrom/wt" >&2
    return 1
}

# Sets REPO_NAME, MAIN_BRANCH, WORKTREE_* , DEVELOPER_CONFIG_*, BUILD_INIT_*,
# REQUIRED_BINS, TEST_COMMAND, RUN_TESTS_BEFORE_REMOVE and AWS_SETUP_ENABLED,
# exactly as the old function did.
load_worktree_config() {
    _wt_require || return 1
    eval "$(wt config --shell)" || return 1
}

get_worktree_path()      { _wt_require && wt path "$1"; }
worktree_branch_name()   { _wt_require && wt branch "$1/$2"; }
strip_worktree_prefix()  { _wt_require && wt branch-strip "$1"; }

# The branch actually checked out at a path, which is not derivable from the
# name once the type can vary.
worktree_branch_at() {
    git -C "$1" rev-parse --abbrev-ref HEAD 2>/dev/null
}
```

Add a `branch-strip` case to the `switch` in `cmd/wt/main.go`. It needs the
repository's `TypeSuffix`, so it opens a context like the others:

```go
	case "branch-strip":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: wt branch-strip <branch>")
			os.Exit(2)
		}
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		fmt.Println(naming.StripPrefix(os.Args[2], ctx.Config.TypeSuffix))
```

Add `"github.com/anders-lindstrom/wt/internal/naming"` to the imports.

- [ ] **Step 6: Write the bats test**

`test/compat.bats`:

```bash
#!/usr/bin/env bats

setup() {
    export PATH="$BATS_TEST_DIRNAME/../bin:$PATH"
    REPO="$BATS_TEST_TMPDIR/demo"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/bin/worktree"
    printf 'MAIN_BRANCH="main"\nBUILD_INIT_ENABLED=false\n' > "$REPO/bin/worktree/worktree.conf"
    cd "$REPO"
    source "$BATS_TEST_DIRNAME/../compat/worktree_functions.sh"
}

@test "load_worktree_config sets the legacy variables" {
    load_worktree_config
    [ "$REPO_NAME" = "demo" ]
    [ "$MAIN_BRANCH" = "main" ]
    [ "$WORKTREE_BRANCH_PREFIX" = "feat_wt" ]
    [ "$WORKTREE_DEFAULT_TYPE" = "feat" ]
}

@test "DEVELOPER_CONFIG_DIRS arrives as a real array" {
    load_worktree_config
    [ "${#DEVELOPER_CONFIG_DIRS[@]}" -eq 5 ]
    [ "${DEVELOPER_CONFIG_DIRS[0]}" = ".cursor" ]
}

@test "worktree_branch_name matches the old contract" {
    run worktree_branch_name fix login-crash
    [ "$status" -eq 0 ]
    [ "$output" = "fix_wt/login-crash" ]
}

@test "strip_worktree_prefix returns the work name" {
    run strip_worktree_prefix "research_wt/caching"
    [ "$status" -eq 0 ]
    [ "$output" = "caching" ]
}

@test "get_worktree_path returns the canonical location" {
    run get_worktree_path "fix/login-crash"
    [ "$status" -eq 0 ]
    [[ "$output" == */demo_wt/fix_wt/login-crash ]]
}
```

- [ ] **Step 7: Run the bats suite**

```bash
cd ~/programmering/private/wt
command -v bats >/dev/null || brew install bats-core
mkdir -p bin && go build -o bin/wt ./cmd/wt
bats test/compat.bats
```

Expected: 5 tests pass.

- [ ] **Step 8: Commit**

```bash
printf 'bin/\n' > .gitignore
git add .gitignore cmd internal compat test
git commit -m "feat(resolve): add wt path and branch with herdr compat layer"
```

---

## What this plan delivers

A `wt` binary that reads all seven repositories' real configs, validates them with real errors, and answers path and branch questions — plus a compat layer proven by `bats` to satisfy the contract the Herdr skills and plugin depend on. No repository is modified.

## Follow-on plans

- **Plan 2 — mutating commands:** `wt new`, `setup`, `remove` (+`--me`), `adopt`, `migrate`, `list`, `status`, `doctor`, the Claude Code hook handlers, and `shell/wt.sh` absorbing the `wt_*` functions from dotfiles.
- **Plan 3 — repo migration:** shims into all seven repos, `provision.sh` extracted where `AWS_SETUP_ENABLED` was true, hook registrations repointed, script bodies deleted, `tc-ops:sync-scripts` worktree duty retired.
