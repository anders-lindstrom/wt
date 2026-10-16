package syncfix

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The same during resume's fresh handover: the rebase and a handover are
// still there for resume to pick up again, and the interrupt says so.
func TestSyncResumeInterruptedWhileHandingOverAgainSaysToResume(t *testing.T) {
	if testing.Short() {
		t.Skip("interrupts a real run with a signal")
	}
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "interrupted while resuming bump") || !strings.Contains(out, "wt sync resume bump") {
			t.Fatalf("status %d; the interrupt did not point back at resume:\n%s", status, out)
		}
		return
	}
	ctx, bump := provisiontest.LaterStopFixture(t, true)
	gitDir, st := synccmdtest.HandOverNow(t, ctx, bump)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitTry(t, bump, "rebase", "--continue")
	first, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	testkit.InterruptWhileHandingOver(t, gitDir, func() bool {
		now, err := os.ReadFile(wtsync.PlanPath(gitDir))
		return err == nil && !bytes.Equal(now, first)
	})
	err = SyncResume(ctx, st.Branch, noResumeAgents(), os.Stdout)
	t.Fatalf("resume returned (%v) instead of being interrupted", err)
}
