package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/superset"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// supersetAt writes a `superset` whose one live worktree workspace, w1, is
// at path, beside the main checkout's own "local" one, and points
// probeSuperset at it. deleteBody is what `ws delete` does; before it runs
// the fake notes in the log whether path still exists and whether git still
// lists it, so a test can prove the delete came after both were gone.
func supersetAt(t *testing.T, ctx *Context, path, deleteBody string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "superset")
	list := `[{"id":"w0","type":"local","worktreePath":"` + ctx.Repo.MainRoot + `","archivedAt":null},` +
		`{"id":"w1","type":"worktree","branch":"b","worktreePath":"` + path + `","archivedAt":null}]`
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  'ws list') echo '" + list + "' ;;\n" +
		"  'ws delete')\n" +
		"    [ -e '" + path + "' ] && echo 'DIR STILL THERE' >> " + log + "\n" +
		"    git -C '" + ctx.Repo.MainRoot + "' worktree list --porcelain | grep -q '/" + filepath.Base(path) + "$' && echo 'STILL LISTED' >> " + log + "\n" +
		"    " + deleteBody + " ;;\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w2\"},\"alreadyExists\":false}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stub(t, superset.Status{Exe: exe, Running: true})
	return log
}

const deleted = `echo '{"deleted":["w1"],"warnings":[]}'`

// noClaude keeps a removal off `claude agents`.
var noClaude = []wtsync.Agent{}

// supersetLines is every output line about Superset. Paths are left out of
// the question: they carry the test's own name.
func supersetLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		var words []string
		for _, word := range strings.Fields(line) {
			if !strings.Contains(word, "/") {
				words = append(words, word)
			}
		}
		if strings.Contains(strings.ToLower(strings.Join(words, " ")), "superset") {
			lines = append(lines, line)
		}
	}
	return lines
}

// wt remove, deleting or quarantining, deletes the worktree's Superset
// workspace — and only once the directory is gone from its path and git no
// longer lists it, because Superset's delete force-removes the worktree.
func TestRemoveDeregistersTheSupersetWorkspace(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/mirrored")
			optIn(ctx)
			log := supersetAt(t, ctx, path, deleted)
			opts := RemoveOptions{Agents: noClaude}
			if quarantined {
				opts.Quarantine = trashFor(t, ctx)
			}
			var res RemoveResult
			opts.Result = &res
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, opts, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			want := []string{"ws list --local --json", "ws delete --local w1 --json"}
			if got := argvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
			}
			if res.Superset != StepDeregistered || res.SupersetReason != "" {
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
	for name, tc := range map[string]struct {
		status     *superset.Status
		deleteBody string
		result     string
		line       string
	}{
		"not installed":  {&superset.Status{}, deleted, StepSkipped, ""},
		"host stopped":   {&superset.Status{Exe: "/nope/superset"}, deleted, StepSkipped, "- Superset's host service is not running; its workspace, if any, was left"},
		"status broken":  {&superset.Status{Exe: "/nope/superset", Err: errBoom}, deleted, StepFailed, "! boom; its Superset workspace, if any, was left"},
		"list fails":     {&superset.Status{Exe: "/nope/superset", Running: true}, deleted, StepFailed, "! superset ws list --local --json failed"},
		"delete fails":   {nil, `echo 'Error: nope' >&2; exit 1`, StepFailed, "! superset ws delete --local w1 --json failed: nope; its Superset workspace was left"},
		"delete warns":   {nil, `echo '{"deleted":["w1"],"warnings":["teardown failed"]}'`, StepDeregistered, "✓ deleted its Superset workspace; Superset warned: teardown failed"},
		"not registered": {nil, deleted, StepNotRegistered, ""},
		"warns on lines": {nil, `printf '%s\n' '{"deleted":["w1"],"warnings":["teardown failed\nat line 3"]}'`, StepDeregistered, "✓ deleted its Superset workspace; Superset warned: teardown failed at line 3"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/mirrored")
			optIn(ctx)
			at := path
			if name == "not registered" {
				at = t.TempDir()
			}
			log := supersetAt(t, ctx, at, tc.deleteBody)
			if tc.status != nil {
				stub(t, *tc.status)
			}
			var res RemoveResult
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Agents: noClaude, Result: &res}, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			if exists(path) || res.Outcome != RemoveRemoved {
				t.Fatalf("the removal must go ahead: %+v", res)
			}
			if res.Superset != tc.result {
				t.Errorf("superset = %q (%q), want %q", res.Superset, res.SupersetReason, tc.result)
			}
			lines := supersetLines(out.String())
			switch {
			case tc.line == "" && len(lines) != 0:
				t.Errorf("want silence, got %q", lines)
			case tc.line != "" && (len(lines) != 1 || !strings.HasPrefix(lines[0], tc.line)):
				t.Errorf("lines = %q, want one starting %q", lines, tc.line)
			}
			if name == "not registered" {
				for _, a := range argvOf(t, log) {
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
	for name, set := range map[string]func(*Context){
		"not opted in": func(*Context) {},
		"repo off":     func(c *Context) { optIn(c); c.Config.SupersetRegister = config.SupersetOff },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/mirrored")
			set(ctx)
			log := supersetAt(t, ctx, path, deleted)
			var res RemoveResult
			var out bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Agents: noClaude, Result: &res}, &out); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, out.String())
			}
			if got := argvOf(t, log); got != nil {
				t.Errorf("superset was run: %q", got)
			}
			if res.Superset != StepSkipped {
				t.Errorf("superset = %q", res.Superset)
			}
			if lines := supersetLines(out.String()); len(lines) != 0 {
				t.Errorf("want silence, got %q", lines)
			}
		})
	}
}

