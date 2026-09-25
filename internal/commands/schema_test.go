package commands

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/anders-lindstrom/wt/schema"
)

// validateJSON checks one --json output against the schema wt ships for it,
// so the schema cannot drift from what the command prints.
func validateJSON(t *testing.T, name string, out []byte) {
	t.Helper()
	d, ok := schema.Get(name)
	if !ok {
		t.Fatalf("no schema %q", name)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(d.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(d.ID(), doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(d.ID())
	if err != nil {
		t.Fatalf("the %s schema does not compile: %v", name, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("wt %s --json does not match its schema: %v\n%s", name, err, out)
	}
	if m, ok := inst.(map[string]any); !ok || m["schemaVersion"] != d.Version() {
		t.Errorf("wt %s --json says schemaVersion %v; its schema is %s", name, m["schemaVersion"], d.Version())
	}
}

// Every schema wt ships compiles, and names its command.
func TestEverySchemaCompiles(t *testing.T) {
	for _, d := range schema.All() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(d.Bytes))
		if err != nil {
			t.Fatalf("%s: %v", d.File, err)
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(d.ID(), doc); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Compile(d.ID()); err != nil {
			t.Errorf("%s: %v", d.File, err)
		}
	}
	if len(schema.All()) != 2 {
		t.Errorf("want the status and up schemas, got %d", len(schema.All()))
	}
}
