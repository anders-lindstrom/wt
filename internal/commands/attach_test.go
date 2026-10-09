package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// attachFixture is a repository with the worktree `thing`, the search roots
// kept to it, and the test standing in its main checkout.
func attachFixture(t *testing.T) (ctx *Context, main, wt string) {
	t.Helper()
	ctx, main, wt = canonicalWorktree(t)
	t.Setenv("WT_ROOTS", filepath.Dir(main))
	return ctx, main, wt
}

// attach runs wt attach as the shell layer calls it on a terminal, with
// answers typed at it, and returns the plan, what the person was shown, and
// the error.
func attach(t *testing.T, ctx *Context, o AttachOptions, answers string) (plan, shown string, err error) {
	t.Helper()
	var out, errw bytes.Buffer
	o.Out, o.Err = &out, &errw
	if answers != "" {
		o.In, o.Ask = strings.NewReader(answers), true
	}
	err = Attach(ctx, o)
	return out.String(), errw.String(), err
}

func forShell(pattern string) AttachOptions {
	return AttachOptions{Pattern: pattern, ForShell: true}
}

// attachPlan is what the shell layer is handed to attach by id: the verb,
// the id, no path, and the line that ends a plan.
func attachPlan(id string) string { return "attach\n" + id + "\n\nend\n" }

func TestAttachOpensTheOneBackgroundSession(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	claudeLists(t, background("fix the crash", "3f9a1c20", "blocked", wt))

	plan, shown, err := attach(t, ctx, forShell("thing"), "")
	if err != nil {
		t.Fatalf("Attach: %v\n%s", err, shown)
	}
	if plan != attachPlan("3f9a1c20") {
		t.Errorf("want the shell told to attach by id, got %q", plan)
	}
	if shown != "" {
		t.Errorf("nothing to say when there is one to open: %q", shown)
	}
}

// Without the shell layer the binary starts nothing: it names what to run.
func TestAttachWithoutTheShellLayerPrintsTheCommand(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	claudeLists(t, background("fix the crash", "3f9a1c20", "working", wt))

	plan, _, err := attach(t, ctx, AttachOptions{Pattern: "thing", Terminal: true}, "")
	if plan != "" {
		t.Errorf("stdout is the shell layer's alone: %q", plan)
	}
	if err == nil || !strings.Contains(err.Error(), SourceShellLayer) || !strings.Contains(err.Error(), "claude attach 3f9a1c20") {
		t.Errorf("want the shell layer's line and the command, got %v", err)
	}

	_, _, err = attach(t, ctx, AttachOptions{Pattern: "thing"}, "")
	if err == nil || !strings.Contains(err.Error(), "no terminal") || !strings.HasSuffix(err.Error(), "\n  claude attach 3f9a1c20") {
		t.Errorf("want the command a person would run, got %v", err)
	}
}

func TestAttachAsksWhichOfSeveral(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	first := background("asks-you", "aaaaaaaa", "blocked", wt)
	second := background("second look", "bbbbbbbb", "working", wt)
	second.StartedAt = time.Now().Add(-3 * time.Hour).UnixMilli()
	claudeLists(t, second, first, inTerminal("typing", "idle", wt, 4242))

	plan, shown, err := attach(t, ctx, forShell("thing"), "2\n")
	if err != nil {
		t.Fatalf("Attach: %v\n%s", err, shown)
	}
	if plan != attachPlan("bbbbbbbb") {
		t.Errorf("want the second one attached, got %q", plan)
	}
	for _, want := range []string{
		"Claude sessions in thing:\n",
		"  1  asks-you     needs input  background aaaaaaaa",
		"  2  second look  working      background bbbbbbbb             started 3h ago",
		"  -  typing       idle         in another terminal (pid 4242)",
		"Which one? [1-2] ",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("want %q in:\n%s", want, shown)
		}
	}
}

