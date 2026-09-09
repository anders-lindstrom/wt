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
// punctuation around the value survive.
type ValueRule interface {
	Apply(branch, trunk string) (string, error)
}

// RuleNamed returns the rule a declaration names.
func RuleNamed(name string) (ValueRule, error) {
	switch name {
	case "max-plus-patch":
		return maxPlusPatch{}, nil
	case "keep-branch":
		return keepBranch{}, nil
	case "keep-trunk":
		return keepTrunk{}, nil
	}
	return nil, fmt.Errorf("unknown rule %q", name)
}

type keepBranch struct{}
type keepTrunk struct{}
type maxPlusPatch struct{}

func (keepBranch) Apply(branch, _ string) (string, error) { return branch, nil }
func (keepTrunk) Apply(_, trunk string) (string, error)   { return trunk, nil }

// Apply rewrites the version inside the branch's line to MaxPlusPatch of the
// two versions, keeping everything else on the line as the branch wrote it.
// The replacement happens at the regex match's own offset, not by a plain
// string replace of the version text: a line like "pin 1.2.3.4 to 1.2.3"
// contains the target version "1.2.3" as a substring of "1.2.3.4" too, and a
// plain replace would corrupt the wrong occurrence.
func (maxPlusPatch) Apply(branch, trunk string) (string, error) {
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
