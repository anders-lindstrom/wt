package main

import (
	"strings"
	"testing"
)

// The destination is the half of this command nobody can guess, so help has to
// show it.
func TestMigrateHelpShowsTheDestination(t *testing.T) {
	out, err := runCmd(t, "migrate", "--help")
	if err != nil {
		t.Fatalf("migrate --help: %v", err)
	}
	for _, want := range []string{"[<type>/<name>]", "--dry-run", "wt migrate fix/login-crash chore/tidy"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not mention %q:\n%s", want, out)
		}
	}
}

func TestMigrateArgErrorNamesWhatIsNeeded(t *testing.T) {
	out, err := runCmd(t, "migrate")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("message does not say what is missing: %q", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("no usage shown:\n%s", out)
	}
}

func TestMigrateRefusesMoreThanADestination(t *testing.T) {
	if _, err := runCmd(t, "migrate", "a", "b", "c"); err == nil {
		t.Error("want an error for a third argument")
	}
}

// "move" is what this does; the verb should not be the thing standing between
// a user and it.
func TestMoveIsAnAliasForMigrate(t *testing.T) {
	out, err := runCmd(t, "move", "--help")
	if err != nil {
		t.Fatalf("move --help: %v", err)
	}
	if !strings.Contains(out, "Move a worktree") {
		t.Errorf("move is not migrate:\n%s", out)
	}
}
