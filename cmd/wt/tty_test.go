package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// canAsk looks at the reader a prompt would read. One that is not a file at
// all has nobody behind it.
func TestCanAskNeedsATerminalOnTheCommandsInput(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("y\n"))
	if canAsk(cmd) {
		t.Error("a string reader is not a terminal")
	}

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd.SetIn(f)
	if canAsk(cmd) {
		t.Error("/dev/null is not a terminal")
	}
}
