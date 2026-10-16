package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// listedSessions has every command that names no sessions of its own see agents.
// A busy Codex session refuses what a busy Claude one refuses, under the
// same --force.
func TestUpRefusesUnderABusyCodexSession(t *testing.T) {
	ctx, bump := runFixture(t, false)
	opts := noAgents()
	opts.Agents = []wtsync.Agent{testkit.CodexSession(bump, "busy")}
	opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
	var out bytes.Buffer
	if err := Up(ctx, "bump", opts, &out); err == nil || !strings.Contains(out.String(), "busy in it") ||
		!strings.Contains(out.String(), "codex exec") {
		t.Fatalf("a busy Codex session refuses and is named: %v\n%s", err, out.String())
	}
	opts.Force = true
	out.Reset()
	if err := Up(ctx, "bump", opts, &out); err != nil || !strings.Contains(out.String(), "codex exec") {
		t.Fatalf("--force goes past it and names it: %v\n%s", err, out.String())
	}
}

func TestRemovePlanJSONSaysWhoseEachSessionIs(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/codex-plan")
	resolved, _ := filepath.EvalSymlinks(path)
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Agents: []wtsync.Agent{
		{ID: "s1", Name: "one", Cwd: resolved, Status: "idle"},
		testkit.CodexSession(path, "busy"),
		{Tool: wtsync.ToolCodex, ID: "01a0-thread", Name: "codex (Codex Desktop)", Cwd: resolved, Status: "busy"},
	}})
	if err == nil {
		t.Fatal("a plan with sessions in it refuses")
	}
	if len(p.Sessions) != 3 {
		t.Fatalf("sessions %+v", p.Sessions)
	}
	claude, codex, thread := p.Sessions[0], p.Sessions[1], p.Sessions[2]
	if claude.Kind != "claude" || claude.State != "idle" {
		t.Errorf("claude session %+v", claude)
	}
	if codex.Kind != "codex" || codex.State != "busy" || codex.ID != "codex-4242" || codex.PID == nil || *codex.PID != 4242 {
		t.Errorf("codex session %+v", codex)
	}
	if thread.Kind != "codex" || thread.PID != nil || thread.ID != "01a0-thread" {
		t.Errorf("app-server thread %+v", thread)
	}
	var force []string
	for _, pr := range p.Problems {
		if pr.Code == KeptSession {
			force = append(force, deref(pr.ForceWith))
		}
	}
	if strings.Join(force, " ") != "idle-sessions busy-sessions" {
		t.Errorf("session problems are forced by %v", force)
	}
}

func TestStatusAndSyncJSONListACodexSession(t *testing.T) {
	ctx, bump := runFixture(t, false)
	testkit.ListedSessions(t, &session.ListSessions, testkit.CodexSession(bump, "busy"), wtsync.Agent{Name: "parked-1", Cwd: testkit.CodexSession(bump, "").Cwd, Kind: "interactive", Status: "idle"})

	p := planOfWork(t, ctx, "bump")
	if len(p.Sessions) != 2 {
		t.Fatalf("status sessions %+v", p.Sessions)
	}
	for _, rows := range [][]PlanSession{p.Sessions, overviewRow(t, overviewJSON(t, ctx), "bump").Sessions} {
		got := map[string]string{}
		for _, s := range rows {
			got[s.Kind] = s.Name + " " + s.State
		}
		if got["codex"] != "codex exec busy" || got["claude"] != "parked-1 idle" {
			t.Errorf("sessions %+v", rows)
		}
	}
}

// An idle interactive Codex is treated as an idle Claude session is: named,
// and not a refusal.
func TestUpGoesAheadUnderAnIdleCodexSession(t *testing.T) {
	ctx, bump := runFixture(t, false)
	idle := testkit.CodexSession(bump, "idle")
	idle.Name = "codex"
	opts := noAgents()
	opts.Agents = []wtsync.Agent{idle}
	opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
	var out bytes.Buffer
	if err := Up(ctx, "bump", opts, &out); err != nil || !strings.Contains(out.String(), "codex (idle)") {
		t.Fatalf("an idle Codex session is named and the run goes ahead: %v\n%s", err, out.String())
	}
}
