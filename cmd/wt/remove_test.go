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
