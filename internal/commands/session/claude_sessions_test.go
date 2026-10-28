package session

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
)

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
	for !PathTableFits(ascii, width) {
		width++
	}
	if !PathTableFits(wide, width+2) || PathTableFits(wide, width+1) {
		t.Errorf("two wide characters need two more columns than %d", width)
	}
	for s, want := range map[string]int{"abc": 3, "修正": 4, "e\u0301": 1, "🙂": 2, "": 0} {
		if got := DisplayWidth(s); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", s, got, want)
		}
	}
	if got := cutToWidth("修正修正修正", 7); got != "修正修…" || DisplayWidth(got) != 7 {
		t.Errorf("cutToWidth gave %q (%d columns)", got, DisplayWidth(got))
	}
	if got := cutToWidth("修正修正", 8); got != "修正修正" {
		t.Errorf("a name that fits is left whole, got %q", got)
	}
}

// A state this build has never seen is printed as claude spells it.
func TestSessionStateIsClaudesOwnWordWhenUnknown(t *testing.T) {
	for state, want := range map[string]string{
		"working": "working", "blocked": "needs input", "done": "done",
		"failed": "failed", "stopped": "stopped", "hibernating": "hibernating",
	} {
		s := ClaudeSession{Agent: agents.Agent{Kind: "background", State: state}}
		if got := s.state(); got != want {
			t.Errorf("state %q reads %q, want %q", state, got, want)
		}
	}
}
