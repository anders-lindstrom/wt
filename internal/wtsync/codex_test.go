package wtsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyCodexTellsSessionsFromEverythingElse(t *testing.T) {
	const native = "/Users/a/.bun/install/global/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/"
	cases := []struct {
		command string
		role    codexRole
		exec    bool
		cd      string
	}{
		{"node /Users/a/.bun/bin/codex exec -m gpt -c model_reasoning_effort=\"high\" -s workspace-write --json", codexRunner, true, ""},
		{native + "codex exec -m gpt -s workspace-write", codexRunner, true, ""},
		{"node --no-warnings /usr/lib/node_modules/@openai/codex/bin/codex.js e fix it", codexRunner, true, ""},
		{"codex review --uncommitted", codexRunner, true, ""},
		{"codex", codexRunner, false, ""},
		{"codex fix the app-server crash", codexRunner, false, ""},
		{"codex -m gpt resume --last", codexRunner, false, ""},
		{"codex -c app-server fork", codexRunner, false, ""},
		{"codex --some-new-flag brand-new-command", codexRunner, false, ""},
		{"codex exec -C /repo/wt do it", codexRunner, true, "/repo/wt"},
		{"codex --cd=sub", codexRunner, false, "sub"},
		{"/Users/a/.codex/packages/app-server-daemon/releases/0.160.0/bin/codex app-server --listen unix:// --managed-daemon", codexHost, false, ""},
		{"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex -c features.code_mode_host=true app-server --analytics-default-enabled", codexHost, false, ""},
		{"codex mcp-server", codexHost, false, ""},
		{"/Users/a/.codex/packages/app-server-daemon/releases/0.160.0/bin/codex app-server daemon pid-update-loop", codexNone, false, ""},
		{"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex exec-server --remote https://x", codexNone, false, ""},
		{"codex login", codexNone, false, ""},
		{native + "codex-code-mode-host", codexNone, false, ""},
		{"/Users/a/.nvm/versions/node/v24/bin/node /Users/a/src/codex-control/bin/cxc _turn r-1 1", codexNone, false, ""},
		{"node /Users/a/.local/bin/cxc run implement --cwd /Users/a/src/codex", codexNone, false, ""},
		{"/Users/a/.local/bin/claude remote-control --remote-control-session-name-prefix codex", codexNone, false, ""},
		{"/Applications/ChatGPT.app/Contents/Frameworks/Codex Framework.framework/Helpers/Codex (Renderer) --type=renderer", codexNone, false, ""},
		{"/Applications/ChatGPT.app/Contents/Frameworks/Sparkle.framework/Versions/B/Autoupdate com.openai.codex /Users/a a", codexNone, false, ""},
		{"/bin/zsh -c export CODEX_COMPANION_SESSION_ID=x && codex exec y", codexNone, false, ""},
		{"vim codex", codexNone, false, ""},
	}
	for _, c := range cases {
		role, exec, cd := classifyCodex(strings.Fields(c.command), false)
		if role != c.role || exec != c.exec || cd != c.cd {
			t.Errorf("%q: role %d exec %v cd %q, want %d %v %q", c.command, role, exec, cd, c.role, c.exec, c.cd)
		}
	}
}

// With the arguments as given, a prompt is one argument whatever it says,
// and a path keeps its spaces.
func TestClassifyCodexReadsExactArguments(t *testing.T) {
	cases := []struct {
		argv []string
		role codexRole
		exec bool
		cd   string
	}{
		{[]string{"codex", "update the README"}, codexRunner, false, ""},
		{[]string{"codex", "a quick question"}, codexRunner, false, ""},
		{[]string{"codex", "app-server crash fix"}, codexRunner, false, ""},
		{[]string{"codex", "exec", "look at git -C /other/repo log and fix"}, codexRunner, true, ""},
		{[]string{"codex", "exec", "--cd", "/repos/my worktree", "do it"}, codexRunner, true, "/repos/my worktree"},
		{[]string{"codex", "--", "login"}, codexRunner, false, ""},
		{[]string{"/Users/a b/bin/codex", "-c", "x = y", "app-server"}, codexHost, false, ""},
		{[]string{"/Users/a b/bin/codex", "app-server", "daemon", "pid-update-loop"}, codexNone, false, ""},
		{[]string{"node", "--max-old-space-size", "4096", "/Users/a b/bin/codex", "exec", "x"}, codexRunner, true, ""},
		{[]string{"/opt/codex-x86_64-unknown-linux-musl", "exec", "x"}, codexRunner, true, ""},
		{[]string{"codex", "update"}, codexNone, false, ""},
		{[]string{"codex", "login", "status"}, codexNone, false, ""},
		{[]string{"node", "/Users/a/src/tool.js", "/Users/a/bin/codex"}, codexNone, false, ""},
	}
	for _, c := range cases {
		role, exec, cd := classifyCodex(c.argv, true)
		if role != c.role || exec != c.exec || cd != c.cd {
			t.Errorf("%q: role %d exec %v cd %q, want %d %v %q", c.argv, role, exec, cd, c.role, c.exec, c.cd)
		}
	}
}

