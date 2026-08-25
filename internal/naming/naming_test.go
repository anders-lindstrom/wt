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
