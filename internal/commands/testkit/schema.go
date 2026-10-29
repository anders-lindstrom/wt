package testkit

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/schema"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidateJSON checks one --json output against the schema wt ships for it,
// so the schema cannot drift from what the command prints. It is stricter
// than the shipped schema: an object whose schema lists properties may carry
// no other key, so a field added to the output without a schema change fails
// here. The shipped schemas stay open, for readers of older output.
func ValidateJSON(t *testing.T, name string, out []byte) {
	t.Helper()
	d, ok := schema.Get(name)
	if !ok {
		t.Fatalf("no schema %q", name)
	}
	sch, err := compileClosed(d.ID(), d.Bytes)
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

// compileClosed compiles the schema data, published at id, with every object
// that lists its properties closed to others.
func compileClosed(id string, data []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	closeObjects(doc)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		return nil, err
	}
	return c.Compile(id)
}

// closeObjects sets additionalProperties to false on every schema in doc
// that lists properties and says nothing about others. It follows only the
// keywords that hold schemas, so a property named "properties" is a name.
func closeObjects(s any) {
	n, ok := s.(map[string]any)
	if !ok {
		return
	}
	if props, ok := n["properties"].(map[string]any); ok {
		if _, set := n["additionalProperties"]; !set {
			n["additionalProperties"] = false
		}
		for _, v := range props {
			closeObjects(v)
		}
	}
	for _, k := range []string{"$defs", "definitions", "patternProperties", "dependentSchemas"} {
		if m, ok := n[k].(map[string]any); ok {
			for _, v := range m {
				closeObjects(v)
			}
		}
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
		if l, ok := n[k].([]any); ok {
			for _, v := range l {
				closeObjects(v)
			}
		}
	}
	for _, k := range []string{"items", "additionalProperties", "not", "if", "then", "else", "contains"} {
		closeObjects(n[k])
	}
}