// Split on spaces a prompt may start with a subcommand's name: more words
// after one is a session, not nothing.
func TestClassifyCodexCountsAnInexactPromptAsASession(t *testing.T) {
	for _, command := range []string{"codex update the README", "codex debug the failing test", "codex app-server crash fix"} {
		if role, _, _ := classifyCodex(strings.Fields(command), false); role != codexRunner {
			t.Errorf("%q: role %d, want a session", command, role)
		}
	}
}

// codexWorld is a machine for codexSessions: a process table, the working
// directories lsof would report, and a CODEX_HOME to write rollouts into.
type codexWorld struct {
	t     *testing.T
	home  string
	now   time.Time
	procs []Process
	cwds  map[int]string
	// denied are the processes that live and will not say where.
	denied map[int]bool
}

func newCodexWorld(t *testing.T) *codexWorld {
	w := &codexWorld{t: t, home: t.TempDir(), now: time.Now(), cwds: map[int]string{}, denied: map[int]bool{}}
	t.Setenv("CODEX_HOME", w.home)
	old := processCwds
	processCwds = func(pids []int) (map[int]string, map[int]bool, error) {
		out, denied := map[int]string{}, map[int]bool{}
		for _, pid := range pids {
			if dir, ok := w.cwds[pid]; ok {
				out[pid] = dir
			}
			if w.denied[pid] {
				denied[pid] = true
			}
		}
		return out, denied, nil
	}
	t.Cleanup(func() { processCwds = old })
	return w
}

// proc adds one of the user's processes that started ago before now. An
// empty cwd is a process nobody may ask the directory of.
func (w *codexWorld) proc(pid, ppid int, ago time.Duration, cwd, command string) {
	w.procs = append(w.procs, Process{PID: pid, PPID: ppid, UID: os.Getuid(), Started: w.now.Add(-ago), Argv: strings.Fields(command)})
	if cwd != "" {
		w.cwds[pid] = cwd
	}
}

// rollout writes a thread's log, last written ago before now. source is
// session_meta's source as JSON; events are event_msg payload types.
func (w *codexWorld) rollout(id, cwd, originator, source string, ago time.Duration, events ...string) string {
	w.t.Helper()
	lines := []string{fmt.Sprintf(`{"timestamp":"t","type":"session_meta","payload":{"id":%q,"cwd":%q,"originator":%q,"source":%s}}`,
		id, cwd, originator, source)}
	for _, e := range events {
		lines = append(lines, fmt.Sprintf(`{"timestamp":"t","type":"event_msg","payload":{"type":%q,"turn_id":"x"}}`, e),
			`{"timestamp":"t","type":"response_item","payload":{"type":"message","text":"said \"task_complete\" and \"type\":\"task_started\""}}`)
	}
	return w.file(id, ago, strings.Join(lines, "\n")+"\n")
}

