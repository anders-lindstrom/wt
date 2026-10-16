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
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
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
	// noProbe leaves out the connection to each inbox: it is then taken
	// from the registry and the checks on the file alone.
	noProbe bool
	// projects is the directories of claude's projects/, read once.
	projects *projectDirs
}

// projectDirs is the directories under claude's projects/, listed once for
// the transcripts of a whole listing.
type projectDirs struct {
	once sync.Once
	dirs []string
}

func (p *projectDirs) list(configDir string) []string {
	p.once.Do(func() {
		entries, _ := os.ReadDir(filepath.Join(configDir, "projects"))
		for _, e := range entries {
			if e.IsDir() {
				p.dirs = append(p.dirs, filepath.Join(configDir, "projects", e.Name()))
			}
		}
	})
	return p.dirs
}

// transcriptOf is the .jsonl of a conversation, and its modification time,
// when one of the projects holds it. A stat each, never a read.
func (e sessionEnv) transcriptOf(sessionID string) (string, time.Time, bool) {
	dirs := e.projects
	if dirs == nil {
		dirs = &projectDirs{}
	}
	for _, d := range dirs.list(e.configDir) {
		p := filepath.Join(d, sessionID+".jsonl")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, fi.ModTime(), true
		}
	}
	return "", time.Time{}, false
}

func defaultSessionEnv() sessionEnv {
	e := sessionEnv{uid: os.Getuid(), configDir: os.Getenv("CLAUDE_CONFIG_DIR"), projects: &projectDirs{}}
	if e.configDir == "" {
		home, _ := os.UserHomeDir()
		e.configDir = filepath.Join(home, ".claude")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = os.TempDir()
	}
	for _, d := range []string{
		"/tmp/cc-socks",
		filepath.Join(runtime, "cc-socks"),
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
	Repo             string  `json:"repo"`
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
	StateSince       *string `json:"stateSince"`
	LastActivityAt   *string `json:"lastActivityAt"`
	Activity         string  `json:"activity"`
	Background       bool    `json:"background"`
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
	CodexError    *string       `json:"codexError"`
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
	// Selection, when it names any, lists the sessions of every repository
	// it covers instead, from one read of claude's sessions. User is the
	// configuration it is resolved against.
	Selection kit.Selection
	User      *config.User
	// NoInboxCheck leaves out the connection to each inbox.
	NoInboxCheck bool
	JSON         bool
	Out          io.Writer
	Err          io.Writer
	// Sessions is the read of claude's sessions; nil reads them.
	Sessions *ClaudeSessions
	env      *sessionEnv
}

func (o SessionListOptions) environment() sessionEnv {
	e := defaultSessionEnv()
	if o.env != nil {
		e = *o.env
	}
	e.noProbe = e.noProbe || o.NoInboxCheck
	return e
}

// listCodexSessions is a var so tests stay off the machine's process table.
var listCodexSessions = wtsync.ListCodexSessions

// with is the read plus extra sessions of another program, as one.
func (c *ClaudeSessions) with(extra []wtsync.Agent) *ClaudeSessions {
	<-c.done
	m := &ClaudeSessions{done: make(chan struct{}), found: c.found, err: c.err}
	close(m.done)
	m.sessions = append(m.sessions, c.sessions...)
	for _, a := range extra {
		m.sessions = append(m.sessions, claudeSession{a})
	}
	return m
}

// worktreeSessions finds the worktree pattern names and its sessions, each with the
// inbox it can be written to, or why it has none. sessionsError is set when
// claude's sessions could not be read; err when the worktree could not be
// found.
func worktreeSessions(ctx *kit.Context, pattern string, errw io.Writer, read *ClaudeSessions, env sessionEnv) (t attachTarget, infos []SessionInfo, sessionsError error, err error) {
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
	return t, describeTarget(t, env), nil, nil
}

// describeTarget is the sessions of t as the JSON shows them; the first of
// each worktree is the one wt list shows.
func describeTarget(t attachTarget, env sessionEnv) []SessionInfo {
	var infos []SessionInfo
	seen := map[string]bool{}
	for i, s := range t.sessions {
		work, path := t.label, t.path
		if t.work != nil {
			work, path = t.work[i], t.paths[i]
		}
		info := describeSession(s, t.repo, work, path, env)
		if !seen[path] {
			seen[path] = true
			info.Shown = true
		}
		infos = append(infos, info)
	}
	return infos
}

// repoSessions is every session in any worktree of ctx's repository, of the
// sessions read holds.
func repoSessions(ctx *kit.Context, read *ClaudeSessions, env sessionEnv) ([]SessionInfo, error) {
	names, err := kit.WorkNames(ctx)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = n.Path
	}
	by := read.byWorktree(paths)
	t := attachTarget{repo: ctx.Repo.Name}
	for _, n := range names {
		work := n.Work
		switch {
		case n.IsMain:
			work = "(main)"
		case work == "":
			work = kit.WorktreeName(ctx, n.Branch, n.Path)
		}
		for _, s := range by[n.Path] {
			t.sessions = append(t.sessions, s)
			t.work = append(t.work, work)
			t.paths = append(t.paths, n.Path)
		}
	}
	return describeTarget(t, env), nil
}

