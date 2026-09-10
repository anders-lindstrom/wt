package wtsync

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ScopeCount is one conventional-commit scope and how often it appears in
// what landed on trunk.
type ScopeCount struct {
	Scope string
	Count int
}

// Landing is what landed on trunk since the branch left it: the first-parent
// commits, and the scopes underneath them.
type Landing struct {
	Commits int
	Scopes  []ScopeCount
}

// maxScopes is how many scopes the plan file's header names. The point of
// the line is orientation, not an inventory.
const maxScopes = 5

// conventional matches a conventional-commit subject and captures its scope.
var conventional = regexp.MustCompile(`^[a-z]+(?:\(([^)]+)\))?!?:`)

// LandingList reads the first-parent log of base..trunk and counts the
// conventional-commit scopes underneath it. A direct commit carries its
// scope in its own subject; a merge commit's subject is "Merge pull request
// #N from …", which has none, so its scopes come from the ranges it merged —
// <sha>^1..<sha>^N for every parent after the first, so an octopus merge is
// not read as if it had two sides (spec §5). Everything is local and no PR
// body is fetched; the one cost is a git log per merge commit, which is why
// this runs when a handover is written and never at triage.
func LandingList(mainRoot, base, trunk string) (Landing, error) {
	out, err := gitEnv(mainRoot, nil, nil, "log", "--first-parent", "--format=%H %P%x00%s", base+".."+trunk, "--")
	if err != nil {
		return Landing{}, fmt.Errorf("landing list: %w", err)
	}
	counts := map[string]int{}
	var l Landing
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		l.Commits++
		shas, subject, _ := strings.Cut(line, "\x00")
		fields := strings.Fields(shas)
		if len(fields) < 3 { // <sha> <parent>, or a root commit: not a merge
			countScope(counts, subject)
			continue
		}
		sha := fields[0]
		for n := 2; n <= len(fields)-1; n++ {
			rng := fmt.Sprintf("%s^1..%s^%d", sha, sha, n)
			merged, err := gitEnv(mainRoot, nil, nil, "log", "--format=%s", rng, "--")
			if err != nil {
				return Landing{}, fmt.Errorf("landing list %s: %w", short(sha), err)
			}
			for _, s := range strings.Split(merged, "\n") {
				countScope(counts, s)
			}
		}
	}
	for scope, n := range counts {
		l.Scopes = append(l.Scopes, ScopeCount{Scope: scope, Count: n})
	}
	sort.Slice(l.Scopes, func(i, j int) bool {
		if l.Scopes[i].Count != l.Scopes[j].Count {
			return l.Scopes[i].Count > l.Scopes[j].Count
		}
		return l.Scopes[i].Scope < l.Scopes[j].Scope
	})
	if len(l.Scopes) > maxScopes {
		l.Scopes = l.Scopes[:maxScopes]
	}
	return l, nil
}

func countScope(counts map[string]int, subject string) {
	m := conventional.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil || m[1] == "" {
		return
	}
	counts[m[1]]++
}

// ScopeLine renders the header's scopes: "pins ×6, statepush ×4, api ×2".
func (l Landing) ScopeLine() string {
	var parts []string
	for _, s := range l.Scopes {
		parts = append(parts, fmt.Sprintf("%s ×%d", s.Scope, s.Count))
	}
	return strings.Join(parts, ", ")
}
