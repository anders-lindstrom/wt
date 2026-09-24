package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func TestConfirmPushDefaultsToNo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"\n", false},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"q\n", false},
		{"", false}, // ^D declines
	} {
		var out bytes.Buffer
		got, err := confirmPush(newPrompter(strings.NewReader(tc.in), &out))([]string{"bump"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "push bump with --force-with-lease? [y/N]") {
			t.Errorf("question %q", out.String())
		}
	}
	var out bytes.Buffer
	if _, err := confirmPush(newPrompter(strings.NewReader("\n"), &out))([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "push a, b with --force-with-lease? [y/N]") {
		t.Errorf("question %q", out.String())
	}
}

func TestConfirmAskNamesTheWorktreesAndDefaultsToNo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"\n", false},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"", false}, // ^D declines
	} {
		var out bytes.Buffer
		got, err := confirmAsk(newPrompter(strings.NewReader(tc.in), &out), "rebase")([]string{"a", "b"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "rebase a, b? [y/N]") {
			t.Errorf("question %q", out.String())
		}
	}
}

// The verb flags on wt sync name one verb, or say which two were given.
func TestSyncVerbFlagsNameOneVerb(t *testing.T) {
	for _, tc := range []struct {
		flags syncVerbFlags
		verb  string
		err   string
	}{
		{syncVerbFlags{}, "", ""},
		{syncVerbFlags{run: true}, "run", ""},
		{syncVerbFlags{resume: true}, "resume", ""},
		{syncVerbFlags{undo: true}, "undo", ""},
		{syncVerbFlags{run: true, undo: true}, "", "--run and --undo cannot both be given"},
		{syncVerbFlags{resume: true, undo: true}, "", "--resume and --undo cannot both be given"},
		{syncVerbFlags{run: true, resume: true, undo: true}, "", "--run, --resume and --undo cannot both be given"},
	} {
		verb, err := tc.flags.verb()
		if verb != tc.verb {
			t.Errorf("%+v: verb %q, want %q", tc.flags, verb, tc.verb)
		}
		if got := errString(err); got != tc.err {
			t.Errorf("%+v: error %q, want %q", tc.flags, got, tc.err)
		}
	}
}

// A verb's flag without its verb is refused by name, the way --push with
// --no-push is: one line, no usage.
func TestSyncVerbFlagsRefuseAFlagWithoutItsVerb(t *testing.T) {
	for _, tc := range []struct {
		flags syncVerbFlags
		err   string
	}{
		{syncVerbFlags{}, ""},
		{syncVerbFlags{noFetch: true}, ""},
		{syncVerbFlags{run: true, yes: true, noFetch: true, push: commands.PushAlways}, ""},
		{syncVerbFlags{resume: true, yes: true, push: commands.PushNever}, ""},
		{syncVerbFlags{undo: true, yes: true, force: true}, ""},
		{syncVerbFlags{run: true, undo: true, force: true}, "--run and --undo cannot both be given"},
		{syncVerbFlags{push: commands.PushAlways}, "--push needs --run or --resume"},
		{syncVerbFlags{undo: true, push: commands.PushAlways}, "--push needs --run or --resume"},
		{syncVerbFlags{push: commands.PushNever}, "--no-push needs --run or --resume"},
		{syncVerbFlags{undo: true, push: commands.PushNever}, "--no-push needs --run or --resume"},
		{syncVerbFlags{force: true}, "--force needs --undo"},
		{syncVerbFlags{run: true, force: true}, "--force needs --undo"},
		{syncVerbFlags{resume: true, force: true}, "--force needs --undo"},
		{syncVerbFlags{yes: true}, "--yes needs --run, --resume or --undo"},
		{syncVerbFlags{resume: true, noFetch: true}, "--no-fetch needs --run, or no verb"},
		{syncVerbFlags{undo: true, noFetch: true}, "--no-fetch needs --run, or no verb"},
	} {
		if got := errString(tc.flags.check()); got != tc.err {
			t.Errorf("%+v: error %q, want %q", tc.flags, got, tc.err)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The flag spelling takes the count its verb's subcommand takes, refused in
// the same words; the overview keeps taking at most one.
func TestSyncVerbFlagsTakeTheVerbsArgumentCount(t *testing.T) {
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"sync", "a", "b"}, "accepts at most 1 arg(s), received 2"},
		{[]string{"sync", "--resume"}, "accepts 1 arg(s), received 0"},
		{[]string{"sync", "a", "b", "--resume"}, "accepts 1 arg(s), received 2"},
		{[]string{"sync", "resume", "a", "b"}, "accepts 1 arg(s), received 2"},
		{[]string{"sync", "a", "b", "--undo"}, "accepts 1 arg(s), received 2"},
		{[]string{"sync", "undo", "a", "b"}, "accepts 1 arg(s), received 2"},
	} {
		out, err := runCmd(t, tc.args...)
		if err == nil {
			t.Errorf("%v: want an error", tc.args)
			continue
		}
		if err.Error() != tc.err {
			t.Errorf("%v: error %q, want %q", tc.args, err.Error(), tc.err)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: an argument mistake shows usage:\n%s", tc.args, out)
		}
	}
}

