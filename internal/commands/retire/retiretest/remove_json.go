package retiretest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// RemoveJSON is wt remove <path> --yes --json: exactly one object, checked
// against its schema.
func RemoveJSON(t *testing.T, ctx *kit.Context, path string, opts retire.RemoveOptions) (retire.RemoveOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := retire.RemoveJSON(ctx, retire.RemoveTarget{Path: path}, opts, &out, &progress)
	return DecodeRemoveOutput(t, out.Bytes()), err
}

// DecodeRemoveOutput checks out against the remove schema and decodes it,
// failing on more than one object.
func DecodeRemoveOutput(t *testing.T, out []byte) retire.RemoveOutput {
	t.Helper()
	testkit.ValidateJSON(t, "remove", out)
	dec := json.NewDecoder(bytes.NewReader(out))
	var r retire.RemoveOutput
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if dec.More() {
		t.Errorf("more than one object:\n%s", out)
	}
	return r
}

// Step is the step of r named name.
func Step(t *testing.T, r retire.RemoveOutput, name string) retire.RemoveStep {
	t.Helper()
	for _, s := range r.Steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("no step %s in %+v", name, r.Steps)
	return retire.RemoveStep{}
}
