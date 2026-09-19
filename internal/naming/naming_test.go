package naming

import (
	"path/filepath"
	"testing"
)

func TestInferTypeReadsTheTypeOutOfTheWorkName(t *testing.T) {
	types := []string{"feat", "fix", "docs", "chore", "test"}
	cases := []struct {
		work     string
		typ      string
		rest     string
		inferred bool
	}{
		{"fix_dev-123", "fix", "dev-123", true},
		{"fix-login-crash", "fix", "login-crash", true},
		{"chore_cleanup", "chore", "cleanup", true},
		{"test", "", "", false},           // the whole name, with nothing left over
		{"fix_", "", "", false},           // ditto, with a separator
		{"review_sentry", "", "", false},  // "review" is not a type
		{"spring-boot-4", "", "", false},  // nor is "spring"
		{"login", "", "", false},          // no separator at all
		{"prefetch_scope", "", "", false}, // a work name that must survive whole
	}
	for _, c := range cases {
		t.Run(c.work, func(t *testing.T) {
			typ, rest, ok := InferType(c.work, types)
			if ok != c.inferred || typ != c.typ || rest != c.rest {
				t.Errorf("InferType(%q) = %q %q %v, want %q %q %v",
					c.work, typ, rest, ok, c.typ, c.rest, c.inferred)
			}
		})
	}
}

func TestParseSpecPrefersAnExplicitTypeOverTheName(t *testing.T) {
	types := []string{"feat", "fix"}
	typ, work, err := ParseSpec("feat/fix_dev-123", "feat", types)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "feat" || work != "fix_dev-123" {
		t.Errorf("an explicit type must win: got %q %q", typ, work)
	}
}

