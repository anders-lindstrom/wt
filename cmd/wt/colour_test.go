package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/creack/pty"
)

// Colour reaches a person and nobody else: under a pipe, and under a
// pseudo-terminal with an agent marker set (some harnesses run commands in
// one), help and status output hold no escape byte. The control, a bare
// pseudo-terminal, shows the test can see colour when it is allowed.
func TestColourNeverReachesAProgramOrAnAgent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "CODEX_THREAD_ID",
		"CODEX_SANDBOX", "CODEX_CI", "CURSOR_AGENT", "OPENCODE", "AGENT", "AI_AGENT", "CI", "NO_COLOR"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "xterm-256color")

	// run writes wt's help, a status listing (doctor) and an error line to w.
	run := func(w io.Writer) {
		for _, args := range [][]string{{"--help"}, {"new", "--help"}, {"doctor"}} {
			root := newRootCmd(args...)
			root.SetOut(w)
			root.SetErr(io.Discard)
			_ = root.Execute()
		}
		_, _ = io.WriteString(w, errorLine(w, errors.New("something failed")))
	}
	const escape = "\x1b"

	t.Run("pipe", func(t *testing.T) {
		var out bytes.Buffer
		run(&out)
		if strings.Contains(out.String(), escape) || out.Len() == 0 {
			t.Errorf("a pipe got %d bytes with an escape in them: %q", out.Len(), out.String())
		}
	})

	onPty := func(t *testing.T) string {
		t.Helper()
		master, slave, err := pty.Open()
		if err != nil {
			t.Skipf("no pseudo-terminal here: %v", err)
		}
		defer master.Close()
		var got bytes.Buffer
		done := make(chan struct{})
		go func() { _, _ = io.Copy(&got, master); close(done) }()
		run(slave)
		_ = slave.Close()
		<-done
		return got.String()
	}

	t.Run("pseudo-terminal with an agent marker", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		if out := onPty(t); strings.Contains(out, escape) || out == "" {
			t.Errorf("an agent's terminal got escapes: %q", out)
		}
	})
	t.Run("pseudo-terminal with NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if out := onPty(t); strings.Contains(out, escape) || out == "" {
			t.Errorf("NO_COLOR was ignored: %q", out)
		}
	})
	t.Run("control: a bare pseudo-terminal", func(t *testing.T) {
		if out := onPty(t); !strings.Contains(out, escape) {
			t.Errorf("a person's terminal got no colour at all: %q", out)
		}
	})
}
