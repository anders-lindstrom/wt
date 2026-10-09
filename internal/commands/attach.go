package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/find"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SourceShellLayer is the line that loads wt's shell layer.
const SourceShellLayer = "source ~/.local/share/wt/wt.sh"

// attachDeadline is how long wt attach waits for claude agents --json. A
// listing gives up sooner; attach cannot go on without the answer.
var attachDeadline = 10 * time.Second

// AttachOptions is one `wt attach`.
type AttachOptions struct {
	// Pattern names the worktree as `wt find` takes it, or is a session's id
	// alone. Empty is the worktree the caller stands in.
	Pattern string
	// Session picks one of the worktree's sessions: an id, the start of the
	// full session id, or part of the name. With Resume it is what was typed
	// after --resume: the conversation to resume, handed to claude as it is.
	Session string
	// Resume continues a conversation in the worktree where attaching opens
	// a background session that is listed.
	Resume bool
	// ForShell says wt's shell layer is the caller, on a terminal: what is
	// to be started is written to Out for it, and nothing else is.
	ForShell bool
	// Ask says somebody is at In to answer a question.
	Ask bool
	// Terminal says wt was typed at a terminal, without the shell layer.
	Terminal bool
	In       io.Reader
	Out, Err io.Writer
	// Sessions is the read of claude's sessions; nil reads them.
	Sessions *ClaudeSessions
}

// attachTarget is where wt attach looks for sessions: one worktree, or with
// path empty every worktree of the repository the caller stands in.
type attachTarget struct {
	// label is what messages call it, and arg what names it after `wt attach`.
	label, arg string
	path       string
	sessions   []claudeSession
	// work is the worktree each session is in, by index, when there are
	// several worktrees.
	work []string
}

// Attach finds the Claude session a person means and has wt's shell layer
// open it: claude attach for a background session, claude --continue or
// --resume in the worktree with Resume. The binary starts nothing itself. A
// session that resumes has to come from the shell's own `claude`, which may
// be a function that sets the session up, and without the shell layer or a
// terminal the answer is the command a person would run. ctx is nil outside
// a repository.
func Attach(ctx *Context, o AttachOptions) error {
	read := o.Sessions
	if read == nil {
		read = ReadClaudeSessions(attachDeadline)
	}
	<-read.done
	switch {
	case read.err != nil:
		return read.err
	case !read.found:
		return errors.New("claude is not on the PATH, so there are no sessions to attach to")
	}
	if o.In != nil {
		o.In = bufio.NewReader(o.In)
	}
	if o.Resume {
		return o.resume(ctx, read)
	}
	// A session's id alone, with no worktree named.
	if o.Pattern != "" && o.Session == "" {
		if s := sessionWithID(read.all(), o.Pattern); s != nil {
			if matches, _ := Find(ctx, o.Pattern); len(matches) > 0 {
				m := matches[0]
				how := attachCommand(*s)
				if how == "" {
					how = "it " + whyNotAttachable(*s)
				}
				return fmt.Errorf("%q is the id of the session %s and also matches the worktree %s, "+
					"so nothing was opened.\n"+
					"  The session:   %s\n"+
					"  The worktree:  wt attach %s/%s",
					o.Pattern, s.name(), m.Work, how, m.Repo, m.Work)
			}
			return o.open(*s)
		}
	}
	t, err := o.target(ctx, read)
	if err != nil {
		return err
	}
	s, err := o.choose(t)
	if err != nil {
		return err
	}
	return o.open(s)
}

// sessionWithID is the session whose attach id or full session id is id, or
// nil.
func sessionWithID(sessions []claudeSession, id string) *claudeSession {
	for i, s := range sessions {
		if s.hasID(id) {
			return &sessions[i]
		}
	}
	return nil
}

// hasID reports whether id is the session's attach id or its full session
// id, whole. The letters of an id are hex digits, so case does not tell two
// apart.
func (s claudeSession) hasID(id string) bool {
	return id != "" && (strings.EqualFold(id, s.ID) || strings.EqualFold(id, s.SessionID))
}

// idStartsWith reports whether the session's full id starts with text.
func (s claudeSession) idStartsWith(text string) bool {
	return text != "" && strings.HasPrefix(strings.ToLower(s.SessionID), strings.ToLower(text))
}

// canOpen reports a session wt attach can open: a background one that is
// not the caller's own, with an id fit for a command line.
func (s claudeSession) canOpen() bool {
	return s.Attachable() && !s.Own && attachID(s.ID)
}

