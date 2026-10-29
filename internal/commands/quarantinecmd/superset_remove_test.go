package quarantinecmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// wt restore registers the worktree it put back, the way wt new does.
func TestRestoreRegistersWithSuperset(t *testing.T) {
	t.Parallel()
	ctx, path, branch, _, dir := retiretest.Quarantined(t, "fix/back", false)
	testkit.OptIn(ctx)
	log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	want := []string{
		"projects list --local --json",
		"ws create --local --project p1 --name " + branch + " --branch " + branch + " --skip-branch-prefix --json",
	}
	if got := testkit.ArgvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
	}
	if !strings.Contains(buf.String(), "✓ registered with Superset") {
		t.Errorf("output:\n%s", buf.String())
	}
}

// A worktree that comes back detached has no branch for Superset to adopt:
// it says so the way wt new does, and registers nothing.
func TestRestoreDetachedSkipsSuperset(t *testing.T) {
	t.Parallel()
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/moved-on", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/moved-on", "main")
	testkit.OptIn(ctx)
	log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), path+" is not on a branch; not registered with Superset") {
		t.Errorf("output:\n%s", buf.String())
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was asked to adopt a detached worktree: %q", got)
	}
}

// With the integration off, a restore runs no superset.
func TestRestoreLeavesSupersetAloneWhenOff(t *testing.T) {
	t.Parallel()
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
	log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was run: %q", got)
	}
}
