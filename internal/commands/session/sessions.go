package session

import "github.com/anders-lindstrom/wt/internal/agents"

// ListSessions reads the agent sessions, Claude's and Codex's, leaving
// out the one wt runs under: the session a WorktreeRemove hook fires in, or
// whose shell runs wt_rm_me, is not somebody else in the worktree. A var so
// tests stay off the machine's own claude.
var ListSessions = agents.ListOtherAgents

// SessionsIn is the one answer remove, migrate and sweep give to "is an
// agent session in this worktree": every session whose working directory is
// inside path, idle or busy, and an error when they could not be read, which
// is not the same answer as none. listed and listErr are a listing the
// caller already has; nil listed and no error list them here. No claude on
// the PATH is no sessions: nobody here uses Claude.
func SessionsIn(listed []agents.Agent, listErr error, path string) (agents.Sessions, error) {
	if listErr != nil {
		return nil, listErr
	}
	if listed == nil {
		var err error
		if listed, err = ListSessions(); err != nil {
			return nil, err
		}
	}
	return agents.SessionsAt(listed, path), nil
}
