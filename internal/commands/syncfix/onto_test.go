package syncfix

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A run onto a local integration branch is put back by undo like any
// other.
func TestUndoPutsBackARunOntoATarget(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	integ := synccmdtest.Integration(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("integ", ""), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD~1") != integ {
		t.Fatalf("not onto integ:\n%s", out.String())
	}
	out.Reset()
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatalf("undo did not put bump back:\n%s", out.String())
	}
}

// A stop handed over onto a target other than trunk: the plan names the
// target, and resume finishes onto the target the run recorded, even once
// that branch has moved on.
func TestResumeFinishesOntoTheTargetTheRunRecorded(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	testkit.WriteFile(t, bump, "a.txt", "branch\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "--amend", "--no-edit")
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	integ := synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"a.txt": "integ\n", "v.txt": "1.0.7\n"})

	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("integ", ""), &out)
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v, want a handover\n%s", err, out.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState %v %v", ok, err)
	}
	if st.OntoRef != "integ" || st.Onto != integ {
		t.Errorf("the sidecar records onto %s (%q), want integ %s", st.Onto, st.OntoRef, integ)
	}
	plan, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# rebase bump onto integ\n", "HEAD is integ,"} {
		if !strings.Contains(string(plan), want) {
			t.Errorf("the plan lacks %q:\n%s", want, plan)
		}
	}

	moved := synccmdtest.Branch(t, ctx, "integ2", "integ", map[string]string{"j.txt": "j\n"})
	testkit.GitOut(t, ctx.Repo.MainRoot, "branch", "-f", "integ", moved)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	var res bytes.Buffer
	opts := noResumeAgents()
	opts.Journal = syncrun.NewSyncRunJournal(&res, "sync resume")
	out.Reset()
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("resume: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ rebased 1 commit onto integ") {
		t.Errorf("resume does not name the target:\n%s", out.String())
	}
	if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != integ {
		t.Errorf("bump sits on %s, want the recorded integ %s", got, integ)
	}
	testkit.ValidateJSON(t, "sync-run", res.Bytes())
	var r struct {
		Onto   string         `json:"onto"`
		Target map[string]any `json:"target"`
	}
	if err := json.Unmarshal(res.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Onto != integ || r.Target["ref"] != "integ" || r.Target["trunk"] != false {
		t.Errorf("resume --json: onto %s target %v", r.Onto, r.Target)
	}

	out.Reset()
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatalf("undo did not put bump back:\n%s", out.String())
	}
}

// A run onto a target that hands the named worktree over still owes the
// commands that move what is stacked on it: resume prints them once it
// finishes, and they work.
func TestResumePrintsTheFollowUpsOfARunOntoATarget(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	testkit.WriteFile(t, bump, "a.txt", "branch\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "--amend", "--no-edit")
	child := provisiontest.StackFixture(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"a.txt": "integ\n"})

	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("integ", ""), &out); err == nil {
		t.Fatalf("want a handover\n%s", out.String())
	}
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	out.Reset()
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("resume: %v\n%s", err, out.String())
	}
	follow := "wt sync rebase child --onto feat_wt/bump --upstream " + git.ShortID(old, 7)
	if !strings.Contains(out.String(), follow) {
		t.Fatalf("resume does not print %q:\n%s", follow, out.String())
	}
	opts := synccmdtest.Onto("feat_wt/bump", git.ShortID(old, 7))
	opts.Now = func() time.Time { return time.Unix(0, 300) }
	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"child"}, opts, &out); err != nil {
		t.Fatalf("the follow-up: %v\n%s", err, out.String())
	}
	if n := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-list", "--count", "feat_wt/bump..feat_wt/child"); n != "1" {
		t.Errorf("child has %s commits above bump, want its own one", n)
	}
	if testkit.GitOut(t, child, "rev-parse", "HEAD~1") != testkit.GitOut(t, bump, "rev-parse", "HEAD") {
		t.Error("child does not sit on bump")
	}
}

// B—X—C—D with a layer at C, onto a target that changed X's file and
// cherry-picked C, above B: the run hands X over, and once resume finishes
// the rebase git has emptied C, and the layer at C ends on X.
func TestResumeCountsAPickEmptiedAboveTheStopItFinished(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	b := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	commit := func(file, content string) string {
		testkit.WriteFile(t, bump, file, content)
		testkit.GitOut(t, bump, "add", "-A")
		testkit.GitOut(t, bump, "commit", "-q", "-m", file)
		return testkit.GitOut(t, bump, "rev-parse", "HEAD")
	}
	commit("a.txt", "branch\n")
	c := commit("c.txt", "c\n")
	testkit.GitOut(t, main, "branch", "lc", c)
	commit("d.txt", "d\n")
	testkit.GitOut(t, main, "checkout", "-q", "-b", "integ", "main")
	testkit.WriteFile(t, main, "a.txt", "integ\n")
	testkit.GitOut(t, main, "commit", "-q", "-am", "a on integ")
	testkit.GitOut(t, main, "cherry-pick", c)
	testkit.GitOut(t, main, "checkout", "-q", "main")

	opts := synccmdtest.Onto("integ", b)
	opts.Stack = &synccmd.StackOptions{Layers: []string{"lc"}}
	_, human, err := synccmdtest.StackRun(t, ctx, "bump", opts)
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v, want a handover at a.txt\n%s", err, human)
	}
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("resume: %v\n%s", err, out.String())
	}
	if n := testkit.GitOut(t, main, "rev-list", "--count", "integ..lc"); n != "1" {
		t.Errorf("lc has %s commits above integ, want the replayed a.txt alone\n%s", n, out.String())
	}
	if got, want := testkit.GitOut(t, main, "rev-parse", "lc"), testkit.GitOut(t, main, "rev-parse", "feat_wt/bump~1"); got != want {
		t.Errorf("lc is at %s, want the line's commit below d, %s\n%s", git.ShortID(got, 7), git.ShortID(want, 7), out.String())
	}
}