func TestAttachListsAndStopsWithNobodyToAsk(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	claudeLists(t, background("one", "aaaaaaaa", "working", wt), background("two", "bbbbbbbb", "working", wt))

	plan, shown, err := attach(t, ctx, forShell("thing"), "")
	if err == nil || !strings.Contains(err.Error(), "wt attach thing <session>") {
		t.Errorf("want to be told how to name one, got %v", err)
	}
	if plan != "" || !strings.Contains(shown, "background aaaaaaaa") || !strings.Contains(shown, "background bbbbbbbb") {
		t.Errorf("want both listed and nothing started: plan %q\n%s", plan, shown)
	}

	for answer, want := range map[string]string{"\n": "none chosen", "7\n": "not a number from 1 to 2", "x\n": "not a number"} {
		if _, _, err := attach(t, ctx, forShell("thing"), answer); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("answer %q: want %q, got %v", answer, want, err)
		}
	}
}

func TestAttachNamesSessionsItCannotOpen(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	claudeLists(t, inTerminal("typing", "busy", wt, 4242))

	plan, shown, err := attach(t, ctx, forShell("thing"), "")
	if err == nil || plan != "" {
		t.Fatalf("a session in a terminal cannot be attached: plan %q, err %v", plan, err)
	}
	if !strings.Contains(shown, "typing  busy  in another terminal (pid 4242)") {
		t.Errorf("want it named with its terminal's pid:\n%s", shown)
	}
}

func TestAttachWithNoSessionNamesResume(t *testing.T) {
	ctx, _, _ := attachFixture(t)
	claudeLists(t)

	_, _, err := attach(t, ctx, forShell("thing"), "")
	if err == nil || !strings.Contains(err.Error(), "no Claude session in thing") ||
		!strings.Contains(err.Error(), "wt attach thing --resume") {
		t.Errorf("want none found and --resume named, got %v", err)
	}
}

// <session> picks without the question: an id, the start of the full
// session id, or part of the name. An id given whole wins over a name.
func TestAttachPicksTheSessionNamed(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	review := background("review of bbbbbbbb", "aaaaaaaa", "working", wt)
	build := background("build", "bbbbbbbb", "working", wt)
	claudeLists(t, review, build, inTerminal("typing", "idle", wt, 4242))

	for session, want := range map[string]string{
		"aaaaaaaa":          "aaaaaaaa",
		build.SessionID:     "bbbbbbbb",
		"bbbbbbbb":          "bbbbbbbb", // the id, though review's name has it too
		"aaaaaaaa-0000-400": "aaaaaaaa",
		"REVIEW":            "aaaaaaaa",
		"uil":               "bbbbbbbb",
	} {
		o := forShell("thing")
		o.Session = session
		plan, shown, err := attach(t, ctx, o, "")
		if err != nil || plan != attachPlan(want) {
			t.Errorf("%q: want %s attached, got %q, %v\n%s", session, want, plan, err, shown)
		}
	}

	o := forShell("thing")
	o.Session = "i"
	_, shown, err := attach(t, ctx, o, "")
	if err == nil || !strings.Contains(err.Error(), `3 sessions match "i"`) || !strings.Contains(shown, "typing") {
		t.Errorf("want a tie listed and nothing opened, got %v\n%s", err, shown)
	}
	o.Session = "typing"
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.Contains(err.Error(), "open in a terminal (pid 4242)") {
		t.Errorf("want the terminal's session refused by name, got %v", err)
	}
	o.Session = "nothing-like-it"
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.Contains(err.Error(), "no session in thing has") {
		t.Errorf("want no match said, got %v", err)
	}
}

