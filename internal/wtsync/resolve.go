package wtsync

import "fmt"

// Resolution is what a strategy answered for one conflict: the outcome as
// triage reports it, and the bytes to write when it resolved. InPlace means
// the strategy already wrote and staged the file itself (a script's
// --resolve, Task 4), so there is nothing to Apply.
type Resolution struct {
	Outcome FileOutcome
	Content []byte
	InPlace bool
}

// resolveConflict asks the declared strategy for its answer. A conflict
// without three regular blobs is refused before any strategy sees it. wtPath
// is "" at triage, where nothing may be written; a run passes the worktree.
// The error return is for things going wrong — a strategy that cannot be
// built, a script that cannot run — as opposed to a refusal, which is a
// normal outcome carried in the Note.
func resolveConflict(mainRoot, onto string, cfg *Config, c Conflict, wtPath string) (Resolution, error) {
	r := Resolution{Outcome: FileOutcome{Path: c.Path, Note: "unclaimed"}}
	if c.Incomplete != "" {
		r.Outcome.Note = c.Incomplete
		return r, nil
	}
	if cfg == nil {
		return r, nil
	}
	rule, ok := cfg.RuleFor(c.Path)
	if !ok {
		return r, nil
	}
	r.Outcome.Strategy = rule.Strategy
	s, err := FromRule(rule, mainRoot, onto)
	if err != nil {
		r.Outcome.Note = err.Error()
		return r, fmt.Errorf("%s: %w", c.Path, err)
	}
	if sc, ok := s.(Script); ok && wtPath != "" {
		if err := sc.ResolveInWorktree(wtPath, c.Path); err != nil {
			if ref := refusalOf(err); ref != nil {
				r.Outcome.Note = ref.Reason
				return r, nil
			}
			r.Outcome.Note = err.Error()
			return r, fmt.Errorf("%s: %w", c.Path, err)
		}
		r.Outcome.Resolved, r.Outcome.Note, r.InPlace = true, "", true
		return r, nil
	}
	content, err := s.Resolve(c)
	if err != nil {
		if ref := refusalOf(err); ref != nil {
			r.Outcome.Note = ref.Reason
			r.Outcome.Keys = ref.Keys
			return r, nil
		}
		r.Outcome.Note = err.Error()
		return r, fmt.Errorf("%s: %w", c.Path, err)
	}
	r.Outcome.Resolved, r.Outcome.Note = true, ""
	r.Content = content
	return r, nil
}

// tryStrategy is the triage view of resolveConflict: the outcome only.
func tryStrategy(mainRoot, onto string, cfg *Config, c Conflict) (FileOutcome, error) {
	r, err := resolveConflict(mainRoot, onto, cfg, c, "")
	return r.Outcome, err
}
