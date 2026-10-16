package provisiontest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// NewPlanOf is the checked --json plan wt new --plan prints for spec.
func NewPlanOf(t *testing.T, ctx *kit.Context, spec string, opts provision.NewOptions) provision.NewPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := provision.NewPlanJSON(ctx, spec, opts, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "new-plan", buf.Bytes())
	var p provision.NewPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

// CheckoutPlanOf is the checked --json plan wt checkout --plan prints.
func CheckoutPlanOf(t *testing.T, ctx *kit.Context, branch, work string) provision.CheckoutPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := provision.CheckoutPlanJSON(ctx, branch, work, provision.NewOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "checkout-plan", buf.Bytes())
	var p provision.CheckoutPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

// Codes is the problems' codes, comma separated.
func Codes(ps []provision.Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return strings.Join(out, ",")
}