// A session's id works with no worktree named. Text that is both an id and
// a worktree opens neither.
func TestAttachTakesASessionIdAlone(t *testing.T) {
	ctx, main, _ := attachFixture(t)
	elsewhere := background("far away", "3f9a1c20", "working", t.TempDir())
	claudeLists(t, elsewhere)

	for _, id := range []string{"3f9a1c20", elsewhere.SessionID} {
		plan, shown, err := attach(t, ctx, forShell(id), "")
		if err != nil || plan != attachPlan("3f9a1c20") {
			t.Errorf("%s: want the session attached, got %q, %v\n%s", id, plan, err, shown)
		}
	}
	if _, _, err := attach(t, ctx, forShell("3f9a"), ""); err == nil || !strings.Contains(err.Error(), "no worktree matches") {
		t.Errorf("part of an id is not an id alone, got %v", err)
	}

	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/3f9a1c20", filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "3f9a1c20"))
	plan, _, err := attach(t, ctx, forShell("3f9a1c20"), "")
	if err == nil || plan != "" {
		t.Fatalf("an id that is also a worktree must open neither: %q, %v", plan, err)
	}
	for _, want := range []string{"far away", "claude attach 3f9a1c20", "wt attach demo/3f9a1c20"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

// With no pattern: the worktree you are in, and from the main checkout
// every session in the repository's worktrees.
func TestAttachWithNoPatternLooksWhereYouStand(t *testing.T) {
	ctx, main, wt := attachFixture(t)
	other := filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "other")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/other", other)
	claudeLists(t, background("in thing", "aaaaaaaa", "working", wt), background("in other", "bbbbbbbb", "working", other))

	here, err := Open(wt)
	if err != nil {
		t.Fatal(err)
	}
	if plan, _, err := attach(t, here, forShell(""), ""); err != nil || plan != attachPlan("aaaaaaaa") {
		t.Errorf("in a worktree: want its own session, got %q, %v", plan, err)
	}

	// The rows follow `wt list`: git's order of the worktrees.
	plan, shown, err := attach(t, ctx, forShell(""), "2\n")
	if err != nil {
		t.Fatalf("Attach: %v\n%s", err, shown)
	}
	if !strings.Contains(shown, "Claude sessions in any worktree of demo:") ||
		!strings.Contains(shown, "  1  other  in other") || !strings.Contains(shown, "  2  thing  in thing") {
		t.Errorf("want every worktree's session numbered, with its worktree:\n%s", shown)
	}
	if plan != attachPlan("aaaaaaaa") {
		t.Errorf("want the second attached, got %q", plan)
	}

	if _, _, err := attach(t, nil, forShell(""), ""); err == nil || !strings.Contains(err.Error(), "name a worktree") {
		t.Errorf("outside a repository there is nowhere to look, got %v", err)
	}
}

func TestAttachAsksWhichWorktreeOnATie(t *testing.T) {
	ctx, main, wt := attachFixture(t)
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing-two", filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "thing-two"))
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing-one", filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "thing-one"))
	claudeLists(t, background("fix the crash", "3f9a1c20", "working", wt))

	_, shown, err := attach(t, ctx, forShell("thing-"), "")
	if err == nil || !strings.Contains(shown, `"thing-" matches 2 worktrees:`) || !strings.Contains(err.Error(), "<repository>/<work>") {
		t.Errorf("want the tie listed and nothing guessed, got %v\n%s", err, shown)
	}
	if _, _, err := attach(t, ctx, forShell("thing-"), "1\n"); err == nil || !strings.Contains(err.Error(), "no Claude session in thing-") {
		t.Errorf("want the chosen worktree looked in, got %v", err)
	}
}

// --resume continues a conversation in the worktree, and never one claude
// still lists as a session.
func TestAttachResume(t *testing.T) {
	ctx, main, wt := attachFixture(t)
	claudeLists(t)

	o := forShell("thing")
	o.Resume = true
	if plan, _, err := attach(t, ctx, o, ""); err != nil || plan != "continue\n\n"+wt+"\nend\n" {
		t.Errorf("want claude --continue in the worktree, got %q, %v", plan, err)
	}
	o.Session = "0b5c9d1e-1111-4222-8333-444455556666"
	if plan, _, err := attach(t, ctx, o, ""); err != nil || plan != "resume\n"+o.Session+"\n"+wt+"\nend\n" {
		t.Errorf("want that conversation resumed in the worktree, got %q, %v", plan, err)
	}
	for _, bad := range []string{"-p", "two\nlines", "esc\x1b[2J"} {
		o.Session = bad
		if plan, _, err := attach(t, ctx, o, ""); err == nil || plan != "" {
			t.Errorf("%q is not a session id: %q, %v", bad, plan, err)
		}
	}

	// With no pattern it is the worktree you stand in, the main checkout
	// included.
	bare := AttachOptions{ForShell: true, Resume: true}
	if plan, _, err := attach(t, ctx, bare, ""); err != nil || plan != "continue\n\n"+main+"\nend\n" {
		t.Errorf("want the main checkout, got %q, %v", plan, err)
	}

	// Without the shell layer: the line to type, the path quoted.
	_, _, err := attach(t, ctx, AttachOptions{Pattern: "thing", Resume: true}, "")
	if err == nil || !strings.HasSuffix(err.Error(), "\n  cd '"+wt+"' && claude --continue") {
		t.Errorf("want the command a person would run, got %v", err)
	}
}

func TestAttachResumeRefusesAListedConversation(t *testing.T) {
	ctx, main, wt := attachFixture(t)
	live := background("fix the crash", "3f9a1c20", "done", wt)
	claudeLists(t, live, inTerminal("typing", "idle", main, 4242))

	o := forShell("thing")
	o.Resume = true
	plan, shown, err := attach(t, ctx, o, "")
	if err == nil || plan != "" {
		t.Fatalf("want a refusal, got %q, %v", plan, err)
	}
	if !strings.Contains(shown, "fix the crash  done  background 3f9a1c20") || !strings.Contains(err.Error(), "wt attach thing  opens the background one") {
		t.Errorf("want the session named and attach offered: %v\n%s", err, shown)
	}

	// In the main checkout the session is in a terminal: nothing to attach.
	o.Pattern = "/"
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.Contains(err.Error(), "used in its terminal") {
		t.Errorf("want the terminal's session to block it too, got %v", err)
	}

	// By id: refused when that conversation is listed, wherever it runs.
	o.Session = live.SessionID
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.Contains(err.Error(), "claude attach 3f9a1c20 opens it") {
		t.Errorf("want the listed conversation refused, got %v", err)
	}
	// Another conversation is resumed beside the live one.
	o.Session = "0b5c9d1e-1111-4222-8333-444455556666"
	if plan, _, err := attach(t, ctx, o, ""); err != nil || plan != "resume\n"+o.Session+"\n"+main+"\nend\n" {
		t.Errorf("want another conversation resumed, got %q, %v", plan, err)
	}
}

// What goes onto the shell's command line is an id, never free text.
func TestAttachRefusesAnIdThatIsNotOne(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	for _, id := range []string{"--dangerously-skip-permissions", "a b", "$(reboot)", "a;b", "x\ny"} {
		claudeLists(t, background("odd", id, "working", wt))
		plan, _, err := attach(t, ctx, forShell("thing"), "")
		if err == nil || plan != "" {
			t.Errorf("id %q reached the shell: %q, %v", id, plan, err)
		}
	}
}

func TestAttachNeedsClaude(t *testing.T) {
	ctx, _, _ := attachFixture(t)
	if _, _, err := attach(t, ctx, forShell("thing"), ""); err == nil || !strings.Contains(err.Error(), "claude is not on the PATH") {
		t.Errorf("want to be told claude is missing, got %v", err)
	}
}

// The session wt runs under is listed and cannot be chosen.
func TestAttachDoesNotOpenItsOwnSession(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	own := background("me", "aaaaaaaa", "working", wt)
	own.Own = true
	claudeLists(t, own)

	plan, shown, err := attach(t, ctx, forShell("thing"), "")
	if err == nil || plan != "" || !strings.Contains(shown, "me  -  this session") {
		t.Errorf("want it listed as this session and not opened: %q, %v\n%s", plan, err, shown)
	}
}

// A background session resumed in a terminal is one row, the terminal's,
// and its attach id still names it: the refusal says where it is.
func TestAttachByTheIdOfASessionResumedInATerminal(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	resumed := inTerminal("resumed", "idle", wt, 4242)
	resumed.ID, resumed.SessionID = "bbbb2222", "bbbb2222-1111-4222-8333-444455556666"
	claudeLists(t, resumed)

	for _, o := range []AttachOptions{forShell("bbbb2222"), forShell(resumed.SessionID), {Pattern: "thing", Session: "bbbb2222", ForShell: true}} {
		plan, _, err := attach(t, ctx, o, "")
		if err == nil || plan != "" || !strings.Contains(err.Error(), "resumed is open in a terminal (pid 4242)") {
			t.Errorf("%+v: want it refused as the terminal's, got %q, %v", o, plan, err)
		}
	}
}

// A line a person may paste carries only an id that looks like one.
func TestAttachNeverPrintsACommandWithAnOddId(t *testing.T) {
	ctx, main, wt := attachFixture(t)
	odd := background("odd", "x; touch /p", "working", wt)
	odd.SessionID = "0dd00000-1111-4222-8333-444455556666"
	claudeLists(t, odd)

	o := forShell("/")
	o.Resume, o.Session = true, odd.SessionID
	plan, shown, err := attach(t, ctx, o, "")
	if err == nil || plan != "" {
		t.Fatalf("want the listed conversation refused: %q, %v", plan, err)
	}
	if strings.Contains(err.Error()+shown, "claude attach") {
		t.Errorf("a claude attach line was printed with an id that is not one: %v\n%s", err, shown)
	}

	// The same id that is also a worktree's name: neither is opened, and
	// the session's line is not a command.
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/0dd00000", filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "0dd00000"))
	odd.SessionID = "0dd00000"
	claudeLists(t, odd)
	_, _, err = attach(t, ctx, forShell("0dd00000"), "")
	if err == nil || strings.Contains(err.Error(), "claude attach") || !strings.Contains(err.Error(), "wt attach demo/0dd00000") {
		t.Errorf("want both named and no command with that id, got %v", err)
	}

	// In the list it is a background session that cannot be attached by id.
	claudeLists(t, odd, background("ok", "aaaaaaaa", "working", wt), background("no id", "", "working", wt))
	o = forShell("thing")
	o.Session = "o"
	_, shown, _ = attach(t, ctx, o, "")
	if strings.Count(shown, "background, no id to attach by") != 2 || !strings.Contains(shown, "background aaaaaaaa") {
		t.Errorf("want the two without a usable id said so:\n%s", shown)
	}
}

// --resume <id> refuses the listed conversation however the id is cased,
// and says what it knows when the text is only the start of a listed id.
func TestAttachResumeMatchesIdsWhateverTheirCase(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	live := background("fix the crash", "3f9a1c20", "working", wt)
	claudeLists(t, live)

	o := forShell("/")
	o.Resume = true
	o.Session = strings.ToUpper(live.SessionID)
	if plan, _, err := attach(t, ctx, o, ""); err == nil || plan != "" || !strings.Contains(err.Error(), "claude lists that conversation as a session") {
		t.Errorf("an upper-cased id is the same conversation: %q, %v", plan, err)
	}
	o.Session = "3F9A1C20-00"
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.Contains(err.Error(), "claude lists a session whose id starts with that") {
		t.Errorf("want the start of an id refused in words that are true of it, got %v", err)
	}
	o = forShell("3F9A1C20")
	if plan, _, err := attach(t, ctx, o, ""); err != nil || plan != attachPlan("3f9a1c20") {
		t.Errorf("an id alone is found whatever its case: %q, %v", plan, err)
	}
}

// From the main checkout one session is opened without a question, and wt
// says which worktree it is in.
func TestAttachFromTheMainCheckoutSaysWhereTheSessionIs(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	claudeLists(t, background("fix the crash", "3f9a1c20", "working", wt))

	plan, shown, err := attach(t, ctx, forShell(""), "")
	if err != nil || plan != attachPlan("3f9a1c20") {
		t.Fatalf("want it attached: %q, %v", plan, err)
	}
	if shown != "opening fix the crash, in thing\n" {
		t.Errorf("want the worktree named, got %q", shown)
	}
}

// The hint under a refused --resume is true of the session that refused it.
func TestAttachResumeHintFitsTheSession(t *testing.T) {
	ctx, _, wt := attachFixture(t)
	own := background("me", "aaaaaaaa", "working", wt)
	own.Own = true
	claudeLists(t, own)

	o := forShell("thing")
	o.Resume = true
	if _, _, err := attach(t, ctx, o, ""); err == nil || !strings.HasSuffix(err.Error(), "\n  it is the session wt is running in") {
		t.Errorf("want its own session said to be its own, got %v", err)
	}
	o.Pattern = "nothing-like-it"
	if _, _, err := attach(t, ctx, o, ""); err == nil || err.Error() != `no worktree matches "nothing-like-it"` {
		t.Errorf("with --resume a pattern is only ever a worktree, got %v", err)
	}
}
