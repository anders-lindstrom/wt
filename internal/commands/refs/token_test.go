package refs

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/jsonout"
)

// Every field of a plan is in its token or left out on purpose, with why:
// a field added to the plan fails here until someone decides.
func TestRefsTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	one := 1
	p := Plan{Repo: "/r", Trunk: "main", Remote: true, Patterns: []string{"backup/*"}, Age: "30d",
		Endpoint: &refpins.Endpoint{Digest: "sha256:x"},
		Rows:     []RefRow{{ID: "refs/heads/backup/a", Category: RefBackupOld, Unique: &one, Kept: []string{"x"}}}}
	tok := refsTokenOf(p)
	gaps := jsonout.TokenGaps(tok, Plan{}, "", map[string]string{
		"TrunkSource": "how trunk was found; trunk itself is in it",
		"Fetched":     "what the fetch brought is in the rows",
		"Now":         "each row's date, and the category its age gives it, are in it",
		"Problems":    "a plan with a problem stops before it has a token",
	})
	gaps = append(gaps, jsonout.TokenGaps(tok, RefRow{}, "Rows.", map[string]string{
		"Rows.Kind":      "the row's ID names it",
		"Rows.Name":      "the row's ID names it",
		"Rows.Ref":       "the row's ID names it",
		"Rows.Selected":  "the selection is not in it: --only picks from the plan a token names",
		"Rows.Annotated": "Object is the tag object of an annotated tag, so it tells",
		"Rows.Subject":   "Tip names the commit",
	})...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in refsTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

func TestRestoreTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	p := RestoreRefsPlan{Repo: "/r", RunID: "run", Rows: []restoreRow{{Action: RestoreCreate}}}
	tok := restoreTokenOf(p)
	gaps := jsonout.TokenGaps(tok, RestoreRefsPlan{}, "", nil)
	gaps = append(gaps, jsonout.TokenGaps(tok, restoreRow{}, "Rows.", map[string]string{
		"Rows.RunID":    "every row is of the plan's run, which is in it",
		"Rows.Selected": "the selection is not in it: --only picks from the plan a token names",
	})...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in restoreTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

// Origin as resolved now is in a restore's token when a remote ref can go
// back to it, as it is in a sweep's: a URL that changed after the plan was
// read changes it. A plan whose remote refs cannot go back keeps its token,
// so a restore of local refs alone is not refused over origin's URL.
func TestRestoreTokenCoversTheEndpoint(t *testing.T) {
	t.Parallel()
	tokens := func(remote string) (none, a, b string) {
		plan := func(e *refpins.Endpoint) RestoreRefsPlan {
			return RestoreRefsPlan{Repo: "/r", RunID: "run", endpoint: e, Rows: []restoreRow{
				{SweptRef: refpins.SweptRef{Kind: refpins.RefBranch, Name: "a"}, Action: RestoreCreate},
				{SweptRef: refpins.SweptRef{Kind: refpins.RefRemoteBranch, Name: "b"}, Action: remote}}}
		}
		return jsonout.Deref(restoreToken(plan(nil))), jsonout.Deref(restoreToken(plan(&refpins.Endpoint{Digest: "sha256:a"}))),
			jsonout.Deref(restoreToken(plan(&refpins.Endpoint{Digest: "sha256:b"})))
	}
	if none, a, b := tokens(RestoreCreate); none == a || a == b {
		t.Errorf("a remote ref can go back, and a different origin has the same token: none %s, a %s, b %s", none, a, b)
	}
	if none, a, b := tokens(RestoreEndpointChanged); none != a || a != b {
		t.Errorf("no remote ref can go back, and a different origin has another token: none %s, a %s, b %s", none, a, b)
	}
}
