package commands

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A repository whose configuration does not load still has worktrees worth
// searching. Losing repo-first silently — which is what happens if the caller
// just discards the config error — makes wt find return a surprising answer
// with no hint why.
func TestOpenLenientSurvivesInvalidConfig(t *testing.T) {
	main := testkit.CommittedRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\nAWS_SETUP_ENABLED=true\n")
	if _, err := kit.Open(main); err == nil {
		t.Fatal("this fixture is supposed to have an invalid config")
	}

	var warn strings.Builder
	ctx := kit.OpenLenient(main, &warn)
	if ctx == nil {
		t.Fatal("OpenLenient must still produce a context")
	}
	if ctx.Repo.Name != "demo" {
		t.Errorf("repo = %q", ctx.Repo.Name)
	}
	if ctx.Config.TypeSuffix != "_wt" {
		t.Errorf("want the default suffix, got %q", ctx.Config.TypeSuffix)
	}
	if !strings.Contains(warn.String(), "AWS_SETUP_ENABLED") {
		t.Errorf("the config problem must be reported, not swallowed:\n%s", warn.String())
	}
}
