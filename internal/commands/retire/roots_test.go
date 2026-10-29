package retire

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/reposel"
)

// One question covers every repository, each is swept as it would be alone,
// and one that cannot be swept is reported while the rest go ahead.
func TestSweepAllAsksOnceAndSweepsEveryRepository(t *testing.T) {
	t.Parallel()
	ctxA, _, _ := testkit.SweepRepo(t)
	ctxB, _, _ := testkit.SweepRepo(t)
	pathA := provisiontest.MergedWorktree(t, ctxA, "fix/one")
	pathB := provisiontest.MergedWorktree(t, ctxB, "fix/two")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{
		ctxA.Repo.MainRoot, ctxB.Repo.MainRoot, filepath.Join(testkit.RealTempDir(t), "gone"),
	}}}}

	var asked []string
	opts := SweepAllOptions{SweepOptions: SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}},
		Ask: func(q string) bool { asked = append(asked, q); return true }}
	var buf bytes.Buffer
	err := SweepAll(u, reposel.Selection{Profiles: []string{"p"}}, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 repositories could not be swept in full: gone") {
		t.Fatalf("want the missing entry reported: %v\n%s", err, buf.String())
	}
	if !slices.Equal(asked, []string{"Sweep 2 worktrees across 2 repositories?"}) {
		t.Errorf("asked %q", asked)
	}
	if testkit.Exists(pathA) || testkit.Exists(pathB) {
		t.Errorf("both merged worktrees should be gone:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "== demo") || !strings.Contains(buf.String(), "not there any more") {
		t.Errorf("each repository gets a section:\n%s", buf.String())
	}
}

func TestSweepAllChangesNothingWhenTheAnswerIsNo(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/one")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{ctx.Repo.MainRoot}}}}
	opts := SweepAllOptions{SweepOptions: SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}},
		Ask: func(string) bool { return false }}
	var buf bytes.Buffer
	if err := SweepAll(u, reposel.Selection{Profiles: []string{"p"}}, opts, &buf); err != nil {
		t.Fatal(err)
	}
	if !testkit.Exists(path) || !strings.Contains(buf.String(), "Nothing was swept.") {
		t.Errorf("no means nothing goes:\n%s", buf.String())
	}
}

// --dry-run prints the plan and changes nothing, on remove and on sweep, in
// one repository or across many.
func TestDryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/one")
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{DryRun: true, Agents: []agents.Agent{}}, &buf); err != nil {
		t.Fatal(err)
	}
	if !testkit.Exists(path) || !strings.Contains(buf.String(), "Nothing was removed: --dry-run.") {
		t.Errorf("remove --dry-run:\n%s", buf.String())
	}
	buf.Reset()
	if err := Sweep(ctx, SweepOptions{NoFetch: true, DryRun: true, Yes: false, Agents: []agents.Agent{}, PRs: map[string]github.PR{}}, &buf); err != nil {
		t.Fatal(err)
	}
	if !testkit.Exists(path) || !strings.Contains(buf.String(), "Nothing was swept: --dry-run.") {
		t.Errorf("sweep --dry-run:\n%s", buf.String())
	}
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{ctx.Repo.MainRoot}}}}
	buf.Reset()
	opts := SweepAllOptions{SweepOptions: SweepOptions{NoFetch: true, DryRun: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}},
		Ask: func(string) bool { t.Fatal("a dry run asks nothing"); return false }}
	if err := SweepAll(u, reposel.Selection{Profiles: []string{"p"}}, opts, &buf); err != nil {
		t.Fatal(err)
	}
	if !testkit.Exists(path) || !strings.Contains(buf.String(), "Nothing was swept: --dry-run.") {
		t.Errorf("sweep --all --dry-run:\n%s", buf.String())
	}
}
