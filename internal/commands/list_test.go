package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	gittest.Git(t, dir, args...)
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
	if err := List(ctx, &buf, 0); err != nil {
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
	if err := List(ctx, &buf, 0); err != nil {
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
	if err := List(ctx, &buf, 0); err != nil {
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
	if err := List(ctx, &buf, 0); err != nil {
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
	if err := List(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "wt migrate") {
		t.Errorf("nothing is marked, so nothing needs explaining:\n%s", buf.String())
	}
}

// On a terminal every row fits its width: paths give way from the left, where
// all worktrees of a repository share the same leading directories.
func TestListFitsPathsToTheTerminalWidth(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)

	const width = 60
	var buf bytes.Buffer
	if err := List(ctx, &buf, width); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if n := len([]rune(line)); n > width {
			t.Errorf("%d columns, want at most %d: %q", n, width, line)
		}
	}
	line := lineContaining(t, buf.String(), "feat_wt/thing")
	if !strings.Contains(line, "…/") || !strings.HasSuffix(line, "/demo_wt/feat_wt/thing") {
		t.Errorf("want the path shortened from the left, its tail intact: %q", line)
	}
}

// Piped, a path is an argument to other commands, so it is printed whole.
func TestListPrintsWholePathsWithoutATerminal(t *testing.T) {
	main, wt := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)

	var buf bytes.Buffer
	if err := List(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), wt) {
		t.Errorf("want the whole path %s:\n%s", wt, buf.String())
	}
}

func TestListShowsPathsFromHomeOnATerminal(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)
	t.Setenv("HOME", ctx.Repo.Parent)

	var buf bytes.Buffer
	if err := List(ctx, &buf, 500); err != nil {
		t.Fatal(err)
	}
	if line := lineContaining(t, buf.String(), "feat_wt/thing"); !strings.HasSuffix(line, "  ~/demo_wt/feat_wt/thing") {
		t.Errorf("want the path from ~: %q", line)
	}
}

func TestElideLeft(t *testing.T) {
	for _, tc := range []struct {
		path  string
		limit int
		want  string
	}{
		{"~/src/repo_wt/feat_wt/thing", 40, "~/src/repo_wt/feat_wt/thing"},
		{"~/src/repo_wt/feat_wt/thing", 27, "~/src/repo_wt/feat_wt/thing"},
		{"~/src/repo_wt/feat_wt/thing", 23, "…/repo_wt/feat_wt/thing"},
		{"~/src/repo_wt/feat_wt/thing", 20, "…/feat_wt/thing"},
		{"/a/averyveryverylongname", 10, "…ylongname"},
	} {
		if got := elideLeft(tc.path, tc.limit); got != tc.want {
			t.Errorf("elideLeft(%q, %d) = %q, want %q", tc.path, tc.limit, got, tc.want)
		}
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
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(buf.String(), "clean") {
		t.Errorf("want a cleanliness report:\n%s", buf.String())
	}
}

func TestStatusFitsPathsToTheTerminalWidth(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)

	const width = 60
	var buf bytes.Buffer
	if err := Status(ctx, &buf, width); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if n := len([]rune(line)); n > width {
			t.Errorf("%d columns, want at most %d: %q", n, width, line)
		}
	}
	line := lineContaining(t, buf.String(), "feat_wt/thing")
	if !strings.Contains(line, "…/") || !strings.HasSuffix(line, "/demo_wt/feat_wt/thing") {
		t.Errorf("want the path shortened from the left, its tail intact: %q", line)
	}
}

// A row whose branch is outside the convention has "-" in the WORK column, so
// a legend telling the reader to type a work name names something that is not
// on the screen. The branch and the path are.
func TestListLegendNamesSomethingTheRowActuallyShows(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix/flaky-test",
		filepath.Join(ctx.Repo.Parent, "demo-idiot"))

	var buf bytes.Buffer
	if err := List(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	legend := lineContaining(t, buf.String(), "wt migrate")
	_, arg, _ := strings.Cut(legend, "wt migrate ")
	arg, _, _ = strings.Cut(arg, "`")
	if !strings.Contains(arg, "branch") && !strings.Contains(arg, "path") {
		t.Errorf("the legend tells you to type %q, which this row does not show", arg)
	}
}

