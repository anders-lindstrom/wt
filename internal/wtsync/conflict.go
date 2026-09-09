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
}

// Refusal is a strategy declining a conflict it does not own completely. It
// is a normal outcome, not a failure: the file is left exactly as it was and
// a person looks. Reason names what collided.
type Refusal struct {
	Path   string
	Reason string
}

func (r *Refusal) Error() string { return r.Path + ": " + r.Reason }

// Refuse builds a Refusal.
func Refuse(path, format string, a ...any) error {
	return &Refusal{Path: path, Reason: fmt.Sprintf(format, a...)}
}

// IsRefusal reports whether err is a strategy refusing, as opposed to
// something going wrong.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}
