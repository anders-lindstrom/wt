package wtsync

import (
	"regexp"
	"strings"
)

// OwnedLine resolves a file where the only thing both sides may change is the
// one line matching Line, whose value Rule decides. git leaves markers only
// where the sides genuinely disagree, so everything else in the file comes
// through as git merged it.
type OwnedLine struct {
	Line *regexp.Regexp
	Rule ValueRule
}

// Name identifies this strategy in errors and reports.
func (OwnedLine) Name() string { return "owned-line" }

// Resolve merges c and collapses each conflict block by way of collapseOwned.
func (s OwnedLine) Resolve(c Conflict) ([]byte, error) {
	segs, err := Merge3(c)
	if err != nil {
		return nil, err
	}
	return Render(segs, func(b Block) ([]string, error) {
		return collapseOwned(b, s.Line, s.Rule, c.Path)
	})
}

// collapseOwned resolves one block. Each side must hold exactly one owned
// line. git folds an adjacent edit into the same block, so the block may hold
// other lines too, as long as only one side changed them against the base:
// that side's lines are kept, with the owned line replaced by the rule's
// answer. Both sides changing the other lines is a real disagreement.
func collapseOwned(b Block, line *regexp.Regexp, rule ValueRule, path string) ([]string, error) {
	branchLine, branchRest, ok := splitOwned(b.Branch, line)
	if !ok {
		return nil, Refuse(path, "no owned line on one side, or more than one")
	}
	trunkLine, trunkRest, ok := splitOwned(b.Trunk, line)
	if !ok {
		return nil, Refuse(path, "no owned line on one side, or more than one")
	}
	_, baseRest, _ := splitOwned(b.Base, line)

	resolved, err := rule.Apply(branchLine, trunkLine)
	if err != nil {
		return nil, Refuse(path, "%v", err)
	}
	var keep []string
	switch {
	case equalLines(branchRest, baseRest):
		keep = b.Trunk
	case equalLines(trunkRest, baseRest):
		keep = b.Branch
	default:
		return nil, Refuse(path, "the two sides differ by more than the owned line")
	}
	out := make([]string, 0, len(keep))
	for _, l := range keep {
		if line.MatchString(l) {
			out = append(out, resolved)
		} else {
			out = append(out, l)
		}
	}
	return out, nil
}

// splitOwned separates the one owned line from the rest of a side. ok is
// false when the side has no owned line or more than one.
func splitOwned(side []string, line *regexp.Regexp) (owned string, rest []string, ok bool) {
	n := 0
	for _, l := range side {
		if line.MatchString(l) {
			owned = l
			n++
		} else {
			rest = append(rest, l)
		}
	}
	return owned, rest, n == 1
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}
