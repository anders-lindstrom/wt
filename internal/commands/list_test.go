package commands

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// repoWithWorktree builds one repo and adds a worktree at the path returned by
// where(parent), so the caller cannot accidentally compute a path from a
// different repository than the one it mutates.
func repoWithWorktree(t *testing.T, where func(parent string) string) (main, wt string) {
	t.Helper()
	main = fixtureRepo(t, minimalConf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	wt = where(filepath.Dir(main))
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing", wt)
	return main, wt
}

func TestListShowsWorktreesAndMarksNonCanonical(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo-legacy")
	})

	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatalf("List: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "feat_wt/thing") {
		t.Errorf("branch missing:\n%s", out)
	}
	if !strings.Contains(out, "demo-legacy") {
		t.Errorf("legacy path missing:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Errorf("non-canonical worktree not marked:\n%s", out)
	}
}

func TestListMarksCanonicalWorktreeCleanly(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "feat_wt/thing") && strings.Contains(line, "!") {
			t.Errorf("canonical worktree wrongly marked: %q", line)
		}
	}
}

func TestStatusReportsCleanliness(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Status(ctx, &buf); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(buf.String(), "clean") {
		t.Errorf("want a cleanliness report:\n%s", buf.String())
	}
}
