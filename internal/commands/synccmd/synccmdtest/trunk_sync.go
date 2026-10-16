package synccmdtest

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TrunkFixture is a main checkout on main with a bare origin of its own, both
// at the same commit, and a second clone that can move origin on. push
// commits n times in that clone and pushes; the main checkout then fetches.
type TrunkFixture struct {
	Ctx    *kit.Context
	Main   string
	Origin string
	Other  string
}

// NewTrunkFixture builds a TrunkFixture.
func NewTrunkFixture(t *testing.T) *TrunkFixture {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-q", "-m", "configure")
	origin := gittest.WithOrigin(t, main)
	other := filepath.Join(t.TempDir(), "other")
	testkit.GitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	testkit.GitIn(t, other, "config", "user.email", "t@example.com")
	testkit.GitIn(t, other, "config", "user.name", "T")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return &TrunkFixture{Ctx: ctx, Main: main, Origin: origin, Other: other}
}

// AdvanceOrigin commits n times on origin's main and fetches it into the
// main checkout, leaving local main where it was.
func (f *TrunkFixture) AdvanceOrigin(t *testing.T, n int) string {
	t.Helper()
	testkit.GitIn(t, f.Other, "pull", "-q", "--ff-only")
	for i := 0; i < n; i++ {
		testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	}
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	return testkit.GitOut(t, f.Main, "rev-parse", "origin/main")
}

// Local is the fixture's local main.
func (f *TrunkFixture) Local(t *testing.T) string {
	t.Helper()
	return testkit.GitOut(t, f.Main, "rev-parse", "refs/heads/main")
}
