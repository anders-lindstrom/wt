package session

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/schema"
)

// TestRefusalCodesAreTheSchemasEnums holds the codes send and stop can
// report to the enum each schema declares, both ways.
func TestRefusalCodesAreTheSchemasEnums(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		schema, field string
		codes         []refusalCode
	}{
		{"session-send", "errorCode", sendCodes},
		{"session-stop", "reasonCode", stopCodes},
	} {
		d, ok := schema.Get(c.schema)
		if !ok {
			t.Fatalf("no schema %q", c.schema)
		}
		var doc struct {
			Properties map[string]struct {
				OneOf []struct {
					Enum []string `json:"enum"`
				} `json:"oneOf"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(d.Bytes, &doc); err != nil {
			t.Fatalf("%s: %v", c.schema, err)
		}
		var enum []string
		for _, o := range doc.Properties[c.field].OneOf {
			enum = append(enum, o.Enum...)
		}
		var codes []string
		for _, code := range c.codes {
			codes = append(codes, string(code))
		}
		slices.Sort(enum)
		slices.Sort(codes)
		if len(enum) == 0 || !slices.Equal(codes, enum) {
			t.Errorf("%s %s: the code has %v, the schema's enum %v", c.schema, c.field, codes, enum)
		}
	}
}
