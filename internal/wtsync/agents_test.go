package wtsync

import "testing"

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
