package commands

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/anders-lindstrom/wt/schema"
)

// MaxSessionMessage is the most message text `wt session send` writes to an
// inbox. Claude Code's inbox drops a connection whose line is longer than
// 1 MiB, silently, so the cap is a quarter of that.
const MaxSessionMessage = 256 * 1024

// maxInboxLine is the longest line the inbox takes, in bytes.
const maxInboxLine = 1 << 20

// UsageError is a mistake in how wt was asked: exit 2.
type UsageError struct{ Err error }

func (e UsageError) Error() string { return e.Err.Error() }
func (e UsageError) Unwrap() error { return e.Err }

// sessionEnv is what the inbox lookup reads outside wt: whose files may be
// trusted, and where claude keeps its registry and sockets.
type sessionEnv struct {
	uid int
	// configDir is claude's configuration directory: its sessions/ holds a
	// registry file per running session, its projects/ the transcripts.
	configDir string
	// socketDirs are where a session's socket is when its registry file is
	// missing, in the order claude falls back to them.
	socketDirs []string
}

func defaultSessionEnv() sessionEnv {
	e := sessionEnv{uid: os.Getuid(), configDir: os.Getenv("CLAUDE_CONFIG_DIR")}
	if e.configDir == "" {
		home, _ := os.UserHomeDir()
		e.configDir = filepath.Join(home, ".claude")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = os.TempDir()
	}
	for _, d := range []string{
		filepath.Join(runtime, "cc-socks"),
		"/tmp/cc-socks",
		"/tmp/cc-socks-" + strconv.Itoa(e.uid),
	} {
		if !containsString(e.socketDirs, d) {
			e.socketDirs = append(e.socketDirs, d)
		}
	}
	return e
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// SessionInfo is one session as `wt session list --json` and `wt session
// send --json` print it.
type SessionInfo struct {
	Work             string  `json:"work"`
	Path             string  `json:"path"`
	Kind             string  `json:"kind"`
	ID               *string `json:"id"`
	SessionID        *string `json:"sessionId"`
	Name             *string `json:"name"`
	PID              *int    `json:"pid"`
	Terminal         bool    `json:"terminal"`
	State            string  `json:"state"`
	ClaudeState      *string `json:"claudeState"`
	ClaudeStatus     *string `json:"claudeStatus"`
	WaitingFor       *string `json:"waitingFor"`
	Inbox            *string `json:"inbox"`
	InboxUnavailable *string `json:"inboxUnavailable"`
	Transcript       *string `json:"transcript"`
	StartedAt        *string `json:"startedAt"`
	Shown            bool    `json:"shown"`

	// sessionID is the listing's id, whole, for the checks that need it.
	sessionID string
}

// SessionList is `wt session list --json`.
type SessionList struct {
	Schema        int           `json:"schema"`
	SchemaVersion string        `json:"schemaVersion"`
	Command       string        `json:"command"`
	Sessions      []SessionInfo `json:"sessions"`
	SessionsError *string       `json:"sessionsError"`
	Error         *string       `json:"error"`
}

// SessionSend is `wt session send --json`.
type SessionSend struct {
	Schema        int          `json:"schema"`
	SchemaVersion string       `json:"schemaVersion"`
	Command       string       `json:"command"`
	Target        *SessionInfo `json:"target"`
	DryRun        bool         `json:"dryRun"`
	Sent          bool         `json:"sent"`
	MsgID         *string      `json:"msgId"`
	Bytes         int          `json:"bytes"`
	Limit         int          `json:"limit"`
	Error         *string      `json:"error"`
	ErrorCode     *string      `json:"errorCode"`
}

// SessionListOptions is one `wt session list`.
type SessionListOptions struct {
	// Pattern names the worktree as `wt attach` takes it; empty is the one
	// the caller stands in, or every worktree from the main checkout.
	Pattern string
	JSON    bool
	Out     io.Writer
	Err     io.Writer
	// Sessions is the read of claude's sessions; nil reads them.
	Sessions *ClaudeSessions
	env      *sessionEnv
}

func (o SessionListOptions) environment() sessionEnv {
	if o.env != nil {
		return *o.env
	}
	return defaultSessionEnv()
}

// worktreeSessions finds the worktree pattern names and its sessions, each with the
// inbox it can be written to, or why it has none. sessionsError is set when
// claude's sessions could not be read; err when the worktree could not be
// found.
func worktreeSessions(ctx *Context, pattern string, errw io.Writer, read *ClaudeSessions, env sessionEnv) (t attachTarget, infos []SessionInfo, sessionsError error, err error) {
	if read == nil {
		read = ReadClaudeSessions(attachDeadline)
	}
	<-read.done
	switch {
	case read.err != nil:
		return t, nil, read.err, nil
	case !read.found:
		return t, nil, errors.New("claude is not on the PATH, so there are no sessions to list"), nil
	}
	o := AttachOptions{Pattern: pattern, Err: errw}
	t, err = o.target(ctx, read)
	if err != nil {
		return t, nil, nil, err
	}
	seen := map[string]bool{}
	for i, s := range t.sessions {
		work, path := t.label, t.path
		if t.work != nil {
			work, path = t.work[i], t.paths[i]
		}
		info := describeSession(s, work, path, env)
		if !seen[path] {
			seen[path] = true
			info.Shown = true
		}
		infos = append(infos, info)
	}
	return t, infos, nil, nil
}

// SessionListRun is `wt session list`.
func SessionListRun(ctx *Context, o SessionListOptions) error {
	_, infos, sessionsErr, err := worktreeSessions(ctx, o.Pattern, o.Err, o.Sessions, o.environment())
	out := SessionList{Schema: 1, SchemaVersion: schema.VersionOf("session-list"), Command: "session list",
		Sessions: []SessionInfo{}}
	if infos != nil {
		out.Sessions = infos
	}
	switch {
	case err != nil:
		out.Error = strp(oneLine(err.Error()))
	case sessionsErr != nil:
		out.SessionsError = strp(oneLine(sessionsErr.Error()))
	}
	if o.JSON {
		if werr := writeJSON(o.Out, out); werr != nil {
			return werr
		}
		if err != nil {
			return err
		}
		return sessionsErr
	}
	if err != nil {
		return err
	}
	if sessionsErr != nil {
		return sessionsErr
	}
	if len(infos) == 0 {
		fmt.Fprintln(o.Out, "no Claude session in the worktree")
		return nil
	}
	return printTable(o.Out, sessionRows(infos))
}

// sessionRows is the human listing: worktree, session, state, inbox, age.
func sessionRows(infos []SessionInfo) [][]string {
	now := time.Now()
	rows := make([][]string, len(infos))
	for i, s := range infos {
		name := "unnamed"
		if s.Name != nil && *s.Name != "" {
			name = *s.Name
		} else if s.ID != nil {
			name = *s.ID
		}
		state := s.State
		switch {
		case s.WaitingFor != nil:
			state = "waiting: " + *s.WaitingFor
		case s.Terminal:
			state += " in a terminal"
		}
		inbox := "inbox"
		if s.InboxUnavailable != nil {
			inbox = "no inbox: " + *s.InboxUnavailable
		}
		started := "-"
		if s.StartedAt != nil {
			if t, err := time.Parse(time.RFC3339, *s.StartedAt); err == nil {
				started = "started " + ago(now.Sub(t))
			}
		}
		rows[i] = []string{s.Work, cutToWidth(name, maxSessionName), state, inbox, started}
	}
	return rows
}

var sessionIDShape = regexp.MustCompile(`^[0-9A-Za-z-]{8,64}$`)

// describeSession is s as the JSON shows it, with its inbox looked up.
func describeSession(s claudeSession, work, path string, env sessionEnv) SessionInfo {
	info := SessionInfo{Work: work, Path: path, Kind: "claude", Terminal: s.Kind == "interactive",
		State: "busy", sessionID: s.SessionID}
	if s.Idle() {
		info.State = "idle"
	}
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	info.ID, info.SessionID, info.Name = opt(s.ID), opt(s.SessionID), opt(printable(s.Name))
	info.ClaudeState, info.ClaudeStatus, info.WaitingFor = opt(s.State), opt(s.Status), opt(printable(s.WaitingFor))
	if s.PID > 1 {
		pid := s.PID
		info.PID = &pid
	}
	if s.StartedAt > 0 {
		info.StartedAt = strp(time.UnixMilli(s.StartedAt).UTC().Format(time.RFC3339))
	}
	if sessionIDShape.MatchString(s.SessionID) {
		if hits, _ := filepath.Glob(filepath.Join(env.configDir, "projects", "*", s.SessionID+".jsonl")); len(hits) > 0 {
			info.Transcript = &hits[0]
		}
	}
	if s.Own {
		info.InboxUnavailable = strp("own")
		return info
	}
	info.Inbox, info.InboxUnavailable = findInbox(s, env)
	return info
}

// findInbox is the socket of s's inbox, or why it has none. Nothing is
// written to it: it must be a socket owned by the user, in a directory only
// the user can write to, that a session whose id matches names, and that
// accepts a connection.
func findInbox(s claudeSession, env sessionEnv) (inbox, why *string) {
	no := func(reason string) (*string, *string) { return nil, &reason }
	if s.PID <= 1 || s.SessionID == "" {
		if s.PID <= 1 {
			return no("notRunning")
		}
		return no("noRegistry")
	}
	var path string
	data, err := os.ReadFile(filepath.Join(env.configDir, "sessions", strconv.Itoa(s.PID)+".json"))
	switch {
	case err == nil:
		var reg struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Socket    string `json:"messagingSocketPath"`
		}
		// A pid is reused, and a registry file outlives its session: the
		// file is the session's only when it names the same conversation.
		if json.Unmarshal(data, &reg) != nil || reg.PID != s.PID ||
			!strings.EqualFold(reg.SessionID, s.SessionID) || reg.Socket == "" {
			return no("noRegistry")
		}
		path = reg.Socket
	case errors.Is(err, os.ErrNotExist):
		for _, d := range env.socketDirs {
			p := filepath.Join(d, strconv.Itoa(s.PID)+".sock")
			if _, err := os.Lstat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return no("noRegistry")
		}
	default:
		return no("noRegistry")
	}
	if !env.trustedSocket(path) {
		return no("notASocket")
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return no("refused")
	}
	_ = c.Close()
	return &path, nil
}

