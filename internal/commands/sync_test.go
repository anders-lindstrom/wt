package commands

import (
	"bytes"
	"errors"
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

// syncBlock is a worktree's row in the wt sync overview and the lines under it.
func syncBlock(t *testing.T, out, work string) string {
	t.Helper()
	var block []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "  "+work+" "):
			in = true
		case in && !strings.HasPrefix(line, "    "):
			in = false
		}
		if in {
			block = append(block, line)
		}
	}
	if len(block) == 0 {
		t.Fatalf("no row for %s in:\n%s", work, out)
	}
	return strings.Join(block, "\n")
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
	block := syncBlock(t, buf.String(), "bump")
	// Both commands, as Preflight and doctor name them: resume refuses a
	// handover the person aborted by hand, and undo ends that one.
	if !strings.Contains(block, "contested") || !strings.Contains(block, "left mid-rebase by wt sync run: wt sync resume, or wt sync undo") {
		t.Fatalf("bump block %q:\n%s", block, buf.String())
	}
	if strings.Contains(block, "dirty") {
		t.Fatalf("a handover is called dirty: %q", block)
	}
	if strings.Index(buf.String(), "needs you") > strings.Index(buf.String(), "  bump ") {
		t.Errorf("a handover is filed under needs you:\n%s", buf.String())
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
	block := syncBlock(t, out, "bump")
	if !strings.Contains(block, "recipe") {
		t.Errorf("expected the bump worktree as recipe:\n%s", out)
	}
	if strings.Index(out, "ready · wt sync run") > strings.Index(out, "  bump ") {
		t.Errorf("a recipe worktree nothing holds is filed under ready:\n%s", out)
	}
	if strings.Contains(out, "other") {
		t.Errorf("a current worktree is not printed:\n%s", out)
	}
	if !strings.Contains(block, "1/1") || !strings.Contains(block, "v.txt✓") {
		t.Errorf("the stop line names the stop and the resolved file:\n%s", out)
	}
	if after := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/heads"); after != before {
		t.Error("sync changed a ref")
	}
	if !strings.Contains(out, "not fetched") {
		t.Errorf("expected the header to say it never fetched:\n%s", out)
	}
}

// The stop line names the stop that decides the class, not the first one
// the rebase reaches. Here stop 1 is the declared owned line, which the
// strategy resolves, and stop 2 is a file nothing claims: 2/2 is what a
// person needs, and the resolved stop is a count, not the headline.
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
		t.Errorf("expected the line counting the stops already resolved:\n%s", out)
	}

	// wt sync <work> lists both stops in full, and says which one is yours.
	buf.Reset()
	if err := SyncWorktree(ctx, "bump", &buf); err != nil {
		t.Fatalf("SyncWorktree: %v", err)
	}
	out = buf.String()
	for _, want := range []string{
		"\n  1/2  bump\n",
		"\n    ✓ v.txt  resolved by owned-line\n",
		"\n  2/2  branch edits a  ← yours\n",
		"\n    ✗ a.txt  yours: no strategy claims it\n",
		"  run     wt sync run bump rebases up to 2/2 and hands that stop to you\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
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
		t.Errorf("expected the chdir error's own text, not an empty message:\n%s", out)
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

func TestSyncGroupsAWorktreeByWhatToDoAboutIt(t *testing.T) {
	busy := wtsync.Sessions{{Name: "busy"}}
	parked := wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}
	for _, tc := range []struct {
		name string
		a    wtsync.Assessment
		want syncSection
	}{
		{"clean", wtsync.Assessment{Class: wtsync.Clean}, sectionReady},
		{"recipe", wtsync.Assessment{Class: wtsync.Recipe}, sectionReady},
		{"contested", wtsync.Assessment{Class: wtsync.Contested}, sectionNeedsYou},
		{"divergent", wtsync.Assessment{Class: wtsync.Divergent}, sectionNeedsYou},
		{"dirty recipe", wtsync.Assessment{Class: wtsync.Recipe, Dirty: true}, sectionNeedsYou},
		{"handed over", wtsync.Assessment{Class: wtsync.Contested, Paused: true}, sectionNeedsYou},
		{"not assessed", wtsync.Assessment{Err: errors.New("boom")}, sectionNeedsYou},
		{"session in a recipe", wtsync.Assessment{Class: wtsync.Recipe, Sessions: busy}, sectionSkipped},
		{"session in a dirty contested", wtsync.Assessment{Class: wtsync.Contested, Dirty: true, Sessions: busy}, sectionSkipped},
		{"idle session in a recipe", wtsync.Assessment{Class: wtsync.Recipe, Sessions: parked}, sectionReady},
		{"idle session in a contested", wtsync.Assessment{Class: wtsync.Contested, Sessions: parked}, sectionNeedsYou},
		{"stale", wtsync.Assessment{Class: wtsync.Stale}, sectionSkipped},
		{"detached", wtsync.Assessment{Class: wtsync.Detached}, sectionSkipped},
	} {
		if got := sectionOf(tc.a); got != tc.want {
			t.Errorf("%s: section %d, want %d", tc.name, got, tc.want)
		}
	}
}

