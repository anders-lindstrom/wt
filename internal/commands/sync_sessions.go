package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// idleNotice is said before anything moves under an idle session, whether or
// not anybody is asked: a verb never changes files under one silently.
func idleNotice(work string, s wtsync.Sessions) string {
	return fmt.Sprintf("⚠ %s: %s is in it; the files it has read will change", work, whoLabel(s))
}

// tellIdle prints a §5 line for the idle sessions in a worktree, on a line of
// its own so it can be relayed to them verbatim. Nothing when there are none.
func tellIdle(w io.Writer, s wtsync.Sessions, line string) {
	if len(s) == 0 {
		return
	}
	names := make([]string, len(s))
	for i := range s {
		names[i] = sessionLabel(&s[i])
	}
	fmt.Fprintf(w, "  ⚠ tell %s, idle in it:\n    %s\n", strings.Join(names, ", "), line)
}

// listAgain lists the sessions a second time, for the check after a question
// or at the lock: through relist when a caller supplied one, the caller's own
// list when it supplied that, and claude otherwise.
func listAgain(given []wtsync.Agent, relist func() ([]wtsync.Agent, error)) ([]wtsync.Agent, error) {
	switch {
	case relist != nil:
		return relist()
	case given != nil:
		return given, nil
	}
	return wtsync.ListOtherAgents()
}

// sessionsChanged is why the sessions in a worktree are no longer the ones a
// verb checked: one is busy now, or one arrived that nobody was told about.
// Empty when nothing changed that matters.
func sessionsChanged(told, now wtsync.Sessions) string {
	if len(now.Busy()) > 0 {
		return "an agent session is busy in it now: " + now.Label(sessionLabel)
	}
	if arrived := now.Arrived(told); len(arrived) > 0 {
		return "a session arrived since it was checked: " + arrived.Label(sessionLabel)
	}
	return ""
}

// pathsOnce joins lists of paths, keeping the first of each.
func pathsOnce(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}
