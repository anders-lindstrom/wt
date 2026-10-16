package session

import "github.com/anders-lindstrom/wt/internal/wtsync"

// ListSessions reads the agent sessions, Claude's and Codex's, leaving
// out the one wt runs under: the session a WorktreeRemove hook fires in, or
// whose shell runs wt_rm_me, is not somebody else in the worktree. A var so
// tests stay off the machine's own claude.
var ListSessions = wtsync.ListOtherAgents

// SessionsIn is the one answer remove, migrate and sweep give to "is an
// agent session in this worktree": every session whose working directory is
// inside path, idle or busy, and an error when they could not be read, which
// is not the same answer as none. agents and listErr are a listing the
// caller already has; nil agents and no error list them here. No claude on
// the PATH is no sessions: nobody here uses Claude.
func SessionsIn(agents []wtsync.Agent, listErr error, path string) (wtsync.Sessions, error) {
	if listErr != nil {
		return nil, listErr
	}
	if agents == nil {
		var err error
		if agents, err = ListSessions(); err != nil {
			return nil, err
		}
	}
	return wtsync.SessionsAt(agents, path), nil
}
