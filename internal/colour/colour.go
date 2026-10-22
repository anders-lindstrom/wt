// Package colour decides whether wt's own words get colour, and paints them.
//
// Colour reaches a person and nobody else. It is on only when the stream is a
// terminal, no agent or CI marker is set, NO_COLOR is unset and TERM is not
// dumb; the one way to force it is the per-call --color=always. FORCE_COLOR
// and CLICOLOR_FORCE are ignored: a global setting would leak escapes into
// pipes. Output another program parses (--json, a path on stdout) is never
// painted, and a child whose output wt reads is started with NO_COLOR=1 in its
// own environment, so what wt passes straight to a person keeps its colour.
package colour

import (
	"io"
	"os"

	"golang.org/x/term"
)

// The 16 standard ANSI colours, so the terminal's theme applies.
const (
	Reset  = "\x1b[0m"
	Bold   = "\x1b[1m"
	Dim    = "\x1b[2m"
	Red    = "\x1b[31m"
	Green  = "\x1b[32m"
	Yellow = "\x1b[33m"
	Cyan   = "\x1b[36m"
)

// markers are the variables an agent or a CI runner sets. Some harnesses run a
// command in a pseudo-terminal, so a terminal alone does not prove a person is
// reading.
var markers = []string{
	"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "CODEX_THREAD_ID",
	"CODEX_SANDBOX", "CODEX_CI", "CURSOR_AGENT", "OPENCODE", "AGENT", "AI_AGENT", "CI",
}

// Enabled says whether w gets colour. mode is the value of --color: "always"
// forces it, "never" refuses it, anything else decides from the environment.
func Enabled(w io.Writer, mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	for _, name := range markers {
		if os.Getenv(name) != "" {
			return false
		}
	}
	return true
}

// Paint wraps s in code and a reset, or returns it as it is when off.
func Paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return code + s + Reset
}