func (w *codexWorld) file(id string, ago time.Duration, content string) string {
	w.t.Helper()
	dir := filepath.Join(w.home, "sessions", "2026", "10", "02")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-10-02T12-00-00-"+id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	at := w.now.Add(-ago)
	if err := os.Chtimes(path, at, at); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *codexWorld) sessions(ancestors ...int) []Agent {
	w.t.Helper()
	above := map[int]bool{}
	for _, pid := range ancestors {
		above[pid] = true
	}
	got, err := codexSessions(w.procs, above)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// one is the single session expected, by its state.
func (w *codexWorld) one(wantIdle bool) Agent {
	w.t.Helper()
	got := w.sessions()
	if len(got) != 1 {
		w.t.Fatalf("sessions %+v, want one", got)
	}
	if got[0].Idle() != wantIdle || got[0].Program() != ToolCodex {
		w.t.Fatalf("session %+v: idle %v, want %v, and a codex one", got[0], got[0].Idle(), wantIdle)
	}
	return got[0]
}

const minute = time.Minute

// codex exec is a node launcher, the binary it starts and a helper under
// that: one session, busy whatever its log says.
func TestCodexExecIsOneBusySession(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "node /Users/a/.bun/bin/codex exec -m gpt do it")
	w.proc(101, 100, 10*minute, wt, "/pkg/codex-darwin-arm64/bin/codex exec -m gpt do it")
	w.proc(102, 101, 9*minute, wt, "/pkg/codex-darwin-arm64/bin/codex-code-mode-host")
	w.rollout("t1", wt, "codex_exec", `"exec"`, minute, "task_started", "task_complete")
	a := w.one(false)
	if a.ID != "codex-100" || a.PID != 100 || a.Name != "codex exec" || a.Cwd != resolved(wt) {
		t.Fatalf("session %+v", a)
	}
}

func TestInteractiveCodexIsIdleOnlyWhenItsLogSaysTheTurnIsOver(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		idle   bool
	}{
		{"nothing asked yet", nil, true},
		{"turn finished", []string{"task_started", "task_complete"}, true},
		{"turn interrupted", []string{"task_started", "turn_aborted"}, true},
		{"turn in flight", []string{"task_started", "task_complete", "task_started"}, false},
		{"markers of another version", []string{"turn_begun", "turn_ended"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newCodexWorld(t)
			wt := t.TempDir()
			w.proc(100, 1, 10*minute, wt, "codex")
			w.rollout("t1", wt, "codex-tui", `"cli"`, minute, c.events...)
			if a := w.one(c.idle); a.Name != "codex" || a.ID != "codex-100" {
				t.Fatalf("session %+v", a)
			}
		})
	}
}

// Not finding the log is not knowing: a codex with another CODEX_HOME, a
// resumed thread nothing has been written to yet, a log that cannot be read.
func TestInteractiveCodexWithNoReadableLogIsBusy(t *testing.T) {
	wt := t.TempDir()
	t.Run("no log", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(100, 1, 10*minute, wt, "codex")
		w.one(false)
	})
	t.Run("log from before it started", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(100, 1, 10*minute, wt, "codex resume --last")
		w.rollout("t1", wt, "codex-tui", `"cli"`, time.Hour, "task_started", "task_complete")
		w.one(false)
	})
	t.Run("first line is not session_meta", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(100, 1, 10*minute, wt, "codex")
		w.file("t1", minute, "not json at all\n")
		w.one(false)
	})
	t.Run("unreadable", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root reads everything")
		}
		w := newCodexWorld(t)
		w.proc(100, 1, 10*minute, wt, "codex")
		path := w.rollout("t1", wt, "codex-tui", `"cli"`, minute, "task_started", "task_complete")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		w.one(false)
	})
}

// A run killed mid-turn leaves its turn open for ever. It is nobody once its
// process is gone, and it does not make the codex that came later busy.
func TestCrashedCodexTurnIsNobody(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.rollout("dead", wt, "codex_exec", `"exec"`, time.Hour, "task_started")
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none: nothing is alive", got)
	}
	w.proc(100, 1, 10*minute, wt, "codex")
	w.rollout("mine", wt, "codex-tui", `"cli"`, minute, "task_started", "task_complete")
	w.one(true)
}

// The desktop app's app-server stands in / and the daemon wherever it was
// started; their threads say where they work.
func TestAppServerThreadIsASessionWhileItsTurnIsOpen(t *testing.T) {
	w := newCodexWorld(t)
	wt, elsewhere := t.TempDir(), t.TempDir()
	w.proc(200, 1, 5*time.Hour, "", "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex -c x=y app-server")
	w.rollout("open", wt, "Codex Desktop", `"vscode"`, minute, "task_started")
	w.rollout("done", elsewhere, "Codex Desktop", `"vscode"`, minute, "task_started", "task_complete")
	w.rollout("fresh", elsewhere, "Codex Desktop", `"vscode"`, minute)
	a := w.one(false)
	if a.ID != "open" || a.Name != "codex (Codex Desktop)" || a.PID != 200 || a.Cwd != resolved(wt) {
		t.Fatalf("session %+v", a)
	}

	// A second app-server that could be running it: no one pid to name.
	w.proc(201, 1, 6*time.Hour, "", "codex app-server --listen unix:// --managed-daemon")
	if a := w.one(false); a.PID != 0 {
		t.Fatalf("session %+v, want no pid with two app-servers", a)
	}
}

