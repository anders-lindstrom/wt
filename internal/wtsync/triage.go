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
	Recipe                 // every stop the replay reached is resolved by strategies
	Contested              // something at the first stop nothing resolves is a person's
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
	// Keys is the refusal's Keys, when the strategy refused: non-empty only
	// for a genuine key-by-key collision, never for an ordinary refusal.
	Keys []string
	// Groups is Keys by section of the document.
	Groups []KeyGroup
}

// KeyGroup is one section of a generated document and the keys in it that
// both sides changed differently.
type KeyGroup struct {
	Section string // "paths", "schemas" or "tags"
	Keys    []string
}

// KeyCounts counts keys by section: "13 paths, 15 schemas".
func KeyCounts(groups []KeyGroup) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		noun := strings.TrimSuffix(g.Section, "s")
		if len(g.Keys) != 1 {
			noun = g.Section
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(g.Keys), noun))
	}
	return strings.Join(parts, ", ")
}

// Collision is a generated document the openapi strategy refuses at the
// endpoint because both sides changed the same keys: why a branch is divergent.
type Collision struct {
	Path   string
	Groups []KeyGroup
}

func (c Collision) String() string {
	return fmt.Sprintf("openapi refuses %s at the endpoint: both sides changed %s", c.Path, KeyCounts(c.Groups))
}

// GraphOverlap is the dependency_graph paths the branch and trunk both changed
// since they parted, beyond lines an owned-line rule declares.
type GraphOverlap struct {
	Branch []string
	Trunk  []string
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
	Divergent []Collision // why the class is divergent
	// Graph is set when both sides changed the dependency graph. It is worth
	// a person's attention and does not affect the class.
	Graph    *GraphOverlap
	Notes    []string // other observations that do not affect the class
	NoConfig bool
	// Unverified means the replay could not be carried to the end: a script
	// claims a path, and a script can only be checked before a run. The
	// class is what the replay earned up to that point.
	Unverified bool
	// Paused is a worktree a run left mid-rebase with a handover in it.
	Paused bool
	Err    error
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
	gitDir, err := GitDir(wt.Path)
	if err != nil {
		a.Err = fmt.Errorf("git dir: %w", err)
		return a
	}
	if a.Paused, err = HasPlan(gitDir); err != nil {
		a.Err = err
		return a
	}
	// --no-optional-locks: a plain status may refresh and rewrite the index,
	// and this command must not touch a worktree.
	out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		a.Err = fmt.Errorf("status: %w", err)
		return a
	}
	// A handover's staged resolutions always make status non-empty; they are
	// what a person is finishing, not dirt. Every reader of Dirty checks
	// Paused first, so nothing that refuses a handover relies on Dirty.
	a.Dirty = out != "" && !a.Paused
	behind, ahead, err := BehindAhead(mainRoot, onto, wt.Branch)
	if err != nil {
		a.Err = err
		return a
	}
	a.Behind, a.Ahead = behind, ahead
	if a.Paused {
		// A run stopped here and is waiting on a person. The worktree holds
		// a half-finished rebase, so simulating another one would describe
		// a state nobody is in; the plan file has the detail.
		a.Class = Contested
		return a
	}
	switch {
	case behind == 0:
		a.Class = Current
		return a
	case ahead == 0:
		a.Class = Stale
		return a
	}

	a.Replay, err = SimulateRebase(mainRoot, onto, wt.Branch, cfg)
	if err != nil {
		a.Err = err
		return a
	}
	a.Err = errors.Join(a.Err, a.Replay.Err)
	a.Unverified = a.Replay.Truncated
	switch {
	case a.Replay.Stop != nil:
		a.Files = a.Replay.Stop.Files
		a.Class, a.Files = classifyStop(a.Files, a.Replay.Stop.Messages)
	case len(a.Replay.Stops) > 0:
		// Every stop reached was resolved by a strategy.
		a.Class = Recipe
		a.Files = a.Replay.Stops[0].Files
	default:
		a.Class = Clean
	}

	collisions, graph, err := divergence(mainRoot, onto, cfg, wt.Branch)
	a.Err = errors.Join(a.Err, err)
	a.Divergent = collisions
	a.Graph = graph
	if a.Replay.Truncated {
		a.Notes = append(a.Notes, a.Replay.Why)
	}
	if len(a.Divergent) > 0 {
		a.Class = Divergent
	}
	return a
}

// classifyStop decides the class of the stop that decides it, from the files a
// person would see and merge-tree's own messages. A stop with no files at
// all has nothing to show: it gets a synthetic FileOutcome so the report
// still names something, distinguishing "merge-tree said nothing useful"
// from "merge-tree explained itself in messages".
func classifyStop(files []FileOutcome, messages string) (Class, []FileOutcome) {
	if len(files) == 0 {
		note := messages
		if note == "" {
			note = "merge-tree reported a conflict with no details"
		}
		return Contested, append(files, FileOutcome{Path: messagesPath, Note: note})
	}
	for _, f := range files {
		if !f.Resolved {
			return Contested, files
		}
	}
	return Recipe, files
}

// divergence returns the collisions that make a branch a workstream rather
// than a rebase (spec §1) — the openapi strategy refusing at the endpoint,
// generated output that no longer merges — and separately the dependency
// graph both sides changed, which is worth a person's attention and does not
// itself change the class. That overlap is common on a long-lived branch and
// does not by itself discriminate a workstream from an ordinary rebase, so it
// is never a reason. An owned-line refusal on ordinary configuration is a
// normal conflict, not divergence either. Nor is every openapi refusal: only
// a genuine key-by-key collision (Refusal carries Keys) is generated output
// that no longer merges; the strategy's other refusals — a section guard, a
// missing section, a malformed document, a non-semver version — are ordinary
// conflicts.
func divergence(mainRoot, onto string, cfg *Config, branch string) (collisions []Collision, graph *GraphOverlap, err error) {
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
		if f.Strategy == "openapi" && !f.Resolved && len(f.Keys) > 0 {
			collisions = append(collisions, Collision{Path: c.Path, Groups: f.Groups})
		}
	}
	if len(cfg.DependencyGraph) == 0 {
		return collisions, nil, err
	}
	base, berr := gitEnv(mainRoot, nil, nil, "merge-base", onto, branch, "--")
	if berr != nil {
		return collisions, nil, errors.Join(err, fmt.Errorf("merge-base: %w", berr))
	}
	branchTouched, terr := dependencyChanges(mainRoot, cfg, base, branch)
	err = errors.Join(err, terr)
	trunkTouched, terr := dependencyChanges(mainRoot, cfg, base, onto)
	err = errors.Join(err, terr)
	if len(branchTouched) > 0 && len(trunkTouched) > 0 {
		graph = &GraphOverlap{Branch: branchTouched, Trunk: trunkTouched}
	}
	return collisions, graph, err
}

// dependencyChanges lists the dependency_graph paths changed between base
// and rev beyond lines an owned-line rule declares. Paths are read
// NUL-separated so quoted names are not missed.
func dependencyChanges(mainRoot string, cfg *Config, base, rev string) ([]string, error) {
	changed, err := gitEnv(mainRoot, nil, nil, "diff", "--name-only", "-z", base, rev, "--")
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
