package synccmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// listedSessions has every command that names no sessions of its own see agents.
// A busy Codex session refuses what a busy Claude one refuses, under the
// same --force.
func TestUpRefusesUnderABusyCodexSession(t *testing.T) {
	ctx, bump := provisiontest.RunFixture(t, false)
	opts := synccmdtest.NoAgents()
	opts.Agents = []wtsync.Agent{testkit.CodexSession(bump, "busy")}
	opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
	var out bytes.Buffer
	if err := synccmd.Up(ctx, "bump", opts, &out); err == nil || !strings.Contains(out.String(), "busy in it") ||
		!strings.Contains(out.String(), "codex exec") {
		t.Fatalf("a busy Codex session refuses and is named: %v\n%s", err, out.String())
	}
	opts.Force = true
	out.Reset()
	if err := synccmd.Up(ctx, "bump", opts, &out); err != nil || !strings.Contains(out.String(), "codex exec") {
		t.Fatalf("--force goes past it and names it: %v\n%s", err, out.String())
	}
}

func TestStatusAndSyncJSONListACodexSession(t *testing.T) {
	ctx, bump := provisiontest.RunFixture(t, false)
	testkit.ListedSessions(t, &session.ListSessions, testkit.CodexSession(bump, "busy"), wtsync.Agent{Name: "parked-1", Cwd: testkit.CodexSession(bump, "").Cwd, Kind: "interactive", Status: "idle"})

	p := synccmdtest.PlanOfWork(t, ctx, "bump")
	if len(p.Sessions) != 2 {
		t.Fatalf("status sessions %+v", p.Sessions)
	}
	for _, rows := range [][]synccmd.PlanSession{p.Sessions, synccmdtest.OverviewRow(t, synccmdtest.OverviewJSON(t, ctx), "bump").Sessions} {
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
	ctx, bump := provisiontest.RunFixture(t, false)
	idle := testkit.CodexSession(bump, "idle")
	idle.Name = "codex"
	opts := synccmdtest.NoAgents()
	opts.Agents = []wtsync.Agent{idle}
	opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
	var out bytes.Buffer
	if err := synccmd.Up(ctx, "bump", opts, &out); err != nil || !strings.Contains(out.String(), "codex (idle)") {
		t.Fatalf("an idle Codex session is named and the run goes ahead: %v\n%s", err, out.String())
	}
}