func TestAppServerThreadFromBeforeTheAppServerStartedIsNobody(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(200, 1, 10*minute, "", "codex app-server --listen unix:// --managed-daemon")
	w.rollout("stale", t.TempDir(), "Codex Desktop", `"vscode"`, time.Hour, "task_started")
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none: the app-server that ran it is gone", got)
	}
}

// The daemon's own working directory is wherever it happened to be started
// and pins nothing; it is not even asked for.
func TestAppServerDoesNotPinItsOwnDirectory(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(200, 1, time.Hour, "", "codex app-server --listen unix:// --managed-daemon")
	w.proc(201, 1, time.Hour, "", "codex app-server daemon pid-update-loop")
	processCwds = func(pids []int) (map[int]string, map[int]bool, error) {
		if len(pids) > 0 {
			t.Errorf("asked for the directories of %v", pids)
		}
		return nil, nil, nil
	}
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none", got)
	}
}

// A subagent is its parent's session: one session, named for the root.
func TestSubagentThreadsAreTheirRootsSession(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(200, 1, time.Hour, "", "codex app-server")
	w.rollout("root", wt, "Claude Code", `"vscode"`, 2*minute, "task_started")
	w.rollout("kid", wt, "Claude Code", `{"subagent":{"thread_spawn":{"parent_thread_id":"root","depth":1}}}`, minute, "task_started")
	w.rollout("grandkid", wt, "Claude Code", `{"subagent":{"thread_spawn":{"parent_thread_id":"kid"}}}`, minute, "task_started")
	if a := w.one(false); a.ID != "root" || a.Name != "codex (Claude Code)" {
		t.Fatalf("session %+v", a)
	}
}

// A subagent of a codex exec that died is as dead as its parent, even with
// an app-server alive and the parent's log out of reach.
func TestOrphanedSubagentOfACodexProcessIsNobody(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(200, 1, time.Hour, "", "codex app-server")
	w.rollout("kid", t.TempDir(), "codex_exec", `{"subagent":{"thread_spawn":{"parent_thread_id":"gone"}}}`, minute, "task_started")
	w.rollout("review", t.TempDir(), "codex_exec", `{"subagent":"review"}`, minute, "task_started")
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none", got)
	}
}

func TestCodexCdNamesTheDirectory(t *testing.T) {
	w := newCodexWorld(t)
	base := t.TempDir()
	wt := filepath.Join(base, "wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	w.proc(100, 1, minute, base, "codex exec --cd wt do it")
	if a := w.one(false); a.Cwd != resolved(wt) {
		t.Fatalf("cwd %q, want %q", a.Cwd, resolved(wt))
	}
}

// A codex started from another codex's shell in another worktree is a
// session of its own.
func TestCodexStartedByCodexElsewhereIsItsOwnSession(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(100, 1, 10*minute, t.TempDir(), "codex exec outer")
	w.proc(101, 100, minute, t.TempDir(), "codex exec inner")
	if got := w.sessions(); len(got) != 2 {
		t.Fatalf("sessions %+v, want two", got)
	}
}

func TestCodexOfAnotherUserIsNotLookedAt(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(100, 1, minute, t.TempDir(), "codex exec x")
	w.procs[0].UID = os.Getuid() + 1
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none", got)
	}
}

// wt run by Codex's own shell tool must not be refused by that Codex.
func TestCodexRunningWtIsNotCounted(t *testing.T) {
	w := newCodexWorld(t)
	mine, other := t.TempDir(), t.TempDir()
	w.proc(100, 1, 10*minute, mine, "node /bin/codex exec x")
	w.proc(101, 100, 10*minute, mine, "/pkg/bin/codex exec x")
	w.proc(110, 1, 10*minute, other, "codex exec y")
	got := w.sessions(101, 100)
	if len(got) != 1 || got[0].PID != 110 {
		t.Fatalf("sessions %+v, want only the other codex", got)
	}
}

