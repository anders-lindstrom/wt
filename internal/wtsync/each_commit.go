package wtsync

// CheckFailure is an each_commit check that failed at a replayed commit:
// the step, the commit it ran at, where that commit is in the rebase, what
// the step's exit said and the tail of its output.
type CheckFailure struct {
	Step         string
	Commit       string
	Subject      string
	Index, Total int
	Err          string
	Output       string
}

// PlanChecks lists the commits of base...branch a rebase would check.
func PlanChecks(wtPath string, cfg *Config, base, branch string) ([]Commit, error) {
	return nil, nil
}
