package retire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// RemovePlanOf is wt remove <path> --dry-run --json, checked against its
// schema.
func RemovePlanOf(t *testing.T, ctx *kit.Context, path string, opts RemoveOptions) (RemovePlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := RemovePlanJSON(ctx, RemoveTarget{Path: path}, opts, &out, &progress)
	testkit.ValidateJSON(t, "remove-plan", out.Bytes())
	var p RemovePlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return p, err
}

func ProblemCodes(ps []RemoveProblem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// The plan names the worktree, its commit, where the branch stands and what
// will become of it, and a token; it changes nothing.
func TestRemovePlanJSONDescribesTheRemoval(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/planned")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))

	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Token == nil || p.Error != nil || len(p.Problems) != 0 {
		t.Fatalf("a removable worktree has a token and no problems: %+v", p)
	}
	if Deref(p.Path) != path || Deref(p.Head) != tip || p.Detached || Deref(p.AdminDir) != testkit.GitDirFor(t, path) {
		t.Errorf("worktree facts %+v", p)
	}
	b := p.Branch
	if b == nil || b.Name != "fix_wt/planned" || Deref(b.Tip) != tip || b.Merge != "merged" ||
		b.Outcome != quarantine.BranchPlanDelete || Deref(b.Base) != "main" {
		t.Errorf("branch %+v", b)
	}
	if len(p.Bases) != 1 || p.Bases[0].Name != "main" {
		t.Errorf("bases %+v", p.Bases)
	}
	if !testkit.Exists(path) || !ctx.Repo.BranchExists("fix_wt/planned") {
		t.Error("a plan changes nothing")
	}
}

// A plan the removal would refuse has every reason as a code sweep's --json
// knows, an error, and no token.
func TestRemovePlanJSONRefusalHasCodesAndNoToken(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/dirty-plan")
	testkit.MustWrite(t, filepath.Join(path, "wip.txt"), "x")
	testkit.GitIn(t, path, "worktree", "lock", "--reason", "held", path)

	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{Agents: []wtsync.Agent{{ID: "s1", Name: "one", Cwd: path, Status: "idle"}}})
	if err == nil {
		t.Fatal("a refused plan exits non-zero")
	}
	if p.Token != nil || p.Error == nil {
		t.Errorf("token %v error %v", p.Token, p.Error)
	}
	codes := ProblemCodes(p.Problems)
	for _, want := range []string{KeptDirty, KeptSession, KeptLockHeld} {
		if !slices.Contains(codes, want) {
			t.Errorf("want %s in %v", want, codes)
		}
	}
	for _, pr := range p.Problems {
		if pr.Force != (pr.Code == KeptSession || pr.Code == KeptLockHeld) {
			t.Errorf("force on %+v", pr)
		}
	}
	if len(p.Sessions) != 1 || p.Sessions[0].ID != "s1" || p.Sessions[0].State != "idle" {
		t.Errorf("sessions %+v", p.Sessions)
	}
	if p.Lock == nil || !p.Lock.Held || Deref(p.Lock.Reason) != "held" {
		t.Errorf("lock %+v", p.Lock)
	}
}

// A detached worktree has no branch, and a quarantine folder that cannot be
// used is a problem with its own code.
func TestRemovePlanJSONDetachedAndQuarantineProblem(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/detached-plan")
	testkit.GitIn(t, path, "checkout", "-q", "--detach")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{Quarantine: dir})
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !p.Detached || p.Branch != nil || Deref(p.Quarantine) != dir {
		t.Errorf("%+v", p)
	}
	if !slices.Contains(ProblemCodes(p.Problems), ProblemQuarantineUnusable) {
		t.Errorf("problems %+v", p.Problems)
	}
}

// A branch the removal deletes whose commits nothing else holds is listed,
// with the command that brings them back.
func TestRemovePlanJSONListsTheCommitsADeletedBranchTakes(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/squashed")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	landed := func(string, string) int { return 12 }

	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{Landed: landed})
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Branch; b == nil || b.Outcome != quarantine.BranchPlanDelete || b.Merge != "unmerged" ||
		b.PullRequest == nil || *b.PullRequest != 12 || b.Ahead != 1 {
		t.Fatalf("branch %+v", p.Branch)
	}
	if len(p.Unreachable) != 1 {
		t.Fatalf("unreachable %+v", p.Unreachable)
	}
	l := p.Unreachable[0]
	want := []string{"git", "-C", ctx.Repo.MainRoot, "branch", "fix_wt/squashed", tip}
	if l.Kind != LostBranch || l.OID != tip || l.Count != 1 || !slices.Equal(l.RestoreCommand, want) {
		t.Errorf("lost %+v", l)
	}
}

// A sequencer left without its HEAD marker is an operation too, under a name
// the schema knows.
func TestRemovePlanJSONNamesALeftoverSequencer(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/sequencer")
	testkit.MustMkdir(t, filepath.Join(testkit.GitDirFor(t, path), "sequencer"))
	p, err := RemovePlanOf(t, ctx, path, RemoveOptions{})
	if err == nil || Deref(p.Operation) != "sequencer" || !slices.Contains(ProblemCodes(p.Problems), KeptOperation) {
		t.Errorf("%v %+v", err, p)
	}
}
