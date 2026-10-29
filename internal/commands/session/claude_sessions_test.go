package session

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
)

// A state this build has never seen is printed as claude spells it.
func TestSessionStateIsClaudesOwnWordWhenUnknown(t *testing.T) {
	t.Parallel()
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
