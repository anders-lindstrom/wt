package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// BareOrigin gives the fixture an origin of its own in place of the main
// checkout standing in for one: a pass fetches each branch's own remote, and
// against itself every branch would always be found pushed.
func BareOrigin(t *testing.T, ctx *kit.Context) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	GitOut(t, ctx.Repo.MainRoot, "clone", "-q", "--bare", ctx.Repo.MainRoot, origin)
	GitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "origin", origin)
	GitOut(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")
}
