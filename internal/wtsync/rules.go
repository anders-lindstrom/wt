package wtsync

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// semverRE finds the X.Y.Z inside a line, as a whole token: "2.38.3-SNAPSHOT"
// and "2.38.3.1" are not versions this rule knows, and must refuse rather
// than be lifted to "2.38.6-SNAPSHOT". Group 1 is the version.
var semverRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9.-])(\d+\.\d+\.\d+)(?:[^A-Za-z0-9.-]|$)`)

// exactSemverRE matches a bare X.Y.Z and nothing else, used to validate
// info.version, which carries no surrounding punctuation to anchor against.
var exactSemverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// findSemver returns the version token in a line, or "".
func findSemver(line string) string {
	m := semverRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// ValueRule decides which line the branch keeps when both sides changed the
// one line a strategy owns. It sees whole lines so that indentation and
// punctuation around the value survive: base is the line in the pick's
// parent, "" when that side has none, branch the pick's and trunk the line
// the pick is applied on.
type ValueRule interface {
	Apply(base, branch, trunk string) (string, error)
}

// The value rules a declaration may name.
const (
	RuleMaxPlusPatch = "max-plus-patch"
	RuleShiftBump    = "shift-bump"
	RuleKeepBranch   = "keep-branch"
	RuleKeepTrunk    = "keep-trunk"
)

// RuleNamed returns the rule a declaration names.
func RuleNamed(name string) (ValueRule, error) {
	switch name {
	case RuleMaxPlusPatch:
		return maxPlusPatch{}, nil
	case RuleShiftBump:
		return shiftBump{}, nil
	case RuleKeepBranch:
		return keepBranch{}, nil
	case RuleKeepTrunk:
		return keepTrunk{}, nil
	}
	return nil, fmt.Errorf("unknown rule %q", name)
}

type keepBranch struct{}
type keepTrunk struct{}
type maxPlusPatch struct{}
type shiftBump struct{}

func (keepBranch) Apply(_, branch, _ string) (string, error) { return branch, nil }
func (keepTrunk) Apply(_, _, trunk string) (string, error)   { return trunk, nil }

// Apply rewrites the version inside the branch's line to MaxPlusPatch of the
// two versions, keeping everything else on the line as the branch wrote it.
// The replacement happens at the regex match's own offset, not by a plain
// string replace of the version text: a line like "pin 1.2.3.4 to 1.2.3"
// contains the target version "1.2.3" as a substring of "1.2.3.4" too, and a
// plain replace would corrupt the wrong occurrence.
func (maxPlusPatch) Apply(_, branch, trunk string) (string, error) {
	loc := semverRE.FindStringSubmatchIndex(branch)
	tv := findSemver(trunk)
	if loc == nil || tv == "" {
		return "", fmt.Errorf("max-plus-patch needs an X.Y.Z version on both sides, got %q and %q",
			strings.TrimSpace(branch), strings.TrimSpace(trunk))
	}
	bv := branch[loc[2]:loc[3]]
	return branch[:loc[2]] + MaxPlusPatch(bv, tv) + branch[loc[3]:], nil
}

// MaxPlusPatch is the version a branch keeps after trunk moved: the higher of
// the two, and when that is trunk's, one patch above it, because the branch
// still needs a version of its own above everything trunk has published.
func MaxPlusPatch(branchV, trunkV string) string {
	if compareSemver(branchV, trunkV) > 0 {
		return branchV
	}
	major, minor, patch := parseSemver(trunkV)
	return fmt.Sprintf("%d.%d.%d", major, minor, patch+1)
}

// Apply rewrites the version inside the branch's line to ShiftBump of the
// three versions, keeping everything else on the line as the branch wrote it.
func (shiftBump) Apply(base, branch, trunk string) (string, error) {
	loc := semverRE.FindStringSubmatchIndex(branch)
	bv, tv := findSemver(base), findSemver(trunk)
	if loc == nil || bv == "" || tv == "" {
		return "", fmt.Errorf("shift-bump needs an X.Y.Z version on every side, got %q, %q and %q",
			strings.TrimSpace(base), strings.TrimSpace(branch), strings.TrimSpace(trunk))
	}
	v, err := ShiftBump(bv, branch[loc[2]:loc[3]], tv)
	if err != nil {
		return "", err
	}
	return branch[:loc[2]] + v + branch[loc[3]:], nil
}

// ShiftBump is the version a pick keeps when it is applied on under instead
// of base: the pick's own bump, of the same kind and size, made on under.
// The kind is the highest component where branch differs from base; that
// component is under's raised by the same difference, the ones above it are
// under's and the ones below it the branch's. A pick that did not bump keeps
// under, and one that lowered the version is refused.
func ShiftBump(base, branch, under string) (string, error) {
	if branch == base {
		return under, nil
	}
	if compareSemver(branch, base) < 0 {
		return "", fmt.Errorf("the branch lowered the version from %s to %s", base, branch)
	}
	b, p, u := semverParts(base), semverParts(branch), semverParts(under)
	k := bumpComponent(b, p)
	out := p
	copy(out[:k], u[:k])
	out[k] = u[k] + p[k] - b[k]
	return fmt.Sprintf("%d.%d.%d", out[0], out[1], out[2]), nil
}

// bumpKind names the bump from base to branch for a note: "a minor on
// 2.46.0".
func bumpKind(base, branch string) string {
	kinds := [...]string{"a major", "a minor", "a patch"}
	return kinds[bumpComponent(semverParts(base), semverParts(branch))] + " on " + base
}

// bumpComponent is the index of the highest component where two versions
// differ, 2 when none does.
func bumpComponent(a, b [3]int) int {
	k := 0
	for k < 2 && a[k] == b[k] {
		k++
	}
	return k
}

func semverParts(v string) [3]int {
	major, minor, patch := parseSemver(v)
	return [3]int{major, minor, patch}
}

func parseSemver(v string) (major, minor, patch int) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) == 3 {
		major, _ = strconv.Atoi(parts[0])
		minor, _ = strconv.Atoi(parts[1])
		patch, _ = strconv.Atoi(parts[2])
	}
	return
}

func compareSemver(a, b string) int {
	am, an, ap := parseSemver(a)
	bm, bn, bp := parseSemver(b)
	switch {
	case am != bm:
		return am - bm
	case an != bn:
		return an - bn
	default:
		return ap - bp
	}
}
