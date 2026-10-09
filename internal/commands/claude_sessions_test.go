package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// claudeLists has claude list sessions for the length of the test, and
// returns how many times it was asked.
func claudeLists(t *testing.T, sessions ...wtsync.Agent) *int {
	t.Helper()
	asked := new(int)
	was := listClaudeSessions
	listClaudeSessions = func(time.Duration) ([]wtsync.Agent, bool, error) {
		*asked++
		return sessions, true, nil
	}
	t.Cleanup(func() { listClaudeSessions = was })
	return asked
}

func background(name, id, state, cwd string) wtsync.Agent {
	return wtsync.Agent{Name: name, ID: id, SessionID: id + "-0000-4000-8000-000000000000", Kind: "background", State: state, Cwd: cwd}
}

func inTerminal(name, status, cwd string, pid int) wtsync.Agent {
	return wtsync.Agent{Name: name, Kind: "interactive", Status: status, Cwd: cwd, PID: pid}
}

func listed(t *testing.T, ctx *Context, opts ListOptions, width int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := List(ctx, opts, &buf, width); err != nil {
		t.Fatalf("List: %v", err)
	}
	return buf.String()
}

func canonicalWorktree(t *testing.T) (ctx *Context, main, wt string) {
	t.Helper()
	main, wt = repoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, wt
}

// The column says which worktree has a session, by name, and what state it
// is in. blocked is the state Claude Code's agent view calls "Needs input".
func TestListShowsTheSessionInAWorktree(t *testing.T) {
	ctx, _, wt := canonicalWorktree(t)
	claudeLists(t, background("fix the crash", "3f9a1c20", "blocked", wt))

	out := listed(t, ctx, ListOptions{}, 0)
	if !strings.Contains(out, "SESSION") {
		t.Fatalf("no SESSION column:\n%s", out)
	}
	if line := lineContaining(t, out, "feat_wt/thing"); !strings.Contains(line, "fix the crash · needs input  ") {
		t.Errorf("want the session's name and state on its worktree's row: %q", line)
	}
	if line := lineContaining(t, out, "(main)"); !strings.Contains(line, "  -  ") {
		t.Errorf("want a dash for a worktree with no session: %q", line)
	}
}

// With no session anywhere the listing is what it was without the column.
func TestListPrintsNoSessionColumnWithoutASession(t *testing.T) {
	ctx, _, _ := canonicalWorktree(t)
	without := listed(t, ctx, ListOptions{NoSessions: true}, 0)

	claudeLists(t, background("elsewhere", "3f9a1c20", "working", t.TempDir()))
	if got := listed(t, ctx, ListOptions{}, 0); got != without {
		t.Errorf("a session in no worktree of this repository changed the listing:\n%s", got)
	}
}

func TestListNoSessionsDoesNotAskClaude(t *testing.T) {
	ctx, _, wt := canonicalWorktree(t)
	asked := claudeLists(t, background("fix the crash", "3f9a1c20", "working", wt))

	if out := listed(t, ctx, ListOptions{NoSessions: true}, 0); strings.Contains(out, "SESSION") {
		t.Errorf("--no-sessions printed the column:\n%s", out)
	}
	if *asked != 0 {
		t.Errorf("claude was asked %d time(s)", *asked)
	}
}

