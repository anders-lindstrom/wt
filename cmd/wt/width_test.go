package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/creack/pty"
)

// On a terminal the flag list wraps to the terminal's width, not just the
// description: UsageString renders into a buffer, so the width has to be
// worked out from the real writer first.
func TestFlagListWrapsToTheTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "70")
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer master.Close()
	var got bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&got, master); close(done) }()
	root := newRootCmd("remove", "--help")
	root.SetOut(slave)
	root.SetErr(io.Discard)
	_ = root.Execute()
	_ = slave.Close()
	<-done
	_, flags, _ := strings.Cut(strings.ReplaceAll(got.String(), "\r", ""), "\nFlags:\n")
	if flags == "" {
		t.Fatalf("no flag list in:\n%s", got.String())
	}
	for _, line := range strings.Split(flags, "\n") {
		if n := utf8.RuneCountInString(line); n > 70 {
			t.Errorf("a %d-column line on a 70-column terminal: %q", n, line)
		}
	}
}

func TestReflowHangsATableRowUnderItsOwnCell(t *testing.T) {
	const table = "  look  wt list      what exists,\n" +
		"                     hand-wrapped\n" +
		"        wt alone\n" +
		"  act   wt up        the same for the worktree you are in only if it goes through"
	want := "  look  wt list      what exists, hand-wrapped\n" +
		"        wt alone\n" +
		"  act   wt up        the same for the worktree\n" +
		"                     you are in only if it\n" +
		"                     goes through"
	if got := reflow(table, 46); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The column a cell starts at counts what stands before it in columns, not
// in bytes: å, ä and ö are two bytes each but one column.
func TestReflowHangsUnderACellAfterNonASCII(t *testing.T) {
	const table = "  åäö   wt up        the same for the worktree you are in only if it goes through"
	want := "  åäö   wt up        the same for the worktree\n" +
		"                     you are in only if it\n" +
		"                     goes through"
	if got := reflow(table, 46); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A last cell that starts with a wide character still starts where its first
// character does.
func TestReflowHangsUnderACellThatStartsWithAWideCharacter(t *testing.T) {
	const table = "  key  日本 the same for the worktree you are in only if it goes through"
	want := "  key  日本 the same for the worktree you are\n" +
		"       in only if it goes through"
	if got := reflow(table, 46); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
