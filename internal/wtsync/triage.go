package wtsync

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Class is what wt sync would do with a worktree.
type Class int

// The classes, from spec §1. Unknown is the zero value, so an assessment
// that returns early on an error before any class is decided reads
// "unknown" rather than misreporting the worktree as Detached.
const (
	Unknown   Class = iota // Err is set and no class was decided
	Detached               // no branch: skipped, reported
	Current                // behind 0: nothing to do
	Stale                  // ahead 0: nothing of its own; a lifecycle question
	Clean                  // every commit replays without conflict
	Recipe                 // the first stop is entirely resolved by strategies
	Contested              // something at the first stop is a person's
	Divergent              // the openapi strategy refuses at the endpoint: a workstream, not a rebase
)

func (c Class) String() string {
	return [...]string{"unknown", "detached", "current", "stale", "clean", "recipe", "contested", "divergent"}[c]
}

// FileOutcome is one conflicted file at the first stop.
type FileOutcome struct {
	Path     string
	Strategy string // "" when unclaimed
	Resolved bool
	Note     string // "unclaimed", or the refusal's reason
}

// Assessment is everything wt sync knows about one worktree, computed with
// nothing touched.
type Assessment struct {
	Path      string
	Branch    string
	Class     Class
	Behind    int
	Ahead     int
	Dirty     bool // tracked changes; untracked files never block a rebase
	Agent     *Agent
	Replay    Replay
	Files     []FileOutcome
	Divergent []string // why the class is divergent
	Notes     []string // observations that do not affect the class
	NoConfig  bool
	Err       error
}

// Assess classifies one worktree against onto, the ref it would be rebased
// onto. A nil cfg means the repository declared nothing: it is still
// classified, nothing is claimed, and NoConfig says so.
func Assess(mainRoot, onto string, cfg *Config, wt repo.Worktree, agents []Agent) Assessment {
	// A worktree's path in the fleet can carry a symlink (e.g. a macOS /tmp
	// in tests, or a mounted home directory) that an agent's reported cwd
	// has already resolved; matching them literally would miss the agent.
	agentPath := wt.Path
	if resolved, err := filepath.EvalSymlinks(wt.Path); err == nil {
		agentPath = resolved
	}
	a := Assessment{Path: wt.Path, Branch: wt.Branch, NoConfig: cfg == nil, Agent: AgentAt(agents, agentPath)}
	if wt.Detached || wt.Branch == "" {
		a.Class = Detached
		return a
	}
	// --no-optional-locks: a plain status may refresh and rewrite the index,
	// and this command must not touch a worktree.
	out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		a.Err = fmt.Errorf("status: %w", err)
		return a
	}
	a.Dirty = out != ""
	behind, ahead, err := BehindAhead(mainRoot, onto, wt.Branch)
	if err != nil {
		a.Err = err
		return a
	}
	a.Behind, a.Ahead = behind, ahead
	switch {
	case behind == 0:
		a.Class = Current
		return a
	case ahead == 0:
		a.Class = Stale
		return a
	}

	a.Replay, err = SimulateRebase(mainRoot, onto, wt.Branch)
	if err != nil {
		a.Err = err
		return a
	}
	if a.Replay.Stop == nil {
		a.Class = Clean
	} else {
		a.Class = Recipe
		for _, c := range a.Replay.Stop.Conflicts {
			f, err := tryStrategy(mainRoot, onto, cfg, c)
			a.Err = errors.Join(a.Err, err)
			if !f.Resolved {
				a.Class = Contested
			}
			a.Files = append(a.Files, f)
		}
		if a.Replay.Stop.Messages != "" && len(a.Files) == 0 {
			a.Class = Contested
			a.Files = append(a.Files, FileOutcome{Path: messagesPath, Note: a.Replay.Stop.Messages})
		}
	}

	reasons, notes, err := divergence(mainRoot, onto, cfg, wt.Branch)
	a.Err = errors.Join(a.Err, err)
	a.Divergent = reasons
	a.Notes = notes
	if len(a.Divergent) > 0 {
		a.Class = Divergent
	}
	return a
}

