package wtsync

import (
	"errors"
	"fmt"
)

// Conflict is one file that both sides changed, as the three blobs git holds
// for it. Trunk is the side being rebased onto (stage 2 during a rebase) and
// Branch is the commit being replayed (stage 3); naming them this way avoids
// the merge-time ambiguity about which side is "mine".
type Conflict struct {
	Path   string
	Base   []byte
	Trunk  []byte
	Branch []byte

	// Incomplete is non-empty when a side is missing (a modify/delete
	// conflict) or an entry is not a regular blob (a rename, a mode change,
	// a submodule). Assess refuses such a conflict before any strategy sees
	// it: a file one side deleted or renamed is a person's call.
	Incomplete string
}

// Refusal is a strategy declining a conflict it does not own completely. It
// is a normal outcome, not a failure: the file is left exactly as it was and
// a person looks. Reason names what collided. Keys is set only when the
// refusal is a genuine key-by-key collision (both sides changed the same
// key differently) — the signal triage uses to tell a workstream from an
// ordinary refusal; every other refusal leaves it nil. Groups is the same
// keys by section, for a report to count or list.
type Refusal struct {
	Path   string
	Reason string
	Keys   []string
	Groups []KeyGroup
}

func (r *Refusal) Error() string { return r.Path + ": " + r.Reason }

// Refuse builds a Refusal.
func Refuse(path, format string, a ...any) error {
	return &Refusal{Path: path, Reason: fmt.Sprintf(format, a...)}
}

// refusalOf returns the *Refusal err carries, or nil when err is not a
// strategy refusing.
func refusalOf(err error) *Refusal {
	var r *Refusal
	if errors.As(err, &r) {
		return r
	}
	return nil
}

// IsRefusal reports whether err is a strategy refusing, as opposed to
// something going wrong.
func IsRefusal(err error) bool {
	return refusalOf(err) != nil
}
