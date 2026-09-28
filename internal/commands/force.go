package commands

import (
	"fmt"
	"strings"
)

// ForceSet is what wt remove --force goes past: somebody's claim on the
// checkout, never work that would be lost with it. Bare --force is ForceAll.
type ForceSet uint8

// The categories --force=<list> names. busy-sessions implies idle-sessions.
const (
	ForceIdleSessions ForceSet = 1 << iota
	ForceBusySessions
	ForceSessionsUnknown
	ForceHiddenFiles
	ForceLock

	ForceAll = ForceIdleSessions | ForceBusySessions | ForceSessionsUnknown | ForceHiddenFiles | ForceLock
)

// forceNames is each category's name, in the order they are listed.
var forceNames = []struct {
	set  ForceSet
	name string
}{
	{ForceIdleSessions, "idle-sessions"},
	{ForceBusySessions, "busy-sessions"},
	{ForceSessionsUnknown, "sessions-unknown"},
	{ForceHiddenFiles, "hidden-files"},
	{ForceLock, "lock"},
}

// ParseForce reads --force's values, each a comma-separated list of
// categories or "all". An empty or unknown name is an error.
func ParseForce(values []string) (ForceSet, error) {
	var f ForceSet
	for _, v := range values {
		for _, name := range strings.Split(v, ",") {
			name = strings.TrimSpace(name)
			g, ok := forceNamed(name)
			if !ok {
				return 0, fmt.Errorf("--force takes all or a list of %s, not %q",
					strings.Join(ForceAll.Names(), ", "), name)
			}
			f |= g
		}
	}
	return f.implied(), nil
}

// implied is f with what its categories imply: going past a busy session
// goes past an idle one, so a session that stops working between the plan
// and the delete does not turn a forced removal into a refusal.
func (f ForceSet) implied() ForceSet {
	if f&ForceBusySessions != 0 {
		f |= ForceIdleSessions
	}
	return f
}

// IsForceList reports that s is a list --force takes.
func IsForceList(s string) bool {
	_, err := ParseForce([]string{s})
	return s != "" && err == nil
}

func forceNamed(name string) (ForceSet, bool) {
	if name == "all" {
		return ForceAll, true
	}
	for _, n := range forceNames {
		if n.name == name {
			return n.set, true
		}
	}
	return 0, false
}

// Has reports that every category of g is in f; no categories is never had.
func (f ForceSet) Has(g ForceSet) bool {
	return g != 0 && f&g == g
}

// Names is the categories in f, in their listed order.
func (f ForceSet) Names() []string {
	names := []string{}
	for _, n := range forceNames {
		if f&n.set != 0 {
			names = append(names, n.name)
		}
	}
	return names
}

// Flag is f as --force spells it.
func (f ForceSet) Flag() string {
	if f == ForceAll {
		return "--force"
	}
	return "--force=" + strings.Join(f.Names(), ",")
}
