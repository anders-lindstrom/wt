package commands

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/anders-lindstrom/wt/schema"
)

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
	for _, name := range []string{"status", "up", "sweep", "sweep-plan", "recovery", "restore-plan", "restore", "sync", "sync-run", "new-plan", "new", "checkout-plan", "checkout", "remove-plan", "remove", "refs-sweep-plan", "refs-sweep", "refs-restore-plan", "refs-restore", "refs-purge-plan", "refs-purge", "refs-swept", "session-list", "session-send"} {
		if _, ok := schema.Get(name); !ok {
			t.Errorf("no %s schema", name)
		}
	}
}