// The TRUNK column counts each branch against origin/<trunk> as last fetched,
// the base wt remove and wt sweep call merged, and the first line says so. A
// branch with nothing either way is on trunk; the main checkout has no row
// to count.
func TestStatusCountsEachBranchAgainstTrunkAsLastFetched(t *testing.T) {
	ctx := syncRepo(t)

	var buf bytes.Buffer
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatalf("Status: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "against origin/main, as last fetched") {
		t.Errorf("want the base and the fetch age first:\n%s", out)
	}
	if head := lineContaining(t, out, "BRANCH"); !strings.Contains(head, "STATE  TRUNK") || !strings.HasSuffix(head, "PATH") {
		t.Errorf("want TRUNK between STATE and PATH: %q", head)
	}
	if line := lineContaining(t, out, "feat_wt/bump"); !strings.Contains(line, "  1 behind · 1 ahead  ") {
		t.Errorf("want bump counted: %q", line)
	}
	if line := lineContaining(t, out, "feat_wt/other"); !strings.Contains(line, "  on trunk  ") {
		t.Errorf("want other on trunk: %q", line)
	}
	if line := lineContaining(t, out, ctx.Repo.MainRoot); !strings.Contains(line, "  -  ") {
		t.Errorf("want - for the main checkout: %q", line)
	}
}

// Without origin/<trunk> the local trunk is the base, and the first line
// says so without a fetch age nothing fetched.
func TestStatusFallsBackToTheLocalTrunkWithoutOrigin(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)

	var buf bytes.Buffer
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "against main\n") {
		t.Errorf("want the local trunk named first:\n%s", buf.String())
	}
	if line := lineContaining(t, buf.String(), "feat_wt/thing"); !strings.Contains(line, "  on trunk  ") {
		t.Errorf("want thing on trunk: %q", line)
	}
}

// A detached worktree has no branch to count.
func TestStatusPrintsADashForADetachedWorktree(t *testing.T) {
	ctx := syncRepo(t)
	detached := filepath.Join(ctx.Repo.Parent, "demo-detached")
	gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", detached)

	var buf bytes.Buffer
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	line := lineContaining(t, buf.String(), "demo-detached")
	if f := strings.Fields(line); len(f) != 4 || f[0] != "(detached)" || f[2] != "-" {
		t.Errorf("want - for a detached worktree: %q", line)
	}
}

func noStatusSessions() StatusOptions {
	return StatusOptions{Agents: []wtsync.Agent{}}
}

// wt status <work> is the worktree alone, one fact per line, then the
// verdict the overview would give it, in its words, with the advice its
// heading gives, and labelled as the simulation it is.
func TestStatusWorktreePrintsTheFactsAndTheVerdictForARecipeWorktree(t *testing.T) {
	ctx, bump := runFixture(t, false)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "bump", noStatusSessions(), &buf); err != nil {
		t.Fatalf("StatusWorktree: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"bump\n",
		"  branch  feat_wt/bump\n",
		"  path    " + bump + "\n",
		"  state   clean\n",
		"  trunk   1 behind · 1 ahead of origin/main (as last fetched ",
		"  sync    recipe · wt sync run bump\n",
		"1 stop, resolved · 1/1: v.txt✓\n",
		"simulated against origin/main as last fetched",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sessions") {
		t.Errorf("no session is in it, so no sessions line:\n%s", out)
	}
}

func TestStatusWorktreeSaysCleanAndCurrentInTheOverviewsWords(t *testing.T) {
	ctx := syncRepo(t)

	// other was cut from trunk after trunk last moved: nothing either way.
	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "other", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  trunk   on origin/main (as last fetched ") ||
		!strings.Contains(buf.String(), "  sync    current\n") {
		t.Errorf("want a current worktree on trunk, with nothing to do:\n%s", buf.String())
	}

	// clean adds a file of its own, then trunk moves past it on another: a
	// rebase to simulate, and nothing for it to stop on.
	clean, err := New(ctx, "feat/clean", NewOptions{NoSetup: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, clean, "new.txt", "new\n")
	gitIn(t, clean, "add", "-A")
	gitIn(t, clean, "commit", "-q", "-m", "new file")
	writeFile(t, ctx.Repo.MainRoot, "b.txt", "trunk\n")
	gitIn(t, ctx.Repo.MainRoot, "add", "-A")
	gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "-m", "b on trunk")
	gitIn(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")

	buf.Reset()
	if err := StatusWorktree(ctx, "clean", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  sync    clean · wt sync run clean\n") {
		t.Errorf("want the clean verdict with the run advice:\n%s", buf.String())
	}
}

// A contested worktree is filed under needs you, and the advice is the
// heading's: the detail is wt sync's.
func TestStatusWorktreePointsAContestedOneAtTheDetail(t *testing.T) {
	ctx, _ := contestedFixture(t)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "bump", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  sync    contested · wt sync bump for the detail\n") {
		t.Errorf("want the contested verdict with the detail advice:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "a.txt✗") {
		t.Errorf("want the stop that is yours under the verdict:\n%s", buf.String())
	}
}

// A session in the worktree is a fact of its own, in the overview's words.
func TestStatusWorktreeNamesTheSessionsInIt(t *testing.T) {
	ctx, bump := runFixture(t, false)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "bump", StatusOptions{Agents: idleIn(t, bump, "parked-1")}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  sessions  session parked-1 (idle)\n") {
		t.Errorf("want the idle session named:\n%s", buf.String())
	}
}

// A repository whose trunk declares nothing is assessed all the same, and
// the verdict says what the overview's heading says: ready once it does.
func TestStatusWorktreeSaysWhenTrunkDeclaresNothing(t *testing.T) {
	main, wt := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "new file")
	writeFile(t, main, "b.txt", "trunk\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "b on trunk")
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, _ := Open(main)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "thing", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  sync    clean · ready, once trunk declares .wt-sync.yaml\n",
		"demo declares no .wt-sync.yaml on origin/main: reported only, never rebased.\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("want %q in:\n%s", want, buf.String())
		}
	}
}

