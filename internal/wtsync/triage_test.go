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
	// spec.json must already exist in the base commit: a path absent from
	// base that both trunk and the branch create independently is an
	// add/add conflict with no base blob, which Assess correctly refuses as
	// Incomplete before any strategy runs - the openapi strategy would never
	// see it. linearRepo's base has no spec.json, so this fixture is built
	// by hand.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	writeSpec := func(v, paths string) {
		if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(docWith(v, paths)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSpec("1.0.0", `{"/a":{"get":{}}}`)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "branch", "feature")

	writeSpec("1.0.5", `{"/a":{"get":{"x":1}},"/t":{"get":{}}}`)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "trunk 1")

	gitIn(t, dir, "checkout", "-q", "feature")
	writeSpec("1.0.1", `{"/a":{"get":{}},"/b":{"get":{}}}`) // disjoint from trunk's edit: the first stop resolves
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "branch 1")
	writeSpec("1.0.2", `{"/a":{"get":{"x":2}},"/b":{"get":{}}}`) // /a now collides with trunk's edit at the endpoint
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "branch 2")
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

func TestAssessNotesWhenBothSidesChangeTheDependencyGraph(t *testing.T) {
	// both sides touch build.gradle (a dependency_graph path): on a
	// long-lived branch this is common and does not by itself distinguish a
	// workstream from an ordinary rebase, so it is a note, never a reason.
	dir := linearRepo(t, []map[string]string{{"build.gradle": "trunk deps\n"}, {"a.txt": "a2\n"}}, []map[string]string{{"build.gradle": "branch deps\n"}})
	a := Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if a.Class == Divergent || len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "both sides") {
		t.Errorf("noted, not divergent: %+v", a)
	}
	// the branch alone changing it is routine
	dir = linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"build.gradle": "deps\n"}})
	a = Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if len(a.Notes) != 0 {
		t.Errorf("one side alone is not worth a note: %+v", a)
	}
	// an owned line inside a dependency-graph file is routine even when trunk moved the graph
	dir = linearRepo(t, []map[string]string{{"build.gradle": "trunk deps\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	a = Assess(dir, "main", triageCfg(t), featureWorktree(t, dir), nil)
	if len(a.Notes) != 0 {
		t.Errorf("a version bump alone is not worth a note: %+v", a)
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
	if !a.NoConfig || a.Class != Contested || len(a.Files) != 1 || a.Files[0].Note != "unclaimed" {
		t.Errorf("no config: %+v", a)
	}
}

func TestAssessReportsUnknownWhenTheWorktreePathDoesNotExist(t *testing.T) {
	// Every early error return must leave Class at its zero value, Unknown,
	// rather than reading as some specific (wrong) class - Detached used to
	// be the zero value, which would have misreported this as detached.
	dir := linearRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	wt := repo.Worktree{Path: filepath.Join(dir, "does-not-exist"), Branch: "feature"}
	a := Assess(dir, "main", triageCfg(t), wt, nil)
	if a.Err == nil || a.Class != Unknown {
		t.Errorf("missing worktree: %+v", a)
	}
}