// attachCommand is the line that opens s, and "" for a session that cannot
// be opened. It is the one place a claude attach line is written, so none
// carries an id that did not pass attachID.
func attachCommand(s claudeSession) string {
	if !s.canOpen() {
		return ""
	}
	return "claude attach " + s.ID
}

// whyNotAttachable finishes "<name> …" for a session wt attach does not
// open.
func whyNotAttachable(s claudeSession) string {
	switch {
	case s.Own:
		return "is the session wt is running in"
	case s.Kind == "interactive":
		return "is open in a terminal" + pidNote(s) + ", and is used there: claude attach opens background sessions only"
	case s.ID == "":
		return "is a background session claude lists without an id to attach by"
	}
	return fmt.Sprintf("is listed by claude with an id wt will not put on a command line: %q", s.ID)
}

// target resolves the pattern to one worktree, the way `wt find` does, or
// with no pattern takes the worktree the caller stands in. In the main
// checkout no pattern means the whole repository: every session in any of
// its worktrees.
func (o AttachOptions) target(ctx *Context, read *ClaudeSessions) (attachTarget, error) {
	if o.Pattern == "" {
		return o.standing(ctx, read)
	}
	matches, err := Find(ctx, o.Pattern)
	if err != nil {
		return attachTarget{}, err
	}
	var m find.Scored
	switch len(matches) {
	case 0:
		if o.Resume {
			return attachTarget{}, fmt.Errorf("no worktree matches %q", o.Pattern)
		}
		return attachTarget{}, fmt.Errorf("no worktree matches %q, and no session has that id", o.Pattern)
	case 1:
		m = matches[0]
	default:
		fmt.Fprintf(o.Err, "%q matches %d worktrees:\n", o.Pattern, len(matches))
		rows := make([][]string, len(matches))
		for i, c := range matches {
			rows[i] = []string{"  " + strconv.Itoa(i+1), c.Work, c.Repo, c.Path}
		}
		_ = printTable(o.Err, rows)
		n, err := o.pick(len(matches), "name one as <repository>/<work>")
		if err != nil {
			return attachTarget{}, err
		}
		m = matches[n]
	}
	t := attachTarget{label: m.Work, arg: o.Pattern, path: m.Path}
	t.sessions = read.byWorktree(worktreePaths(m.Path))[m.Path]
	return t, nil
}

// worktreePaths is every worktree of the repository path is in, so that a
// session in a worktree nested under path is not taken for one of path's.
// Just path when its repository cannot be read.
func worktreePaths(path string) []string {
	paths := []string{path}
	r, err := repo.Discover(path)
	if err != nil {
		return paths
	}
	worktrees, err := r.Worktrees()
	if err != nil {
		return paths
	}
	for _, wt := range worktrees {
		if wt.Path != path {
			paths = append(paths, wt.Path)
		}
	}
	return paths
}

// standing is the target with no pattern.
func (o AttachOptions) standing(ctx *Context, read *ClaudeSessions) (attachTarget, error) {
	if ctx == nil {
		return attachTarget{}, errors.New("not inside a git repository — name a worktree: wt attach <pattern>")
	}
	names, err := WorkNames(ctx)
	if err != nil {
		return attachTarget{}, err
	}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = n.Path
	}
	by := read.byWorktree(paths)
	if here, _, err := standingWorktree(ctx, names); err == nil && !here.IsMain {
		return attachTarget{label: worktreeName(ctx, here.Branch, here.Path), arg: ".", path: here.Path, sessions: by[here.Path]}, nil
	}
	t := attachTarget{label: "any worktree of " + ctx.Repo.Name}
	for _, n := range names {
		work := n.Work
		if n.IsMain {
			work = "(main)"
		} else if work == "" {
			work = worktreeName(ctx, n.Branch, n.Path)
		}
		for _, s := range by[n.Path] {
			t.sessions = append(t.sessions, s)
			t.work = append(t.work, work)
		}
	}
	return t, nil
}

