package provision

import (
	"io"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/jsonout"
)

// createSkips are the fields of CreatePlan, which both plans embed, that
// neither token hashes.
var createSkips = map[string]string{
	"Schema":        "the output's own",
	"SchemaVersion": "the output's own",
	"Token":         "the token itself",
	"TrunkSource":   "how trunk was found; the commit the worktree lands on is in it",
	"Configured":    "the configuration itself is in it",
	"Types":         "the configuration's, which is in it",
	"DefaultType":   "the configuration's, which is in it",
	"Type":          "with the work it gives the branch and the path, which are in it",
	"Work":          "with the type it gives the branch and the path, which are in it",
	"Provision":     "the setup after the worktree is made: the token names the worktree, not its setup",
	"BuildCommand":  "the setup after the worktree is made: the token names the worktree, not its setup",
	"Superset":      "the setup after the worktree is made: the token names the worktree, not its setup",
	"Problems":      "a plan with a problem has no token",
}

func withSkips(extra map[string]string) map[string]string {
	all := map[string]string{}
	for k, v := range createSkips {
		all[k] = v
	}
	for k, v := range extra {
		all[k] = v
	}
	return all
}

// Every field of a plan is in its token or left out on purpose, with why:
// a field added to the plan fails here until someone decides.
func TestNewTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	p := NewPlan{CreatePlan: CreatePlan{Command: "new", Branch: jsonout.Strp("b"), Path: jsonout.Strp("/w")}, BaseCommit: jsonout.Strp("c")}
	gaps := jsonout.TokenGaps(newTokenOf(ctx, p), NewPlan{}, "", withSkips(map[string]string{
		"Base": "BaseCommit, what it resolves to, is in it",
	}))
	if len(gaps) > 0 {
		t.Errorf("decide each, in newTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

func TestCheckoutTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	p := CheckoutPlan{CreatePlan: CreatePlan{Command: "checkout", Branch: jsonout.Strp("b"), Path: jsonout.Strp("/w")}}
	tok := checkoutTokenOf(ctx, p)
	p.Source = jsonout.Strp(SourceRemote)
	remote := checkoutTokenOf(ctx, p)
	if tok.String() == remote.String() {
		t.Error("a branch from a remote has the token of a local one")
	}
	skips := withSkips(map[string]string{"Remote": "RemoteRef names it"})
	gaps := jsonout.TokenGaps(tok, CheckoutPlan{}, "", skips)
	// Each source hashes its own commit: a gap in one is hashed in the other.
	gaps = inBoth(gaps, jsonout.TokenGaps(remote, CheckoutPlan{}, "", skips))
	if len(gaps) > 0 {
		t.Errorf("decide each, in checkoutTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

// inBoth is the gaps in both.
func inBoth(a, b []string) []string {
	var both []string
	for _, g := range a {
		for _, h := range b {
			if g == h {
				both = append(both, g)
			}
		}
	}
	return both
}
