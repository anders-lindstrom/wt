package syncrun

// StepOptions is what a sync verb is told about the steps a declaration
// runs: whether to go on when a step's need does not answer, and the probe
// of each need.
type StepOptions struct {
	// AllowOwed starts a run whose steps need something that did not answer
	// its probe: those steps are not run, and are owed.
	AllowOwed bool
	// Probes replaces the probe of each need; nil probes for real.
	Probes map[string]func() error
}

// CheckFailed is an each_commit check that failed, for --json.
type CheckFailed struct {
	Step    string `json:"step"`
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Error   string `json:"error"`
}

// DeferredOwed did not run because a need of it did not answer; owed.
const DeferredOwed = "owed"