// trustedSocket reports a path that is a socket, not a link to one, owned by
// the user and closed to everybody else, in a directory the same holds for.
func (e sessionEnv) trustedSocket(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode()&os.ModeSymlink != 0 ||
		fi.Mode().Perm()&0o077 != 0 || !e.ownedByUser(fi) {
		return false
	}
	dir, err := os.Lstat(filepath.Dir(path))
	return err == nil && dir.IsDir() && dir.Mode()&os.ModeSymlink == 0 &&
		dir.Mode().Perm()&0o022 == 0 && e.ownedByUser(dir)
}

func (e sessionEnv) ownedByUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == e.uid
}

// SessionSendOptions is one `wt session send`.
type SessionSendOptions struct {
	// Pattern names the worktree as `wt attach` takes it.
	Pattern string
	// Session picks one of its sessions: an id, the start of the session
	// id, or part of the name.
	Session string
	// FromName is the name the receiving session shows; empty is "wt".
	FromName string
	// ReplyTo is the address a reply goes to, uds:<socket path>.
	ReplyTo string
	// ExpectSession refuses the send unless the target is this session id.
	ExpectSession string
	DryRun        bool
	JSON          bool
	// In is the message.
	In       io.Reader
	Out, Err io.Writer
	Sessions *ClaudeSessions
	env      *sessionEnv
}