// Without origin/<trunk> the counts fall back to the local trunk, but the
// verdict cannot: it is what wt sync would say, and wt sync says this.
func TestStatusWorktreeWithoutOriginCountsAgainstTheLocalTrunk(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, _ := Open(main)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "thing", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  trunk   on main\n",
		"  sync    origin/main is not known here; run git fetch origin\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("want %q in:\n%s", want, buf.String())
		}
	}
}

func TestStatusWorktreeRefusesTheMainCheckout(t *testing.T) {
	ctx := syncRepo(t)

	err := StatusWorktree(ctx, ctx.Repo.MainRoot, noStatusSessions(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Errorf("want the main-checkout error, got %v", err)
	}
	err = StatusWorktree(ctx, "nope", noStatusSessions(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), `no worktree "nope"`) {
		t.Errorf("want Locate's error for an unknown name, got %v", err)
	}
}

// A branch whose count git cannot give prints ?, and the row is not an
// error: here the branch ref is gone from under the checkout.
func TestStatusPrintsAQuestionMarkWhenACountCannotBeRead(t *testing.T) {
	ctx := syncRepo(t)
	gitIn(t, ctx.Repo.MainRoot, "update-ref", "-d", "refs/heads/feat_wt/other")

	var buf bytes.Buffer
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatalf("Status: %v", err)
	}
	line := lineContaining(t, buf.String(), "feat_wt/other")
	if f := strings.Fields(line); len(f) != 4 || f[2] != "?" {
		t.Errorf("want ? for an unreadable count: %q", line)
	}
	if line := lineContaining(t, buf.String(), "feat_wt/bump"); !strings.Contains(line, "  1 behind · 1 ahead  ") {
		t.Errorf("the other rows still count: %q", line)
	}
}

// With neither origin/<trunk> nor <trunk> there is nothing to count
// against: the first line says so and every cell is ?.
func TestStatusSaysWhenThereIsNoTrunkToCompareWith(t *testing.T) {
	main, _ := repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	gitIn(t, main, "branch", "-m", "main", "elsewhere")
	ctx, _ := Open(main)

	var buf bytes.Buffer
	if err := Status(ctx, &buf, 0); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "neither origin/main nor main is here to compare with\n") {
		t.Errorf("want the missing trunk named first:\n%s", buf.String())
	}
	line := lineContaining(t, buf.String(), "feat_wt/thing")
	if f := strings.Fields(line); len(f) != 4 || f[2] != "?" {
		t.Errorf("want ? with no trunk: %q", line)
	}
}

// A worktree a run handed over says so on the sync line, in the overview's
// words, and the stop it waits at under it; its staged resolutions are not
// called dirty.
func TestStatusWorktreeShowsAHandedOverWorktree(t *testing.T) {
	ctx, bump := contestedFixture(t)
	_, st := handOverNow(t, ctx, bump)

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "bump", noStatusSessions(), &buf); err != nil {
		t.Fatalf("StatusWorktree: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"  sync    contested, handed over · wt sync bump for the detail\n",
		fmt.Sprintf("at %d/%d", st.Stop, st.Total),
		"a.txt✗ v.txt✓  waits on you: " + wtsync.WayOut(wtsync.Way{Plan: true, Rebasing: true}),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if strings.Contains(lineContaining(t, out, "  sync "), "dirty") {
		t.Errorf("a handover is called dirty:\n%s", out)
	}
}

// Tracked changes put a worktree under needs you, and the sync line says
// dirty beside the class the way the overview's row does.
func TestStatusWorktreeMarksADirtyWorktreeOnTheSyncLine(t *testing.T) {
	ctx, bump := runFixture(t, false)
	writeFile(t, bump, "v.txt", "1.0.2\n")

	var buf bytes.Buffer
	if err := StatusWorktree(ctx, "bump", noStatusSessions(), &buf); err != nil {
		t.Fatalf("StatusWorktree: %v", err)
	}
	for _, want := range []string{
		"  state   dirty\n",
		"  sync    recipe, dirty · wt sync bump for the detail\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("want %q in:\n%s", want, buf.String())
		}
	}
}
