package syncrun

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
)

func TestSessionsChangedNamesWhatIsNewOrBusy(t *testing.T) {
	t.Parallel()
	parked := agents.Agent{ID: "a1", Name: "parked-1", Cwd: "/w", Kind: "interactive", Status: "idle"}
	busy := parked
	busy.Status = "busy"
	other := agents.Agent{ID: "a2", Name: "new-2", Cwd: "/w", Kind: "interactive", Status: "idle"}
	told := agents.Sessions{parked}
	for _, tc := range []struct {
		now  agents.Sessions
		want string
	}{
		{agents.Sessions{parked}, ""},
		{nil, ""},
		{agents.Sessions{busy}, "an agent session is busy in it now: parked-1"},
		{agents.Sessions{parked, other}, "a session arrived since it was checked: new-2 (idle)"},
	} {
		if got := SessionsChanged(told, tc.now); got != tc.want {
			t.Errorf("now %+v: got %q, want %q", tc.now, got, tc.want)
		}
	}
}
