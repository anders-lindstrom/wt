package admin

import (
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func tmRepo(t *testing.T) (*kit.Context, *testkit.Tmutil) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, testkit.FakeTmutil(t, ctx, false)
}

// wt adopt excludes what it adopts: <repo>_wt/ for a worktree inside it, the
// worktree's own folder for one elsewhere, and after --relocate the folder
// it now lives in, not the one it left.
func TestAdoptExcludesTheWorktreeFromTimeMachine(t *testing.T) {
	cases := map[string]struct {
		at       func(parent string) string
		relocate bool
		want     func(parent, at string) string
	}{
		"in <repo>_wt": {
			at:   func(p string) string { return filepath.Join(p, "demo_wt", "demo", "feat_wt", "x") },
			want: func(p, _ string) string { return filepath.Join(p, "demo_wt") },
		},
		"elsewhere": {
			at:   func(p string) string { return filepath.Join(p, "agent", "x") },
			want: func(_, at string) string { return at },
		},
		"elsewhere, relocated": {
			at:       func(p string) string { return filepath.Join(p, "agent", "x") },
			relocate: true,
			want:     func(p, _ string) string { return filepath.Join(p, "demo_wt") },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, tm := tmRepo(t)
			at := c.at(ctx.Repo.Parent)
			testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/x", at)
			if _, err := Adopt(ctx, at, c.relocate, provision.SetupOptions{}, io.Discard); err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			if got, want := tm.Excluded(t), c.want(ctx.Repo.Parent, at); !slices.Equal(got, []string{want}) {
				t.Errorf("excluded %q, want %s", got, want)
			}
		})
	}
}

// wt migrate excludes the folder the worktree moved into.
func TestMigrateExcludesWhereTheWorktreeLands(t *testing.T) {
	ctx, tm := tmRepo(t)
	at := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/legacy", at)
	to, err := Migrate(ctx, at, "", MigrateOptions{Agents: []agents.Agent{}}, io.Discard)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if want := filepath.Join(ctx.Repo.Parent, "demo_wt"); filepath.Dir(filepath.Dir(to)) != want ||
		!slices.Equal(tm.Excluded(t), []string{want}) {
		t.Errorf("moved to %s; excluded %q, want %s", to, tm.Excluded(t), want)
	}
}

// wt migrate --dry-run moves nothing, so it excludes nothing.
func TestMigrateDryRunRunsNoTmutil(t *testing.T) {
	ctx, tm := tmRepo(t)
	at := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/legacy", at)
	if _, err := Migrate(ctx, at, "", MigrateOptions{DryRun: true, Agents: []agents.Agent{}}, io.Discard); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := tm.Calls(t); len(got) != 0 {
		t.Errorf("tmutil was called %q", got)
	}
}

// wt pr checkout puts its worktree under <repo>_wt/.
func TestPRCheckoutExcludesTheWorktreeFolderFromTimeMachine(t *testing.T) {
	ctx, tm := tmRepo(t)
	testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "residential_fixes", "Residents no longer rewrite a door"))
	if _, err := PRCheckout(ctx, 12, PROptions{}, io.Discard); err != nil {
		t.Fatalf("PRCheckout: %v", err)
	}
	if got, want := tm.Excluded(t), filepath.Join(ctx.Repo.Parent, "demo_wt"); !slices.Equal(got, []string{want}) {
		t.Errorf("excluded %q, want %s", got, want)
	}
}
