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
	for _, want := range []string{"wt up --json", "wt sync --json", "wt sync rebase|resume|undo --json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "wt sync-run") {
		t.Errorf("a command that does not exist:\n%s", out.String())
	}
}

// The list lines up however long a schema's name or title is: a plan's
// schema, checkout-plan, is longer than the first ones were.
func TestSchemaListLinesUp(t *testing.T) {
	out, err := runCmd(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	col := -1
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		at := strings.Index(line, "https://")
		if col >= 0 && at != col {
			t.Errorf("ids do not line up:\n%s", out)
			break
		}
		col = at
	}
}
