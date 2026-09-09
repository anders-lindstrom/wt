package wtsync

import "fmt"

// Strategy resolves one conflict shape. Resolve returns the resolved file, a
// *Refusal when the conflict is not the shape the strategy owns completely,
// or another error when something went wrong. It never guesses.
type Strategy interface {
	Name() string
	Resolve(c Conflict) ([]byte, error)
}

// FromRule builds the strategy a declaration names. root is the repository
// root, needed only by script strategies.
func FromRule(r Rule, _ string) (Strategy, error) {
	switch r.Strategy {
	case "take-trunk":
		return TakeTrunk{}, nil
	}
	return nil, fmt.Errorf("unknown strategy %q", r.Strategy)
}
