package retiretest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// Quarantined is a wt worktree for work, moved into a quarantine; it
// returns the context, the worktree's path, its branch, its tip and the
// folder. commit gives the branch a commit of its own, so it is kept under
// its bare name rather than deleted.
func Quarantined(t *testing.T, work string, commit bool) (*kit.Context, string, string, string, string) {
	t.Helper()
	return QuarantinedWith(t, work, commit, nil)
}

// QuarantinedWith is Quarantined with hooks the removal runs.
func QuarantinedWith(t *testing.T, work string, commit bool, h *quarantine.Hooks) (*kit.Context, string, string, string, string) {
	t.Helper()
	ctx, path := provisiontest.SafetyWorktree(t, work)
	branch := strings.TrimSpace(testkit.GitOut(t, path, "symbolic-ref", "--short", "HEAD"))
	if commit {
		testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description", "mine")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir, QuarantineHooks: h}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	return ctx, path, branch, tip, dir
}
