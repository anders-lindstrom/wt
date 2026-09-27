package main

import (
	"bytes"
	"strings"
	"testing"
)

// The listing names the command each schema describes, as its title says,
// not one made up from the schema's name.
func TestSchemaListNamesEachCommand(t *testing.T) {
	cmd := newSchemaCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wt up --json", "wt sync --json", "wt sync run|resume|undo --json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "wt sync-run") {
		t.Errorf("a command that does not exist:\n%s", out.String())
	}
}
