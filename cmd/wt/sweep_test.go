package main

import (
	"regexp"
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
	for _, want := range []string{`(?m)^sweep-plan +wt sweep --dry-run --json `, `(?m)^sweep +wt sweep --yes --json `} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// --expect only means something for a refs command that goes ahead and
// reports in JSON; each refuses it otherwise, before anything runs.
func TestRefsExpectNeedsYesJSON(t *testing.T) {
	for _, args := range [][]string{
		{"refs", "sweep", "--expect", "rs1-abc"},
		{"refs", "sweep", "--json", "--expect", "rs1-abc"},
		{"refs", "restore", "20260930T091500Z-3f2a", "--yes", "--expect", "rr1-abc"},
		{"refs", "purge", "--older-than", "1d", "--json", "--expect", "rp1-abc"},
	} {
		out, err := runCmd(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--yes --json") {
			t.Errorf("%v: want an error naming --yes --json, got %v\n%s", args, err, out)
		}
	}
	out, err := runCmd(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`(?m)^refs-sweep-plan +wt refs sweep --dry-run --json `, `(?m)^refs-swept +wt refs swept --json `} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}
