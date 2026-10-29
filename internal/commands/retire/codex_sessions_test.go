package retire

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/jsonout"
)

func TestRemovePlanJSONSaysWhoseEachSessionIs(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/codex-plan")
	resolved, _ := filepath.EvalSymlinks(path)
	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{Agents: []agents.Agent{
		{ID: "s1", Name: "one", Cwd: resolved, Status: "idle"},
		testkit.CodexSession(path, "busy"),
		{Tool: agents.ToolCodex, ID: "01a0-thread", Name: "codex (Codex Desktop)", Cwd: resolved, Status: "busy"},
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
			force = append(force, jsonout.Deref(pr.ForceWith))
		}
	}
	if strings.Join(force, " ") != "idle-sessions busy-sessions" {
		t.Errorf("session problems are forced by %v", force)
	}
}
