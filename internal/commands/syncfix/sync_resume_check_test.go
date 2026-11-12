package syncfix

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A check that fails hands the commit over with a plan; resume after a fix
// amended into that commit runs the check again and finishes.
func TestSyncResumeRechecksAFailedCheckAfterAnAmend(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"! grep -q 1.0.6 v.txt\"\n    paths: [v.txt]\n")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `check "! grep -q 1.0.6 v.txt" failed`) {
		t.Fatalf("output does not name the failing check:\n%s", out.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok || st.Check == nil || st.Check.Subject != "bump" {
		t.Fatalf("state %+v ok %v err %v", st, ok, err)
	}
	plan := readFile(t, wtsync.PlanPath(gitDir))
	if !strings.Contains(plan, "! grep -q 1.0.6 v.txt") || !strings.Contains(plan, "wt sync resume bump") {
		t.Fatalf("plan:\n%s", plan)
	}

	// Resume without a fix stops again at the same commit.
	out.Reset()
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err == nil {
		t.Fatalf("resume went on past a check that still fails:\n%s", out.String())
	}
	if busy, _ := wtsync.RebaseInProgress(bump); !busy {
		t.Fatal("the rebase is no longer in progress")
	}

	testkit.WriteFile(t, bump, "v.txt", "1.0.7\n")
	testkit.GitOut(t, bump, "commit", "-q", "-a", "--amend", "--no-edit")
	out.Reset()
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("resume: %v\n%s", err, out.String())
	}
	if busy, _ := wtsync.RebaseInProgress(bump); busy {
		t.Fatal("the rebase did not finish")
	}
	if got := readFile(t, bump+"/v.txt"); got != "1.0.7\n" {
		t.Fatalf("v.txt %q", got)
	}
}

// A check handover refuses a resume over changes nobody committed: the
// check would run on what is not the commit.
func TestSyncResumeRefusesACheckHandoverWithUncommittedChanges(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"! grep -q 1.0.6 v.txt\"\n    paths: [v.txt]\n")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out); err == nil {
		t.Fatal("no handover")
	}
	testkit.WriteFile(t, bump, "v.txt", "1.0.7\n")
	testkit.GitOut(t, bump, "add", "v.txt")
	out.Reset()
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "commit --amend") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