// A claude that errors or does not answer costs the column and one line,
// never the listing.
func TestListSaysWhenSessionsCouldNotBeRead(t *testing.T) {
	ctx, _, _ := canonicalWorktree(t)
	was := listClaudeSessions
	listClaudeSessions = func(time.Duration) ([]wtsync.Agent, bool, error) {
		return nil, true, errors.New("claude agents --json did not answer within 2s")
	}
	t.Cleanup(func() { listClaudeSessions = was })

	out := listed(t, ctx, ListOptions{}, 0)
	if strings.Contains(out, "SESSION") || !strings.Contains(out, "feat_wt/thing") {
		t.Errorf("want the listing without the column:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n   sessions not shown: claude agents --json did not answer within 2s\n") {
		t.Errorf("want one line under the table saying why:\n%s", out)
	}
}

// A session in a subfolder belongs to its worktree, and the main checkout
// does not take the sessions of the worktrees beside it or nested in it.
func TestListGivesASessionToTheWorktreeItIsIn(t *testing.T) {
	ctx, main, wt := canonicalWorktree(t)
	nested := filepath.Join(main, ".worktrees", "inner")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/inner", nested)
	deep := filepath.Join(wt, "src", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeLists(t,
		background("in-a-subfolder", "aaaaaaaa", "working", deep),
		background("in-the-nested-one", "bbbbbbbb", "working", nested))

	out := listed(t, ctx, ListOptions{}, 0)
	if line := lineContaining(t, out, "feat_wt/thing"); !strings.Contains(line, "in-a-subfolder · working") {
		t.Errorf("want the subfolder's session on its worktree: %q", line)
	}
	if line := lineContaining(t, out, "feat_wt/inner"); !strings.Contains(line, "in-the-nested-one · working") {
		t.Errorf("want the nested worktree's session on its own row: %q", line)
	}
	if line := lineContaining(t, out, "(main)"); strings.Contains(line, "·") {
		t.Errorf("the main checkout took another worktree's session: %q", line)
	}
}

// One row per worktree: the session that needs its person is the one named,
// else the newest, and the rest are counted.
func TestListNamesOneSessionAndCountsTheRest(t *testing.T) {
	ctx, _, wt := canonicalWorktree(t)
	older := background("older", "aaaaaaaa", "working", wt)
	older.StartedAt = 1000
	newer := background("newer", "bbbbbbbb", "done", wt)
	newer.StartedAt = 2000
	claudeLists(t, older, newer)
	if line := lineContaining(t, listed(t, ctx, ListOptions{}, 0), "feat_wt/thing"); !strings.Contains(line, "newer · done +1") {
		t.Errorf("want the newest named and one more counted: %q", line)
	}

	waiting := background("asks-you", "cccccccc", "blocked", wt)
	waiting.StartedAt = 500
	claudeLists(t, older, newer, waiting)
	if line := lineContaining(t, listed(t, ctx, ListOptions{}, 0), "feat_wt/thing"); !strings.Contains(line, "asks-you · needs input +2") {
		t.Errorf("want the one that needs input named first: %q", line)
	}
}

// A session in a terminal says so, because it cannot be attached; the one
// wt runs under is marked, and gives way to any other.
func TestListMarksSessionsInATerminalAndItsOwn(t *testing.T) {
	ctx, main, wt := canonicalWorktree(t)
	me := inTerminal("me", "busy", wt, 4243)
	me.Own = true
	claudeLists(t, inTerminal("typing", "idle", main, 4242), me)

	out := listed(t, ctx, ListOptions{}, 0)
	if line := lineContaining(t, out, "(main)"); !strings.Contains(line, "typing · idle in a terminal") {
		t.Errorf("want the terminal's session to say where it is: %q", line)
	}
	if line := lineContaining(t, out, "feat_wt/thing"); !strings.Contains(line, "me · this session") {
		t.Errorf("want wt's own session marked: %q", line)
	}

	claudeLists(t, me, background("other", "aaaaaaaa", "working", wt))
	if line := lineContaining(t, listed(t, ctx, ListOptions{}, 0), "feat_wt/thing"); !strings.Contains(line, "other · working +1") {
		t.Errorf("want another session named before wt's own: %q", line)
	}
}

// A name is free text somebody typed: nothing in it may move the cursor,
// break the row or run on for ever.
func TestListCleansAndCutsASessionName(t *testing.T) {
	ctx, _, wt := canonicalWorktree(t)
	claudeLists(t, background("bad\x1b[2J\tname\nsecond\u202eline "+strings.Repeat("x", 80), "aaaaaaaa", "working", wt))

	out := listed(t, ctx, ListOptions{}, 0)
	for _, r := range out {
		if r != '\n' && (r < ' ' || r == '\u202e' || r == 0x7f) {
			t.Fatalf("control character %q reached the table:\n%q", r, out)
		}
	}
	line := lineContaining(t, out, "feat_wt/thing")
	if !strings.Contains(line, "  bad[2J name secondline xxx") || !strings.Contains(line, "x… · working  ") {
		t.Errorf("want the name on one line, cut with an ellipsis: %q", line)
	}
	if cell := strings.Split(line, " · ")[0]; len([]rune(strings.TrimSpace(strings.SplitN(cell, "feat_wt/thing", 2)[1]))) != maxSessionName {
		t.Errorf("want the name cut to %d characters: %q", maxSessionName, line)
	}
	if got := strings.Count(out, "\n"); got != 3 {
		t.Errorf("want a header and two rows, got %d lines:\n%s", got, out)
	}
}

// ownShape is a repository shaped like this one: a long work name, its
// branch, a pull request on it, and two sessions in the worktree, one of
// which needs input.
func ownShape(t *testing.T) *Context {
	t.Helper()
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(77, "feat_wt/list-sessions", "Sessions in wt list"))
	var errs bytes.Buffer
	wt, err := New(ctx, "feat/list-sessions", NewOptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}
	claudeLists(t,
		background("20260930-090330/list-sessions", "aaaaaaaa", "blocked", wt),
		background("a second look", "bbbbbbbb", "working", wt))
	return ctx
}

func widestLine(out string) int {
	widest := 0
	for _, line := range strings.Split(out, "\n") {
		widest = max(widest, displayWidth(line))
	}
	return widest
}

// On a terminal the column is shown only when every row fits with it. At 80
// columns this repository's own names do not, and the listing is the one
// without the column plus a line saying so: no row ever wraps for it.
func TestListLeavesTheSessionColumnOutWhereARowWouldNotFit(t *testing.T) {
	ctx := ownShape(t)
	const width = 80
	without := listed(t, ctx, ListOptions{NoSessions: true}, width)
	if n := widestLine(without); n > width {
		t.Fatalf("the fixture does not fit %d columns even without the column (%d):\n%s", width, n, without)
	}

	got := listed(t, ctx, ListOptions{}, width)
	want := without + "\n   sessions left out at this width — `wt list --wide` shows them\n"
	if got != want {
		t.Errorf("want the listing without the column, and one line:\n%s\ngot:\n%s", want, got)
	}
	if n := widestLine(got); n > width {
		t.Errorf("a line is %d columns, want at most %d:\n%s", n, width, got)
	}
}

// Where every row fits, the column is there, whole.
func TestListShowsTheSessionColumnWhereEveryRowFits(t *testing.T) {
	ctx := ownShape(t)
	piped := listed(t, ctx, ListOptions{}, 0)
	// Wide enough for the table with its paths cut to their shortest, and
	// one column short of that.
	fits := 0
	for width := 60; width < 400 && fits == 0; width++ {
		if strings.Contains(listed(t, ctx, ListOptions{}, width), "SESSION") {
			fits = width
		}
	}
	if fits == 0 {
		t.Fatalf("the column was shown at no width:\n%s", piped)
	}
	got := listed(t, ctx, ListOptions{}, fits)
	if n := widestLine(got); n > fits {
		t.Errorf("shown at %d columns, but a line is %d:\n%s", fits, n, got)
	}
	line := lineContaining(t, got, "feat_wt/list-sessions")
	if !strings.Contains(line, "#77 open  20260930-090330/list-sessions · needs input +1  ") {
		t.Errorf("want the name, the state and the count whole: %q", line)
	}
	if narrower := listed(t, ctx, ListOptions{}, fits-1); strings.Contains(narrower, "SESSION") || widestLine(narrower) > fits-1 {
		t.Errorf("one column narrower the column must be gone and nothing wrapped:\n%s", narrower)
	}
}

// --wide is the listing as it is when piped, whatever the width.
func TestListWideShowsTheColumnWhateverTheWidth(t *testing.T) {
	ctx := ownShape(t)
	piped := listed(t, ctx, ListOptions{}, 0)
	if got := listed(t, ctx, ListOptions{Wide: true}, 40); got != piped {
		t.Errorf("want what a pipe gets:\n%s\ngot:\n%s", piped, got)
	}
	if !strings.Contains(piped, "· needs input +1") || strings.Contains(piped, "left out") {
		t.Errorf("piped, the column is always there:\n%s", piped)
	}
}

// A row fits by what the terminal draws: a character two columns wide
// counts as two, and a name is cut by columns too.
func TestRowsFitByDisplayWidth(t *testing.T) {
	rows := func(session string) [][]string {
		return [][]string{
			{"", "WORK", "SESSION", "PATH"},
			{"", "w", session, "/a/path/that/is/long/enough/to/count"},
		}
	}
	ascii, wide := rows("ab · done"), rows("修正 · done")
	width := 1
	for !pathTableFits(ascii, width) {
		width++
	}
	if !pathTableFits(wide, width+2) || pathTableFits(wide, width+1) {
		t.Errorf("two wide characters need two more columns than %d", width)
	}
	for s, want := range map[string]int{"abc": 3, "修正": 4, "e\u0301": 1, "🙂": 2, "": 0} {
		if got := displayWidth(s); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", s, got, want)
		}
	}
	if got := cutToWidth("修正修正修正", 7); got != "修正修…" || displayWidth(got) != 7 {
		t.Errorf("cutToWidth gave %q (%d columns)", got, displayWidth(got))
	}
	if got := cutToWidth("修正修正", 8); got != "修正修正" {
		t.Errorf("a name that fits is left whole, got %q", got)
	}
}

