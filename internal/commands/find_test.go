package commands

import (
	"os"
	"path/filepath"
	"testing"
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

func TestRootsDefaultsAndSplitsOnColon(t *testing.T) {
	t.Setenv("WT_ROOTS", "/a:/b with space:/c")
	got := Roots()
	if len(got) != 3 || got[1] != "/b with space" {
		t.Errorf("got %q", got)
	}
	t.Setenv("WT_ROOTS", "")
	if len(Roots()) == 0 {
		t.Error("want a default when WT_ROOTS is unset")
	}
}

// A strong local hit ends the search.
func TestFindStrongLocalHitWins(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	ctx, _ := Open(committedRepo(t, minimalConf))
	if _, err := New(ctx, "feat/opensearch_ism", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	got, err := Find(ctx, "opensearch_ism")
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
	lctx, _ := Open(local)
	if _, err := New(lctx, "feat/opensearch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	other := committedRepoIn(t, root, "other", minimalConf)
	octx, _ := Open(other)
	if _, err := New(octx, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
		t.Fatal(err)
	}

	got, err := Find(lctx, "arch")
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

func TestFindReportsEveryTiedCandidate(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_ROOTS", root)
	for _, name := range []string{"one", "two"} {
		r := committedRepoIn(t, root, name, minimalConf)
		c, _ := Open(r)
		if _, err := New(c, "feat/arch", NewOptions{NoSetup: true}, os.Stderr); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Find(nil, "arch")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want both repos' arch, got %d: %+v", len(got), got)
	}
}

func TestFindNoMatch(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	got, err := Find(nil, "definitely-nothing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want no matches, got %+v", got)
	}
}
