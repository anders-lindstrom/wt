package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRelocatesToCanonicalPath(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	legacy := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix_wt/legacy", legacy)

	var buf bytes.Buffer
	got, err := Migrate(ctx, "fix/legacy", &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "legacy")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("worktree not at canonical path: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("legacy path should be gone")
	}
}

func TestMigrateIsANoOpWhenAlreadyCanonical(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/already", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Migrate(ctx, "fix/already", &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got != path {
		t.Errorf("got %q, want unchanged %q", got, path)
	}
}

func TestAdoptProvisionsAnExternallyCreatedWorktree(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	// A worktree made the way Superset makes them: outside the canonical path.
	external := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "outside")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/outside", external)

	var buf bytes.Buffer
	got, err := Adopt(ctx, external, false, SetupOptions{Source: main}, &buf)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got != external {
		t.Errorf("without --relocate the path must not change: %q", got)
	}
}

func TestAdoptRelocates(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	external := filepath.Join(ctx.Repo.Parent, "elsewhere")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/moved", external)

	var buf bytes.Buffer
	got, err := Adopt(ctx, external, true, SetupOptions{Source: main}, &buf)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "moved")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAdoptRefusesAPathThatIsNotAWorktree(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := Adopt(ctx, t.TempDir(), false, SetupOptions{}, &buf); err == nil {
		t.Error("want an error adopting a directory that is not a worktree of this repo")
	}
}

func TestDoctorReportsNestedWorktree(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	nested := filepath.Join(main, "inside_wt", "feat_wt", "oops")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/oops", nested)

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems == 0 {
		t.Errorf("want problems reported:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "inside the main checkout") {
		t.Errorf("nested worktree not reported:\n%s", buf.String())
	}
}

func TestDoctorIsQuietOnAHealthyRepo(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("healthy repo reported %d problems:\n%s", problems, buf.String())
	}
}

func TestDoctorReportsMissingRequiredBin(t *testing.T) {
	ctx, _ := Open(committedRepo(t,
		"MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\nREQUIRED_BINS=\"definitely-not-a-real-binary-xyz\"\n"))
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if problems == 0 || !strings.Contains(buf.String(), "definitely-not-a-real-binary-xyz") {
		t.Errorf("missing REQUIRED_BINS not reported:\n%s", buf.String())
	}
}