// --all, --roots and --profile list many repositories from one read.
func TestListsShareOneReadOfTheSessions(t *testing.T) {
	ctx, _, wt := canonicalWorktree(t)
	asked := claudeLists(t, background("fix the crash", "3f9a1c20", "working", wt))

	read := ReadClaudeSessions(SessionsDeadline)
	for range 3 {
		if out := listed(t, ctx, ListOptions{Sessions: read}, 0); !strings.Contains(out, "fix the crash · working") {
			t.Fatalf("want the session from the shared read:\n%s", out)
		}
	}
	if *asked != 1 {
		t.Errorf("claude was asked %d times for three listings", *asked)
	}
	if note := read.Note(); note != "" {
		t.Errorf("a read that worked has nothing to say: %q", note)
	}
}

// A state this build has never seen is printed as claude spells it.
func TestSessionStateIsClaudesOwnWordWhenUnknown(t *testing.T) {
	for state, want := range map[string]string{
		"working": "working", "blocked": "needs input", "done": "done",
		"failed": "failed", "stopped": "stopped", "hibernating": "hibernating",
	} {
		s := claudeSession{Agent: wtsync.Agent{Kind: "background", State: state}}
		if got := s.state(); got != want {
			t.Errorf("state %q reads %q, want %q", state, got, want)
		}
	}
}
