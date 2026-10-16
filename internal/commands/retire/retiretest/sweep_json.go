package retiretest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// SweepPlanJSON runs wt sweep --plan --json and decodes its checked output.
func SweepPlanJSON(t *testing.T, ctx *kit.Context, opts retire.SweepOptions) (retire.SweepPlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := retire.SweepPlanJSON(ctx, opts, &out, &progress)
	testkit.ValidateJSON(t, "sweep-plan", out.Bytes())
	var p retire.SweepPlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return p, err
}

// SweepJSON runs wt sweep --json --yes and decodes its checked output.
func SweepJSON(t *testing.T, ctx *kit.Context, opts retire.SweepOptions) (retire.SweepResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = retire.NewSweepJournal(&out)
	opts.Yes = true
	err := retire.Sweep(ctx, opts, &human)
	testkit.ValidateJSON(t, "sweep", out.Bytes())
	var r retire.SweepResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, human.String(), err
}

// ResultItem is the item of r for branch.
func ResultItem(t *testing.T, r retire.SweepResult, branch string) *retire.SweepResultItem {
	t.Helper()
	for _, it := range r.Items {
		if it.Branch != nil && *it.Branch == branch {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", branch, r.Items)
	return nil
}