type sendFailure struct {
	code  string
	msg   string
	usage bool
}

// SessionSendRun is `wt session send`. It writes one line to one inbox and
// never tries again: sent says the line was written and the connection
// closed cleanly, not that the session's Claude has read it.
func SessionSendRun(ctx *Context, o SessionSendOptions) error {
	out := SessionSend{Schema: 1, SchemaVersion: schema.VersionOf("session-send"), Command: "session send",
		DryRun: o.DryRun, Limit: MaxSessionMessage}
	env := defaultSessionEnv()
	if o.env != nil {
		env = *o.env
	}
	fail := func(f sendFailure) error {
		out.Sent = false
		out.MsgID = nil
		out.Error, out.ErrorCode = strp(f.msg), strp(f.code)
		if o.JSON {
			if err := writeJSON(o.Out, out); err != nil {
				return err
			}
		}
		err := errors.New(f.msg)
		if f.usage {
			return UsageError{err}
		}
		return err
	}
	if o.FromName == "" {
		o.FromName = "wt"
	}
	if err := checkFromName(o.FromName); err != nil {
		return fail(sendFailure{"badArgument", err.Error(), true})
	}
	if o.ReplyTo != "" && !strings.HasPrefix(o.ReplyTo, "uds:") {
		return fail(sendFailure{"badArgument", fmt.Sprintf("--reply-to %q is not a uds:<socket path> address", o.ReplyTo), true})
	}

	target, f := chooseSessionTarget(ctx, o, env)
	if f != nil {
		if target != nil {
			out.Target = target
		}
		return fail(*f)
	}
	out.Target = target
	reply := ""
	if o.ReplyTo != "" {
		reply = strings.TrimPrefix(o.ReplyTo, "uds:")
		if !env.trustedSocket(reply) || filepath.Dir(reply) != filepath.Dir(*target.Inbox) {
			return fail(sendFailure{"badArgument", fmt.Sprintf("--reply-to %q is not a socket of yours in the directory of the session's inbox (%s)",
				o.ReplyTo, filepath.Dir(*target.Inbox)), true})
		}
	}

	text, size, err := readSessionMessage(o.In)
	out.Bytes = size
	switch {
	case err != nil:
		return fail(sendFailure{"empty", "the message could not be read: " + err.Error(), false})
	case strings.TrimSpace(text) == "":
		return fail(sendFailure{"empty", "the message is empty: nothing was sent", false})
	case size > MaxSessionMessage:
		return fail(sendFailure{"tooLarge", fmt.Sprintf("the message is %d bytes; a session takes up to %d. Nothing was sent",
			size, MaxSessionMessage), false})
	}

	msgID := newUUID()
	line, err := buildFrame(msgID, target.sessionID, text, o.FromName, reply)
	if err != nil {
		return fail(sendFailure{"writeFailed", err.Error(), false})
	}
	if len(line) > maxInboxLine-1 {
		return fail(sendFailure{"tooLarge", fmt.Sprintf("the message is %d bytes, but wrapped and escaped for the inbox it is %d, "+
			"over the %d a line may be. Nothing was sent", size, len(line), maxInboxLine-1), false})
	}
	if o.DryRun {
		return finishSend(o, out, target, false)
	}
	if err := writeToInbox(*target.Inbox, line); err != nil {
		return fail(sendFailure{"writeFailed", fmt.Sprintf("writing to %s failed: %v", *target.Inbox, err), false})
	}
	out.Sent, out.MsgID = true, &msgID
	return finishSend(o, out, target, true)
}