// A removal that is refused, or only a dry run, deletes no workspace.
func TestRemoveThatDoesNotHappenKeepsTheSupersetWorkspace(t *testing.T) {
	for name, dirty := range map[string]bool{"refused": true, "dry run": false} {
		t.Run(name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/mirrored")
			optIn(ctx)
			log := supersetAt(t, ctx, path, deleted)
			if dirty {
				mustWrite(t, filepath.Join(path, "wip.txt"), "wip")
			}
			var res RemoveResult
			var out bytes.Buffer
			_ = RemoveAt(ctx, path, RemoveOptions{Agents: noClaude, DryRun: !dirty, Result: &res}, &out)
			if !exists(path) {
				t.Fatal("the worktree must still be there")
			}
			for _, a := range argvOf(t, log) {
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

// The guard itself: while the directory is at its path, or git still lists
// the worktree, Superset is not asked to delete — its delete would
// force-remove the checkout past every check wt made.
func TestDeregisterNeverRunsWhileTheWorktreeIsThere(t *testing.T) {
	for _, how := range []string{"directory", "registration"} {
		t.Run(how, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/mirrored")
			optIn(ctx)
			log := supersetAt(t, ctx, path, deleted)
			found := findSupersetWorkspaces(ctx, path)
			if how == "registration" {
				// Gone from disk, still in git's list: a partial quarantine.
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			result, reason := found.deregister(ctx, path, &out)
			for _, a := range argvOf(t, log) {
				if strings.HasPrefix(a, "ws delete") {
					t.Fatalf("delete was called with the worktree still there: %q", argvOf(t, log))
				}
			}
			if result != StepSkipped || !strings.Contains(reason, "still") {
				t.Errorf("result = %q (%q)", result, reason)
			}
			if lines := supersetLines(out.String()); len(lines) != 1 {
				t.Errorf("want one line saying why, got %q", lines)
			}
		})
	}
}

// wt sweep deletes the workspace of each worktree it removes, and its --json
// says so per item.
func TestSweepDeregistersEachRemovedWorktree(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			ctx, _, _ := sweepRepo(t)
			path := mergedWorktree(t, ctx, "fix/one")
			optIn(ctx)
			log := supersetAt(t, ctx, path, deleted)
			opts := SweepOptions{NoFetch: true, Agents: noClaude, PRs: map[string]github.PR{}}
			if quarantined {
				opts.Quarantine = trashFor(t, ctx)
			}
			r, human, err := sweepJSON(t, ctx, opts)
			if err != nil || r.Outcome != OutcomeDone {
				t.Fatalf("%v %+v\n%s", err, r, human)
			}
			it := resultItem(t, r, "fix_wt/one")
			if it.Superset == nil || it.Superset.Result != StepDeregistered || it.Superset.Reason != nil {
				t.Errorf("item superset = %+v", it.Superset)
			}
			if got := argvOf(t, log); len(got) != 2 || got[1] != "ws delete --local w1 --json" {
				t.Errorf("argv = %q", got)
			}
		})
	}
}

// A sweep item that removed no worktree has no Superset step.
func TestSweepItemWithoutAWorktreeHasNoSupersetStep(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	r, human, err := sweepJSON(t, ctx, SweepOptions{NoFetch: true, Agents: noClaude, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if it := resultItem(t, r, "done-work"); it.Superset != nil {
		t.Errorf("superset = %+v, want null", it.Superset)
	}
}

// wt restore registers the worktree it put back, the way wt new does.
func TestRestoreRegistersWithSuperset(t *testing.T) {
	ctx, path, branch, _, dir := quarantined(t, "fix/back", false)
	optIn(ctx)
	log := supersetAt(t, ctx, path, deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	want := []string{
		"projects list --local --json",
		"ws create --local --project p1 --name " + branch + " --branch " + branch + " --skip-branch-prefix --json",
	}
	if got := argvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
	}
	if !strings.Contains(buf.String(), "✓ registered with Superset") {
		t.Errorf("output:\n%s", buf.String())
	}
}

// A worktree that comes back detached has no branch for Superset to adopt:
// it says so the way wt new does, and registers nothing.
func TestRestoreDetachedSkipsSuperset(t *testing.T) {
	ctx, path, _, _, dir := quarantined(t, "fix/moved-on", true)
	gitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/moved-on", "main")
	optIn(ctx)
	log := supersetAt(t, ctx, path, deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), path+" is not on a branch; not registered with Superset") {
		t.Errorf("output:\n%s", buf.String())
	}
	if got := argvOf(t, log); got != nil {
		t.Errorf("superset was asked to adopt a detached worktree: %q", got)
	}
}

// With the integration off, a restore runs no superset.
func TestRestoreLeavesSupersetAloneWhenOff(t *testing.T) {
	ctx, path, _, _, dir := quarantined(t, "fix/back", false)
	log := supersetAt(t, ctx, path, deleted)
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	if got := argvOf(t, log); got != nil {
		t.Errorf("superset was run: %q", got)
	}
}
