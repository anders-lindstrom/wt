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
 {"id":"a1","cwd":"/repo_wt/feat_wt/one","kind":"background","name":"fix it","state":"blocked"},
 {"id":"a2","cwd":"/repo_wt/feat_wt/two","kind":"interactive","name":"two-3a","state":null},
 {"id":"a3","cwd":"/repo_wt/feat_wt/three","kind":"background","name":"finished","state":"done"},
 {"id":"a4","cwd":"/repo_wt/feat_wt/two/sub/dir","kind":"interactive","name":"deep","state":null}
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
