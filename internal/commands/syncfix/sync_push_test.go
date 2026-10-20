package syncfix

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestSyncResumePushesWithPush(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump, _, _ := synccmdtest.HandedOver(t)
	bare := testkit.PushOrigin(t, ctx)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	opts := noResumeAgents()
	opts.Push = synccmd.PushAlways
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ pushed feat_wt/bump") {
		t.Fatalf("out %s", out.String())
	}
	if !testkit.Pushed(t, bare, bump) {
		t.Fatal("origin does not have the resumed tip")
	}
}
