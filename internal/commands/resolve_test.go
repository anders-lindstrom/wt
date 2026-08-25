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
// This is what keeps the legacy path shapes working with no migration —
// including Superset's <repo>_wt/<repo>/<type>_wt/<work>.
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

// Superset's own shape must resolve too, since it is hardcoded in that tool.
func TestPathResolvesSupersetShape(t *testing.T) {
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
	superset := filepath.Join(filepath.Dir(main), "demo_wt", "demo", "feat_wt", "arch")
	run(main, "worktree", "add", "-q", "-b", "feat_wt/arch", superset)

	ctx, _ := Open(main)
	got, err := Path(ctx, "feat/arch")
	if err != nil {
		t.Fatal(err)
	}
	if got != superset {
		t.Errorf("got %q, want superset shape %q", got, superset)
	}
}
