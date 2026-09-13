package commands

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// nothingDone is how one verb says it changed nothing: the line it prints
// when the answer to its question is no, and the tail of every refusal that
// says the same thing inside a sentence.
type nothingDone struct{ line, tail string }

var (
	rebasedNothing = nothingDone{"nothing rebased", "nothing is rebased"}
	resumedNothing = nothingDone{"nothing resumed", "nothing is resumed"}
	undidNothing   = nothingDone{"nothing undone", "nothing undone"}
)

// verbOptions is what run, resume and undo are all tuned by: the sessions to
// check against, the clock, and who to ask before anything moves.
type verbOptions struct {
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
	// Relist lists the sessions again, at the lock or after a question. Nil
	// lists them the way Agents did.
	Relist func() ([]wtsync.Agent, error)
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
	// Confirm is asked once before anything moves under an idle session, and
	// by a run before it rebases more than one worktree. A nil Confirm never
	// asks: a script or a hook with no terminal is not a person who can
	// answer.
	Confirm func(works []string) (bool, error)
}

// agents is the sessions to check against, asked for when the caller named
// none. A verb that cannot find out who is in a worktree changes nothing.
func (o verbOptions) agents(nothing nothingDone) ([]wtsync.Agent, error) {
	if o.Agents != nil {
		return o.Agents, nil
	}
	agents, err := wtsync.ListOtherAgents()
	if err != nil {
		return nil, fmt.Errorf("cannot list agent sessions (%v); %s", err, nothing.tail)
	}
	return agents, nil
}

// relist is the second listing, for the check after a question or at the lock.
func (o verbOptions) relist(nothing nothingDone) ([]wtsync.Agent, error) {
	agents, err := listAgain(o.Agents, o.Relist)
	if err != nil {
		return nil, fmt.Errorf("cannot list agent sessions again (%v); %s", err, nothing.tail)
	}
	return agents, nil
}

// now is the clock a verb stamps its epoch and its locks with.
func (o verbOptions) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// pushOptions is what a run or a resume does with a branch that finished with
// nothing owed.
type pushOptions struct {
	// Push is what happens at the end. Under PushAsk, ConfirmPush is asked
	// once; a nil ConfirmPush prints the push command instead, for Confirm's
	// reason.
	Push        PushMode
	ConfirmPush func(works []string) (bool, error)
}

// idle is one checkout a verb is about to change, as its messages name it:
// the label its refusals use — a work name, or undo's branch — where it is,
// and the sessions the verb checked it against, which may be none.
type idle struct {
	label    string
	path     string
	sessions wtsync.Sessions
}

// sessionsOf is what a verb was told about the checkout label names.
func sessionsOf(told []idle, label string) wtsync.Sessions {
	for _, t := range told {
		if t.label == label {
			return t.sessions
		}
	}
	return nil
}

// askIdle is the one question a verb asks before it changes files under an
// idle session, and the check that makes the answer worth anything: a
// question can sit unanswered for as long as it likes, so the sessions are
// listed again once it is answered, and one that woke or arrived meanwhile
// refuses rather than being worked around. A nil Confirm asks nobody and goes
// ahead: a script or a hook with no terminal is not a person who can answer.
//
// A false ok is the answer no, which is not an error: the line saying so has
// been printed and the verb returns having touched nothing. fresh is the
// sessions to go on with — the second listing where there was one, agents
// otherwise. told is empty for a run, whose re-check is the lock's instead.
func askIdle(w io.Writer, opts verbOptions, works []string, told []idle, agents []wtsync.Agent, nothing nothingDone) (ok bool, fresh []wtsync.Agent, err error) {
	if opts.Confirm == nil {
		return true, agents, nil
	}
	if ok, err = opts.Confirm(works); err != nil {
		return false, agents, err
	}
	if !ok {
		fmt.Fprintln(w, nothing.line)
		return false, agents, nil
	}
	if len(told) == 0 {
		return true, agents, nil
	}
	if fresh, err = opts.relist(nothing); err != nil {
		return false, agents, err
	}
	for _, t := range told {
		if why := sessionsChanged(t.sessions, wtsync.SessionsAt(fresh, t.path)); why != "" {
			return false, fresh, fmt.Errorf("%s: %s; %s", t.label, why, nothing.tail)
		}
	}
	return true, fresh, nil
}

// idleNotice is said before anything moves under an idle session, whether or
// not anybody is asked: a verb never changes files under one silently.
func idleNotice(work string, s wtsync.Sessions) string {
	return fmt.Sprintf("⚠ %s: %s is in it; the files it has read will change", work, whoLabel(s))
}

// tellIdle prints a §5 line for the idle sessions in a worktree, on a line of
// its own so it can be relayed to them verbatim. Nothing when there are none.
func tellIdle(w io.Writer, s wtsync.Sessions, line string) {
	if len(s) == 0 {
		return
	}
	names := make([]string, len(s))
	for i := range s {
		names[i] = sessionLabel(&s[i])
	}
	fmt.Fprintf(w, "  ⚠ tell %s, idle in it:\n    %s\n", strings.Join(names, ", "), line)
}

// listAgain lists the sessions a second time, for the check after a question
// or at the lock: through relist when a caller supplied one, the caller's own
// list when it supplied that, and claude otherwise.
func listAgain(given []wtsync.Agent, relist func() ([]wtsync.Agent, error)) ([]wtsync.Agent, error) {
	switch {
	case relist != nil:
		return relist()
	case given != nil:
		return given, nil
	}
	return wtsync.ListOtherAgents()
}

// sessionsChanged is why the sessions in a worktree are no longer the ones a
// verb checked: one is busy now, or one arrived that nobody was told about.
// Empty when nothing changed that matters.
func sessionsChanged(told, now wtsync.Sessions) string {
	if len(now.Busy()) > 0 {
		return "an agent session is busy in it now: " + now.Label(sessionLabel)
	}
	if arrived := now.Arrived(told); len(arrived) > 0 {
		return "a session arrived since it was checked: " + arrived.Label(sessionLabel)
	}
	return ""
}

// pathsOnce joins lists of paths, keeping the first of each.
func pathsOnce(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}
