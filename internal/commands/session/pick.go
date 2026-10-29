package session

import (
	"fmt"
	"strings"
)

// refusalCode is the errorCode of `wt session send --json` or the
// reasonCode of `wt session stop --json`. Its values are the enums of
// schema/session-send.v1.json and schema/session-stop.v1.json.
type refusalCode string

const (
	codeNoSession     refusalCode = "noSession"
	codeAmbiguous     refusalCode = "ambiguous"
	codeChanged       refusalCode = "changed"
	codeNotExact      refusalCode = "notExact"
	codeNotRunning    refusalCode = "notRunning"
	codeNoInbox       refusalCode = "noInbox"
	codeTooLarge      refusalCode = "tooLarge"
	codeEmpty         refusalCode = "empty"
	codeWriteFailed   refusalCode = "writeFailed"
	codeBadArgument   refusalCode = "badArgument"
	codeInteractive   refusalCode = "interactive"
	codeCodex         refusalCode = "codex"
	codeOwn           refusalCode = "own"
	codeNoID          refusalCode = "noId"
	codeNotBackground refusalCode = "notBackground"
	codeNoProcess     refusalCode = "noProcess"
	codeStopFailed    refusalCode = "stopFailed"
	codeOwnUnknown    refusalCode = "ownUnknown"
)

// sendCodes and stopCodes are the codes each command can report; a test
// holds them to the schema's enum.
var (
	sendCodes = []refusalCode{codeNoSession, codeAmbiguous, codeNotRunning, codeNoInbox, codeChanged,
		codeTooLarge, codeEmpty, codeWriteFailed, codeBadArgument, codeNotExact}
	stopCodes = []refusalCode{codeNoSession, codeAmbiguous, codeChanged, codeInteractive, codeCodex, codeOwn,
		codeNoID, codeNotBackground, codeNoProcess, codeStopFailed, codeOwnUnknown, codeNotExact}
)

// refusal is why a send or a stop did not happen. failed marks a failure
// rather than a refusal (stop's outcome "failed"); send does not tell the
// two apart. usage makes it a usage mistake.
type refusal struct {
	code   refusalCode
	msg    string
	failed bool
	usage  bool
}

// pickRules is what send and stop do differently when choosing a session.
type pickRules struct {
	pattern, session, expectSession string
	// notExact is the code for a pattern that is not a worktree's exact name.
	notExact refusalCode
	// verb finishes "Nothing was …".
	verb string
	// word is what a candidate line shows after the name and id.
	word func(Info) string
	// fallback chooses when neither --session nor --expect-session is given.
	// list formats candidates for a message.
	fallback func(list func([]int) string) (int, *refusal)
}

// pickSession is the index of the one session in infos that r names, or
// the refusal. It never picks among several. t.sessions[i] is infos[i].
func pickSession(t attachTarget, infos []Info, r pickRules) (int, *refusal) {
	list := func(idx []int) string {
		var b strings.Builder
		for _, i := range idx {
			s := infos[i]
			name := "unnamed"
			if s.Name != nil {
				name = *s.Name
			}
			id := ""
			if s.ID != nil {
				id = *s.ID
			} else if s.SessionID != nil {
				id = (*s.SessionID)[:min(8, len(*s.SessionID))]
			}
			fmt.Fprintf(&b, "\n  %s  %s  %s", name, id, r.word(s))
		}
		return b.String()
	}
	if r.expectSession == "" && !namesWorktreeExactly(r.pattern, t) {
		return -1, &refusal{code: r.notExact, msg: fmt.Sprintf("%q is not the name of a worktree but matched %s (%s): "+
			"name it exactly, or give --expect-session. Nothing was %s", r.pattern, t.label, t.path, r.verb)}
	}
	if len(infos) == 0 {
		return -1, &refusal{code: codeNoSession, failed: true, msg: fmt.Sprintf("no Claude session in %s", t.label)}
	}
	pick := -1
	switch {
	case r.session != "":
		hits := matchSessions(t.sessions, r.session)
		switch len(hits) {
		case 0:
			return -1, &refusal{code: codeNoSession, failed: true, msg: fmt.Sprintf("no session in %s has %q as its id or in its name", t.label, r.session)}
		case 1:
			pick = hits[0]
		default:
			return -1, &refusal{code: codeAmbiguous, usage: true, msg: fmt.Sprintf("%d sessions in %s match %q; give one's id:%s",
				len(hits), t.label, r.session, list(hits))}
		}
	case r.expectSession != "":
		for i, s := range infos {
			if strings.EqualFold(s.sessionID, r.expectSession) && (pick < 0 || s.PID != nil) {
				pick = i
			}
		}
		if pick < 0 {
			return -1, &refusal{code: codeChanged, msg: fmt.Sprintf("session %s is not in %s any more. Nothing was %s", r.expectSession, t.label, r.verb)}
		}
	default:
		var f *refusal
		if pick, f = r.fallback(list); f != nil {
			return -1, f
		}
	}
	if r.expectSession != "" && !strings.EqualFold(infos[pick].sessionID, r.expectSession) {
		return pick, &refusal{code: codeChanged, msg: fmt.Sprintf("the session is %s, not the %s that was expected. Nothing was %s",
			infos[pick].sessionID, r.expectSession, r.verb)}
	}
	return pick, nil
}

// allOf is every index of n things.
func allOf(n int) []int {
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	return all
}