// tryStrategy asks the declared strategy whether it resolves the conflict.
// A conflict without three regular blobs is refused before any strategy sees
// it. The error return is for things going wrong — a strategy that cannot be
// built, a script that cannot run — as opposed to a refusal, which is a
// normal outcome carried in the Note.
func tryStrategy(mainRoot, onto string, cfg *Config, c Conflict) (FileOutcome, error) {
	f := FileOutcome{Path: c.Path, Note: "unclaimed"}
	if c.Incomplete != "" {
		f.Note = c.Incomplete
		return f, nil
	}
	if cfg == nil {
		return f, nil
	}
	rule, ok := cfg.RuleFor(c.Path)
	if !ok {
		return f, nil
	}
	f.Strategy = rule.Strategy
	s, err := FromRule(rule, mainRoot, onto)
	if err != nil {
		f.Note = err.Error()
		return f, fmt.Errorf("%s: %w", c.Path, err)
	}
	if _, err := s.Resolve(c); err != nil {
		var r *Refusal
		if errors.As(err, &r) {
			f.Note = r.Reason
			return f, nil
		}
		f.Note = err.Error()
		return f, fmt.Errorf("%s: %w", c.Path, err)
	}
	f.Resolved, f.Note = true, ""
	return f, nil
}

// divergence returns the reasons a branch is a workstream rather than a
// rebase (spec §1) — the openapi strategy refusing at the endpoint, generated
// output that no longer merges — and separately any notes worth a person's
// attention that do not themselves change the class. Both sides having
// changed a dependency_graph path beyond a line a strategy owns is common on
// a long-lived branch and does not by itself discriminate a workstream from
// an ordinary rebase, so it is a note, never a reason. An owned-line refusal
// on ordinary configuration is a normal conflict, not divergence either.
func divergence(mainRoot, onto string, cfg *Config, branch string) (reasons, notes []string, err error) {
	if cfg == nil {
		return nil, nil, nil
	}
	conflicts, err := Endpoint(mainRoot, onto, branch)
	if err != nil {
		return nil, nil, fmt.Errorf("endpoint: %w", err)
	}
	for _, c := range conflicts {
		f, ferr := tryStrategy(mainRoot, onto, cfg, c)
		err = errors.Join(err, ferr)
		if f.Strategy == "openapi" && !f.Resolved {
			reasons = append(reasons, fmt.Sprintf("openapi refuses %s at the endpoint: %s", c.Path, f.Note))
		}
	}
	if len(cfg.DependencyGraph) == 0 {
		return reasons, notes, err
	}
	base, berr := gitEnv(mainRoot, nil, nil, "merge-base", onto, branch)
	if berr != nil {
		return reasons, notes, errors.Join(err, fmt.Errorf("merge-base: %w", berr))
	}
	branchTouched, terr := dependencyChanges(mainRoot, cfg, base, branch)
	err = errors.Join(err, terr)
	trunkTouched, terr := dependencyChanges(mainRoot, cfg, base, onto)
	err = errors.Join(err, terr)
	if len(branchTouched) > 0 && len(trunkTouched) > 0 {
		notes = append(notes, fmt.Sprintf("both sides changed the dependency graph: branch %s; trunk %s",
			strings.Join(branchTouched, ", "), strings.Join(trunkTouched, ", ")))
	}
	return reasons, notes, err
}

// dependencyChanges lists the dependency_graph paths changed between base
// and rev beyond lines an owned-line rule declares. Paths are read
// NUL-separated so quoted names are not missed.
func dependencyChanges(mainRoot string, cfg *Config, base, rev string) ([]string, error) {
	changed, err := gitEnv(mainRoot, nil, nil, "diff", "--name-only", "-z", base, rev)
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w", base, rev, err)
	}
	var touched []string
	for _, path := range strings.Split(changed, "\x00") {
		if path == "" || !matchesAny(cfg.DependencyGraph, path) {
			continue
		}
		only, err := onlyOwnedLines(mainRoot, cfg, base, rev, path)
		if err != nil {
			return nil, err
		}
		if !only {
			touched = append(touched, path)
		}
	}
	return touched, nil
}

// onlyOwnedLines reports whether every line changed in path between base
// and rev is one an owned-line rule declares for it: a version bump in a
// manifest is routine, a new dependency is not. A change with no textual
// lines at all (binary, mode only) is not "only owned lines".
func onlyOwnedLines(mainRoot string, cfg *Config, base, rev, path string) (bool, error) {
	rule, ok := cfg.RuleFor(path)
	if !ok || rule.Strategy != "owned-line" {
		return false, nil
	}
	re, _ := regexp.Compile(rule.Line) // Parse already rejected a bad regex
	diff, err := gitEnv(mainRoot, nil, nil, "diff", "-U0", base, rev, "--", path)
	if err != nil {
		return false, fmt.Errorf("diff %s: %w", path, err)
	}
	textual := 0
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			textual++
			if !re.MatchString(l[1:]) {
				return false, nil
			}
		}
	}
	return textual > 0, nil
}
