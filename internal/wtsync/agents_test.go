package wtsync

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// An interrupt takes down a claude agents still running: it is in the
// registry git.KillRunning kills, which is what wt's signal handler calls.
func TestKillRunningTakesDownARunningClaudeAgents(t *testing.T) {
	stub := t.TempDir()
	started := filepath.Join(stub, "started")
	claude := filepath.Join(stub, "claude")
	script := "#!/bin/sh\n[ \"$1\" = warm ] && exit 0\ntouch " + started + "\nexec sleep 30\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(claude, "warm").Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := agentsDeadline
	agentsDeadline = time.Minute
	t.Cleanup(func() { agentsDeadline = old })
	done := make(chan error, 1)
	go func() {
		_, err := ListAgents()
		done <- err
	}()
	waitForFile(t, started)
	if n := git.KillRunning(); n < 1 {
		t.Fatal("KillRunning signalled nothing: claude agents was not registered")
	}
	select {
	case err := <-done:
		if err == nil || strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("err %v, want the killed claude's failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("claude agents outlived the kill")
	}
}

// A claude that never answers must not hold a run hostage. The stub's sleep
// is a child of sh that holds the output pipe, so the deadline has to take
// down the whole process group, not just sh.
func TestListAgentsGivesUpOnAClaudeThatDoesNotAnswer(t *testing.T) {
	stub := t.TempDir()
	pidFile := filepath.Join(stub, "sleep.pid")
	script := "#!/bin/sh\n[ \"$1\" = warm ] && exit 0\nsleep 30 &\necho $! > " + pidFile + "\nwait\necho '[]'\n"
	claude := filepath.Join(stub, "claude")
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The first exec of a new file can be slow (macOS vets it), and a stub
	// killed before it started proves nothing about the process group.
	if err := exec.Command(claude, "warm").Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := agentsDeadline
	agentsDeadline = 500 * time.Millisecond
	t.Cleanup(func() { agentsDeadline = old })
	start := time.Now()
	_, err := ListAgents()
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s; the deadline did not hold", took)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the stub never started its sleep, so the group kill is unproven: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	for gone := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(gone) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("sleep %d outlived the deadline: only sh was killed", pid)
		}
	}
}

const agentsJSON = `[
 {"id":"a1","pid":11,"cwd":"/repo_wt/feat_wt/one","kind":"background","name":"fix it","state":"blocked","status":"idle"},
 {"id":"a2","pid":12,"cwd":"/repo_wt/feat_wt/two","kind":"interactive","name":"two-3a","status":"idle"},
 {"id":"a3","pid":13,"cwd":"/repo_wt/feat_wt/three","kind":"background","name":"finished","state":"done","status":"idle"},
 {"id":"a4","pid":14,"cwd":"/repo_wt/feat_wt/two/sub/dir","kind":"interactive","name":"deep","status":"busy"}
]`

