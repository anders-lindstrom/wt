package wtsync

import (
	"sort"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Parents computes the stack relation across branch-attached worktrees: for
// each branch, the nearest other worktree branch that is its ancestor. A
// branch at the very same commit as another is neither parent nor child of
// it. A branch whose ancestors do not form a chain is ambiguous and gets no
// parent. --update-refs cannot do this for branches checked out in
// worktrees (spec §4), which is every branch here, so the relation is
// explicit.
func Parents(mainRoot string, worktrees []repo.Worktree) (map[string]string, map[string][]string, error) {
	var branches []string
	tips := map[string]string{}
	for _, wt := range worktrees {
		if wt.IsMain || wt.Detached || wt.Branch == "" {
			continue
		}
		tip, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", wt.Branch)
		if err != nil {
			return nil, nil, err
		}
		branches = append(branches, wt.Branch)
		tips[wt.Branch] = tip
	}
	sort.Strings(branches)
	isAncestor := func(a, b string) (bool, error) {
		if tips[a] == tips[b] {
			return false, nil
		}
		_, err := gitEnv(mainRoot, nil, nil, "merge-base", "--is-ancestor", a, b)
		if err == nil {
			return true, nil
		}
		if isExit(err, 1) {
			return false, nil
		}
		return false, err
	}
	parents := map[string]string{}
	ambiguous := map[string][]string{}
	for _, b := range branches {
		var ancestors []string
		for _, a := range branches {
			if a == b {
				continue
			}
			ok, err := isAncestor(a, b)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				ancestors = append(ancestors, a)
			}
		}
		if len(ancestors) == 0 {
			continue
		}
		// The nearest ancestor descends from every other ancestor, or shares
		// its tip with it; the first qualifying name in sorted order wins.
		found := false
		for _, cand := range ancestors {
			nearest := true
			for _, other := range ancestors {
				if other == cand || tips[other] == tips[cand] {
					continue
				}
				ok, err := isAncestor(other, cand)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					nearest = false
					break
				}
			}
			if nearest {
				parents[b] = cand
				found = true
				break
			}
		}
		if !found {
			ambiguous[b] = ancestors
		}
	}
	return parents, ambiguous, nil
}

// Members is the stack a branch belongs to, root first, then descendants in
// name order at each depth.
func Members(parents map[string]string, branch string) []string {
	root := branch
	seen := map[string]bool{root: true}
	for p, ok := parents[root]; ok && !seen[p]; p, ok = parents[root] {
		seen[p] = true
		root = p
	}
	children := map[string][]string{}
	for c, p := range parents {
		children[p] = append(children[p], c)
	}
	var out []string
	var walk func(string)
	walk = func(b string) {
		out = append(out, b)
		kids := children[b]
		sort.Strings(kids)
		for _, k := range kids {
			walk(k)
		}
	}
	walk(root)
	return out
}

// Order sorts branches so every parent precedes its children; branches that
// are not in a stack keep their relative input order.
func Order(parents map[string]string, branches []string) []string {
	index := map[string]int{}
	for i, b := range branches {
		index[b] = i
	}
	depth := func(b string) int {
		d := 0
		seen := map[string]bool{}
		for p, ok := parents[b]; ok && !seen[p]; p, ok = parents[p] {
			seen[p] = true
			d++
		}
		return d
	}
	out := append([]string(nil), branches...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := depth(out[i]), depth(out[j])
		if di != dj {
			return di < dj
		}
		return index[out[i]] < index[out[j]]
	})
	return out
}

// Descendants is everything above a branch in its stack — its children,
// their children, and so on — in the order Members uses, without the branch
// itself. A rebase that failed or was restored invalidates only what sits on
// top of it: its parent and its siblings are untouched and still rebase.
func Descendants(parents map[string]string, branch string) []string {
	children := map[string][]string{}
	for c, p := range parents {
		children[p] = append(children[p], c)
	}
	var out []string
	seen := map[string]bool{branch: true}
	var walk func(string)
	walk = func(b string) {
		kids := children[b]
		sort.Strings(kids)
		for _, k := range kids {
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
			walk(k)
		}
	}
	walk(branch)
	return out
}
