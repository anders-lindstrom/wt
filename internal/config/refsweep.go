package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// age is an age as ref_sweep_age and wt refs purge --older-than write it: a
// whole number of hours, days or weeks.
var age = regexp.MustCompile(`^([0-9]+)([hdw])$`)

// ParseAge reads an age such as 14d, 2w or 36h.
func ParseAge(s string) (time.Duration, error) {
	m := age.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not an age: write a whole number and h, d or w, e.g. 14d", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > 100000 {
		return 0, fmt.Errorf("%q is not an age wt can count", s)
	}
	unit := map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, nil
}

// validRefPatterns checks ref_sweep_patterns as the file spells it: globs
// separated by spaces, each with something besides wildcards in it, since a
// pattern that matches every name would leave no ref to contain a backup.
func validRefPatterns(value string) error {
	for _, p := range strings.Fields(value) {
		if strings.Trim(p, "*?") == "" {
			return fmt.Errorf("%q matches every name; a backup pattern needs some fixed text, e.g. backup/*", p)
		}
	}
	return nil
}

// RefPattern is one ref_sweep_patterns glob, compiled: * matches any run of
// characters, slashes included, and ? any one character.
type RefPattern struct {
	Glob string
	re   *regexp.Regexp
}

// CompileRefPatterns compiles the globs, in order.
func CompileRefPatterns(globs []string) []RefPattern {
	out := make([]RefPattern, 0, len(globs))
	for _, g := range globs {
		expr := strings.NewReplacer(`\*`, `.*`, `\?`, `.`).Replace(regexp.QuoteMeta(g))
		out = append(out, RefPattern{Glob: g, re: regexp.MustCompile("^" + expr + "$")})
	}
	return out
}

// MatchRefPattern is the first glob name matches, and false when none does.
func MatchRefPattern(patterns []RefPattern, name string) (string, bool) {
	for _, p := range patterns {
		if p.re.MatchString(name) {
			return p.Glob, true
		}
	}
	return "", false
}
