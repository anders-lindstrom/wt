package wtsync

import (
	"regexp"
	"strings"
)

// ListUnion resolves a file whose one contested line is a delimited list that
// both sides only ever add to: the union, trunk's order first, then the
// branch's additions. A removal is a decision, not a merge, and is refused.
type ListUnion struct {
	Line      *regexp.Regexp
	Delimiter string
}

// Name identifies this strategy in errors and reports.
func (ListUnion) Name() string { return "list-union" }

// itemRE is what one list item may look like: a quoted or bare token with no
// whitespace inside. Anything else means the line was not parsed the way it
// was written.
var itemRE = regexp.MustCompile(`^('[^'\s]+'|"[^"\s]+"|[^\s'",]+)$`)

// Resolve merges c and collapses each conflict block by way of collapse.
func (s ListUnion) Resolve(c Conflict) ([]byte, error) {
	// Removal is judged on whole files, so an item a side merely moved to
	// another list line still counts as present.
	baseLines := strings.Split(string(c.Base), "\n")
	if !s.hasLine(baseLines) {
		return nil, Refuse(c.Path, "no list line matching %s", s.Line)
	}
	baseItems, err := s.items(c.Path, baseLines)
	if err != nil {
		return nil, err
	}
	branchAll, err := s.items(c.Path, strings.Split(string(c.Branch), "\n"))
	if err != nil {
		return nil, err
	}
	trunkAll, err := s.items(c.Path, strings.Split(string(c.Trunk), "\n"))
	if err != nil {
		return nil, err
	}
	for _, it := range baseItems {
		if !contains(branchAll, it) {
			return nil, Refuse(c.Path, "the branch removes %s; that is a decision, not a merge", it)
		}
		if !contains(trunkAll, it) {
			return nil, Refuse(c.Path, "trunk removes %s; that is a decision, not a merge", it)
		}
	}
	segs, err := Merge3(c)
	if err != nil {
		return nil, err
	}
	return Render(segs, func(b Block) ([]string, error) {
		return s.collapse(c.Path, b)
	})
}

// collapse resolves one block, which must consist of list lines only. git
// folds a list line added right after the changed one into the same block,
// so a side may hold more than one; they are unioned onto one line.
func (s ListUnion) collapse(path string, b Block) ([]string, error) {
	if len(b.Branch) == 0 || len(b.Trunk) == 0 {
		return nil, Refuse(path, "the conflict is not on the list line")
	}
	var prefix string
	for _, l := range append(append([]string{}, b.Branch...), b.Trunk...) {
		m := s.Line.FindString(l)
		if m == "" {
			return nil, Refuse(path, "the conflict is not on the list line")
		}
		prefix = m
	}
	branchItems, err := s.items(path, b.Branch)
	if err != nil {
		return nil, err
	}
	trunkItems, err := s.items(path, b.Trunk)
	if err != nil {
		return nil, err
	}
	out := append([]string{}, trunkItems...)
	for _, it := range branchItems {
		if !contains(out, it) {
			out = append(out, it)
		}
	}
	return []string{prefix + strings.Join(out, s.Delimiter+" ")}, nil
}

// hasLine reports whether any line matches s.Line, regardless of whether that
// line holds any items: an empty list is still a list, just not one with
// anything to remove.
func (s ListUnion) hasLine(lines []string) bool {
	for _, l := range lines {
		if s.Line.FindString(l) != "" {
			return true
		}
	}
	return false
}

// items collects the list items of every matching line, validated.
func (s ListUnion) items(path string, lines []string) ([]string, error) {
	var out []string
	for _, l := range lines {
		m := s.Line.FindString(l)
		if m == "" {
			continue
		}
		for _, raw := range strings.Split(strings.TrimPrefix(l, m), s.Delimiter) {
			it := strings.TrimSpace(raw)
			if it == "" {
				continue
			}
			if !itemRE.MatchString(it) {
				return nil, Refuse(path, "cannot parse %s as a list item", it)
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
