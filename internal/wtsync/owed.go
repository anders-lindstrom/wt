package wtsync

// Owed is what the last run of a branch left owed.
type Owed struct {
	Branch string     `json:"branch"`
	Epoch  int64      `json:"epoch"`
	Steps  []OwedStep `json:"steps"`
}

// OwedStep is one deferred step owed.
type OwedStep struct {
	Step  string `json:"step"`
	Layer string `json:"layer,omitempty"`
	Why   string `json:"why"`
}

func (o *Owed) StepSet() map[string]bool { return nil }

func WriteOwed(mainRoot string, o *Owed) error { return nil }

func ReadOwed(mainRoot, branch string) (*Owed, error) { return nil, nil }
