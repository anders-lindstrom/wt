package main

import (
	"strings"
	"testing"
)

// --expect only means something for a sweep that goes ahead and reports in
// JSON, and --json sweeps one repository: both refuse before anything runs.
func TestSweepJSONFlagsRefuseWhatTheyCannotDo(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sweep", "--expect", "1:abc"}, "--yes --json"},
		{[]string{"sweep", "--json", "--expect", "1:abc"}, "--yes --json"},
		{[]string{"sweep", "--yes", "--expect", "1:abc"}, "--yes --json"},
		{[]string{"sweep", "--json", "--all"}, "one repository"},
		{[]string{"sweep", "--json", "--dry-run", "--roots", "work"}, "one repository"},
	} {
		out, err := runCmd(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want an error naming %q, got %v\n%s", tc.args, tc.want, err, out)
		}
	}
}

// wt schema lists each schema by the command line whose output it is.
func TestSchemaListsTheSweepSchemas(t *testing.T) {
	out, err := runCmd(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sweep-plan  wt sweep --dry-run --json", "sweep       wt sweep --yes --json"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}