func TestParseAgentsDropsFinishedSessions(t *testing.T) {
	agents, err := ParseAgents([]byte(agentsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("agents = %+v", agents)
	}
	for _, a := range agents {
		if a.State == "done" {
			t.Errorf("finished session kept: %+v", a)
		}
	}
}

func TestAgentAtMatchesTheWorktreeOrADirectoryInsideIt(t *testing.T) {
	agents, _ := ParseAgents([]byte(agentsJSON))
	if a := AgentAt(agents, "/repo_wt/feat_wt/one"); a == nil || a.Name != "fix it" {
		t.Errorf("one = %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/two"); a == nil || a.Name != "two-3a" {
		t.Errorf("two = %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/three"); a != nil {
		t.Errorf("three should be empty, got %+v", a)
	}
	if a := AgentAt(agents, "/repo_wt/feat_wt/tw"); a != nil {
		t.Errorf("prefix of a path is not inside it, got %+v", a)
	}
}

func TestParseAgentsRejectsGarbage(t *testing.T) {
	if _, err := ParseAgents([]byte("not json")); err == nil {
		t.Error("expected an error")
	}
}

func TestAgentAtPrefersFewerSegmentsNotShorterStrings(t *testing.T) {
	agents := []Agent{
		{Name: "shallow", Cwd: "/wt/aaaaaaaaaaaa", State: "blocked", Kind: "interactive"},
		{Name: "deep", Cwd: "/wt/a/b", State: "blocked", Kind: "interactive"},
	}
	a := AgentAt(agents, "/wt")
	if a == nil || a.Name != "shallow" {
		t.Errorf("expected shallow (1 segment), got %+v", a)
	}
}

func TestIdleIsAnInteractiveSessionWithAnIdleStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Agent
		want bool
	}{
		{"interactive idle", Agent{Kind: "interactive", Status: "idle"}, true},
		{"interactive busy", Agent{Kind: "interactive", Status: "busy"}, false},
		{"background working", Agent{Kind: "background", State: "working", Status: "busy"}, false},
		{"background blocked on a question", Agent{Kind: "background", State: "blocked", Status: "idle"}, false},
		{"background with no state", Agent{Kind: "background", Status: "idle"}, false},
		{"interactive with a state nobody has seen", Agent{Kind: "interactive", State: "blocked", Status: "idle"}, false},
		{"no status from an older claude", Agent{Kind: "interactive"}, false},
		{"a status nobody has seen", Agent{Kind: "interactive", Status: "starting"}, false},
	} {
		if got := tc.a.Idle(); got != tc.want {
			t.Errorf("%s: Idle() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSessionsAtListsEverySessionInTheWorktreeShallowestFirst(t *testing.T) {
	agents, err := ParseAgents([]byte(agentsJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := SessionsAt(agents, "/repo_wt/feat_wt/two")
	if len(s) != 2 || s[0].Name != "two-3a" || s[1].Name != "deep" {
		t.Fatalf("sessions = %+v", s)
	}
	if busy := s.Busy(); len(busy) != 1 || busy[0].Name != "deep" {
		t.Errorf("busy = %+v", busy)
	}
	if got := s.Label(agentLabel); got != "deep +1" {
		t.Errorf("a busy session leads the label: got %q", got)
	}
	parked := Sessions{{Name: "old-1", Kind: "interactive", Status: "idle"}, {Name: "new-2", Kind: "interactive", Status: "idle"}}
	if got := parked.Label(agentLabel); got != "old-1 (idle) +1" {
		t.Errorf("idle label = %q", got)
	}
	if got := (Sessions{}).Label(agentLabel); got != "" || (Sessions{}).Lead() != nil {
		t.Errorf("empty label = %q", got)
	}
	if SessionsAt(agents, "/repo_wt/feat_wt/three") != nil {
		t.Error("a done session is nobody")
	}
}

func TestSessionsAtResolvesASymlinkedWorktreePath(t *testing.T) {
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	if s := SessionsAt([]Agent{{Name: "in-1", Cwd: resolved}}, link); len(s) != 1 {
		t.Fatalf("sessions = %+v", s)
	}
}

func TestArrivedIsWhatNobodyWasToldAbout(t *testing.T) {
	told := Sessions{{ID: "a1", Name: "parked-1", Cwd: "/w"}, {Name: "no-id", Cwd: "/w"}}
	now := Sessions{{ID: "a1", Name: "renamed", Cwd: "/w"}, {Name: "no-id", Cwd: "/w"}, {ID: "a9", Name: "new-9", Cwd: "/w"}}
	if got := now.Arrived(told); len(got) != 1 || got[0].Name != "new-9" {
		t.Errorf("arrived = %+v", got)
	}
}

func TestWithoutCallerDropsTheSessionWtRunsUnder(t *testing.T) {
	agents := []Agent{{Name: "me", PID: 100}, {Name: "other", PID: 200}, {Name: "no-pid"}}
	got := WithoutCaller(agents, map[int]bool{100: true})
	if len(got) != 2 || got[0].Name != "other" || got[1].Name != "no-pid" {
		t.Fatalf("got %+v", got)
	}
}

func TestAncestorsIncludeTheParentButNotThisProcess(t *testing.T) {
	a, err := Ancestors()
	if err != nil {
		t.Fatal(err)
	}
	if !a[os.Getppid()] || a[os.Getpid()] {
		t.Fatalf("ancestors %v; parent %d, self %d", a, os.Getppid(), os.Getpid())
	}
}

// The listing a person reads keeps what the acting one drops: a finished
// session is still one to attach to. A conversation resumed in a terminal
// is listed twice under one session id, and the terminal's row is the one
// that says where it is.
func TestParseClaudeSessionsKeepsFinishedAndFoldsAResumedOne(t *testing.T) {
	got, err := ParseClaudeSessions([]byte(`[
		{"id":"3f9a1c20","sessionId":"3f9a1c20-aaaa","name":"finished","kind":"background","state":"done","cwd":"/w/a","startedAt":1791540505653},
		{"id":"7c2e5b11","sessionId":"7c2e5b11-bbbb","name":"resumed","kind":"background","state":"blocked","cwd":"/w/b"},
		{"pid":4242,"sessionId":"7c2e5b11-bbbb","name":"resumed","kind":"interactive","status":"idle","cwd":"/w/b"},
		{"pid":4243,"name":"unsaved","kind":"interactive","status":"busy","cwd":"/w/c"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want three sessions, got %+v", got)
	}
	if got[0].Name != "finished" || got[0].StartedAt != 1791540505653 || !got[0].Attachable() {
		t.Errorf("want the finished background session kept, attachable: %+v", got[0])
	}
	if got[1].Kind != "interactive" || got[1].PID != 4242 || got[1].Attachable() {
		t.Errorf("want the resumed conversation as its terminal's row: %+v", got[1])
	}
	if got[1].ID != "7c2e5b11" {
		t.Errorf("want the terminal's row to keep the attach id that names it, got %q", got[1].ID)
	}
	if got[2].Name != "unsaved" {
		t.Errorf("want a session with no session id left alone: %+v", got[2])
	}
}

func TestOnlyABackgroundClaudeSessionIsAttachable(t *testing.T) {
	if a := (Agent{ID: "3f9a1c20", Kind: "background"}); !a.Attachable() {
		t.Errorf("%+v can be attached", a)
	}
	for _, a := range []Agent{
		{Kind: "interactive", PID: 7},
		{ID: "3f9a1c20", Kind: "interactive", PID: 7},
		{Kind: "background"},
		{ID: "codex-7", Kind: "background", Tool: ToolCodex},
	} {
		if a.Attachable() {
			t.Errorf("%+v cannot be attached", a)
		}
	}
}

// A listing gives claude its own, shorter wait, and tells no claude at all
// from a claude that did not answer.
func TestListClaudeSessionsWaitsOnlyAsLongAsItIsTold(t *testing.T) {
	stub := t.TempDir()
	claude := filepath.Join(stub, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\n[ \"$1\" = warm ] && exit 0\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(claude, "warm").Run(); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", stub+string(os.PathListSeparator)+path)
	start := time.Now()
	_, found, err := ListClaudeSessions(300 * time.Millisecond)
	if !found || err == nil || !strings.Contains(err.Error(), "did not answer within 300ms") {
		t.Fatalf("found %v, err %v", found, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s; the deadline did not hold", took)
	}

	t.Setenv("PATH", t.TempDir())
	if sessions, found, err := ListClaudeSessions(time.Second); found || err != nil || sessions != nil {
		t.Errorf("no claude is nobody to ask: %v, %v, %v", sessions, found, err)
	}
}

// The session wt runs under stays in the listing a person reads, marked.
func TestListClaudeSessionsMarksTheCallersOwnSession(t *testing.T) {
	stub := t.TempDir()
	listing := fmt.Sprintf(`[{"pid":%d,"name":"mine","kind":"interactive","status":"busy","cwd":"/w/a"},`+
		`{"pid":1,"name":"init","kind":"interactive","status":"idle","cwd":"/w/b"},`+
		`{"id":"3f9a1c20","name":"parked","kind":"background","state":"blocked","cwd":"/w/c"}]`, os.Getppid())
	if err := os.WriteFile(filepath.Join(stub, "agents.json"), []byte(listing), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat '" + filepath.Join(stub, "agents.json") + "'\n"
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, found, err := ListClaudeSessions(time.Minute)
	if err != nil || !found || len(got) != 3 {
		t.Fatalf("got %+v, found %v, err %v", got, found, err)
	}
	if !got[0].Own || got[1].Own || got[2].Own {
		t.Errorf("want only the caller's session marked: %+v", got)
	}
}

// What the listing for people reads must never fail the listing the verbs
// act on. A startedAt or sessionId of a type nobody expects is not read
// there at all, and the session is found as it always was.
func TestAnOddDisplayFieldDoesNotFailTheListingVerbsActOn(t *testing.T) {
	for _, odd := range []string{
		`"startedAt":"2026-10-09T10:00:00Z"`,
		`"startedAt":1790187208791.5`,
		`"startedAt":null,"sessionId":7`,
		`"sessionId":{"a":1}`,
	} {
		listing := `[{"id":"a1","pid":11,"cwd":"/repo_wt/feat_wt/one","kind":"background","name":"n","state":"working","status":"busy",` + odd + `}]`
		got, err := ParseAgents([]byte(listing))
		if err != nil || len(got) != 1 || got[0].Name != "n" || got[0].State != "working" {
			t.Errorf("%s: ParseAgents gave %+v, %v", odd, got, err)
		}

		stub := t.TempDir()
		if err := os.WriteFile(filepath.Join(stub, "agents.json"), []byte(listing), 0o644); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\ncat '" + filepath.Join(stub, "agents.json") + "'\n"
		if err := os.WriteFile(filepath.Join(stub, "claude"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
		acting, err := ListAgents()
		if err != nil || len(SessionsAt(acting, "/repo_wt/feat_wt/one")) != 1 {
			t.Errorf("%s: the acting listing gave %+v, %v", odd, acting, err)
		}

		// The listing for people keeps the session and leaves the field empty.
		shown, err := ParseClaudeSessions([]byte(listing))
		if err != nil || len(shown) != 1 || shown[0].Name != "n" {
			t.Errorf("%s: ParseClaudeSessions gave %+v, %v", odd, shown, err)
		}
	}
	shown, err := ParseClaudeSessions([]byte(`[{"id":"a1","kind":"background","startedAt":1790187208791.5,"sessionId":"a1-b2"}]`))
	if err != nil || shown[0].StartedAt != 1790187208791 || shown[0].SessionID != "a1-b2" {
		t.Errorf("want the whole milliseconds and the session id read: %+v, %v", shown, err)
	}
}

// A listing wt cannot read is said in words, not as a decoder's error.
func TestParseClaudeSessionsSaysAnUnreadableListingInWords(t *testing.T) {
	for _, junk := range []string{`{"not":"a list"}`, `[{"id":7}]`, `nonsense`} {
		if _, err := ParseClaudeSessions([]byte(junk)); !errors.Is(err, ErrUnreadableListing) {
			t.Errorf("%s: err = %v", junk, err)
		}
	}
}