func finishSend(o SessionSendOptions, out SessionSend, target *SessionInfo, sent bool) error {
	if o.JSON {
		return writeJSON(o.Out, out)
	}
	name := "the session"
	if target.Name != nil {
		name = *target.Name
	}
	if !sent {
		_, err := fmt.Fprintf(o.Out, "would send %d bytes to %s (%s)\n", out.Bytes, name, *target.Inbox)
		return err
	}
	_, err := fmt.Fprintf(o.Out, "sent %d bytes to %s: written to its inbox, not necessarily read yet\n", out.Bytes, name)
	return err
}

// chooseSessionTarget is the one session o names, with an inbox, or the
// reason there is none. It never picks among several.
func chooseSessionTarget(ctx *Context, o SessionSendOptions, env sessionEnv) (*SessionInfo, *sendFailure) {
	t, infos, sessionsErr, err := worktreeSessions(ctx, o.Pattern, o.Err, o.Sessions, env)
	switch {
	case sessionsErr != nil:
		return nil, &sendFailure{"noSession", sessionsErr.Error(), false}
	case err != nil:
		return nil, &sendFailure{"noSession", err.Error(), false}
	}
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
			fmt.Fprintf(&b, "\n  %s  %s  %s", name, id, s.State)
		}
		return b.String()
	}
	all := make([]int, len(infos))
	for i := range infos {
		all[i] = i
	}
	var pick int
	switch {
	case len(infos) == 0:
		return nil, &sendFailure{"noSession", fmt.Sprintf("no Claude session in %s", t.label), false}
	case o.Session != "":
		hits := matchSessions(t.sessions, o.Session)
		switch len(hits) {
		case 0:
			return nil, &sendFailure{"noSession", fmt.Sprintf("no session in %s has %q as its id or in its name", t.label, o.Session), false}
		case 1:
			pick = hits[0]
		default:
			return nil, &sendFailure{"ambiguous", fmt.Sprintf("%d sessions in %s match %q; give one's id:%s",
				len(hits), t.label, o.Session, list(hits)), true}
		}
	case o.ExpectSession != "":
		pick = -1
		for i, s := range infos {
			if strings.EqualFold(s.sessionID, o.ExpectSession) {
				pick = i
			}
		}
		if pick < 0 {
			return nil, &sendFailure{"changed", fmt.Sprintf("session %s is not in %s any more. Nothing was sent", o.ExpectSession, t.label), false}
		}
	default:
		var live []int
		for i, s := range infos {
			if s.PID != nil {
				live = append(live, i)
			}
		}
		switch len(live) {
		case 0:
			return nil, &sendFailure{"notRunning", fmt.Sprintf("no session in %s is running; wt does not wake a parked session. "+
				"Open it in a terminal instead (wt attach):%s", t.label, list(all)), false}
		case 1:
			pick = live[0]
		default:
			return nil, &sendFailure{"ambiguous", fmt.Sprintf("%d sessions are running in %s; name one:%s", len(live), t.label, list(live)), true}
		}
	}
	target := infos[pick]
	if o.ExpectSession != "" && !strings.EqualFold(target.sessionID, o.ExpectSession) {
		return &target, &sendFailure{"changed", fmt.Sprintf("the session is %s, not the %s that was expected. Nothing was sent",
			target.sessionID, o.ExpectSession), false}
	}
	if target.Inbox == nil {
		why := "none"
		if target.InboxUnavailable != nil {
			why = *target.InboxUnavailable
		}
		name := "the session"
		if target.Name != nil {
			name = *target.Name
		}
		switch why {
		case "notRunning":
			return &target, &sendFailure{"notRunning", name + " is not running; wt does not wake a parked session. Nothing was sent", false}
		case "own":
			return &target, &sendFailure{"noInbox", name + " is the session wt is running in; wt does not send to it", false}
		}
		return &target, &sendFailure{"noInbox", fmt.Sprintf("%s has no inbox wt can write to (%s). Nothing was sent", name, why), false}
	}
	return &target, nil
}

