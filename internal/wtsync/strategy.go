package wtsync

import (
	"fmt"
	"regexp"
)

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
	case "owned-line":
		re, err := regexp.Compile(r.Line)
		if err != nil {
			return nil, fmt.Errorf("owned-line: bad line regex %q: %w", r.Line, err)
		}
		rule, err := RuleNamed(r.Rule)
		if err != nil {
			return nil, err
		}
		return OwnedLine{Line: re, Rule: rule}, nil
	case "take-trunk":
		return TakeTrunk{}, nil
	case "list-union":
		re, err := regexp.Compile(r.Line)
		if err != nil {
			return nil, fmt.Errorf("list-union: bad line regex %q: %w", r.Line, err)
		}
		return ListUnion{Line: re, Delimiter: r.Delimiter}, nil
	case "openapi":
		if _, err := RuleNamed(r.Rule); err != nil {
			return nil, err
		}
		return OpenAPI{Rule: r.Rule}, nil
	}
	return nil, fmt.Errorf("unknown strategy %q", r.Strategy)
}
