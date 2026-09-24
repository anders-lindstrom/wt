package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wt up takes what the declared strategies resolve, and says it is wt up.
func TestUpRebasesAWorktreeTheStrategiesResolve(t *testing.T) {
	ctx, bump := runFixture(t, false)
	var out bytes.Buffer
	if err := Up(ctx, bump, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(out.String(), "wt up  onto origin/main") || !strings.Contains(out.String(), "✓ rebased 1 commit") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A conflict that would be yours is refused untouched, and the run fails.
func TestUpRefusesAWorktreeThatWouldHandOver(t *testing.T) {
	ctx, bump := contestedFixture(t)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := Up(ctx, "bump", noAgents(), &out); err == nil {
		t.Fatalf("want a failure:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "would not sync cleanly") || gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Errorf("want it refused and untouched:\n%s", out.String())
	}
}

// The main checkout is trunk's own: there is nothing to bring onto it.
func TestUpRefusesTheMainCheckout(t *testing.T) {
	ctx, _ := runFixture(t, false)
	err := Up(ctx, "/", noAgents(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "is the main checkout") {
		t.Errorf("err = %v", err)
	}
}

// With no .wt-sync.yaml on trunk nothing is declared, so a conflict-free
// rebase goes and a conflicting one is refused untouched.
func TestUpWithoutADeclarationTakesOnlyAConflictFreeRebase(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	main := ctx.Repo.MainRoot
	var buf bytes.Buffer
	free, err := New(ctx, "feat/free", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	clash, err := New(ctx, "feat/clash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", name)
		gitIn(t, dir, "commit", "-q", "-m", name+" in "+filepath.Base(dir))
	}
	write(free, "mine.txt", "mine\n")
	write(clash, "shared.txt", "branch\n")
	write(main, "shared.txt", "trunk\n")
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")

	var out bytes.Buffer
	if err := Up(ctx, "free", noAgents(), &out); err != nil {
		t.Fatalf("a conflict-free rebase goes: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "no .wt-sync.yaml on origin/main") || !gitAncestor(t, free, "origin/main", "HEAD") {
		t.Errorf("not rebased:\n%s", out.String())
	}
	old := gitOut(t, clash, "rev-parse", "HEAD")
	out.Reset()
	if err := Up(ctx, "clash", noAgents(), &out); err == nil || gitOut(t, clash, "rev-parse", "HEAD") != old {
		t.Errorf("a conflict with nothing declared is refused untouched: %v\n%s", err, out.String())
	}
}
