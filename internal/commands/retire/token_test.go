package retire

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// Every field of a plan is in its token or left out on purpose, with why:
// a field added to the plan fails here until someone decides.
func TestRemoveTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	p := Plan{Path: "/w", Branch: "b", Tip: "t", KeepAs: "k", Force: kit.ForceAll, Reap: true,
		Processes:   []agents.Process{{PID: 1, Argv: []string{"x"}}},
		Unreachable: []Lost{{Kind: LostHead, OID: "o", Count: 1}}}
	// A plan that refuses has no token, so the fields are read as hashed.
	tok := hashRemove(kit.NewToken("1:", "test"), ctx, p)
	gaps := kit.TokenGaps(tok, Plan{}, "", map[string]string{
		"MainBranch":      "trunk is hashed from the configuration, where the plan copies it from",
		"LockHolder":      "LockReason, which it is read from, is in it",
		"QuarantineError": "it refuses the removal, and a refused plan has no token",
		"ProcessesError":  "where it matters, under --reap or for a sweep that keeps busy worktrees, it refuses; elsewhere processes are not acted on",
	})
	gaps = append(gaps, kit.TokenGaps(tok, Lost{}, "Unreachable.", nil)...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in removeTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

func TestSweepTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "one", 0)
	plan := PlanOf(t, ctx)
	items := PlanItemsOf(plan)
	if len(items) == 0 {
		t.Fatal("the plan has no rows")
	}
	// Every optional part of a row, so that each is seen hashed.
	pr, base := 7, "main"
	items[0].Merged = &SweepMerged{How: "squash", Into: "main", PullRequest: &pr}
	items[0].PullRequest = &SweepPR{Number: pr, State: "MERGED", Base: &base}
	items[0].Processes = []RemoveProcess{{PID: 1, Command: "x", Reap: true}}
	tok := kit.NewToken("1:", "test")
	hashItems(tok, items)
	gaps := kit.TokenGaps(sweepTokenOf(ctx, plan, SweepOptions{}, nil), SweepPlan{}, "", map[string]string{
		"Bases":      "what the bases decide, each row's category and how it was merged, is in it",
		"MainRoot":   "the repository is hashed from ctx, where the plan copies it from",
		"Remove":     "each row is in it as an item, PlanItemsOf",
		"Delete":     "each row is in it as an item, PlanItemsOf",
		"CheckedOut": "each row is in it as an item, PlanItemsOf",
		"Gone":       "each row is in it as an item, PlanItemsOf",
	})
	gaps = append(gaps, kit.TokenGaps(tok, SweepItem{}, "Items.", map[string]string{
		"Items.Work":    "the branch and the path name the worktree",
		"Items.Ahead":   "the reason of a row it counts for says it",
		"Items.Subject": "Tip names the commit",
	})...)
	gaps = append(gaps, kit.TokenGaps(tok, SweepMerged{}, "Items.Merged.", nil)...)
	gaps = append(gaps, kit.TokenGaps(tok, SweepPR{}, "Items.PullRequest.", map[string]string{
		"Items.PullRequest.URL": "the number names the pull request",
	})...)
	gaps = append(gaps, kit.TokenGaps(tok, RemoveProcess{}, "Items.Processes.", map[string]string{
		"Items.Processes.Reap": "only the processes --reap stops are hashed",
	})...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in sweepTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}