// choose is the session to attach to in t. One that can be attached is
// taken without a question; several are listed and asked about.
func (o AttachOptions) choose(t attachTarget) (claudeSession, error) {
	if len(t.sessions) == 0 {
		if t.path == "" {
			return claudeSession{}, fmt.Errorf("no Claude session in %s", t.label)
		}
		return claudeSession{}, fmt.Errorf("no Claude session in %s.\n"+
			"  wt attach %s --resume  continues its most recent conversation", t.label, t.arg)
	}
	if o.Session != "" {
		return o.named(t)
	}
	var can []int
	for i, s := range t.sessions {
		if s.canOpen() {
			can = append(can, i)
		}
	}
	switch len(can) {
	case 0:
		o.list(t, nil)
		return claudeSession{}, errors.New("nothing here can be attached: claude attach opens a background " +
			"session, and a session in a terminal is used in that terminal")
	case 1:
		s := t.sessions[can[0]]
		if t.work != nil {
			// Chosen from a whole repository: say which worktree it is in.
			fmt.Fprintf(o.Err, "opening %s, in %s\n", s.name(), t.work[can[0]])
		}
		return s, nil
	}
	o.list(t, can)
	n, err := o.pick(len(can), strconv.Itoa(len(can))+" can be attached; name one: wt attach "+t.argOr("<pattern>")+" <session>")
	if err != nil {
		return claudeSession{}, err
	}
	return t.sessions[can[n]], nil
}

// argOr is how to name the target again on a command line.
func (t attachTarget) argOr(placeholder string) string {
	if t.arg == "" {
		return placeholder
	}
	return t.arg
}

// named is the one session of t that o.Session names. An id given whole
// wins over the start of a session id and over part of a name; more than
// one match is listed and nothing is opened.
func (o AttachOptions) named(t attachTarget) (claudeSession, error) {
	var exact, partial []int
	want := strings.ToLower(o.Session)
	for i, s := range t.sessions {
		switch {
		case s.hasID(o.Session):
			exact = append(exact, i)
		case s.idStartsWith(o.Session), strings.Contains(strings.ToLower(s.name()), want):
			partial = append(partial, i)
		}
	}
	hits := exact
	if len(hits) == 0 {
		hits = partial
	}
	switch len(hits) {
	case 0:
		o.list(t, nil)
		return claudeSession{}, fmt.Errorf("no session in %s has %q as its id or in its name", t.label, o.Session)
	case 1:
		s := t.sessions[hits[0]]
		if !s.canOpen() {
			return s, fmt.Errorf("%s %s", s.name(), whyNotAttachable(s))
		}
		return s, nil
	}
	only := attachTarget{label: t.label, arg: t.arg, path: t.path}
	for _, i := range hits {
		only.sessions = append(only.sessions, t.sessions[i])
		if t.work != nil {
			only.work = append(only.work, t.work[i])
		}
	}
	o.list(only, nil)
	return claudeSession{}, fmt.Errorf("%d sessions match %q; give one's id", len(hits), o.Session)
}

func pidNote(s claudeSession) string {
	if s.PID <= 1 {
		return ""
	}
	return " (pid " + strconv.Itoa(s.PID) + ")"
}

// list prints t's sessions to Err, one a row: its name, its state, where it
// runs and when it started. The rows in can are numbered for pick; the rest
// get a dash, because they cannot be chosen.
func (o AttachOptions) list(t attachTarget, can []int) {
	fmt.Fprintf(o.Err, "Claude sessions in %s:\n", t.label)
	number := map[int]int{}
	for n, i := range can {
		number[i] = n + 1
	}
	now := time.Now()
	rows := make([][]string, len(t.sessions))
	for i, s := range t.sessions {
		mark := "-"
		if n, ok := number[i]; ok {
			mark = strconv.Itoa(n)
		}
		row := []string{"  " + mark}
		if t.work != nil {
			row = append(row, t.work[i])
		}
		where := "background, no id to attach by"
		state := s.state()
		switch {
		case s.Own:
			where, state = "this session", "-"
		case s.Kind == "interactive":
			where = "in another terminal" + pidNote(s)
			if state = printable(s.Status); state == "" {
				state = "-"
			}
		case attachID(s.ID):
			where = "background " + s.ID
		}
		started := "-"
		if s.StartedAt > 0 {
			started = "started " + ago(now.Sub(time.UnixMilli(s.StartedAt)))
		}
		rows[i] = append(row, cutToWidth(s.name(), maxSessionName), state, where, started)
	}
	_ = printTable(o.Err, rows)
}