func TestParseSpecInfersTheTypeFromABareName(t *testing.T) {
	typ, work, err := ParseSpec("fix_dev-123", "feat", []string{"feat", "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if typ != "fix" || work != "dev-123" {
		t.Errorf("got %q %q, want fix dev-123", typ, work)
	}
}

func TestSupersetDirInsertsTheRepositoryName(t *testing.T) {
	got := SupersetDir("/src", "demo", "feat", "login", "_wt")
	want := "/src/demo_wt/demo/feat_wt/login"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClassifyNamesTheLayoutAPathFollows(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Layout
	}{
		{"canonical", "/src/demo_wt/feat_wt/login", Canonical},
		{"superset", "/src/demo_wt/demo/feat_wt/login", Superset},
		{"pre-migration", "/src/demo-login", Foreign},
		{"unrelated", "/elsewhere/login", Foreign},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sch := Scheme{Parent: "/src", Repo: "demo", Suffix: "_wt"}
			if got := sch.Classify(c.path, "feat", "login"); got != c.want {
				t.Errorf("Classify(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestSchemeBranch(t *testing.T) {
	sch := Scheme{Suffix: "_wt"}
	if got := sch.Branch("fix", "login-crash"); got != "fix_wt/login-crash" {
		t.Errorf("got %q", got)
	}
}

func TestSchemeParse(t *testing.T) {
	sch := Scheme{Suffix: "_wt"}
	typ, work, ok := sch.Parse("feat_wt/api-tidy")
	if !ok || typ != "feat" || work != "api-tidy" {
		t.Errorf("got %q %q %v", typ, work, ok)
	}
	if _, _, ok := sch.Parse("main"); ok {
		t.Error("plain branch should not parse as a worktree branch")
	}
	if _, _, ok := sch.Parse("feature/x"); ok {
		t.Error("a slash alone is not the worktree convention")
	}
}

// The pair every command that reports a worktree needs: what the branch says
// the work is, and whether the path matches it.
func TestClassifyBranchReadsBranchAndPathTogether(t *testing.T) {
	sch := Scheme{Parent: "/src", Repo: "demo", Suffix: "_wt"}
	typ, work, layout, ok := sch.ClassifyBranch("/src/demo_wt/feat_wt/login", "feat_wt/login")
	if !ok || typ != "feat" || work != "login" || layout != Canonical {
		t.Errorf("got %q %q %v %v", typ, work, layout, ok)
	}
	if _, _, layout, ok := sch.ClassifyBranch("/src/demo_wt/demo/feat_wt/login", "feat_wt/login"); !ok || layout != Superset {
		t.Errorf("superset path: got %v %v", layout, ok)
	}
	// A branch outside the convention leaves the path nothing to be measured
	// against, so it is not reported as misplaced.
	if _, _, layout, ok := sch.ClassifyBranch("/elsewhere/login", "main"); ok || layout != Foreign {
		t.Errorf("unparseable branch: got %v %v", layout, ok)
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
	parent := filepath.Join("/tmp", "code")
	sch := Scheme{Parent: parent, Repo: "infrastructure", Suffix: "_wt"}
	dir := sch.Dir("feat", "api-tidy")
	want := filepath.Join(parent, "infrastructure_wt", "feat_wt", "api-tidy")
	if dir != want {
		t.Fatalf("got %q, want %q", dir, want)
	}
	tail, err := filepath.Rel(filepath.Join(parent, "infrastructure_wt"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch := sch.Branch("feat", "api-tidy"); tail != branch {
		t.Errorf("tail %q != branch %q", tail, branch)
	}
}

func TestParseSpec(t *testing.T) {
	typ, work, err := ParseSpec("fix/login-crash", "feat", []string{"feat", "fix"})
	if err != nil || typ != "fix" || work != "login-crash" {
		t.Errorf("typed spec: got %q %q %v", typ, work, err)
	}
	typ, work, err = ParseSpec("login-crash", "feat", []string{"feat", "fix"})
	if err != nil || typ != "feat" || work != "login-crash" {
		t.Errorf("bare spec should take the default type: got %q %q %v", typ, work, err)
	}
	if _, _, err := ParseSpec("", "feat", []string{"feat", "fix"}); err == nil {
		t.Error("empty spec should error")
	}
	if _, _, err := ParseSpec("a/b/c", "feat", []string{"feat", "fix"}); err == nil {
		t.Error("two slashes should error")
	}
}

// A worktree in Superset's tree has to be recognised by where it sits, not by
// the work it holds: Superset also mints names wt cannot parse, and those are
// exactly the ones somebody wants to migrate.
func TestUnderSuperset(t *testing.T) {
	const parent, repo, suffix = "/p", "myrepo", "_wt"
	cases := []struct {
		path string
		want bool
	}{
		{"/p/myrepo_wt/myrepo/feat_wt/login", true},
		{"/p/myrepo_wt/myrepo/feat_wt/5f2c8e10/local-cache", true},
		{"/p/myrepo_wt/feat_wt/login", false},
		{"/p/myrepo-login", false},
		{"/p/myrepo_wt/myrepo", false},
	}
	for _, c := range cases {
		if got := UnderSuperset(c.path, parent, repo, suffix); got != c.want {
			t.Errorf("UnderSuperset(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// ClassifyPath reads the name out of the directory, for the worktrees whose
// branch carries none — and must not read Superset's tree, which has one
// segment more.
func TestClassifyPath(t *testing.T) {
	s := Scheme{Parent: "/repos", Repo: "demo", Suffix: "_wt"}
	for name, tc := range map[string]struct {
		path           string
		wantType, want string
	}{
		"canonical":          {"/repos/demo_wt/feat_wt/pr-12-fixes", "feat", "pr-12-fixes"},
		"trailing slash":     {"/repos/demo_wt/fix_wt/login-crash/", "fix", "login-crash"},
		"superset's layout":  {"/repos/demo_wt/demo/feat_wt/arch", "", ""},
		"another repository": {"/repos/other_wt/feat_wt/arch", "", ""},
		"the main checkout":  {"/repos/demo", "", ""},
		"one level short":    {"/repos/demo_wt/feat_wt", "", ""},
		"no type suffix":     {"/repos/demo_wt/feat/arch", "", ""},
		"an empty type":      {"/repos/demo_wt/_wt/arch", "", ""},
		"somewhere else":     {"/tmp/demo_wt/feat_wt/arch", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			typ, work, ok := s.ClassifyPath(tc.path)
			if (tc.wantType != "") != ok {
				t.Fatalf("ClassifyPath(%q) ok = %v", tc.path, ok)
			}
			if ok && (typ != tc.wantType || work != tc.want) {
				t.Errorf("ClassifyPath(%q) = %q, %q; want %q, %q", tc.path, typ, work, tc.wantType, tc.want)
			}
		})
	}
}
