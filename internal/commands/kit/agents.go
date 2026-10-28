package kit

import (
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
)

// Agents is how a context learns who is in a worktree: the agent sessions
// and the processes there. A nil func is MachineAgents'.
type Agents struct {
	// List is every agent session but the one wt runs under, and Required
	// the same for a caller that has to know: no claude to ask is an error.
	List     func() ([]agents.Agent, error)
	Required func() ([]agents.Agent, error)
	// Claude is the Claude listing a person reads, Codex the Codex sessions
	// alone, and Ancestors the pid of every process above this one.
	Claude    func(deadline time.Duration) ([]agents.Agent, bool, error)
	Codex     func() ([]agents.Agent, error)
	Ancestors func() (map[int]bool, error)
	// Scan reads the process table; Reap ends processes, giving SIGTERM
	// grace before SIGKILL.
	Scan func(listed []agents.Agent) (*agents.ProcessScan, error)
	Reap func(procs []agents.Process, grace time.Duration) []agents.Reaped
}

// MachineAgents is the machine's own claude, codex and process table. Each
// TestMain sets it once, to fakes that reach nothing real; a test that wants
// another answer sets its context's Agents instead.
var MachineAgents = Agents{
	List:      agents.ListOtherAgents,
	Required:  agents.ListOtherAgentsRequired,
	Claude:    agents.ListClaudeSessions,
	Codex:     agents.ListCodexSessions,
	Ancestors: agents.Ancestors,
	Scan:      agents.ScanProcesses,
	Reap:      agents.Reap,
}

// agents is c.Agents over MachineAgents, func by func.
func (c *Context) agents() Agents {
	a := MachineAgents
	if c == nil || c.Agents == nil {
		return a
	}
	o := c.Agents
	if o.List != nil {
		a.List = o.List
	}
	if o.Required != nil {
		a.Required = o.Required
	}
	if o.Claude != nil {
		a.Claude = o.Claude
	}
	if o.Codex != nil {
		a.Codex = o.Codex
	}
	if o.Ancestors != nil {
		a.Ancestors = o.Ancestors
	}
	if o.Scan != nil {
		a.Scan = o.Scan
	}
	if o.Reap != nil {
		a.Reap = o.Reap
	}
	return a
}

// ListAgents is every agent session but the one wt runs under.
func (c *Context) ListAgents() ([]agents.Agent, error) { return c.agents().List() }

// ListAgentsRequired is ListAgents for a caller that has to know who is in a
// worktree: no claude to ask is not nobody there, and is an error.
func (c *Context) ListAgentsRequired() ([]agents.Agent, error) { return c.agents().Required() }

// ListClaudeSessions is the Claude listing a person reads.
func (c *Context) ListClaudeSessions(deadline time.Duration) ([]agents.Agent, bool, error) {
	return c.agents().Claude(deadline)
}

// ListCodexSessions is the Codex sessions but the one wt runs under.
func (c *Context) ListCodexSessions() ([]agents.Agent, error) { return c.agents().Codex() }

// Ancestors is the pid of every process above this one.
func (c *Context) Ancestors() (map[int]bool, error) { return c.agents().Ancestors() }

// ScanProcesses reads the process table, sparing the processes of listed.
func (c *Context) ScanProcesses(listed []agents.Agent) (*agents.ProcessScan, error) {
	return c.agents().Scan(listed)
}

// Reap ends procs, with grace between SIGTERM and SIGKILL.
func (c *Context) Reap(procs []agents.Process, grace time.Duration) []agents.Reaped {
	return c.agents().Reap(procs, grace)
}

// SessionsIn is the one answer remove, migrate and sweep give to "is an
// agent session in this worktree": every session whose working directory is
// inside path, idle or busy, and an error when they could not be read, which
// is not the same answer as none. listed and listErr are a listing the
// caller already has; nil listed and no error list them here. No claude on
// the PATH is no sessions: nobody here uses Claude.
func (c *Context) SessionsIn(listed []agents.Agent, listErr error, path string) (agents.Sessions, error) {
	if listErr != nil {
		return nil, listErr
	}
	if listed == nil {
		var err error
		if listed, err = c.ListAgents(); err != nil {
			return nil, err
		}
	}
	return agents.SessionsAt(listed, path), nil
}

// ListAgain lists the sessions a second time, for the check after a question
// or at the lock: through relist when a caller supplied one, the caller's own
// list when it supplied that, and ctx's listing otherwise; ctx may be nil.
func ListAgain(ctx *Context, given []agents.Agent, relist func() ([]agents.Agent, error)) ([]agents.Agent, error) {
	switch {
	case relist != nil:
		return relist()
	case given != nil:
		return given, nil
	}
	return ctx.ListAgents()
}