// pick asks which of n numbered rows and returns its index. With nobody to
// ask, or no answer, nothing is chosen and otherwise says how to choose on
// the command line.
func (o AttachOptions) pick(n int, otherwise string) (int, error) {
	in, ok := o.In.(*bufio.Reader)
	if !o.Ask || !ok {
		return 0, errors.New(otherwise)
	}
	fmt.Fprintf(o.Err, "Which one? [1-%d] ", n)
	line, err := in.ReadString('\n')
	answer := strings.TrimSpace(line)
	if err != nil && answer == "" {
		fmt.Fprintln(o.Err)
	}
	if answer == "" {
		return 0, errors.New("none chosen")
	}
	i, err := strconv.Atoi(answer)
	if err != nil || i < 1 || i > n {
		return 0, fmt.Errorf("%q is not a number from 1 to %d", answer, n)
	}
	return i - 1, nil
}

// resume continues a conversation in the worktree: the one o.Session names,
// or the most recent one there. It refuses a conversation claude lists as a
// session, which attaching reaches and resuming would open a second time.
// The worktree is the unit: from a subfolder the conversation is continued
// at the worktree's root, and a session anywhere in the worktree refuses it.
func (o AttachOptions) resume(ctx *Context, read *ClaudeSessions) error {
	if o.Pattern == "" && ctx == nil {
		return errors.New("not inside a git repository — name a worktree: wt attach <pattern> --resume")
	}
	if o.Pattern == "" {
		// The worktree the caller stands in, the main checkout included: a
		// conversation is resumed in one folder.
		o.Pattern = "."
	}
	t, err := o.target(ctx, read)
	if err != nil {
		return err
	}
	if o.Session == "" {
		if len(t.sessions) > 0 {
			o.list(t, nil)
			hint := "it is used in its terminal"
			switch {
			case slices.ContainsFunc(t.sessions, claudeSession.canOpen):
				hint = "wt attach " + t.arg + "  opens the background one"
			case !slices.ContainsFunc(t.sessions, func(s claudeSession) bool { return !s.Own }):
				hint = "it is the session wt is running in"
			}
			return fmt.Errorf("%s has a session claude lists, and --resume would open its conversation a "+
				"second time.\n  %s", t.label, hint)
		}
		return o.start("continue", "", t.path)
	}
	if strings.HasPrefix(o.Session, "-") || o.Session != printable(o.Session) {
		return fmt.Errorf("%q is not a session id", o.Session)
	}
	for _, s := range read.all() {
		whose := "claude lists that conversation as a session"
		switch {
		case s.hasID(o.Session):
		case s.idStartsWith(o.Session):
			// Not known to be the conversation meant, and not resumed on
			// the chance that claude takes the start of an id for it.
			whose = "claude lists a session whose id starts with that"
		default:
			continue
		}
		how := whyNotAttachable(s)
		if cmd := attachCommand(s); cmd != "" {
			how = "is a background session; " + cmd + " opens it"
		}
		return fmt.Errorf("%s, and --resume could open it a second time: %s (%s) %s",
			whose, s.name(), s.state(), how)
	}
	return o.start("resume", o.Session, t.path)
}

// attachID is what an attach id looks like. The id comes from claude's
// listing and goes onto a command line, so anything else is refused.
func attachID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i, r := range id {
		alnum := r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !alnum && (i == 0 || r != '-' && r != '_') {
			return false
		}
	}
	return true
}

// open attaches to s.
func (o AttachOptions) open(s claudeSession) error {
	if !s.canOpen() {
		return fmt.Errorf("%s %s", s.name(), whyNotAttachable(s))
	}
	return o.start("attach", s.ID, "")
}

// planEnd is the last line of what start prints for the shell layer. The
// shell captures the plan with $(…), which drops trailing newlines; with a
// line after the path, a path that ends in one arrives whole.
const planEnd = "end"

// start hands what is to be run to the shell layer as lines — the verb, the
// id, the worktree's path, then planEnd — which it runs as arguments and
// never evaluates. Without the shell layer nothing is started, and the error
// is the command a person would type.
func (o AttachOptions) start(verb, id, path string) error {
	if o.ForShell {
		_, err := fmt.Fprintf(o.Out, "%s\n%s\n%s\n%s\n", verb, id, path, planEnd)
		return err
	}
	var line string
	switch verb {
	case "attach":
		line = "claude attach " + id
	case "continue":
		line = "cd " + shellQuote(path) + " && claude --continue"
	default:
		line = "cd " + shellQuote(path) + " && claude --resume " + shellQuote(id)
	}
	if o.Terminal {
		return fmt.Errorf("`wt attach` needs wt's shell layer, which is not loaded.\n"+
			"  Add to your shell rc:  %s\n"+
			"  Or run it yourself:    %s", SourceShellLayer, line)
	}
	return fmt.Errorf("there is no terminal here to open it in. At one, run:\n  %s", line)
}
