package main

import (
	"errors"
	"strings"
	"testing"
)

// A target is chosen per piece of work: --onto with nothing named, across
// repositories, or on a verb that takes none is a usage mistake, refused
// before any repository is opened.
func TestOntoUsageMistakes(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sync", "--onto", "integ"}, "--onto takes the worktree to rebase onto it"},
		{[]string{"sync", "--rebase", "--onto", "integ"}, "--onto takes the worktree to rebase onto it"},
		{[]string{"sync", "rebase", "--onto", "integ"}, "--onto takes the worktree to rebase onto it"},
		{[]string{"sync", "--all", "--onto", "integ"}, "--onto is chosen per piece of work"},
		{[]string{"sync", "rebase", "--roots", "work", "--onto", "integ"}, "--onto is chosen per piece of work"},
		{[]string{"sync", "--profile", "api", "--rebase", "--onto", "integ"}, "--onto is chosen per piece of work"},
		{[]string{"sync", "a", "--upstream", "x"}, "--upstream needs --onto"},
		{[]string{"sync", "rebase", "a", "--upstream", "x"}, "--upstream needs --onto"},
		{[]string{"sync", "a", "--resume", "--onto", "integ"}, "--onto needs --rebase, or no verb"},
		{[]string{"up", "--upstream", "x"}, "--upstream needs --onto"},
		{[]string{"up", "--onto", "integ", "--expect", "1:00"}, "wt sync <work> --onto <ref> --json"},
		{[]string{"sync", "rebase", "a", "--stack", "--autosquash", "--onto", "integ"}, "--autosquash folds in place"},
		{[]string{"sync", "a", "--rebase", "--stack", "--autosquash", "--onto", "integ"}, "--autosquash folds in place"},
		{[]string{"sync", "keep", "once", "--onto", "integ"}, "unknown flag: --onto"},
		{[]string{"sync", "steps", "a", "--onto", "integ"}, "unknown flag: --onto"},
	} {
		_, err := runCmd(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error %v, want %q", tc.args, err, tc.want)
			continue
		}
		var u usageError
		if !errors.As(err, &u) && !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v: %v is not a usage mistake", tc.args, err)
		}
	}
}

// --onto and --upstream with a worktree named get as far as the repository.
func TestOntoWithAWorktreeNamedReachesTheVerb(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"sync", "a", "--onto", "integ"},
		{"sync", "a", "--onto", "integ", "--upstream", "x", "--json"},
		{"sync", "rebase", "a", "--onto", "integ", "--upstream", "x"},
		{"sync", "a", "--rebase", "--onto", "integ"},
		{"up", "--onto", "integ"},
	} {
		_, err := runCmd(t, args...)
		if err == nil || !strings.Contains(err.Error(), "git repository") {
			t.Errorf("%v: error %v, want the repository's", args, err)
		}
	}
}