// The overview counts what wt sync <work> lists.
func TestSyncSummaryCutsListsToCounts(t *testing.T) {
	a := wtsync.Assessment{
		Class: wtsync.Divergent,
		Divergent: []wtsync.Collision{{Path: "etc/openapi_v3.json", Groups: []wtsync.KeyGroup{
			{Section: "paths", Keys: []string{"/a", "/b"}},
			{Section: "schemas", Keys: []string{"S"}},
		}}},
		Graph: &wtsync.GraphOverlap{Branch: []string{"core/build.gradle", "build.gradle"}, Trunk: []string{"build.gradle"}},
	}
	got := strings.Join(summaryLines(a), "\n")
	for _, want := range []string{
		"openapi_v3.json: both sides changed 2 paths, 1 schema",
		"dependency graph changed on both sides: 2 files on branch, 1 on trunk",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/a") || strings.Contains(got, "core/build.gradle") {
		t.Errorf("the overview lists what it should count:\n%s", got)
	}
}

func TestSyncSummaryNamesAFewFilesYoursFirst(t *testing.T) {
	a := wtsync.Assessment{
		Class:  wtsync.Contested,
		Replay: wtsync.Replay{Stop: &wtsync.Stop{Index: 2, Total: 5, Subject: "s"}},
		Files: []wtsync.FileOutcome{
			{Path: "x/A.java", Resolved: true}, {Path: "x/B.java"}, {Path: "C.java", Resolved: true}, {Path: "D.java"},
		},
	}
	if got := summaryLines(a)[0]; got != `2/5 "s"  B.java✗ D.java✗ +2 more` {
		t.Errorf("stop line = %q", got)
	}
}

