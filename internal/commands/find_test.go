package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// committedRepoIn builds a repo with one commit inside a chosen parent, so a
// test can control which root the scan will find it under.
func committedRepoIn(t *testing.T, parent, name, conf string) string {
	t.Helper()
	main := filepath.Join(parent, name)
	gitIn(t, parent, "init", "-q", "-b", "main", name)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	dir := filepath.Join(main, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-qm", "init")
	return main
}

// A strong local hit ends the search.
func TestFindStrongLocalHitWins(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	ctx, _ := kit.Open(committedRepo(t, minimalConf))
	if _, err := New(ctx, "feat/opensearch_ism", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	got, err := kit.Find(ctx, "opensearch_ism")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0].Tier != 1 {
		t.Fatalf("want one exact local hit, got %+v", got)
	}
}

// The bug that started all this: standing in a repo where "arch" only appears
// as a substring of "opensearch", an exact match in another repo must win.
func TestFindWeakLocalHitStillScansRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)

	local := committedRepoIn(t, root, "local", minimalConf)
	lctx, _ := kit.Open(local)
	if _, err := New(lctx, "feat/opensearch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	other := committedRepoIn(t, root, "other", minimalConf)
	octx, _ := kit.Open(other)
	if _, err := New(octx, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}

	got, err := kit.Find(lctx, "arch")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one winner, got %d: %+v", len(got), got)
	}
	if got[0].Work != "arch" || got[0].Repo != "other" {
		t.Errorf("the exact match in another repo must win, got %+v", got[0])
	}
}

// <repository>/<work> names a worktree outright. Another worktree whose
// <type>/<work> merely starts with the same letters must not take its place.
func TestFindRepoAndWorkBeatsTheStartOfATypedName(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)
	ctx, _ := kit.Open(committedRepoIn(t, root, "feat", minimalConf))
	for _, spec := range []string{"fix/arch", "feat/archive"} {
		if _, err := New(ctx, spec, NewOptions{NoSetup: true}, os.Stderr); err != nil {
			t.Fatal(err)
		}
	}
	// A fragment of a branch is still read whole, as it always was: this one
	// is also repository feat, work arc.
	gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "refeat/arcade",
		filepath.Join(root, "feat_wt", "fix_wt", "borrowed"))
	if _, err := New(ctx, "fix/arc", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	for pattern, want := range map[string]string{
		"feat/arch":    "arch",     // repository feat, work arch
		"feat/archive": "archive",  // the typed name, exactly
		"feat/archi":   "archive",  // the start of it, and no work is named archi
		"feat/arc":     "borrowed", // in refeat/arcade
	} {
		got, err := kit.Find(ctx, pattern)
		if err != nil {
			t.Fatalf("Find(%q): %v", pattern, err)
		}
		if len(got) != 1 || got[0].Work != want {
			t.Errorf("Find(%q) = %+v, want only %s", pattern, got, want)
		}
	}
}

// The start of a typed name is the weakest reading there is: a pattern that
// found a worktree in another repository before still finds it, however
// loosely it matched there.
func TestFindTheStartOfATypedNameYieldsToAnyOtherMatch(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)
	lctx, _ := kit.Open(committedRepoIn(t, root, "demo", minimalConf))
	if _, err := New(lctx, "feat/archive", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	octx, _ := kit.Open(committedRepoIn(t, root, "feat", minimalConf))
	if _, err := New(octx, "fix/a-r-c", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	got, err := kit.Find(lctx, "feat/arc")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0].Work != "a-r-c" || got[0].Repo != "feat" {
		t.Errorf("Find(feat/arc) = %+v, want a-r-c in repository feat", got)
	}
}

func TestFindReportsEveryTiedCandidate(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)
	for _, name := range []string{"one", "two"} {
		r := committedRepoIn(t, root, name, minimalConf)
		c, _ := kit.Open(r)
		if _, err := New(c, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
			t.Fatal(err)
		}
	}
	got, err := kit.Find(nil, "arch")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want both repos' arch, got %d: %+v", len(got), got)
	}
}

