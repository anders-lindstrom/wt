package synccmdtest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// PlanOfWork is the checked wt up --plan --json for work.
func PlanOfWork(t *testing.T, ctx *kit.Context, work string) synccmd.UpPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := synccmd.UpPlanJSON(ctx, work, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "status", buf.Bytes())
	var p synccmd.UpPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

// UpJSON runs wt up --json for work and decodes its checked output.
func UpJSON(t *testing.T, ctx *kit.Context, work string, opts synccmd.RunOptions) (synccmd.UpResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = synccmd.NewRunJournal(&out)
	err := synccmd.Up(ctx, work, opts, &human)
	testkit.ValidateJSON(t, "up", out.Bytes())
	var r synccmd.UpResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, err
}
