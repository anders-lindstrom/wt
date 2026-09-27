package commands

import "github.com/anders-lindstrom/wt/internal/wtsync"

// listSessions reads the Claude sessions when the caller has not, leaving
// out the one wt runs under: the session a WorktreeRemove hook fires in, or
// whose shell runs wt_rm_me, is not somebody else in the worktree. A var so
// the package's tests stay off the machine's own claude.
var listSessions = wtsync.ListOtherAgents

// sessionsIn is the one answer remove, migrate and sweep give to "is a
// Claude session in this worktree": every session whose working directory is
// inside path, idle or busy, and an error when they could not be read, which
// is not the same answer as none. agents and listErr are a listing the
// caller already has; nil agents and no error list them here. No claude on
// the PATH is no sessions: nobody here uses Claude.
func sessionsIn(agents []wtsync.Agent, listErr error, path string) (wtsync.Sessions, error) {
	if listErr != nil {
		return nil, listErr
	}
	if agents == nil {
		var err error
		if agents, err = listSessions(); err != nil {
			return nil, err
		}
	}
	return wtsync.SessionsAt(agents, path), nil
}
