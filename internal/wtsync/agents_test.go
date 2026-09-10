package wtsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
