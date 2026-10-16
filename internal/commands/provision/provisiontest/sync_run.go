package provisiontest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// RunFixture builds a main checkout that is its own origin; trunk declares v.txt
// owned-line max-plus-patch and, when withDefer, a deferred step that copies
// v.txt to the tracked gen.txt and commits it; worktree bump (feat_wt/bump)
// one commit ahead on v.txt; worktree other (feat_wt/other) current; trunk
// moved on v.txt; origin fetched.
func RunFixture(t *testing.T, withDefer bool) (ctx *kit.Context, bump string) {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"
	if withDefer {
		yaml += "defer:\n  - run: cp v.txt gen.txt\n    paths: [v.txt]\n    commit: \"chore: regen\"\n"
	}
	write(".wt-sync.yaml", yaml)
	write("v.txt", "1.0.0\n")
	write("gen.txt", "stale\n")
	write("a.txt", "a\n")
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err = provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	testkit.GitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := provision.New(ctx, "feat/other", provision.NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

// ContestedFixture is RunFixture's repository with one more file in play:
// the branch's single commit also touches a.txt, which the declaration does
// not claim, and trunk moves a.txt too. Triage sees the whole stop, so the
// class is contested and the run walks into it knowingly.
func ContestedFixture(t *testing.T) (ctx *kit.Context, bump string) {
	t.Helper()
	return Contested(t, false)
}

// Contested is ContestedFixture with or without RunFixture's deferred step.
func Contested(t *testing.T, withDefer bool) (ctx *kit.Context, bump string) {
	t.Helper()
	ctx, bump = RunFixture(t, withDefer)
	main := ctx.Repo.MainRoot
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "--amend", "--no-edit")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "a on trunk")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

// StackFixture adds worktree child (feat_wt/child) on top of feat_wt/bump.
func StackFixture(t *testing.T, ctx *kit.Context) (child string) {
	t.Helper()
	var buf bytes.Buffer
	child, err := provision.New(ctx, "feat/child", provision.NewOptions{NoSetup: true, Base: "feat_wt/bump"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, child, "add", "-A")
	testkit.GitOut(t, child, "commit", "-q", "-m", "child")
	return child
}

// ContestedSibling adds a worktree feat/alpha whose one commit changes a.txt,
// which nothing declares, and moves a.txt on trunk too: alpha is contested
// while bump stays a recipe.
func ContestedSibling(t *testing.T, ctx *kit.Context) (alpha string) {
	t.Helper()
	main := ctx.Repo.MainRoot
	var buf bytes.Buffer
	alpha, err := provision.New(ctx, "feat/alpha", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, alpha, "commit", "-q", "-am", "a on alpha")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "commit", "-q", "-am", "a on trunk")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	return alpha
}

// TwoChildFixture puts two independent children on feat_wt/bump and moves
// a.txt on trunk, so the child that also touches a.txt hits an unclaimed
// conflict on its own commit while its sibling replays cleanly. The names
// put the conflicted one first in the run order, so a whole-stack poison
// would visibly reach the sibling.
func TwoChildFixture(t *testing.T, ctx *kit.Context, main string) (conflicted, clean string) {
	t.Helper()
	var buf bytes.Buffer
	child := func(spec, file, content string) string {
		t.Helper()
		path, err := provision.New(ctx, spec, provision.NewOptions{NoSetup: true, Base: "feat_wt/bump"}, &buf)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		testkit.GitOut(t, path, "add", "-A")
		testkit.GitOut(t, path, "commit", "-q", "-m", spec)
		return path
	}
	conflicted = child("feat/alpha", "a.txt", "alpha\n")
	clean = child("feat/zulu", "c.txt", "c\n")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "a on trunk")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	return conflicted, clean
}
