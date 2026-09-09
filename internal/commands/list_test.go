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

// Superset builds <repo>_wt/<repo>/<type>_wt/<work> and stores that absolute
// path in its own database, so the shape is deliberate and moving one breaks
// the workspace. It gets its own mark rather than the "!" that invites a
// migrate.
func TestListMarksSupersetLayoutApartFromForeignOnes(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "demo", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	line := lineContaining(t, buf.String(), "feat_wt/thing")
	if strings.HasPrefix(line, "!") {
		t.Errorf("Superset's layout must not be marked as unrecognised: %q", line)
	}
	if !strings.HasPrefix(line, "s") {
		t.Errorf("want the Superset mark: %q", line)
	}
	if !strings.Contains(buf.String(), "Superset") {
		t.Errorf("want a legend explaining the mark:\n%s", buf.String())
	}
}

func TestListExplainsTheForeignMark(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo-legacy")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "wt migrate") {
		t.Errorf("want a legend naming the command that fixes it:\n%s", buf.String())
	}
}

func TestListPrintsNoLegendWhenEverythingIsCanonical(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "wt migrate") {
		t.Errorf("nothing is marked, so nothing needs explaining:\n%s", buf.String())
	}
}

func lineContaining(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, out)
	return ""
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

// A row whose branch is outside the convention has "-" in the WORK column, so
// a legend telling the reader to type a work name names something that is not
// on the screen. The branch and the path are.
func TestListLegendNamesSomethingTheRowActuallyShows(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix/idiotthings",
		filepath.Join(ctx.Repo.Parent, "demo-idiot"))

	var buf bytes.Buffer
	if err := List(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	legend := lineContaining(t, buf.String(), "wt migrate")
	_, arg, _ := strings.Cut(legend, "wt migrate ")
	arg, _, _ = strings.Cut(arg, "`")
	if !strings.Contains(arg, "branch") && !strings.Contains(arg, "path") {
		t.Errorf("the legend tells you to type %q, which this row does not show", arg)
	}
}
