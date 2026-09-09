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
		{"test", "", "", false},            // the whole name, with nothing left over
		{"fix_", "", "", false},            // ditto, with a separator
		{"review_sentry", "", "", false},   // "review" is not a type
		{"spring-boot-4", "", "", false},   // nor is "spring"
		{"webkey", "", "", false},          // no separator at all
		{"statepush_scope", "", "", false}, // a real work name that must survive
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
			if got := Classify(c.path, "/src", "demo", "feat", "login", "_wt"); got != c.want {
				t.Errorf("Classify(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

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