// Under an app-server the calling thread is not named anywhere, so it is the
// one working where wt was started; a thread in another worktree still counts.
func TestAppServerThreadRunningWtIsNotCounted(t *testing.T) {
	w := newCodexWorld(t)
	mine, other := t.TempDir(), t.TempDir()
	t.Chdir(mine)
	w.proc(200, 1, time.Hour, "", "codex app-server")
	w.proc(300, 1, 10*minute, mine, "codex")
	w.proc(301, 1, 10*minute, mine, "codex exec x")
	w.rollout("mine", mine, "codex-tui", `"vscode"`, minute, "task_started")
	w.rollout("other", other, "Codex Desktop", `"vscode"`, minute, "task_started")
	var names []string
	for _, a := range w.sessions(200) {
		names = append(names, a.ID)
	}
	if strings.Join(names, " ") != "codex-301 other" {
		t.Fatalf("sessions %v, want the codex exec here and the thread elsewhere", names)
	}
	if got := w.sessions(); len(got) != 4 {
		t.Fatalf("sessions %+v, want both codex processes and both threads when wt is not under the app-server", got)
	}
}

func TestCodexWhoseDirectoryCannotBeReadFailsTheListing(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(100, 1, minute, t.TempDir(), "codex")
	processCwds = func([]int) (map[int]string, map[int]bool, error) {
		return nil, nil, errors.New("cannot tell whether process 100 is still running")
	}
	if _, err := codexSessions(w.procs, nil); err == nil {
		t.Fatal("no error: a codex nobody can place is not nobody")
	}
}

// A codex that exited between ps and lsof has no directory and is nobody.
func TestCodexThatExitedMeanwhileIsDropped(t *testing.T) {
	w := newCodexWorld(t)
	w.proc(100, 1, minute, "", "codex exec x")
	if got := w.sessions(); len(got) != 0 {
		t.Fatalf("sessions %+v, want none", got)
	}
}

// The turn is found from the end of the file, through lines far longer than
// one read, and words a session wrote about are not markers.
func TestLastTurnReadsBackwardsThroughLongLines(t *testing.T) {
	big := `{"type":"response_item","payload":{"text":"` + strings.Repeat(`x \"type\":\"task_complete\" `, 40000) + `"}}`
	started := `{"timestamp":"t","type":"event_msg","payload":{"type":"task_started","turn_id":"x"}}`
	done := `{"timestamp":"t","type":"event_msg","payload":{"type":"task_complete"}}`
	meta := `{"timestamp":"t","type":"session_meta","payload":{"id":"x"}}`
	cases := []struct {
		name  string
		lines []string
		want  turnState
	}{
		{"open behind megabytes", []string{meta, done, started, big, big, big}, turnOpen},
		{"done at the end", []string{meta, started, big, done}, turnDone},
		{"no newline at the end", []string{meta, started}, turnOpen},
		{"meta only", []string{meta, meta}, turnNone},
		{"content and no marker", []string{meta, big}, turnUnknown},
		{"empty", nil, turnNone},
	}
	for _, c := range cases {
		content := strings.Join(c.lines, "\n")
		if c.name != "no newline at the end" && len(c.lines) > 0 {
			content += "\n"
		}
		got, err := lastTurn(strings.NewReader(content), int64(len(content)))
		if err != nil || got != c.want {
			t.Errorf("%s: turn %d err %v, want %d", c.name, got, err, c.want)
		}
	}
}

func TestParseProcessesReadsPsRows(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	out := "  100     1   501       01:02 codex exec do it\n" +
		"  101   100   501 1-02:03:04 /bin/codex\n" +
		"  102   100   501    2:03:04 -zsh\n" +
		"garbage\n  103 x 501 00:01 y\n"
	procs := parseProcesses(out, now)
	if len(procs) != 3 {
		t.Fatalf("processes %+v, want three", procs)
	}
	ages := []time.Duration{62 * time.Second, 26*time.Hour + 3*time.Minute + 4*time.Second, 2*time.Hour + 3*time.Minute + 4*time.Second}
	for i, p := range procs {
		if got := now.Sub(p.Started); got != ages[i] || p.UID != 501 {
			t.Errorf("process %+v: age %s, want %s", p, got, ages[i])
		}
	}
	if strings.Join(procs[0].Argv, " ") != "codex exec do it" || procs[1].PPID != 100 {
		t.Errorf("processes %+v", procs)
	}
}

