package admin

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The case this was built for: a branch pushed as own_apikey, renamed into
// the layout by wt migrate, then rebased and pushed. Its pull request is on
// origin/own_apikey, and that is where the push has to land.
func TestAMigratedBranchIsStillPushedToItsOldRemoteBranch(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	old := filepath.Join(ctx.Repo.Parent, "elsewhere", "own_apikey")
	testkit.GitOut(t, main, "worktree", "add", "-q", "-b", "own_apikey", old, "main~1")
	testkit.WriteFile(t, old, "key.txt", "key\n")
	testkit.GitOut(t, old, "add", "-A")
	testkit.GitOut(t, old, "commit", "-q", "-m", "own key")
	testkit.GitOut(t, old, "push", "-q", "-u", "origin", "own_apikey")
	pushedAs := testkit.OnBare(t, bare, "own_apikey")

	var out bytes.Buffer
	path, err := Migrate(ctx, "own_apikey", "", noSessions(), &out)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, out.String())
	}
	if want := "✓ branch renamed to feat_wt/own_apikey\n  it still pushes to origin/own_apikey, as it did before the rename\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("the rename does not say where it pushes:\n%s", out.String())
	}
	if got := testkit.GitOut(t, main, "config", "--get", "branch.feat_wt/own_apikey.wtPushTo"); got != "origin own_apikey" {
		t.Fatalf("recorded %q", got)
	}

	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"own_apikey"}, synccmdtest.PushAlways(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	tip := testkit.GitOut(t, path, "rev-parse", "HEAD")
	if tip == pushedAs {
		t.Fatal("the fixture did not rebase anything")
	}
	if got := testkit.OnBare(t, bare, "own_apikey"); got != tip {
		t.Fatalf("origin/own_apikey is at %s, the branch at %s:\n%s", got, tip, out.String())
	}
	if got := testkit.OnBare(t, bare, "feat_wt/own_apikey"); got != "" {
		t.Fatalf("the push made a second remote branch, feat_wt/own_apikey at %s", got)
	}
	if up := testkit.GitOut(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/own_apikey" {
		t.Fatalf("upstream %q", up)
	}
	s := out.String()
	said := strings.Index(s, "pushing feat_wt/own_apikey to origin/own_apikey (recorded)")
	done := strings.Index(s, "✓ pushed feat_wt/own_apikey to origin/own_apikey  "+pushedAs[:7]+" → "+tip[:7])
	if said < 0 || done < 0 || said > done {
		t.Fatalf("the destination is not named before the push and with its result:\n%s", s)
	}
}
