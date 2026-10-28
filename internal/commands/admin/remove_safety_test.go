package admin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The hook runs inside the session that is ending, and wt_rm_me in the
// shell of whoever is leaving: neither is somebody else in the worktree.
func TestHookRemovesTheWorktreeItsOwnSessionIsIn(t *testing.T) {
	// Not parallel: puts a fake claude on the PATH of the whole test binary.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/hooked")
	resolved, _ := filepath.EvalSymlinks(path)
	testkit.FakeClaude(t, ctx, []agents.Agent{ownSession(resolved)})

	var log bytes.Buffer
	in := strings.NewReader(`{"path":"` + path + `"}`)
	if err := HookRemove(ctx, in, &log); err != nil {
		t.Fatalf("HookRemove: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree should be gone:\n%s", log.String())
	}
}

func TestHookRefusesWhenAnotherSessionIsIn(t *testing.T) {
	// Not parallel: puts a fake claude on the PATH of the whole test binary.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/shared")
	resolved, _ := filepath.EvalSymlinks(path)
	other := agents.Agent{ID: "other", Name: "neighbour-2", Cwd: resolved, Status: "idle", PID: 999999}
	testkit.FakeClaude(t, ctx, []agents.Agent{ownSession(resolved), other})

	var log bytes.Buffer
	in := strings.NewReader(`{"path":"` + path + `"}`)
	if err := HookRemove(ctx, in, &log); err == nil {
		t.Fatalf("want a refusal: neighbour-2 is in it\n%s", log.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree must still be there")
	}
}