// wt sync <work> is the untruncated view, and still meant to be read: every
// key and file on a line of its own, under what it belongs to.
func TestSyncDetailListsEveryKeyAndFileOnItsOwnLine(t *testing.T) {
	a := wtsync.Assessment{
		Branch: "feat_wt/api", Path: "/src/demo_wt/feat_wt/api", Class: wtsync.Divergent, Behind: 3, Ahead: 2,
		Divergent: []wtsync.Collision{{Path: "etc/openapi_v3.json", Groups: []wtsync.KeyGroup{
			{Section: "paths", Keys: []string{"/a", "/b"}},
			{Section: "schemas", Keys: []string{"S"}},
		}}},
		Graph: &wtsync.GraphOverlap{Branch: []string{"core/build.gradle", "build.gradle"}, Trunk: []string{"pins/build.gradle"}},
	}
	var buf bytes.Buffer
	printDetail(&buf, "api", a)
	out := buf.String()
	for _, want := range []string{
		"\napi  divergent  3 behind  2 ahead\n",
		"\n  run     refused: divergent: openapi refuses etc/openapi_v3.json at the endpoint: both sides changed 2 paths, 1 schema\n",
		"\n  etc/openapi_v3.json\n    paths (2)\n      /a\n      /b\n    schemas (1)\n      S\n",
		"\n  on the branch (2)\n    core/build.gradle\n    build.gradle\n  on trunk (1)\n    pins/build.gradle\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// `recipe?` is not `recipe`: the replay could not be carried to the end, so
// the class is what it earned up to the stop it stopped at.
func TestClassLabelMarksAnUnverifiedReplayWithAQuestionMark(t *testing.T) {
	a := wtsync.Assessment{Class: wtsync.Recipe}
	if got := classLabel(a); got != "recipe" {
		t.Errorf("verified: got %q, want recipe", got)
	}
	a.Unverified = true
	if got := classLabel(a); got != "recipe?" {
		t.Errorf("unverified: got %q, want recipe?", got)
	}
}

func TestSummaryLinesFlattenAMultilineNote(t *testing.T) {
	a := wtsync.Assessment{Files: []wtsync.FileOutcome{{Path: "x", Note: "line one\nline two"}}}
	for _, line := range summaryLines(a) {
		if strings.Contains(line, "\n") {
			t.Errorf("expected single-line notes, got %q", line)
		}
	}
}

func TestHeldByNamesTheSessionsInTheWorktree(t *testing.T) {
	parked := wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}
	for _, tc := range []struct {
		name string
		a    wtsync.Assessment
		want string
	}{
		{"named", wtsync.Assessment{Sessions: wtsync.Sessions{{Name: "busy"}}}, "session busy"},
		{"kind only", wtsync.Assessment{Sessions: wtsync.Sessions{{Kind: "codex"}}}, "session codex"},
		{"nothing to call it", wtsync.Assessment{Sessions: wtsync.Sessions{{}}}, "an unnamed session"},
		{"dirty, nobody in it", wtsync.Assessment{Dirty: true}, "dirty"},
		{"idle", wtsync.Assessment{Sessions: parked}, "session parked-1 (idle)"},
		{"two idle", wtsync.Assessment{Sessions: append(wtsync.Sessions{{Name: "old-1", Kind: "interactive", Status: "idle"}}, parked...)}, "session old-1 (idle) +1"},
		{"a busy one leads", wtsync.Assessment{Sessions: append(wtsync.Sessions{{Name: "new-2", Status: "busy"}}, parked...)}, "session new-2 +1"},
		{"idle and dirty", wtsync.Assessment{Dirty: true, Sessions: parked}, "session parked-1 (idle)  dirty"},
		{"busy and dirty", wtsync.Assessment{Dirty: true, Sessions: wtsync.Sessions{{Name: "busy"}}}, "session busy"},
	} {
		if got := heldBy(tc.a); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunVerdictSaysItAsksFirstUnderAnIdleSession(t *testing.T) {
	a := wtsync.Assessment{Class: wtsync.Clean, Sessions: wtsync.Sessions{{Name: "parked-1", Kind: "interactive", Status: "idle"}}}
	if got := runVerdict("bump", a); got != "wt sync run bump; asks first: session parked-1 (idle) is in it" {
		t.Errorf("got %q", got)
	}
}

func TestSessionsChangedNamesWhatIsNewOrBusy(t *testing.T) {
	parked := wtsync.Agent{ID: "a1", Name: "parked-1", Cwd: "/w", Kind: "interactive", Status: "idle"}
	busy := parked
	busy.Status = "busy"
	other := wtsync.Agent{ID: "a2", Name: "new-2", Cwd: "/w", Kind: "interactive", Status: "idle"}
	told := wtsync.Sessions{parked}
	for _, tc := range []struct {
		now  wtsync.Sessions
		want string
	}{
		{wtsync.Sessions{parked}, ""},
		{nil, ""},
		{wtsync.Sessions{busy}, "an agent session is busy in it now: parked-1"},
		{wtsync.Sessions{parked, other}, "a session arrived since it was checked: new-2 (idle)"},
	} {
		if got := sessionsChanged(told, tc.now); got != tc.want {
			t.Errorf("now %+v: got %q, want %q", tc.now, got, tc.want)
		}
	}
}
