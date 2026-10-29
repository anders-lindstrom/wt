package config

// BranchSuffixFor is what this repository's branches carry before the slash
// for the person u, and where that was decided. The repository outranks the
// person only where it said something, because a committed file that never
// mentions the suffix is not a decision, and whoever clones it may name their
// own branches. Absent both, the branch follows the folders.
func (c *Config) BranchSuffixFor(u *User) (suffix, origin string) {
	switch {
	case c.BranchSuffixSet:
		return c.BranchSuffix, "repo file"
	case u.IsSet(UserKeyBranchSuffix):
		return u.BranchSuffix, "user file"
	default:
		return c.BranchSuffix, "repo default"
	}
}

// TypeNamesFor is what this repository's branches call each type for the
// person u, and where that was decided. It follows the same rule as
// BranchSuffixFor: a repository that named its types has named them for
// everybody who clones it, and one that did not leaves each person their
// own. The person's pairs are read against this repository's types, so the
// ones that are for somewhere else drop out quietly rather than failing a
// command.
func (c *Config) TypeNamesFor(u *User) (map[string]string, string) {
	if c.TypeNamesSet {
		return c.TypeNames, "repo file"
	}
	if u.IsSet(UserKeyTypeNames) {
		names, _ := TypeNamesFrom(u.TypeNames, c.Types)
		return names, "user file"
	}
	return nil, "repo default"
}