// The real lookup, on this process alone: lsof or /proc.
func TestProcessCwdsReadsALiveProcessAndSkipsADeadOne(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const nobody = 2147483646
	cwds, denied, err := processCwds([]int{os.Getpid(), nobody})
	if err != nil || len(denied) != 0 {
		t.Fatalf("err %v denied %v", err, denied)
	}
	if resolved(cwds[os.Getpid()]) != resolved(wd) {
		t.Errorf("cwd %q, want %q", cwds[os.Getpid()], wd)
	}
	if _, ok := cwds[nobody]; ok {
		t.Errorf("cwds %v name a process that does not exist", cwds)
	}
}

func TestListProcessesSeesThisProcess(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs {
		if p.PID == os.Getpid() {
			if p.UID != os.Getuid() || p.PPID != os.Getppid() || time.Since(p.Started) > time.Hour {
				t.Errorf("this process read as %+v", p)
			}
			return
		}
	}
	t.Fatal("ps did not list this process")
}

// ListOtherAgents is Claude's sessions and Codex's; a process table that
// cannot be read is not knowing, with or without a claude to ask.
func TestListOtherAgentsAddsCodexAndFailsWithoutAProcessTable(t *testing.T) {
	w := newCodexWorld(t)
	wt := resolved(t.TempDir())
	w.proc(100, 1, minute, wt, "codex exec x")
	stub := t.TempDir()
	script := "#!/bin/sh\necho '[{\"id\":\"c1\",\"name\":\"claude-1\",\"cwd\":\"" + wt + "\",\"kind\":\"interactive\",\"status\":\"idle\",\"pid\":999999}]'\n"
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub)
	old := listProcesses
	t.Cleanup(func() { listProcesses = old })

	listProcesses = func() ([]Process, error) { return w.procs, nil }
	agents, err := ListOtherAgents()
	if err != nil {
		t.Fatal(err)
	}
	s := SessionsAt(agents, wt)
	if len(s) != 2 || len(s.Busy()) != 1 || s.Lead().Program() != ToolCodex || s.Label(func(a *Agent) string { return a.Name }) != "codex exec +1" {
		t.Fatalf("sessions %+v, want claude-1 idle and codex exec busy and leading", s)
	}

	listProcesses = func() ([]Process, error) { return nil, errors.New("ps: boom") }
	if _, err := ListOtherAgents(); err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("err %v, want the failed look for Codex sessions", err)
	}
}

// With an app-server alive, a log that cannot be read may be a busy thread
// in any worktree: the listing fails rather than leave it out.
func TestUnreadableLogUnderAnAppServerFailsTheListing(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	for _, torn := range []bool{false, true} {
		w := newCodexWorld(t)
		w.proc(200, 1, time.Hour, "", "codex app-server")
		path := w.rollout("t1", t.TempDir(), "Codex Desktop", `"vscode"`, minute, "task_started")
		if torn {
			w.file("t1", minute, "{\"timestamp\":\"t\",\"type\":\"session_me")
		} else if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := codexSessions(w.procs, nil); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("torn %v: err %v, want the log named", torn, err)
		}
	}
}

// A record cut short at the end of the log is one being written, and may be
// the start of a turn: the completion before it does not make the codex idle.
func TestHalfWrittenLastRecordIsNotIdle(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "codex")
	path := w.rollout("t1", wt, "codex-tui", `"cli"`, minute, "task_started", "task_complete")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"timestamp":"t","type":"event_msg","payload":{"type":"task_sta`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w.one(false)
}

// A codex exec killed mid-turn after an interactive codex started in the
// same directory is still nobody, and does not make that codex busy.
func TestCrashedExecDoesNotMakeAnInteractiveCodexBusy(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "codex")
	w.rollout("mine", wt, "codex-tui", `"cli"`, minute, "task_started", "task_complete")
	w.rollout("dead", wt, "codex_exec", `"exec"`, 2*minute, "task_started")
	w.rollout("deadkid", wt, "codex_exec", `{"subagent":{"thread_spawn":{"parent_thread_id":"dead"}}}`, 2*minute, "task_started")
	w.one(true)
}