// checkFromName refuses a name that could not sit in an attribute of the
// wrapper: the receiving Claude reads the name out of it.
func checkFromName(name string) error {
	if len([]rune(name)) > 64 {
		return fmt.Errorf("--from-name is longer than 64 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`"<>&`, r) {
			return fmt.Errorf("--from-name %q has a character that cannot go in the message's header", name)
		}
	}
	return nil
}

// readSessionMessage reads the message, keeping at most the cap and a byte
// of the text, and counting the rest, so a huge input is neither held nor
// reported as just "too large".
func readSessionMessage(r io.Reader) (text string, size int, err error) {
	if r == nil {
		return "", 0, nil
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, MaxSessionMessage+1))
	size = int(n)
	if err != nil {
		return "", size, err
	}
	if size > MaxSessionMessage {
		rest, err := io.Copy(io.Discard, r)
		size += int(rest)
		if err != nil {
			return "", size, err
		}
	}
	return buf.String(), size, nil
}

// wrapperTags are the two tags that make a message a cross-session one. A
// message that carries them could close the wrapper and write outside it,
// so each opening angle bracket is escaped.
var wrapperTags = strings.NewReplacer(
	"</cross-session-message", "&lt;/cross-session-message",
	"<cross-session-message", "&lt;cross-session-message",
)

// buildFrame is the line to write: the message in its wrapper, in the frame
// Claude's inbox reads. session_id makes the inbox drop it if the pid now
// belongs to another session.
func buildFrame(msgID, sessionID, text, fromName, reply string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("<cross-session-message")
	if reply != "" {
		b.WriteString(` from="uds:` + reply + `"`)
	}
	b.WriteString(` from-name="` + fromName + `">` + "\n")
	b.WriteString(wrapperTags.Replace(text))
	b.WriteString("\n</cross-session-message>")
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	frame := struct {
		MsgV      int     `json:"msgV"`
		MsgID     string  `json:"msg_id"`
		Type      string  `json:"type"`
		Message   message `json:"message"`
		Priority  string  `json:"priority"`
		SessionID string  `json:"session_id"`
		From      string  `json:"from,omitempty"`
	}{1, msgID, "user", message{"user", b.String()}, "next", sessionID, ""}
	if reply != "" {
		frame.From = "uds:" + reply
	}
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(frame); err != nil {
		return nil, err
	}
	return line.Bytes(), nil
}

// writeToInbox connects, writes the line, closes the write side and waits a
// moment for the inbox to drop the connection, which is how it says no.
func writeToInbox(path string, line []byte) error {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(line); err != nil {
		return err
	}
	if uc, ok := c.(*net.UnixConn); ok {
		if err := uc.CloseWrite(); err != nil {
			return err
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = io.Copy(io.Discard, io.LimitReader(c, 1<<20))
	var ne net.Error
	if err != nil && (!errors.As(err, &ne) || !ne.Timeout()) {
		return err
	}
	return nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
