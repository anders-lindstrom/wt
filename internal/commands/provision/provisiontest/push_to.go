package provisiontest

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// RecordedBump is the fixture's bump pushed as origin/old-name and recorded
// as pushing there: a renamed branch.
func RecordedBump(t *testing.T) (ctx *kit.Context, bump, bare string) {
	t.Helper()
	ctx, bump = RunFixture(t, false)
	bare = testkit.OwnBareOrigin(t, ctx)
	testkit.GitOut(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump:old-name")
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch.feat_wt/bump.wtPushTo", "origin old-name")
	return ctx, bump, bare
}