// A finished codex exec in the directory is not the interactive codex's own
// thread: with its own log out of reach it stays busy.
func TestAnotherSessionsFinishedThreadDoesNotMakeACodexIdle(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "codex")
	w.rollout("exec", wt, "codex_exec", `"exec"`, minute, "task_started", "task_complete")
	w.one(false)
}

// An interactive codex may resume a thread recorded in another directory:
// an open turn there, with no codex of its own, keeps this one busy.
func TestOpenThreadElsewhereKeepsAnInteractiveCodexBusy(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "codex")
	w.rollout("first", wt, "codex-tui", `"cli"`, 5*minute, "task_started", "task_complete")
	w.rollout("resumed", t.TempDir(), "codex-tui", `"cli"`, minute, "task_started")
	w.one(false)
}

// wt run by a codex exec must still see the app-server thread busy in the
// same directory.
func TestCallingCodexDoesNotHideAnAppServerThreadBesideIt(t *testing.T) {
	w := newCodexWorld(t)
	wt := t.TempDir()
	w.proc(100, 1, 10*minute, wt, "codex exec x")
	w.proc(200, 1, time.Hour, "", "codex app-server")
	w.rollout("desktop", wt, "Codex Desktop", `"vscode"`, minute, "task_started")
	got := w.sessions(100)
	if len(got) != 1 || got[0].ID != "desktop" {
		t.Fatalf("sessions %+v, want the desktop thread", got)
	}
}

// A subagent working in another worktree than its root holds that one too.
func TestSubagentElsewhereIsASessionThere(t *testing.T) {
	w := newCodexWorld(t)
	a, b := t.TempDir(), t.TempDir()
	w.proc(200, 1, time.Hour, "", "codex app-server")
	w.rollout("root", a, "Codex Desktop", `"vscode"`, minute, "task_started")
	w.rollout("kid", b, "Codex Desktop", `{"subagent":{"thread_spawn":{"parent_thread_id":"root"}}}`, minute, "task_started")
	got := w.sessions()
	if len(got) != 2 || len(SessionsAt(got, a)) != 1 || len(SessionsAt(got, b)) != 1 {
		t.Fatalf("sessions %+v, want one in each worktree", got)
	}
}

// A codex that will not say where it stands is placed by its launcher, by
// its helper, by --cd, or by the threads it could be running; with none of
// those the listing fails.
func TestCodexThatHidesItsDirectoryIsStillPlaced(t *testing.T) {
	wt := t.TempDir()
	t.Run("launcher", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(100, 1, minute, wt, "node /bin/codex exec x")
		w.proc(101, 100, minute, "", "/pkg/bin/codex exec x")
		w.denied[101] = true
		if a := w.one(false); a.Cwd != resolved(wt) || a.PID != 100 {
			t.Fatalf("session %+v", a)
		}
	})
	t.Run("helper", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(101, 1, minute, "", "/pkg/bin/codex exec x")
		w.proc(102, 101, minute, wt, "/pkg/bin/codex-code-mode-host")
		w.denied[101] = true
		if a := w.one(false); a.Cwd != resolved(wt) {
			t.Fatalf("session %+v", a)
		}
	})
	t.Run("absolute --cd", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(101, 1, minute, "", "codex exec --cd "+wt+" x")
		w.denied[101] = true
		if a := w.one(false); a.Cwd != resolved(wt) {
			t.Fatalf("session %+v", a)
		}
	})
	t.Run("its threads", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(101, 1, 10*minute, "", "codex")
		w.denied[101] = true
		w.rollout("mine", wt, "codex-tui", `"cli"`, minute, "task_started", "task_complete")
		w.rollout("exec", t.TempDir(), "codex_exec", `"exec"`, minute, "task_started")
		if a := w.one(false); a.Cwd != resolved(wt) {
			t.Fatalf("session %+v, want it busy where its thread is", a)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		w := newCodexWorld(t)
		w.proc(101, 1, minute, "", "codex")
		w.denied[101] = true
		if _, err := codexSessions(w.procs, nil); err == nil {
			t.Fatal("no error: a codex nobody can place is not nobody")
		}
	})
}

func TestProcessArgvReadsThisProcessExactly(t *testing.T) {
	argv, ok := processArgv(os.Getpid())
	if !ok {
		t.Skip("no exact arguments on this platform")
	}
	if strings.Join(argv, "\x00") != strings.Join(os.Args, "\x00") {
		t.Errorf("argv %q, want %q", argv, os.Args)
	}
}
