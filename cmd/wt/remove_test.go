package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// A script or an agent redirects stdin from /dev/null, which is a character
// device — so the usual ModeCharDevice check calls it a terminal and asks a
// question nobody is there to answer.
func TestIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Error("/dev/null is not a terminal")
	}
}

func TestIsTerminalRejectsAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if isTerminal(r) {
		t.Error("a pipe is not a terminal")
	}
}

func TestIsTerminalRejectsARegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
}

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
		got, err := confirmRemoval(strings.NewReader(tc.typed), &out)(commands.Plan{})
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