// acrossRepos lists the sessions of every repository the selection covers
// from the one read of claude's sessions there is: each session is placed in
// its repository by its working directory, and only a repository holding one
// is opened.
func acrossRepos(u *config.User, sel kit.Selection, read *ClaudeSessions, env sessionEnv) ([]SessionInfo, error) {
	all := read.all()
	roots := make([]string, len(all))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	cache := sync.Map{}
	for i, s := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if s.Cwd == "" {
				return
			}
			if v, ok := cache.Load(s.Cwd); ok {
				roots[i] = v.(string)
				return
			}
			if r, err := repo.Discover(s.Cwd); err == nil {
				roots[i] = r.MainRoot
			}
			cache.Store(s.Cwd, roots[i])
		}()
	}
	wg.Wait()
	targets, err := selectedWithSessions(u, sel, roots)
	if err != nil {
		return nil, err
	}
	set := kit.RepoSet{Repos: targets}
	type repoResult struct {
		infos []SessionInfo
		err   error
	}
	results := kit.EachRepo(set.Repos, kit.RepoParallelism, func(t kit.RepoTarget) repoResult {
		var mine []claudeSession
		for i, s := range all {
			if roots[i] != "" && repo.SamePath(roots[i], t.Path) {
				mine = append(mine, s)
			}
		}
		ctx, err := kit.Open(t.Path)
		if err != nil {
			return repoResult{err: fmt.Errorf("%s: %w", t.Path, err)}
		}
		got, err := repoSessions(ctx, &ClaudeSessions{done: closedChan(), sessions: mine, found: true}, env)
		if err != nil {
			err = fmt.Errorf("%s: %w", t.Path, err)
		}
		return repoResult{got, err}
	})
	var infos []SessionInfo
	for _, r := range results {
		infos = append(infos, r.infos...)
		if r.err != nil {
			return infos, r.err
		}
	}
	return infos, nil
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// SessionListRun is `wt session list`. Codex sessions are listed beside
// Claude's, with no inbox; when they cannot be found, codexError says so and
// the Claude sessions stand.
func SessionListRun(ctx *kit.Context, o SessionListOptions) error {
	read := o.Sessions
	if read == nil {
		read = ReadClaudeSessions(attachDeadline)
	}
	type codexRead struct {
		agents []wtsync.Agent
		err    error
	}
	codexDone := make(chan codexRead, 1)
	go func() {
		a, err := listCodexSessions()
		codexDone <- codexRead{a, err}
	}()
	out := SessionList{Schema: 1, SchemaVersion: schema.VersionOf("session-list"), Command: "session list",
		Sessions: []SessionInfo{}}
	env := o.environment()
	var infos []SessionInfo
	var sessionsErr, err error
	<-read.done
	codex := <-codexDone
	if codex.err != nil {
		out.CodexError = kit.Strp(kit.OneLine(codex.err.Error()))
	}
	merged := read.with(codex.agents)
	if o.Selection.Any() {
		switch {
		case read.err != nil:
			sessionsErr = read.err
		case !read.found:
			sessionsErr = errors.New("claude is not on the PATH, so there are no sessions to list")
		default:
			infos, err = acrossRepos(o.User, o.Selection, merged, env)
		}
	} else {
		_, infos, sessionsErr, err = worktreeSessions(ctx, o.Pattern, o.Err, merged, env)
	}
	if infos != nil {
		out.Sessions = infos
	}
	switch {
	case err != nil:
		out.Error = kit.Strp(kit.OneLine(err.Error()))
	case sessionsErr != nil:
		out.SessionsError = kit.Strp(kit.OneLine(sessionsErr.Error()))
	}
	if o.JSON {
		if werr := kit.WriteJSON(o.Out, out); werr != nil {
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
	if out.CodexError != nil {
		fmt.Fprintln(o.Err, "wt: Codex sessions not shown: "+*out.CodexError)
	}
	if len(infos) == 0 {
		fmt.Fprintln(o.Out, "no agent session found")
		return nil
	}
	return kit.PrintTable(o.Out, sessionRows(infos))
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
				started = "started " + kit.Ago(now.Sub(t))
			}
		}
		rows[i] = []string{s.Work, cutToWidth(name, maxSessionName), state, inbox, started}
	}
	return rows
}

