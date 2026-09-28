package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// The prompt defaults to no, because the cost of a mistaken yes is a checkout
// and the cost of a mistaken no is retyping the command.
func TestConfirmRemovalAnswers(t *testing.T) {
	for _, tc := range []struct {
		typed string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" y \n", true},
		{"n\n", false},
		{"\n", false},
		{"anything else\n", false},
		{"", false}, // ^D
	} {
		var out bytes.Buffer
		got, err := confirmRemoval(newPrompter(strings.NewReader(tc.typed), &out))(commands.Plan{})
		if err != nil {
			t.Fatalf("%q: %v", tc.typed, err)
		}
		if got != tc.want {
			t.Errorf("answering %q = %v, want %v", tc.typed, got, tc.want)
		}
		if !strings.Contains(out.String(), "Remove it?") {
			t.Errorf("%q: no prompt was shown", tc.typed)
		}
	}
}

// An --expect given with no token holds the removal to nothing: refused,
// before anything is read, rather than taken as no --expect at all.
func TestRemoveRefusesAnEmptyExpect(t *testing.T) {
	cmd := newRemoveCmd()
	cmd.SetArgs([]string{"login-crash", "--yes", "--json", "--expect", ""})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--expect") {
		t.Fatalf("want a refusal naming --expect, got %v", err)
	}
}

// --expect without --yes --json, or with no token, is refused before
// anything is read.
func TestQuarantinePurgeRefusesAnExpectItCannotHold(t *testing.T) {
	for _, args := range [][]string{
		{"quarantine", "purge", "../trash/lc", "--yes", "--json", "--expect", ""},
		{"quarantine", "purge", "../trash/lc", "--json", "--expect", "1:0123abcd"},
		{"quarantine", "purge", "../trash/lc", "--yes", "--expect", "1:0123abcd"},
	} {
		if _, err := runCmd(t, args...); err == nil || !strings.Contains(err.Error(), "--expect") {
			t.Errorf("%v: want a refusal naming --expect, got %v", args, err)
		}
	}
}

// --force takes a list of what to go past, and refuses a name it does not
// know before anything is read. Bare, it is all of them, as it always was.
func TestRemoveForceFlagForms(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"login-crash", "--force=sessions"}, "--force takes all or a list of idle-sessions"},
		{[]string{"login-crash", "--force=idle-sessions,"}, "--force takes"},
		{[]string{"login-crash", "--force=lock", "--force=bogus"}, "\"bogus\""},
		{[]string{"login-crash", "--force=lock, bogus "}, "not \"bogus\""},
		{[]string{"login-crash", "--force=true"}, "--force takes"},
		{[]string{"login-crash", "--force=false"}, "--force takes"},
		// A category after a space is not --force's value: pflag reads a
		// bare --force and a worktree named idle-sessions.
		{[]string{"--force", "idle-sessions"}, "--force=idle-sessions"},
		{[]string{"-f", "busy-sessions,lock"}, "--force=<list>"},
		// --me and --me-at name the worktree, so nothing else may.
		{[]string{"--me-at", "/tmp/x", "--yes", "--force", "idle-sessions"}, "takes no worktree"},
		{[]string{"--me", "login-crash"}, "takes no worktree"},
	} {
		cmd := newRemoveCmd()
		cmd.SetArgs(tc.args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: want an error naming %q, got %v", tc.args, tc.want, err)
		}
	}
	for _, args := range [][]string{{"x", "--force"}, {"x", "-f"}, {"--force", "x"}, {"x", "--force=idle-sessions,lock"},
		{"x", "--force="}} {
		cmd := newRemoveCmd()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		values, _ := cmd.Flags().GetStringSlice("force")
		got, err := commands.ParseForce(values)
		want := commands.ForceAll
		switch args[1] {
		case "--force=idle-sessions,lock":
			want = commands.ForceIdleSessions | commands.ForceLock
		case "--force=":
			want = 0 // no force at all
		}
		if err != nil || got != want || cmd.Flags().Arg(0) != "x" {
			t.Errorf("%q: force %v (%v), arg %q", args, got, err, cmd.Flags().Arg(0))
		}
	}
}