// A repository whose configuration does not load still has worktrees worth
// searching. Losing repo-first silently — which is what happens if the caller
// just discards the config error — makes wt find return a surprising answer
// with no hint why.
func TestOpenLenientSurvivesInvalidConfig(t *testing.T) {
	main := committedRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\nAWS_SETUP_ENABLED=true\n")
	if _, err := kit.Open(main); err == nil {
		t.Fatal("this fixture is supposed to have an invalid config")
	}

	var warn strings.Builder
	ctx := kit.OpenLenient(main, &warn)
	if ctx == nil {
		t.Fatal("OpenLenient must still produce a context")
	}
	if ctx.Repo.Name != "demo" {
		t.Errorf("repo = %q", ctx.Repo.Name)
	}
	if ctx.Config.TypeSuffix != "_wt" {
		t.Errorf("want the default suffix, got %q", ctx.Config.TypeSuffix)
	}
	if !strings.Contains(warn.String(), "AWS_SETUP_ENABLED") {
		t.Errorf("the config problem must be reported, not swallowed:\n%s", warn.String())
	}
}

// With a broken local config, a strong local hit must still beat a match in
// another repository.
func TestFindKeepsRepoFirstWithInvalidLocalConfig(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)

	other := committedRepoIn(t, root, "other", minimalConf)
	octx, _ := kit.Open(other)
	if _, err := New(octx, "feat/login", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}

	local := committedRepoIn(t, root, "local", minimalConf)
	lctx, _ := kit.Open(local)
	if _, err := New(lctx, "feat/login-crash", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	// Now break the local config the way an unmigrated repo is broken.
	if err := os.WriteFile(filepath.Join(local, "bin", "worktree", "worktree.conf"),
		[]byte(minimalConf+"AWS_SETUP_ENABLED=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var warn strings.Builder
	got, err := kit.Find(kit.OpenLenient(local, &warn), "login")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0].Work != "login-crash" {
		t.Errorf("the local prefix match must win, got %+v", got)
	}
}

// "/" means the repository you are in — its main checkout — and so does an
// empty pattern, which is what a bare `wt cd` sends. Getting back to the base
// repo is the most common jump there is, and it should not need the repo's
// name.
func TestFindRootResolvesToTheMainCheckout(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	if _, err := New(ctx, "feat/somewhere", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}

	// From inside a linked worktree, "/" must still mean the main checkout.
	inner, err := kit.Open(filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "somewhere"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"/", ""} {
		got, err := kit.Find(inner, pattern)
		if err != nil {
			t.Fatalf("Find(%q): %v", pattern, err)
		}
		if len(got) != 1 || got[0].Path != main || got[0].Work != "demo" {
			t.Fatalf("Find(%q): want the main checkout %q, got %+v", pattern, main, got)
		}
	}
}

// "." is the worktree you are standing in, as it is for every other command:
// from its root or anywhere below, and from the main checkout the main
// checkout, which find may answer for.
func TestFindDotResolvesToTheWorktreeYouStandIn(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	if _, err := New(ctx, "feat/somewhere", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "somewhere")
	for _, sub := range []string{filepath.Join(wt, "src", "deep"), filepath.Join(main, "bin")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for cwd, want := range map[string]string{
		wt:                               wt,
		filepath.Join(wt, "src", "deep"): wt,
		main:                             main,
		filepath.Join(main, "bin"):       main,
	} {
		ctx, err := kit.Open(cwd)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range []string{".", "./"} {
			got, err := kit.Find(ctx, pattern)
			if err != nil {
				t.Fatalf("Find(%q) from %s: %v", pattern, cwd, err)
			}
			if len(got) != 1 || got[0].Path != want {
				t.Errorf("Find(%q) from %s = %+v, want %q", pattern, cwd, got, want)
			}
		}
	}
	inner, _ := kit.Open(wt)
	if got, _ := kit.Find(inner, "."); len(got) != 1 || got[0].Work != "somewhere" {
		t.Errorf("want the worktree's own work name, got %+v", got)
	}
}

// A directory in no worktree of the repository is the error Locate gives for
// ".", not a silent miss.
func TestFindDotFromOutsideEveryWorktreeSaysSo(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	ctx := locatable(t, "fix/login-crash")
	ctx.Cwd = t.TempDir()

	_, err := kit.Find(ctx, ".")
	if err == nil {
		t.Fatal("want an error from outside every worktree")
	}
	for _, want := range []string{ctx.Cwd, "none of demo's", "wt list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should say %q, got: %v", want, err)
		}
	}
}