// run with nothing named is every ready worktree, in both spellings: no
// argument count refuses it, so it gets as far as opening the repository.
func TestSyncRunWithNothingNamedIsNotAnArgumentMistake(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"sync", "--run"}, {"sync", "run"}, {"sync", "run", "--no-fetch"}} {
		out, err := runCmd(t, args...)
		if err == nil || err.Error() != "not a git repository" {
			t.Errorf("%v: error %v, want the verb's own not-a-repository error\n%s", args, err, out)
		}
	}
}

// Both spellings of a verb open the repository the verb's way (withContext,
// which refuses a directory outside a repository as "not a git repository")
// rather than the overview's lenient way, which says "not inside a git
// repository". Outside a repository that is the first thing either does, so
// the same error from both is the same code path reached.
func TestSyncVerbFlagsReachTheVerb(t *testing.T) {
	t.Chdir(t.TempDir())
	_, overview := runCmd(t, "sync", "a")
	if overview == nil || overview.Error() != "not inside a git repository" {
		t.Fatalf("overview outside a repository: %v", overview)
	}
	for _, tc := range [][2][]string{
		{{"sync", "run", "a"}, {"sync", "a", "--run"}},
		{{"sync", "run", "a", "b", "--yes", "--push", "--no-fetch"}, {"sync", "a", "b", "--run", "-y", "--push", "--no-fetch"}},
		{{"sync", "resume", "a", "--no-push"}, {"sync", "a", "--resume", "--no-push"}},
		{{"sync", "resume", "a", "-y"}, {"sync", "a", "--resume", "--yes"}},
		{{"sync", "undo", "a", "--force"}, {"sync", "a", "--undo", "--force"}},
		{{"sync", "undo", "a", "-y"}, {"sync", "a", "-y", "--undo"}},
	} {
		verbOut, verbErr := runCmd(t, tc[0]...)
		flagOut, flagErr := runCmd(t, tc[1]...)
		if verbErr == nil || verbErr.Error() == overview.Error() {
			t.Fatalf("%v: the verb did not open the repository its own way: %v", tc[0], verbErr)
		}
		if flagErr == nil || flagErr.Error() != verbErr.Error() {
			t.Errorf("%v: %v; the verb says %v", tc[1], flagErr, verbErr)
		}
		if flagOut != verbOut {
			t.Errorf("%v printed %q; the verb printed %q", tc[1], flagOut, verbOut)
		}
	}
}

// A flag mistake on the flag spelling prints one line and no usage, as
// --push with --no-push does on the verb.
func TestSyncVerbFlagMistakesDoNotShowUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"sync", "a", "--run", "--undo"},
		{"sync", "a", "--push"},
		{"sync", "a", "--force"},
		{"sync", "a", "--yes"},
		{"sync", "a", "--resume", "--no-fetch"},
	} {
		out, err := runCmd(t, args...)
		if err == nil {
			t.Errorf("%v: want an error", args)
			continue
		}
		if strings.Contains(out, "Usage:") {
			t.Errorf("%v: a flag mistake does not show usage:\n%s", args, out)
		}
	}
}

// -y is --yes on every verb, as on wt remove, wt sweep and wt init.
func TestSyncVerbsTakeYForYes(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{{"sync"}, {"sync", "run"}, {"sync", "resume"}, {"sync", "undo"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		f := c.Flags().ShorthandLookup("y")
		if f == nil || f.Name != "yes" {
			t.Errorf("%s: -y is not --yes", c.CommandPath())
		}
	}
}

// sync run asks before the rebase and again before the push. Both answers can
// be typed ahead, so both questions have to read through one prompter.
func TestRunQuestionsShareOneReader(t *testing.T) {
	var out bytes.Buffer
	p := newPrompter(strings.NewReader("n\ny\n"), &out)
	if ok, _ := confirmAsk(p, "rebase")([]string{"bump"}); ok {
		t.Error("the rebase question did not read its n")
	}
	if ok, _ := confirmPush(p)([]string{"bump"}); !ok {
		t.Error("the push question lost the y typed ahead for it")
	}
	if want := "rebase bump? [y/N] push bump with --force-with-lease? [y/N] "; out.String() != want {
		t.Errorf("questions = %q, want %q", out.String(), want)
	}
}

// With nobody at a terminal, a bulk run rebases nothing without --yes; a
// named worktree goes ahead; --yes asks nothing, the push included.
func TestRunOptionsAskNobodyAndRefuseBulkWithoutATerminal(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)

	bulk, _ := runOptions(cmd, syncVerbFlags{run: true}, true)
	if bulk.Confirm == nil {
		t.Fatal("a bulk run with no terminal must not go ahead unasked")
	}
	if ok, _ := bulk.Confirm([]string{"bump"}); ok || !strings.Contains(out.String(), "Pass --yes to rebase bump.") {
		t.Errorf("want a no that says how to say yes: %v %q", ok, out.String())
	}
	if named, _ := runOptions(cmd, syncVerbFlags{run: true}, false); named.Confirm != nil {
		t.Error("a named worktree with no terminal goes ahead")
	}
	yes, p := runOptions(cmd, syncVerbFlags{run: true, yes: true}, true)
	if yes.Confirm != nil || yes.ConfirmPush != nil || p != nil {
		t.Error("--yes asks nothing")
	}
}
