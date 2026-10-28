package kit

import (
	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SessionLabel is what a session is called in a line: its name, else its
// kind.
func SessionLabel(a *agents.Agent) string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Kind != "":
		return a.Kind
	}
	return "an unnamed session"
}

// StandingIn reports whether cwd, the caller's own working directory, is inside
// the worktree about to move.
func StandingIn(cwd, path string) bool {
	return repo.Inside(path, cwd, true)
}

// FetchReason is the line of a failed fetch's error worth printing: ssh and
// git can put warnings before the reason and hints after it.
func FetchReason(err error) string {
	return Truncate(git.Reason(err, "warning:", "hint:"), NoteWidth)
}

// BranchWorkName is the work name a branch reads as under the convention,
// the branch itself when it does not follow it.
func BranchWorkName(ctx *Context, branch string) string {
	if branch == "" {
		return "(detached)"
	}
	if _, work, ok := ctx.Scheme().Parse(branch); ok {
		return work
	}
	return branch
}

// WorktreeName is what a worktree is called, and is what to use wherever one
// is at hand: its branch's work name where the branch follows the convention,
// otherwise the name its path gives it. That is how `wt list`, completion and
// `wt find` name it; any other name is one you cannot type back.
func WorktreeName(ctx *Context, branch, path string) string {
	if _, work, ok := ctx.Scheme().Parse(branch); ok {
		return work
	}
	if _, work, ok := ctx.Scheme().ClassifyPath(path); ok {
		return work
	}
	return BranchWorkName(ctx, branch)
}

// WhoLabel is the sessions in a worktree as a row names them: "session
// parked-1 (idle) +1", or "an unnamed session" when there is nothing to
// call the lead by.
func WhoLabel(s agents.Sessions) string {
	label := s.Label(SessionLabel)
	if lead := s.Lead(); lead != nil && (lead.Name != "" || lead.Kind != "") {
		return "session " + label
	}
	return label
}

// ListAgain lists the sessions a second time, for the check after a question
// or at the lock: through relist when a caller supplied one, the caller's own
// list when it supplied that, and claude otherwise.
func ListAgain(given []agents.Agent, relist func() ([]agents.Agent, error)) ([]agents.Agent, error) {
	switch {
	case relist != nil:
		return relist()
	case given != nil:
		return given, nil
	}
	return agents.ListOtherAgents()
}