var sessionIDShape = regexp.MustCompile(`^[0-9A-Za-z-]{8,64}$`)

// describeSession is s as the JSON shows it, with its inbox looked up.
func describeSession(s claudeSession, repo, work, path string, env sessionEnv) SessionInfo {
	info := SessionInfo{Repo: repo, Work: work, Path: path, Kind: "claude", Terminal: s.Kind == "interactive",
		State: "busy", sessionID: s.SessionID}
	codex := s.Tool == wtsync.ToolCodex
	if codex {
		info.Kind = "codex"
	}
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
		info.StartedAt = kit.Strp(time.UnixMilli(s.StartedAt).UTC().Format(time.RFC3339))
	}
	info.Background = s.ID != "" && !codex
	info.Activity = sessionActivity(s)
	if codex {
		info.InboxUnavailable = kit.Strp("codex")
		return info
	}
	if sessionIDShape.MatchString(s.SessionID) {
		if path, mtime, ok := env.transcriptOf(s.SessionID); ok {
			info.Transcript = &path
			info.LastActivityAt = kit.Strp(mtime.UTC().Format(time.RFC3339))
		}
	}
	var reg registryEntry
	regState := registryMissing
	if s.PID > 1 && s.SessionID != "" {
		reg, regState = readRegistry(s, env)
		if regState == registryOK && reg.statusUpdated > 0 {
			info.StateSince = kit.Strp(time.UnixMilli(reg.statusUpdated).UTC().Format(time.RFC3339))
		}
	}
	if s.Own {
		info.InboxUnavailable = kit.Strp("own")
		return info
	}
	info.Inbox, info.InboxUnavailable = findInbox(s, env, reg, regState)
	return info
}

// registryEntry is what claude's registry file for a session says.
type registryEntry struct {
	socket string
	// statusUpdated is when the session's status last changed, in Unix
	// milliseconds, or 0.
	statusUpdated int64
}

const (
	registryOK = iota
	registryMissing
	registryBad
)

