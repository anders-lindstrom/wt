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

// Resolve merges c and collapses each conflict block: by collapseByPlace
// when the block's owned lines have places of their own and do not pair one
// to one, otherwise by collapseOwned.
func (s OwnedLine) Resolve(c Conflict) ([]byte, error) {
	segs, err := Merge3(c)
	if err != nil {
		return nil, err
	}
	before := sidesBefore(segs)
	i := 0
	return Render(segs, func(b Block) ([]string, error) {
		ctx := before[i]
		i++
		if out, ok, err := collapseByPlace(b, ctx, s.Line, s.Rule, c.Path); ok {
			return out, err
		}
		return collapseOwned(b, s.Line, s.Rule, c.Path)
	})
}

// sidesBefore is, for each block of segs in order, the lines of base, branch
// and trunk that come before it: the merged text and each side's own lines
// of the blocks above. It is what an owned line in the block nests under.
func sidesBefore(segs []Segment) [][3][]string {
	var out [][3][]string
	var acc [3][]string
	for _, seg := range segs {
		if seg.Block == nil {
			for i := range acc {
				acc[i] = append(acc[i], seg.Lines...)
			}
			continue
		}
		out = append(out, [3][]string{append([]string(nil), acc[0]...), append([]string(nil), acc[1]...), append([]string(nil), acc[2]...)})
		acc[0] = append(acc[0], seg.Block.Base...)
		acc[1] = append(acc[1], seg.Block.Branch...)
		acc[2] = append(acc[2], seg.Block.Trunk...)
	}
	return out
}

// collapseByPlace resolves a block whose owned lines pair by their place in
// the file rather than one to one: a side holds more than one, or none, or
// the two it holds sit in different places, as when a pick added a version
// line right beside one trunk bumped. ok is false when the block is not
// that, or some side's places are not unique, and collapseOwned decides it
// as before. before is base's, branch's and trunk's lines above the block.
//
// The kept side is chosen as collapseOwned chooses it. Each owned line in
// it is decided by place: on all three sides, the side that changed it
// wins, and the rule decides when both did; on the kept side only, it was
// added there and stays. A line one side removed, both sides added, or the
// other side added is refused.
func collapseByPlace(b Block, before [3][]string, line *regexp.Regexp, rule ValueRule, path string) ([]string, bool, error) {
	base, baseOK := placed(before[0], b.Base, line)
	branch, branchOK := placed(before[1], b.Branch, line)
	trunk, trunkOK := placed(before[2], b.Trunk, line)
	if !baseOK || !branchOK || !trunkOK {
		return nil, false, nil
	}
	if len(branch) == 1 && len(trunk) == 1 {
		for k := range branch {
			_, same := trunk[k]
			_, inBase := base[k]
			// shift-bump shifts from the base, so it falls through only when
			// the base's line sits in the same place; the other rules ignore
			// the base.
			if _, shift := rule.(shiftBump); same && (!shift || inBase) {
				return nil, false, nil
			}
		}
	}
	_, branchRest, _ := splitOwned(b.Branch, line)
	_, trunkRest, _ := splitOwned(b.Trunk, line)
	_, baseRest, _ := splitOwned(b.Base, line)
	keep, keepBefore, kept, other := b.Trunk, before[2], trunk, branch
	switch {
	case equalLines(branchRest, baseRest):
	case equalLines(trunkRest, baseRest):
		keep, keepBefore, kept, other = b.Branch, before[1], branch, trunk
	default:
		return nil, true, Refuse(path, "the two sides differ by more than the owned line")
	}
	for k := range other {
		if _, ok := kept[k]; !ok {
			return nil, true, Refuse(path, "the two sides differ by more than the owned line")
		}
	}
	all := append(append([]string(nil), keepBefore...), keep...)
	out := make([]string, 0, len(keep))
	for j, l := range keep {
		if !line.MatchString(l) {
			out = append(out, l)
			continue
		}
		k := ownedPlace(all, len(keepBefore)+j, line)
		bl, inBase := base[k]
		br, inBranch := branch[k]
		tr, inTrunk := trunk[k]
		switch {
		case inBase && inBranch && inTrunk:
			resolved, err := decideOwned(bl, br, tr, rule)
			if err != nil {
				return nil, true, Refuse(path, "%v", err)
			}
			out = append(out, resolved)
		case inBase:
			return nil, true, Refuse(path, "one side removed an owned line")
		case inBranch && inTrunk:
			return nil, true, Refuse(path, "both sides added an owned line")
		default:
			out = append(out, l)
		}
	}
	return out, true, nil
}

// decideOwned is the line kept for one place both sides hold: the side that
// changed it, or the rule's answer when both did.
func decideOwned(base, branch, trunk string, rule ValueRule) (string, error) {
	switch {
	case ownedValue(branch) == ownedValue(base):
		return trunk, nil
	case ownedValue(trunk) == ownedValue(base):
		return branch, nil
	}
	return rule.Apply(base, branch, trunk)
}

// placed is the owned lines of one side of a block by place, read with the
// lines above the block. ok is false when two of them share a place.
func placed(before, side []string, line *regexp.Regexp) (map[string]string, bool) {
	all := append(append([]string(nil), before...), side...)
	out := map[string]string{}
	for j, l := range side {
		if !line.MatchString(l) {
			continue
		}
		k := ownedPlace(all, len(before)+j, line)
		if _, dup := out[k]; dup {
			return nil, false
		}
		out[k] = l
	}
	return out, true
}

// ownedPlace is where the owned line lines[i] sits: the lines it nests under
// by indentation, nearest last, each the closest line above with less
// indentation, and the part of the line the owned-line regex matches up to
// its first digit, so a bumped value does not move the line. Blank lines and
// comments are never a parent. A flat file's lines all sit in the same
// place, and pair by position.
func ownedPlace(lines []string, i int, line *regexp.Regexp) string {
	own := line.FindString(strings.TrimRight(lines[i], "\r\n"))
	if d := strings.IndexAny(own, "0123456789"); d >= 0 {
		own = own[:d]
	}
	parts := []string{strings.TrimSpace(own)}
	n := indentOf(lines[i])
	for j := i - 1; j >= 0 && n > 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if d := indentOf(lines[j]); d < n {
			parts = append(parts, t)
			n = d
		}
	}
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return strings.Join(parts, "\n")
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

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
	baseLine, baseRest, ok := splitOwned(b.Base, line)
	if _, shift := rule.(shiftBump); shift && !ok {
		// shift-bump shifts from the base: another version line in a diff3
		// base would be the wrong ancestor. The other rules ignore the base.
		return nil, Refuse(path, "no owned line in the base, or more than one")
	}

	resolved, err := rule.Apply(baseLine, branchLine, trunkLine)
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
