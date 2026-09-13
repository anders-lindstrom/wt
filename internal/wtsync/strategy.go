package wtsync

import (
	"errors"
	"fmt"
	"path"
	"regexp"
)

// The strategies a declaration may name.
const (
	StrategyOwnedLine = "owned-line"
	StrategyOpenAPI   = "openapi"
	StrategyListUnion = "list-union"
	StrategyTakeTrunk = "take-trunk"
	StrategyScript    = "script"
)

// Strategy resolves one conflict shape. Resolve returns the resolved file, a
// *Refusal when the conflict is not the shape the strategy owns completely,
// or another error when something went wrong. It never guesses.
type Strategy interface {
	Name() string
	Resolve(c Conflict) ([]byte, error)
}

// inWorktree is a strategy that resolves against a real index in a worktree
// rather than in the object store, writing and staging the file itself: a
// script's --resolve. A run passes the worktree; triage never does, and a
// strategy that does not implement this is asked for bytes either way.
type inWorktree interface {
	ResolveInWorktree(wtPath, path string) error
}

// kinds is every strategy a declaration may name: what the declaration must
// say to name it, and how to build it once that has been checked. This is the
// one place the set is written down, so a declaration cannot be validated
// against one list and built from another.
var kinds = map[string]struct {
	// prepare fills in the defaults, checks what the strategy needs and
	// compiles what it will use. It runs once, at Parse.
	prepare func(r *Rule) error
	// build makes the strategy from a rule prepare has already checked.
	build func(r Rule, root, trunk string) (Strategy, error)
}{
	StrategyOwnedLine: {
		prepare: func(r *Rule) error {
			if r.Line == "" || r.Rule == "" {
				return errors.New("owned-line needs line and rule")
			}
			return r.compileLine()
		},
		build: func(r Rule, _, _ string) (Strategy, error) {
			re, err := r.lineRE(StrategyOwnedLine)
			if err != nil {
				return nil, err
			}
			rule, err := r.valueRule()
			if err != nil {
				return nil, err
			}
			return OwnedLine{Line: re, Rule: rule}, nil
		},
	},
	StrategyOpenAPI: {
		prepare: func(r *Rule) error {
			if r.Rule == "" {
				r.Rule = RuleMaxPlusPatch
			}
			return nil
		},
		build: func(r Rule, _, _ string) (Strategy, error) {
			return OpenAPI{Rule: r.Rule}, nil
		},
	},
	StrategyListUnion: {
		prepare: func(r *Rule) error {
			if r.Line == "" {
				return errors.New("list-union needs line")
			}
			if err := r.compileLine(); err != nil {
				return err
			}
			if r.Delimiter == "" {
				r.Delimiter = ","
			}
			return nil
		},
		build: func(r Rule, _, _ string) (Strategy, error) {
			re, err := r.lineRE(StrategyListUnion)
			if err != nil {
				return nil, err
			}
			return ListUnion{Line: re, Delimiter: r.Delimiter}, nil
		},
	},
	StrategyTakeTrunk: {
		prepare: func(*Rule) error { return nil },
		build:   func(Rule, string, string) (Strategy, error) { return TakeTrunk{}, nil },
	},
	StrategyScript: {
		prepare: func(r *Rule) error {
			if r.Run == "" {
				return errors.New("script needs run")
			}
			if path.IsAbs(r.Run) || escapesRoot(r.Run) {
				return fmt.Errorf("script run %q must be relative to the root, with no parent-directory segments", r.Run)
			}
			return nil
		},
		build: func(r Rule, root, trunk string) (Strategy, error) {
			return Script{Root: root, Trunk: trunk, Run: r.Run}, nil
		},
	},
}

// FromRule builds the strategy a declaration names. root and trunk are
// needed only by script strategies: root is the repository root where git
// runs, trunk is the ref the script is read from.
func FromRule(r Rule, root, trunk string) (Strategy, error) {
	k, ok := kinds[r.Strategy]
	if !ok {
		return nil, fmt.Errorf("unknown strategy %q", r.Strategy)
	}
	return k.build(r, root, trunk)
}

// compileLine compiles Line onto the rule, at Parse, so nothing downstream
// compiles it again.
func (r *Rule) compileLine() error {
	re, err := regexp.Compile(r.Line)
	if err != nil {
		return fmt.Errorf("bad line regex %q: %w", r.Line, err)
	}
	r.line = re
	return nil
}

// lineRE is Line as Parse compiled it, or compiled now for a Rule built by
// hand; who names the strategy in the error, the way a declaration does.
func (r Rule) lineRE(who string) (*regexp.Regexp, error) {
	if r.line != nil {
		return r.line, nil
	}
	re, err := regexp.Compile(r.Line)
	if err != nil {
		return nil, fmt.Errorf("%s: bad line regex %q: %w", who, r.Line, err)
	}
	return re, nil
}

// valueRule is the rule Parse resolved, or the one the name means for a Rule
// built by hand.
func (r Rule) valueRule() (ValueRule, error) {
	if r.value != nil {
		return r.value, nil
	}
	return RuleNamed(r.Rule)
}