// readRegistry reads ~/.claude/sessions/<pid>.json. A pid is reused and the
// file outlives its session: it is the session's only when it names the same
// pid and conversation.
func readRegistry(s claudeSession, env sessionEnv) (registryEntry, int) {
	data, err := os.ReadFile(filepath.Join(env.configDir, "sessions", strconv.Itoa(s.PID)+".json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return registryEntry{}, registryMissing
	case err != nil:
		return registryEntry{}, registryBad
	}
	var raw struct {
		PID             int             `json:"pid"`
		SessionID       string          `json:"sessionId"`
		Socket          string          `json:"messagingSocketPath"`
		StatusUpdatedAt json.RawMessage `json:"statusUpdatedAt"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.PID != s.PID || !strings.EqualFold(raw.SessionID, s.SessionID) {
		return registryEntry{}, registryBad
	}
	reg := registryEntry{socket: raw.Socket}
	var at float64
	if json.Unmarshal(raw.StatusUpdatedAt, &at) == nil && at > 0 {
		reg.statusUpdated = int64(at)
	}
	return reg, registryOK
}

// findInbox is the socket of s's inbox, or why it has none. Nothing is
// written to it: it must be a socket owned by the user, in a directory only
// the user can write to, that a session whose id matches names, and that
// accepts a connection (unless env.noProbe).
func findInbox(s claudeSession, env sessionEnv, reg registryEntry, regState int) (inbox, why *string) {
	no := func(reason string) (*string, *string) { return nil, &reason }
	if s.PID <= 1 {
		return no("notRunning")
	}
	if s.SessionID == "" {
		return no("noRegistry")
	}
	var path string
	switch regState {
	case registryOK:
		if reg.socket == "" {
			return no("noRegistry")
		}
		path = reg.socket
	case registryMissing:
		for _, d := range env.socketDirs {
			p := filepath.Join(d, strconv.Itoa(s.PID)+".sock")
			if env.trustedSocket(p) {
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
	if env.noProbe {
		return &path, nil
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
		fi.Mode().Perm()&0o022 != 0 || !e.ownedByUser(fi) {
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
func SessionSendRun(ctx *kit.Context, o SessionSendOptions) error {
	out := SessionSend{Schema: 1, SchemaVersion: schema.VersionOf("session-send"), Command: "session send",
		DryRun: o.DryRun, Limit: MaxSessionMessage}
	env := defaultSessionEnv()
	if o.env != nil {
		env = *o.env
	}
	fail := func(f sendFailure) error {
		out.Sent = false
		out.MsgID = nil
		out.Error, out.ErrorCode = kit.Strp(f.msg), kit.Strp(f.code)
		if o.JSON {
			if err := kit.WriteJSON(o.Out, out); err != nil {
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
	if o.ReplyTo != "" && (!strings.HasPrefix(o.ReplyTo, "uds:") || checkFromName(o.ReplyTo) != nil) {
		return fail(sendFailure{"badArgument", fmt.Sprintf("--reply-to %q is not a uds:<socket path> address", o.ReplyTo), true})
	}

	// The message is read before anything is resolved, so that a caller
	// writing it down a pipe is never left with a closed reader, and the
	// dial follows the checks of the inbox at once.
	text, size, err := readSessionMessage(o.In)
	out.Bytes = size

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
	if !env.trustedSocket(*target.Inbox) {
		return fail(sendFailure{"noInbox", "the inbox is no longer a socket of yours. Nothing was sent", false})
	}
	if err := writeToInbox(*target.Inbox, line); err != nil {
		return fail(sendFailure{"writeFailed", fmt.Sprintf("writing to %s failed: %v", *target.Inbox, err), false})
	}
	out.Sent, out.MsgID = true, &msgID
	return finishSend(o, out, target, true)
}

func finishSend(o SessionSendOptions, out SessionSend, target *SessionInfo, sent bool) error {
	if o.JSON {
		return kit.WriteJSON(o.Out, out)
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
func chooseSessionTarget(ctx *kit.Context, o SessionSendOptions, env sessionEnv) (*SessionInfo, *sendFailure) {
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
	if o.ExpectSession == "" && !namesWorktreeExactly(o.Pattern, t) {
		return nil, &sendFailure{"noSession", fmt.Sprintf("%q is not the name of a worktree but matched %s (%s): "+
			"name it exactly, or give --expect-session. Nothing was sent", o.Pattern, t.label, t.path), false}
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
			if strings.EqualFold(s.sessionID, o.ExpectSession) && (pick < 0 || s.PID != nil) {
				pick = i
			}
		}
		if pick < 0 {
			return nil, &sendFailure{"changed", fmt.Sprintf("session %s is not in %s any more. Nothing was sent", o.ExpectSession, t.label), false}
		}
	default:
		var live []int
		for i, s := range infos {
			if s.PID != nil && (s.InboxUnavailable == nil || *s.InboxUnavailable != "own") {
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
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune(`"<>&`, r) {
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
		rest, err := io.Copy(io.Discard, io.LimitReader(r, 64<<20))
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
var wrapperTags = regexp.MustCompile(`(?i)<(/?cross-session-message)`)

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
	b.WriteString(wrapperTags.ReplaceAllString(text, "&lt;$1"))
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
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	// What was dialled is what was checked, not a socket swapped in since.
	if after, err := os.Lstat(path); err != nil || !os.SameFile(before, after) {
		return errors.New("the inbox changed while connecting")
	}
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

// sessionActivity is the session's state as wt list words it, as an
// enumeration: needsInput (a process that is blocked, or waiting on a
// prompt), working, idle, done (claude's done, failed and stopped, whose raw
// word claudeState keeps) and parked (a background session with no process,
// whatever it was waiting for: a wait that began when its process ended
// says nothing about now).
func sessionActivity(s claudeSession) string {
	switch {
	case s.Tool != "":
		if s.Idle() {
			return "idle"
		}
		return "working"
	case s.State == "done" || s.State == "failed" || s.State == "stopped":
		return "done"
	case s.PID <= 1 && s.Kind == "background":
		return "parked"
	case s.needsPerson():
		return "needsInput"
	case s.Idle():
		return "idle"
	}
	return "working"
}

// selectedWithSessions is the repositories among mains that a selection
// covers, each once, in the order first seen. Walking every root to find the
// repositories (a git call each) is what a listing of every repository's
// sessions must not do; the repositories that hold a session are few, and a
// root holds a repository when it is the checkout or its parent directory.
func selectedWithSessions(u *config.User, sel kit.Selection, mains []string) ([]kit.RepoTarget, error) {
	var named *kit.RepoSet
	var roots []kit.Root
	if len(sel.Profiles) > 0 {
		set, err := kit.SelectRepos(u, sel)
		if err != nil {
			return nil, err
		}
		named = &set
	} else {
		var err error
		if roots, err = kit.ChosenRoots(u, sel); err != nil {
			return nil, err
		}
	}
	var out []kit.RepoTarget
	seen := map[string]bool{}
	for _, main := range mains {
		if main == "" || seen[main] {
			continue
		}
		seen[main] = true
		if named != nil {
			for _, t := range named.Repos {
				if repo.SamePath(t.Path, main) {
					out = append(out, t)
				}
			}
			continue
		}
		for _, r := range roots {
			if (repo.SamePath(main, r.Path) || repo.SamePath(filepath.Dir(main), r.Path)) && kit.Managed(main) {
				out = append(out, kit.RepoTarget{Name: filepath.Base(main), Path: main, Root: r.Name})
				break
			}
		}
	}
	return out, nil
}

// namesWorktreeExactly reports that pattern is the worktree's name, as
// <repo>/<work>, its path, or the one the caller stands in. A message goes
// to a session on a fuzzy match only when the session is named exactly too.
func namesWorktreeExactly(pattern string, t attachTarget) bool {
	if pattern == "." || pattern == t.label || (t.repo != "" && pattern == t.repo+"/"+t.label) {
		return true
	}
	if abs, err := filepath.Abs(pattern); err == nil && t.path != "" {
		return repo.SamePath(abs, t.path)
	}
	return false
}

// stopDeadline is how long `claude stop` has to answer.
const stopDeadline = 30 * time.Second

// SessionStop is `wt session stop --json`.
type SessionStop struct {
	Schema        int          `json:"schema"`
	SchemaVersion string       `json:"schemaVersion"`
	Command       string       `json:"command"`
	Session       *SessionInfo `json:"session"`
	DryRun        bool         `json:"dryRun"`
	Outcome       string       `json:"outcome"`
	Reason        *string      `json:"reason"`
	ReasonCode    *string      `json:"reasonCode"`
	Message       *string      `json:"message"`
}

// SessionStopOptions is one `wt session stop`.
type SessionStopOptions struct {
	// Pattern names the worktree as `wt attach` takes it.
	Pattern string
	// Session picks one of its sessions: an id, the start of the session
	// id, or part of the name.
	Session string
	// ExpectSession refuses the stop unless the session is this session id.
	ExpectSession string
	DryRun        bool
	JSON          bool
	Out, Err      io.Writer
	Sessions      *ClaudeSessions
	env           *sessionEnv
}

type stopRefusal struct {
	code   string
	msg    string
	failed bool
	usage  bool
}

// SessionStopRun is `wt session stop`. It stops one background Claude
// session by running `claude stop` on its attach id, and nothing else: a
// session open in a terminal, a Codex session and wt's own are refused, and
// a parked one has no process to stop.
func SessionStopRun(ctx *kit.Context, o SessionStopOptions) error {
	out := SessionStop{Schema: 1, SchemaVersion: schema.VersionOf("session-stop"), Command: "session stop", DryRun: o.DryRun}
	env := defaultSessionEnv()
	if o.env != nil {
		env = *o.env
	}
	env.noProbe = true
	finish := func(err error) error {
		if o.JSON {
			if werr := kit.WriteJSON(o.Out, out); werr != nil {
				return werr
			}
		}
		return err
	}
	refuse := func(r stopRefusal) error {
		out.Outcome = "refused"
		if r.failed {
			out.Outcome = "failed"
		}
		out.Reason, out.ReasonCode = kit.Strp(r.msg), kit.Strp(r.code)
		err := errors.New(r.msg)
		if r.usage {
			err = UsageError{err}
		}
		return finish(err)
	}

	pick, r := chooseStopTarget(ctx, o, env)
	if r != nil {
		if pick != nil {
			out.Session = &pick.info
		}
		return refuse(*r)
	}
	out.Session = &pick.info
	name := "the session"
	if pick.info.Name != nil {
		name = *pick.info.Name
	}
	if pick.info.PID == nil {
		out.Outcome = "already-stopped"
		out.Reason, out.ReasonCode = kit.Strp(name+" has no process: there is nothing to stop"), kit.Strp("noProcess")
		if !o.JSON {
			fmt.Fprintf(o.Out, "%s has no process: already stopped. `claude attach %s` resumes it\n", name, pick.s.ID)
		}
		return finish(nil)
	}
	// Whose session wt runs under comes from the process table; when that
	// cannot be read, an own session would pass as another's.
	if ancestors, aerr := listAncestors(); aerr != nil {
		return refuse(stopRefusal{code: "ownUnknown", msg: "wt cannot tell whether " + name +
			" is the session it runs under (the process table could not be read): " + aerr.Error() + ". Nothing was stopped"})
	} else if ancestors[*pick.info.PID] {
		return refuse(stopRefusal{code: "own", msg: name + " is the session wt is running in; wt does not stop it"})
	}
	if o.DryRun {
		out.Outcome = "would-stop"
		if !o.JSON {
			fmt.Fprintf(o.Out, "would stop %s (claude stop %s)\n", name, pick.s.ID)
		}
		return finish(nil)
	}
	msg, err := runClaudeStop(pick.s.ID)
	if msg != "" {
		out.Message = kit.Strp(msg)
	}
	if err != nil {
		reason := err.Error()
		if msg != "" {
			reason += ": " + kit.OneLine(msg)
		}
		return refuse(stopRefusal{code: "stopFailed", msg: reason, failed: true})
	}
	out.Outcome = "stopped"
	if !o.JSON {
		line := "stopped " + name
		if msg != "" {
			line += ": " + kit.OneLine(msg)
		}
		fmt.Fprintf(o.Out, "%s. Its conversation is kept; `claude attach %s` resumes it\n", line, pick.s.ID)
	}
	return finish(nil)
}

// listAncestors is a var so tests stay off the machine's process table.
var listAncestors = wtsync.Ancestors

// runClaudeStop runs `claude stop <id>` and returns what it printed.
func runClaudeStop(id string) (string, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude is not on the PATH")
	}
	cmd := exec.Command(exe, "stop", id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	timedOut, _, err := git.RunBounded(stopDeadline, cmd)
	msg := strings.TrimSpace(stdout.String())
	if e := strings.TrimSpace(stderr.String()); e != "" {
		if msg != "" {
			msg += "\n"
		}
		msg += e
	}
	switch {
	case timedOut:
		return msg, fmt.Errorf("claude stop did not answer within %s", stopDeadline)
	case err != nil:
		return msg, fmt.Errorf("claude stop failed: %v", err)
	}
	return msg, nil
}

// stopPick is the session chosen, as the JSON shows it and as claude lists it.
type stopPick struct {
	info SessionInfo
	s    claudeSession
}

// chooseStopTarget is the one session o names, if it is one wt may stop, or
// the reason it is not. It never picks among several: with none named, the
// background sessions with a process are the candidates, and a parked one
// is chosen only when it is the only background session.
func chooseStopTarget(ctx *kit.Context, o SessionStopOptions, env sessionEnv) (*stopPick, *stopRefusal) {
	t, infos, sessionsErr, err := worktreeSessions(ctx, o.Pattern, o.Err, o.Sessions, env)
	switch {
	case sessionsErr != nil:
		return nil, &stopRefusal{code: "noSession", msg: sessionsErr.Error(), failed: true}
	case err != nil:
		return nil, &stopRefusal{code: "noSession", msg: err.Error(), failed: true}
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
			fmt.Fprintf(&b, "\n  %s  %s  %s", name, id, s.Activity)
		}
		return b.String()
	}
	if o.ExpectSession == "" && !namesWorktreeExactly(o.Pattern, t) {
		return nil, &stopRefusal{code: "notExact", msg: fmt.Sprintf("%q is not the name of a worktree but matched %s (%s): "+
			"name it exactly, or give --expect-session. Nothing was stopped", o.Pattern, t.label, t.path)}
	}
	if len(infos) == 0 {
		return nil, &stopRefusal{code: "noSession", failed: true, msg: fmt.Sprintf("no Claude session in %s", t.label)}
	}
	// stoppable is a background Claude session of its own, that is not open
	// in a terminal.
	stoppable := func(i int) bool {
		s := t.sessions[i]
		return s.Tool == "" && s.Kind == "background" && attachID(s.ID) && !s.Own
	}
	var pick int
	switch {
	case o.Session != "":
		hits := matchSessions(t.sessions, o.Session)
		switch len(hits) {
		case 0:
			return nil, &stopRefusal{code: "noSession", failed: true, msg: fmt.Sprintf("no session in %s has %q as its id or in its name", t.label, o.Session)}
		case 1:
			pick = hits[0]
		default:
			return nil, &stopRefusal{code: "ambiguous", usage: true, msg: fmt.Sprintf("%d sessions in %s match %q; give one's id:%s",
				len(hits), t.label, o.Session, list(hits))}
		}
	case o.ExpectSession != "":
		pick = -1
		for i, s := range infos {
			if strings.EqualFold(s.sessionID, o.ExpectSession) && (pick < 0 || s.PID != nil) {
				pick = i
			}
		}
		if pick < 0 {
			return nil, &stopRefusal{code: "changed", msg: fmt.Sprintf("session %s is not in %s any more. Nothing was stopped", o.ExpectSession, t.label)}
		}
	default:
		var running, parked []int
		for i := range infos {
			if !stoppable(i) {
				continue
			}
			if infos[i].PID != nil {
				running = append(running, i)
			} else {
				parked = append(parked, i)
			}
		}
		switch {
		case len(running) == 1:
			pick = running[0]
		case len(running) > 1:
			return nil, &stopRefusal{code: "ambiguous", usage: true, msg: fmt.Sprintf("%d background sessions are running in %s; name one:%s",
				len(running), t.label, list(running))}
		case len(parked) == 1:
			pick = parked[0]
		case len(parked) > 1:
			return nil, &stopRefusal{code: "ambiguous", usage: true, msg: fmt.Sprintf("%d background sessions are parked in %s; name one:%s",
				len(parked), t.label, list(parked))}
		case len(infos) == 1:
			pick = 0
		default:
			all := make([]int, len(infos))
			for i := range infos {
				all[i] = i
			}
			return nil, &stopRefusal{code: "notBackground", msg: fmt.Sprintf("no session in %s is a background Claude session; "+
				"wt stops only those. Nothing was stopped:%s", t.label, list(all))}
		}
	}
	target := &stopPick{info: infos[pick], s: t.sessions[pick]}
	if o.ExpectSession != "" && !strings.EqualFold(target.info.sessionID, o.ExpectSession) {
		return target, &stopRefusal{code: "changed", msg: fmt.Sprintf("the session is %s, not the %s that was expected. Nothing was stopped",
			target.info.sessionID, o.ExpectSession)}
	}
	name := "the session"
	if target.info.Name != nil {
		name = *target.info.Name
	}
	switch {
	case target.s.Tool != "":
		return target, &stopRefusal{code: "codex", msg: name + " is a Codex session; wt stops only background Claude sessions. Nothing was stopped"}
	case target.s.Own:
		return target, &stopRefusal{code: "own", msg: name + " is the session wt is running in; wt does not stop it"}
	case target.s.Kind != "background":
		return target, &stopRefusal{code: "interactive", msg: name + " is open in a terminal; wt stops only background sessions. Nothing was stopped"}
	case !attachID(target.s.ID):
		return target, &stopRefusal{code: "noId", msg: name + " has no id `claude stop` could take. Nothing was stopped"}
	}
	return target, nil
}
