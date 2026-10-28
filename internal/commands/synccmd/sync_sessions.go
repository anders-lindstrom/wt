package synccmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// nothingDone is how one verb says it changed nothing: the line it prints
// when the answer to its question is no, and the tail of every refusal that
// says the same thing inside a sentence.
type nothingDone struct{ Line, tail string }

// What each verb says when it did nothing.
var (
	rebasedNothing = nothingDone{"nothing rebased", "nothing is rebased"}
	ResumedNothing = nothingDone{"nothing resumed", "nothing is resumed"}
	UndidNothing   = nothingDone{"nothing undone", "nothing undone"}
)

// VerbOptions is what run, resume and undo are all tuned by: the sessions to
// check against, the clock, and who to ask before anything moves.
type VerbOptions struct {
	// Agents are the sessions to check against. Nil lists them;
	// an empty slice means there are none.
	Agents []agents.Agent
	// Relist lists the sessions again, at the lock or after a question. Nil
	// lists them the way Agents did.
	Relist func() ([]agents.Agent, error)
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
	// Confirm is asked once before anything moves under an idle session, and
	// by a run before it rebases more than one worktree. A nil Confirm never
	// asks: a script or a hook with no terminal is not a person who can
	// answer.
	Confirm func(works []string) (bool, error)
}

// SessionsNow is the sessions to check against, asked for when the caller named
// none. A verb that cannot find out who is in a worktree changes nothing.
func (o VerbOptions) SessionsNow(ctx *kit.Context, nothing nothingDone) ([]agents.Agent, error) {
	if o.Agents != nil {
		return o.Agents, nil
	}
	listed, err := ctx.ListAgents()
	if err != nil {
		return nil, fmt.Errorf("cannot list agent sessions (%w); %s", err, nothing.tail)
	}
	return listed, nil
}

// relist is the second listing, for the check after a question or at the lock.
func (o VerbOptions) relist(ctx *kit.Context, nothing nothingDone) ([]agents.Agent, error) {
	listed, err := kit.ListAgain(ctx, o.Agents, o.Relist)
	if err != nil {
		return nil, fmt.Errorf("cannot list agent sessions again (%w); %s", err, nothing.tail)
	}
	return listed, nil
}

// Clock is the clock a verb stamps its epoch and its locks with.
func (o VerbOptions) Clock() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// PushOptions is what a run or a resume does with a branch that finished with
// nothing owed.
type PushOptions struct {
	// Push is what happens at the end. Under PushAsk, ConfirmPush is asked
	// once; a nil ConfirmPush prints the push command instead, for Confirm's
	// reason.
	Push        PushMode
	ConfirmPush func(works []string) (bool, error)
	// ChoosePush is asked, where ConfirmPush would be, which of two remote
	// branches a branch pushes to. It answers with one of the two, or "" when
	// the person names neither: the branch is then not pushed.
	ChoosePush func(c PushChoice) (string, error)
}

// PushChoice is a branch that tracks a remote branch of another name, and
// the two places a push of it could go, each as remote/branch.
type PushChoice struct{ Branch, Upstream, Own string }

// PushToName is the subcommand of wt sync that records such a choice.
var PushToName = strings.TrimPrefix(wtsync.PushToCommand, "wt sync ")

// Idle is one checkout a verb is about to change, as its messages name it:
// the label its refusals use — a work name, or undo's branch — where it is,
// and the sessions the verb checked it against, which may be none.
type Idle struct {
	Label    string
	Path     string
	Sessions agents.Sessions
}

// SessionsOf is what a verb was told about the checkout label names.
func SessionsOf(told []Idle, label string) agents.Sessions {
	for _, t := range told {
		if t.Label == label {
			return t.Sessions
		}
	}
	return nil
}

// AskIdle is the one question a verb asks before it changes files under an
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
func AskIdle(ctx *kit.Context, w io.Writer, opts VerbOptions, works []string, told []Idle, listed []agents.Agent, nothing nothingDone) (ok bool, fresh []agents.Agent, err error) {
	if opts.Confirm == nil {
		return true, listed, nil
	}
	if ok, err = opts.Confirm(works); err != nil {
		return false, listed, err
	}
	if !ok {
		fmt.Fprintln(w, nothing.Line)
		return false, listed, nil
	}
	if len(told) == 0 {
		return true, listed, nil
	}
	if fresh, err = opts.relist(ctx, nothing); err != nil {
		return false, listed, err
	}
	for _, t := range told {
		if why := sessionsChanged(t.Sessions, agents.SessionsAt(fresh, t.Path)); why != "" {
			return false, fresh, fmt.Errorf("%s: %s; %s", t.Label, why, nothing.tail)
		}
	}
	return true, fresh, nil
}

// IdleNotice is said before anything moves under an idle session, whether or
// not anybody is asked: a verb never changes files under one silently.
func IdleNotice(work string, s agents.Sessions) string {
	return fmt.Sprintf("⚠ %s: %s is in it; the files it has read will change", work, kit.WhoLabel(s))
}

// TellIdle prints a §5 line for the idle sessions in a worktree, on a line of
// its own so it can be relayed to them verbatim. Nothing when there are none.
func TellIdle(w io.Writer, s agents.Sessions, line string) {
	if len(s) == 0 {
		return
	}
	names := make([]string, len(s))
	for i := range s {
		names[i] = kit.SessionLabel(&s[i])
	}
	fmt.Fprintf(w, "  ⚠ tell %s, idle in it:\n    %s\n", strings.Join(names, ", "), line)
}

// sessionsChanged is why the sessions in a worktree are no longer the ones a
// verb checked: one is busy now, or one arrived that nobody was told about.
// Empty when nothing changed that matters.
func sessionsChanged(told, now agents.Sessions) string {
	if len(now.Busy()) > 0 {
		return "an agent session is busy in it now: " + now.Label(kit.SessionLabel)
	}
	if arrived := now.Arrived(told); len(arrived) > 0 {
		return "a session arrived since it was checked: " + arrived.Label(kit.SessionLabel)
	}
	return ""
}

// PathsOnce joins lists of paths, keeping the first of each.
func PathsOnce(lists ...[]string) []string {
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
