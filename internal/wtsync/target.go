package wtsync

import "github.com/anders-lindstrom/wt/internal/agents"
import "github.com/anders-lindstrom/wt/internal/repo"

// Target is what a rebase is assessed against and replayed onto.
type Target struct {
	Onto, Upstream, Trunk, Name string
}

// SimulateRebaseTo is SimulateRebase against t.
func SimulateRebaseTo(mainRoot string, t Target, branch string, cfg *Config) (Replay, error) {
	return SimulateRebase(mainRoot, t.Onto, branch, cfg)
}

// AssessTo is AssessOwn against t.
func AssessTo(mainRoot string, t Target, cfg *Config, wt repo.Worktree, listed []agents.Agent, own OwnRemote) Assessment {
	return AssessOwn(mainRoot, t.Onto, cfg, wt, listed, own)
}
