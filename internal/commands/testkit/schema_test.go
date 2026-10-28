package testkit

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/schema"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// A schema shaped like wt's: nested objects, a nullable object, an array of
// objects from $defs, an open map, and a property named "properties".
const closedFixture = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "a": { "type": "string" },
    "nested": { "type": "object", "properties": { "b": { "type": "integer" } } },
    "maybe": { "oneOf": [{ "type": "null" }, { "type": "object", "properties": { "c": { "type": "boolean" } } }] },
    "list": { "type": "array", "items": { "$ref": "#/$defs/item" } },
    "open": { "type": "object", "additionalProperties": { "type": "string" } },
    "properties": { "type": "object", "properties": { "d": { "type": "string" } } }
  },
  "$defs": { "item": { "type": "object", "properties": { "e": { "type": "string" } } } }
}`

// The test's validation fails on a key the schema does not declare, at
// every depth where the schema lists properties, and leaves an open map
// open.
func TestValidationRejectsAnUndeclaredKeyAtAnyDepth(t *testing.T) {
	t.Parallel()
	sch, err := compileClosed("https://example.com/closed.json", []byte(closedFixture))
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]string{
		"declared keys":     `{"a":"x","nested":{"b":1},"maybe":{"c":true},"list":[{"e":"y"}],"properties":{"d":"z"}}`,
		"a null object":     `{"maybe":null}`,
		"an open map's key": `{"open":{"anything":"goes"}}`,
	}
	for name, doc := range valid {
		if err := validate(sch, doc); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	undeclared := map[string]string{
		"top level":                   `{"a":"x","extra":1}`,
		"a nested object":             `{"nested":{"b":1,"extra":1}}`,
		"a oneOf branch":              `{"maybe":{"c":true,"extra":1}}`,
		"an array item from $defs":    `{"list":[{"e":"y","extra":1}]}`,
		"a property named properties": `{"properties":{"d":"z","extra":1}}`,
	}
	for name, doc := range undeclared {
		if err := validate(sch, doc); err == nil {
			t.Errorf("%s: an undeclared key passed: %s", name, doc)
		}
	}
}

// Closing every object leaves each shipped schema compiling.
func TestEveryShippedSchemaCompilesClosed(t *testing.T) {
	t.Parallel()
	for _, d := range schema.All() {
		if _, err := compileClosed(d.ID(), d.Bytes); err != nil {
			t.Errorf("%s: %v", d.File, err)
		}
	}
}

func validate(sch *jsonschema.Schema, doc string) error {
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}
