package provision_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The guard itself: while the directory is at its path, or git still lists
// the worktree, Superset is not asked to delete — its delete would
// force-remove the checkout past every check wt made.
func TestDeregisterNeverRunsWhileTheWorktreeIsThere(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"directory", "registration"} {
		t.Run(how, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/mirrored")
			testkit.OptIn(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			found := provision.FindSupersetWorkspaces(ctx, path)
			if how == "registration" {
				// Gone from disk, still in git's list: a partial quarantine.
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			result, reason := found.Deregister(ctx, path, &out)
			for _, a := range testkit.ArgvOf(t, log) {
				if strings.HasPrefix(a, "ws delete") {
					t.Fatalf("delete was called with the worktree still there: %q", testkit.ArgvOf(t, log))
				}
			}
			if result != kit.StepSkipped || !strings.Contains(reason, "still") {
				t.Errorf("result = %q (%q)", result, reason)
			}
			if lines := testkit.SupersetLines(out.String()); len(lines) != 1 {
				t.Errorf("want one line saying why, got %q", lines)
			}
		})
	}
}
