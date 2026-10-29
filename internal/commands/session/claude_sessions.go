package session

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SessionsDeadline is how long a listing waits for claude agents --json. It
// answers in about a quarter of a second; the ten seconds an acting path
// gives it would hold up a listing nobody asked to wait for.
var SessionsDeadline = 2 * time.Second

// MaxSessionName is the widest a session's name is printed, in terminal
// columns. A name is free text, so it is always cut there, piped or not.
const MaxSessionName = 32

// ClaudeSession is a Claude session as a person is shown it.
type ClaudeSession struct{ agents.Agent }

// ClaudeSessions is one read of claude's sessions, started when it is made
// and waited for when it is first used, so the read runs beside whatever
// else a listing does and one read serves every repository of a listing.
type ClaudeSessions struct {
	done     chan struct{}
	sessions []ClaudeSession
	found    bool
	err      error
}

// ReadClaudeSessions starts the read, through ctx's listing; ctx may be nil.
func ReadClaudeSessions(ctx *kit.Context, deadline time.Duration) *ClaudeSessions {
	c := &ClaudeSessions{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		var listed []agents.Agent
		listed, c.found, c.err = ctx.ListClaudeSessions(deadline)
		for _, a := range listed {
			c.sessions = append(c.sessions, ClaudeSession{a})
		}
	}()
	return c
}

// Note is the line a listing prints when the sessions could not be read, and
// "" when they were, or when there is no claude to ask.
func (c *ClaudeSessions) Note() string {
	<-c.done
	if c.err == nil {
		return ""
	}
	return "   sessions not shown: " + kit.OneLine(c.err.Error())
}

// all is every session read; none when the read failed.
func (c *ClaudeSessions) all() []ClaudeSession {
	<-c.done
	return c.sessions
}

// ByWorktree gives each session to the worktree its working directory is in,
// keyed by the worktree's path as given. A worktree nested in another's
// directory keeps its own sessions: the deepest path wins. A worktree's path
// can carry a symlink a session's reported cwd has already resolved, so each
// is resolved first.
func (c *ClaudeSessions) ByWorktree(paths []string) map[string][]ClaudeSession {
	sessions := c.all()
	if len(sessions) == 0 {
		return nil
	}
	resolved := make([]string, len(paths))
	for i, p := range paths {
		resolved[i] = p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			resolved[i] = r
		}
	}
	out := map[string][]ClaudeSession{}
	for _, s := range sessions {
		at := -1
		for i, p := range resolved {
			if repo.Inside(p, s.Cwd, false) && (at < 0 || len(p) > len(resolved[at])) {
				at = i
			}
		}
		if at >= 0 {
			out[paths[at]] = append(out[paths[at]], s)
		}
	}
	for _, in := range out {
		orderSessions(in)
	}
	return out
}

// needsPerson reports a session that cannot go on until somebody answers
// it: claude's state blocked, or status waiting on an open prompt.
func (s ClaudeSession) needsPerson() bool {
	return s.State == "blocked" || s.Status == "waiting"
}

// orderSessions puts the session that needs its person first, then the
// newest, and the one wt runs under last: its reader is already in it.
func orderSessions(sessions []ClaudeSession) {
	rank := func(s ClaudeSession) int {
		switch {
		case s.Own:
			return 2
		case s.needsPerson():
			return 0
		}
		return 1
	}
	slices.SortStableFunc(sessions, func(x, y ClaudeSession) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(y.StartedAt, x.StartedAt))
	})
}

// state is the session's state as a person is told it. A background session
// has a state. blocked is printed "needs input": Claude Code's documentation
// describes blocked ("the session is waiting on you") and its agent view's
// "Needs input" in the same words, without saying in one place that they are
// the same, so the pairing is wt's reading of it. Every other state, one
// this build has not seen included, is printed as claude spells it. A
// session open in a terminal has only a status, and says where it is,
// because that is why it cannot be attached.
func (s ClaudeSession) state() string {
	switch {
	case s.Own:
		return "this session"
	case s.Kind == "interactive":
		if s.Status == "" {
			return "in a terminal"
		}
		return printable(s.Status) + " in a terminal"
	case s.State == "blocked":
		return "needs input"
	case s.State != "":
		return printable(s.State)
	case s.Status != "":
		return printable(s.Status)
	}
	return "listed"
}

// name is what the session goes by: the name somebody gave it, cleaned, or
// the id claude attach takes when it has none.
func (s ClaudeSession) name() string {
	switch name := printable(s.Name); {
	case name != "":
		return name
	case s.ID != "":
		return printable(s.ID)
	}
	return "unnamed"
}

// printable makes free text safe to print in a table: a session's name is
// whatever somebody typed. Control and formatting characters go — an escape
// sequence, a newline, a right-to-left override — and runs of space become
// one.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// sessionCell is the SESSION column for one worktree: the first session's
// name and state, and a count of the rest.
func sessionCell(sessions []ClaudeSession) string {
	if len(sessions) == 0 {
		return "-"
	}
	cell := kit.CutToWidth(sessions[0].name(), MaxSessionName) + " · " + sessions[0].state()
	if n := len(sessions) - 1; n > 0 {
		cell += " +" + strconv.Itoa(n)
	}
	return cell
}

// WithSessions is rows with the SESSION column put in before the path. paths
// is the worktree of each row after the header.
func WithSessions(rows [][]string, paths []string, sessions map[string][]ClaudeSession) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		cell := "SESSION"
		if i > 0 {
			cell = sessionCell(sessions[paths[i-1]])
		}
		out[i] = slices.Insert(slices.Clone(r), len(r)-1, cell)
	}
	return out
}
