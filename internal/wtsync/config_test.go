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
		"unknown strategy":        "conflicts:\n  - paths: [a]\n    strategy: magic\n",
		"owned-line needs line":   "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    rule: keep-branch\n",
		"owned-line needs rule":   "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    line: '^v'\n",
		"unknown rule":            "conflicts:\n  - paths: [a]\n    strategy: owned-line\n    line: '^v'\n    rule: newest\n",
		"list-union needs line":   "conflicts:\n  - paths: [a]\n    strategy: list-union\n",
		"script needs run":        "conflicts:\n  - paths: [a]\n    strategy: script\n",
		"a rule needs paths":      "conflicts:\n  - strategy: take-trunk\n",
		"defer needs run":         "defer:\n  - paths: [a]\n",
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
