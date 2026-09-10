package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// syncRepo builds a repository whose origin is itself, so origin/main
// exists, with a declaration on main and two feature worktrees: one that
// conflicts on the declared owned line, and one cut after trunk's last
// commit, so it is current.
// gitOut runs git and returns its stdout; the package's gitIn returns nothing.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// A handed-over worktree's staged resolutions are what a person is
// finishing, not dirt: its row says it is waiting, and nothing else.
func TestSyncShowsAHandedOverWorktreeWithoutCallingItDirty(t *testing.T) {
	ctx, bump := contestedFixture(t)
	handOverNow(t, ctx, bump)
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	var row string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "bump ") {
			row = line
		}
	}
	if !strings.Contains(row, "contested") || !strings.Contains(row, "left mid-rebase by wt sync run") {
		t.Fatalf("bump row %q:\n%s", row, buf.String())
	}
	if strings.Contains(row, "dirty") {
		t.Fatalf("a handover is called dirty: %q", row)
	}
}

func syncRepo(t *testing.T) *Context {
	t.Helper()
	main := committedRepo(t, minimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".wt-sync.yaml", "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	write("v.txt", "1.0.0\n")
	// a.txt is claimed by nothing and touched by nobody here; it is in the
	// base so a test can make both sides edit it and get a real
	// modify/modify conflict rather than an add/add.
	write("a.txt", "base\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err := New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	gitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := New(ctx, "feat/other", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	return ctx
}

func TestSyncPrintsTheTriageAndChangesNothing(t *testing.T) {
	ctx := syncRepo(t)
	before := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/heads")
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "bump") || !strings.Contains(out, "recipe") {
		t.Errorf("expected the bump worktree as recipe:\n%s", out)
	}
	if strings.Contains(out, "other") {
		t.Errorf("a current worktree is not printed:\n%s", out)
	}
	if !strings.Contains(out, "1/1") {
		t.Errorf("expected the first stop 1/1:\n%s", out)
	}
	if after := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/heads"); after != before {
		t.Error("sync changed a ref")
	}
	if !strings.Contains(out, "bump") || !strings.Contains(out, "v.txt✓") {
		t.Errorf("the stop column names the stopping commit and the resolved file:\n%s", out)
	}
	if !strings.Contains(out, "not fetched") {
		t.Errorf("expected the header to say it never fetched:\n%s", out)
	}
}

// The STOP column names the stop that decides the class, not the first one
// the rebase reaches. Here stop 1 is the declared owned line, which the
// strategy resolves, and stop 2 is a file nothing claims: 2/2 is what a
// person needs, and the resolved stop is a note, not the headline.
func TestSyncNamesTheDecidingStopNotTheFirstOne(t *testing.T) {
	ctx := syncRepo(t)
	main := ctx.Repo.MainRoot
	wts, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	bump := ""
	for _, wt := range wts {
		if strings.HasSuffix(wt.Branch, "/bump") {
			bump = wt.Path
		}
	}
	if bump == "" {
		t.Fatal("no bump worktree in the fixture")
	}
	// a second branch commit on a file nothing claims, against a trunk that
	// changed the same file: the replay's second stop, and its last.
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bump, "commit", "-q", "-am", "branch edits a")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "commit", "-q", "-am", "trunk edits a")
	gitIn(t, main, "fetch", "-q", "origin")

	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "contested") {
		t.Errorf("a later unclaimed stop is contested:\n%s", out)
	}
	if !strings.Contains(out, "2/2") {
		t.Errorf("expected the deciding stop 2/2:\n%s", out)
	}
	if strings.Contains(out, "1/2") {
		t.Errorf("the first stop resolved, so it must not be the one named:\n%s", out)
	}
	if !strings.Contains(out, "a.txt✗") {
		t.Errorf("expected the unclaimed file marked unresolved:\n%s", out)
	}
	if !strings.Contains(out, "1 earlier stop resolved") {
		t.Errorf("expected the note counting the stops already resolved:\n%s", out)
	}
}

func TestSyncPrintsAnUnknownRowWithItsError(t *testing.T) {
	ctx := syncRepo(t)
	// break one worktree: its directory is gone, so status fails before a class is decided
	wts, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range wts {
		if strings.HasSuffix(wt.Branch, "/bump") {
			if err := os.RemoveAll(wt.Path); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "unknown") || !strings.Contains(out, "error:") {
		t.Errorf("a failed assessment is printed as unknown with its error, never dropped:\n%s", out)
	}
	if !strings.Contains(out, "no such file") {
		t.Errorf("expected the chdir error's own text in the note, not an empty message:\n%s", out)
	}
}

func TestSyncSaysWhenTrunkDeclaresNothing(t *testing.T) {
	main := committedRepo(t, minimalConf)
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Sync(ctx, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(buf.String(), "no .wt-sync.yaml") {
		t.Errorf("expected the no-config notice:\n%s", buf.String())
	}
}

// `recipe?` is not `recipe`: the replay could not be carried to the end, so
// the class is what it earned up to the stop it stopped at.
func TestClassColumnMarksAnUnverifiedReplayWithAQuestionMark(t *testing.T) {
	a := wtsync.Assessment{Class: wtsync.Recipe}
	if got := classColumn(a); got != "recipe" {
		t.Errorf("verified: got %q, want recipe", got)
	}
	a.Unverified = true
	if got := classColumn(a); got != "recipe?" {
		t.Errorf("unverified: got %q, want recipe?", got)
	}
}

func TestNoteColumnFlattensAMultilineNote(t *testing.T) {
	a := wtsync.Assessment{Files: []wtsync.FileOutcome{{Path: "x", Note: "line one\nline two"}}}
	if got := noteColumn(a); strings.Contains(got, "\n") {
		t.Errorf("expected a single-line note, got %q", got)
	}
}

func TestWhoColumnFallsBackToKindThenAQuestionMark(t *testing.T) {
	a := wtsync.Assessment{Agent: &wtsync.Agent{Name: "busy"}}
	if got := whoColumn(a); got != "busy" {
		t.Errorf("named agent: got %q, want busy", got)
	}
	a = wtsync.Assessment{Agent: &wtsync.Agent{Kind: "codex"}}
	if got := whoColumn(a); got != "codex" {
		t.Errorf("unnamed agent: got %q, want its Kind codex", got)
	}
	a = wtsync.Assessment{Agent: &wtsync.Agent{}}
	if got := whoColumn(a); got != "?" {
		t.Errorf("no name and no kind: got %q, want ?", got)
	}
	a = wtsync.Assessment{}
	if got := whoColumn(a); got != "-" {
		t.Errorf("no agent: got %q, want -", got)
	}
}
