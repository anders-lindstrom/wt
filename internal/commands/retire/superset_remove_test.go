package retire

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// NoClaude keeps a removal off `claude agents`.
var NoClaude = []wtsync.Agent{}

// wt remove, deleting or quarantining, deletes the worktree's Superset
// workspace — and only once the directory is gone from its path and git no
// longer lists it, because Superset's delete force-removes the worktree.
func TestRemoveDeregistersTheSupersetWorkspace(t *testing.T) {
	t.Parallel()
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/mirrored")
			testkit.OptIn(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			opts := RemoveOptions{Agents: NoClaude}
			if quarantined {
				opts.Quarantine = testkit.TrashFor(t, ctx)
			}
			var res RemoveResult
			opts.Result = &res
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, opts, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			want := []string{"ws list --local --json", "ws delete --local w1 --json"}
			if got := testkit.ArgvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
			}
			if res.Superset != provision.StepDeregistered || res.SupersetReason != "" {
				t.Errorf("result superset = %q (%q)", res.Superset, res.SupersetReason)
			}
			if !strings.Contains(out.String(), "✓ deleted its Superset workspace") {
				t.Errorf("output:\n%s", out.String())
			}
		})
	}
}

// Every way Superset can fail to take the delete leaves the removal done and
// its error untouched, with at most one line about it.
func TestRemoveSurvivesEverySupersetState(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		status     *superset.Status
		deleteBody string
		result     string
		line       string
	}{
		"not installed":  {&superset.Status{}, testkit.Deleted, kit.StepSkipped, ""},
		"host stopped":   {&superset.Status{Exe: "/nope/superset"}, testkit.Deleted, kit.StepSkipped, "- Superset's host service is not running; its workspace, if any, was left"},
		"status broken":  {&superset.Status{Exe: "/nope/superset", Err: testkit.ErrBoom}, testkit.Deleted, kit.StepFailed, "! boom; its Superset workspace, if any, was left"},
		"list fails":     {&superset.Status{Exe: "/nope/superset", Running: true}, testkit.Deleted, kit.StepFailed, "! superset ws list --local --json failed"},
		"delete fails":   {nil, `echo 'Error: nope' >&2; exit 1`, kit.StepFailed, "! superset ws delete --local w1 --json failed: nope; its Superset workspace was left"},
		"delete warns":   {nil, `echo '{"deleted":["w1"],"warnings":["teardown failed"]}'`, provision.StepDeregistered, "✓ deleted its Superset workspace; Superset warned: teardown failed"},
		"not registered": {nil, testkit.Deleted, provision.StepNotRegistered, ""},
		"warns on lines": {nil, `printf '%s\n' '{"deleted":["w1"],"warnings":["teardown failed\nat line 3"]}'`, provision.StepDeregistered, "✓ deleted its Superset workspace; Superset warned: teardown failed at line 3"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/mirrored")
			testkit.OptIn(ctx)
			at := path
			if name == "not registered" {
				at = t.TempDir()
			}
			log := provisiontest.SupersetAt(t, ctx, at, tc.deleteBody)
			if tc.status != nil {
				provisiontest.Stub(ctx, *tc.status)
			}
			var res RemoveResult
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Agents: NoClaude, Result: &res}, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			if testkit.Exists(path) || res.Outcome != RemoveRemoved {
				t.Fatalf("the removal must go ahead: %+v", res)
			}
			if res.Superset != tc.result {
				t.Errorf("superset = %q (%q), want %q", res.Superset, res.SupersetReason, tc.result)
			}
			lines := testkit.SupersetLines(out.String())
			switch {
			case tc.line == "" && len(lines) != 0:
				t.Errorf("want silence, got %q", lines)
			case tc.line != "" && (len(lines) != 1 || !strings.HasPrefix(lines[0], tc.line)):
				t.Errorf("lines = %q, want one starting %q", lines, tc.line)
			}
			if name == "not registered" {
				for _, a := range testkit.ArgvOf(t, log) {
					if strings.HasPrefix(a, "ws delete") {
						t.Errorf("deleted a workspace at another path: %q", a)
					}
				}
			}
		})
	}
}

// Until the person opts in, and wherever the repository says off, removal
// runs no superset at all.
func TestRemoveLeavesSupersetAloneWhenTheIntegrationIsOff(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(*kit.Context){
		"not opted in": func(*kit.Context) {},
		"repo off":     func(c *kit.Context) { testkit.OptIn(c); c.Config.SupersetRegister = config.SupersetOff },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/mirrored")
			set(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			var res RemoveResult
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Agents: NoClaude, Result: &res}, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			if got := testkit.ArgvOf(t, log); got != nil {
				t.Errorf("superset was run: %q", got)
			}
			if res.Superset != kit.StepSkipped {
				t.Errorf("superset = %q", res.Superset)
			}
			if lines := testkit.SupersetLines(out.String()); len(lines) != 0 {
				t.Errorf("want silence, got %q", lines)
			}
		})
	}
}

// A removal that is refused, or only a dry run, deletes no workspace.
func TestRemoveThatDoesNotHappenKeepsTheSupersetWorkspace(t *testing.T) {
	t.Parallel()
	for name, dirty := range map[string]bool{"refused": true, "dry run": false} {
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/mirrored")
			testkit.OptIn(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			if dirty {
				testkit.MustWrite(t, filepath.Join(path, "wip.txt"), "wip")
			}
			var res RemoveResult
			var out bytes.Buffer
			_ = RemoveAt(ctx, path, RemoveOptions{Agents: NoClaude, DryRun: !dirty, Result: &res}, &out)
			if !testkit.Exists(path) {
				t.Fatal("the worktree must still be there")
			}
			for _, a := range testkit.ArgvOf(t, log) {
				if strings.HasPrefix(a, "ws delete") {
					t.Errorf("deleted the workspace of a worktree that stays: %q", a)
				}
			}
			if res.Superset != "" {
				t.Errorf("superset = %q, want it not reached", res.Superset)
			}
		})
	}
}

// --keep-superset leaves the workspace alone: Superset is not asked
// anything, and the step says it was opted out of rather than skipped.
func TestRemoveKeepSupersetAsksSupersetNothing(t *testing.T) {
	t.Parallel()
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/kept-ws")
			testkit.OptIn(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			opts := RemoveOptions{Agents: NoClaude, KeepSuperset: true}
			if quarantined {
				opts.Quarantine = testkit.TrashFor(t, ctx)
			}
			var res RemoveResult
			opts.Result = &res
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, opts, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			if got := testkit.ArgvOf(t, log); got != nil {
				t.Errorf("superset was run: %q", got)
			}
			if res.Superset != provision.StepOptedOut {
				t.Errorf("result superset = %q (%q)", res.Superset, res.SupersetReason)
			}
			if lines := testkit.SupersetLines(out.String()); len(lines) != 1 || !strings.Contains(lines[0], "--keep-superset") {
				t.Errorf("superset lines = %q", lines)
			}
		})
	}
}
