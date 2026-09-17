package wtsync

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// liftYAML declares the server's two version carriers: the yaml whose
// version lines owned-line owns, and the generated spec openapi owns.
const liftYAML = `conflicts:
  - paths: [app.yaml]
    strategy: owned-line
    line: '^\s*version:'
    rule: max-plus-patch
  - paths: [spec.json]
    strategy: openapi
`

// appYAML is the shape of the server's openapi: block, two APIs with a
// version each, under a server block whose port is far enough from either
// version line for an edit there to merge clean beside a bump.
func appYAML(main, remote string) string {
	return appYAMLPort("8080", main, remote)
}

func appYAMLPort(port, main, remote string) string {
	return "server:\n  port: " + port + "\n  context: /\nopenapi:\n  main:\n    name: Backend API\n    version: " + main + "\n  remote:\n    name: Remote API\n    version: " + remote + "\n"
}

// specJSON is a generated document in the generator's own style, with the
// version on the line a lift rewrites.
func specJSON(version string) string {
	return "{\n  \"openapi\" : \"3.1.0\",\n  \"info\" : {\n    \"title\" : \"T\",\n    \"version\" : \"" + version + "\"\n  },\n  \"tags\" : [ ],\n  \"paths\" : { },\n  \"components\" : {\n    \"schemas\" : { }\n  }\n}"
}

// liftRepo is runRepo with the server's declaration: app.yaml owned-line
// max-plus-patch on every version line, spec.json openapi. The base carries
// both at 1.2.2 (remote 1.5.1) and two files nothing claims.
func liftRepo(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir string, wt string, cfg *Config) {
	t.Helper()
	base := map[string]string{"a.txt": "a\n", "b.txt": "b\n", "app.yaml": appYAML("1.2.2", "1.5.1"), "spec.json": specJSON("1.2.2")}
	dir = repoWith(t, base, trunkEdits, branchEdits)
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(liftYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	w := featureWorktree(t, dir)
	return dir, w.Path, cfg
}

func readWT(t *testing.T, wt, path string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(wt, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// The silent case: the branch bumped 1.2.2 → 1.2.3 and trunk independently
// took 1.2.3 too. git merges the yaml clean, so no strategy ever ran, and
// the branch landed on trunk's number. The rule now runs at that pick and
// lifts the line to trunk plus one patch; a later pick that does not touch
// the line leaves it there.
func TestSimulateLiftsAVersionBothSidesBumpedToTheSameNumber(t *testing.T) {
	dir, _, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil || r.Err != nil {
		t.Fatalf("Stop = %+v, Err = %v: a lift is a resolution, not a person's stop", r.Stop, r.Err)
	}
	if len(r.Stops) != 1 || r.Stops[0].Index != 1 || r.Stops[0].Total != 2 || !r.Stops[0].Resolved {
		t.Fatalf("Stops = %+v, want one resolved stop at 1/2", r.Stops)
	}
	want := []FileOutcome{{Path: "app.yaml", Strategy: "owned-line", Resolved: true, Lifted: true, Note: "lifted the version to 1.2.4: trunk took 1.2.3"}}
	if !reflect.DeepEqual(r.Stops[0].Files, want) {
		t.Fatalf("Files = %+v, want %+v", r.Stops[0].Files, want)
	}
	if got := gitIn(t, dir, "show", r.Tree+":app.yaml"); got != strings.TrimRight(appYAML("1.2.4", "1.5.1"), "\n") {
		t.Fatalf("simulated app.yaml = %q, want the lifted 1.2.4 carried to the end", got)
	}
}

func TestRebaseLiftsAVersionBothSidesBumpedToTheSameNumber(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Left != nil || res.Replayed != 2 || len(res.Stops) != 1 {
		t.Fatalf("result %+v", res)
	}
	s := res.Stops[0]
	want := []FileOutcome{{Path: "app.yaml", Strategy: "owned-line", Resolved: true, Lifted: true, Note: "lifted the version to 1.2.4: trunk took 1.2.3"}}
	if s.Index != 1 || s.Total != 2 || !reflect.DeepEqual(s.Files, want) {
		t.Fatalf("stop %+v, want 1/2 with %+v", s, want)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
	// The lift rides in the pick that bumped, not in a commit of its own or
	// in the pick after it.
	if got := gitIn(t, wt, "show", "HEAD~1:app.yaml"); got != strings.TrimRight(appYAML("1.2.4", "1.5.1"), "\n") {
		t.Fatalf("the bumping pick carries %q", got)
	}
	if got := gitIn(t, wt, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD~1", "HEAD"); got != "b.txt" {
		t.Fatalf("the later pick changed %q, want only b.txt", got)
	}
	if !strings.Contains(log.String(), "✓ app.yaml owned-line lifted the version to 1.2.4: trunk took 1.2.3") {
		t.Fatalf("log %q", log.String())
	}
}

// A bump commit that is nothing but the bump is patch-identical to trunk's
// and git drops it as already upstream before it is ever applied. The run
// replays it anyway (--reapply-cherry-picks), so the pick reaches its stop
// with nothing committed, and the lift is committed under the pick's own
// author and message.
func TestRebaseLiftsABumpCommitGitWouldDropAsAlreadyUpstream(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1")}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1")}, {"b.txt": "b2\n"}})
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 2 || len(r.Stops) != 1 || r.Stop != nil || !r.Stops[0].Files[0].Lifted {
		t.Fatalf("simulation %+v, want the bump commit replayed and lifted", r)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Left != nil || res.Replayed != 2 || len(res.Stops) != 1 {
		t.Fatalf("result %+v", res)
	}
	if got := gitIn(t, wt, "log", "--format=%s", "origin/main..HEAD"); got != "branch 2\nbranch 1" {
		t.Fatalf("commits %q, want the bump commit kept under its own subject", got)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
	if got := gitIn(t, wt, "show", "HEAD~1:app.yaml"); got != gitIn(t, dir, "show", r.Tree+":app.yaml") {
		t.Fatalf("the run's bump commit carries %q, the simulation ended on something else", got)
	}
}

// A handover leaves the rest of the list as a plain rebase would have it, so
// a person's own git rebase --continue does not stop at the run's marks; a
// resume puts them back and lifts at the pick they were around.
func TestLiftMarksAreTakenOutAtAHandoverAndPutBackByResume(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}, {"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil || res.Left == nil || res.Left.Index != 1 {
		t.Fatalf("Rebase = %+v, %v; want a handover at 1/2", res, err)
	}
	todo, err := os.ReadFile(filepath.Join(gitIn(t, wt, "rev-parse", "--git-path", "rebase-merge"), "git-rebase-todo"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(todo), "break") || strings.Contains(string(todo), "edit ") {
		t.Fatalf("the list left for a person carries the run's marks:\n%s", todo)
	}
	// The markers a person resolves by hand name the commit the way git
	// always has, by its abbreviation, not by the full id.
	if got := readWT(t, wt, "a.txt"); !regexp.MustCompile(`(?m)^>>>>>>> [0-9a-f]{7,16} \(branch 1\)$`).MatchString(got) {
		t.Fatalf("a.txt's markers:\n%s", got)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "--", "a.txt")
	out, err := Resume(dir, cfg, trunkReq(wt, 1), res.OldTip, res.Safety, nil)
	if err != nil || out.Left != nil || out.Replayed != 2 {
		t.Fatalf("Resume = %+v, %v", out, err)
	}
	if len(out.Stops) != 1 || !out.Stops[0].Files[0].Lifted || out.Stops[0].Index != 2 {
		t.Fatalf("stops %+v, want the lift at 2/2", out.Stops)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
}

// The branch alone bumped, above trunk, and trunk changed another line of
// the same file: a clean merge that needs no lift.
func TestLiftLeavesABranchAlreadyAboveTrunkAlone(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAMLPort("9090", "1.2.2", "1.5.1")}},
		[]map[string]string{{"app.yaml": appYAML("1.3.0", "1.5.1")}})
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stops) != 0 {
		t.Fatalf("Stops = %+v, want none", r.Stops)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 0 || res.Replayed != 1 {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAMLPort("9090", "1.3.0", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
}

// Trunk bumped and the branch did not touch the line: nothing to lift, the
// branch takes trunk's number as any clean merge would.
func TestLiftLeavesALineTheBranchDidNotChangeAlone(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1")}},
		[]map[string]string{{"app.yaml": appYAMLPort("9090", "1.2.2", "1.5.1")}})
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stops) != 0 {
		t.Fatalf("Stops = %+v, want none", r.Stops)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 0 || res.Replayed != 1 {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAMLPort("9090", "1.2.3", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
}

// A genuine conflict on the line still goes through the strategy: branch
// 1.2.3 against trunk 1.2.5 is 1.2.6, resolved rather than lifted.
func TestLiftDoesNotTouchAConflictTheStrategyOwns(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.5", "1.5.1")}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1")}})
	want := []FileOutcome{{Path: "app.yaml", Strategy: "owned-line", Resolved: true}}
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stops) != 1 || !reflect.DeepEqual(r.Stops[0].Files, want) {
		t.Fatalf("simulated Stops = %+v", r.Stops)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 1 || !reflect.DeepEqual(res.Stops[0].Files, want) {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.6", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
}

// The generated spec gets the same treatment on info.version, and only that
// line changes: the rest of the document is left byte for byte.
func TestLiftRewritesTheSpecsVersionAndNothingElse(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"spec.json": specJSON("1.2.3"), "a.txt": "trunk\n"}},
		[]map[string]string{{"spec.json": specJSON("1.2.3"), "b.txt": "b2\n"}})
	want := []FileOutcome{{Path: "spec.json", Strategy: "openapi", Resolved: true, Lifted: true, Note: "lifted the version to 1.2.4: trunk took 1.2.3"}}
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stops) != 1 || !reflect.DeepEqual(r.Stops[0].Files, want) {
		t.Fatalf("simulated Stops = %+v", r.Stops)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 1 || !reflect.DeepEqual(res.Stops[0].Files, want) {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "spec.json"); got != specJSON("1.2.4") {
		t.Fatalf("spec.json = %q", got)
	}
}

// Two APIs in one file: each version line is its own question. The branch
// bumped both, trunk only main, so main is lifted and remote is the
// branch's own.
func TestLiftDecidesEachOwnedLineOnItsOwn(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.2"), "b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 1 || res.Stops[0].Files[0].Note != "lifted the version to 1.2.4: trunk took 1.2.3" {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.2") {
		t.Fatalf("app.yaml = %q", got)
	}
}

func TestLiftLiftsBothOwnedLinesWhenBothSidesBumpedBoth(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.2"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.2"), "b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stops) != 1 || res.Stops[0].Files[0].Note != "lifted the versions to 1.2.4 and 1.5.3: trunk took 1.2.3 and 1.5.2" {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.3") {
		t.Fatalf("app.yaml = %q", got)
	}
}

// The overview and the run must say the same thing about the same branch.
func TestTriageAndRunAgreeOnALift(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "spec.json": specJSON("1.2.3"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "spec.json": specJSON("1.2.3"), "b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	a := Assess(dir, "origin/main", cfg, repo.Worktree{Path: wt, Branch: "feature"}, nil)
	if a.Err != nil || a.Class != Recipe {
		t.Fatalf("assessment %+v", a)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Replay.Stops) != 1 || len(res.Stops) != 1 {
		t.Fatalf("stops: triage %+v, run %+v", a.Replay.Stops, res.Stops)
	}
	sim, live := a.Replay.Stops[0], res.Stops[0]
	if sim.Index != live.Index || sim.Total != live.Total || !reflect.DeepEqual(sim.Files, live.Files) || !reflect.DeepEqual(a.Files, live.Files) {
		t.Fatalf("triage says %d/%d %+v, the run did %d/%d %+v", sim.Index, sim.Total, sim.Files, live.Index, live.Total, live.Files)
	}
	for _, p := range []string{"app.yaml", "spec.json"} {
		if simulated, ran := gitIn(t, dir, "show", a.Replay.Tree+":"+p), gitIn(t, wt, "show", "HEAD:"+p); simulated != ran {
			t.Fatalf("%s: simulated %q, run %q", p, simulated, ran)
		}
	}
}

// Both sides took a version the rule cannot lift: refused with the
// strategy's own wording, and the stop is a person's.
func TestLiftRefusesAVersionThatIsNotSemver(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1"), "b.txt": "b2\n"}})
	const reason = `max-plus-patch needs an X.Y.Z version on both sides, got "version: 1.2.3-SNAPSHOT" and "version: 1.2.3-SNAPSHOT"`
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Files) != 1 || r.Stop.Files[0].Resolved || r.Stop.Files[0].Strategy != "owned-line" || r.Stop.Files[0].Note != reason {
		t.Fatalf("Stop = %+v", r.Stop)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil || !reflect.DeepEqual(res.Left.Left, []string{"app.yaml"}) || res.Left.Files[0].Note != reason {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.3-SNAPSHOT", "1.5.1") {
		t.Fatalf("app.yaml = %q, want untouched", got)
	}
}

// A handover after a lift is numbered by commits, not by the lines the run
// added to the sequencer's list to stop at the lift.
func TestLiftDoesNotShiftTheStopNumbersOfALaterHandover(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}, {"a.txt": "branch\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil || res.Left.Index != 2 || res.Left.Total != 2 || len(res.Stops) != 2 {
		t.Fatalf("result %+v", res)
	}
	if !res.Stops[0].Files[0].Lifted || res.Stops[1].Files[0].Note != "unclaimed" {
		t.Fatalf("stops %+v", res.Stops)
	}
	p, err := RebaseProgress(wt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Index != 2 || p.Total != 2 {
		t.Fatalf("progress %+v", p)
	}
}

// The second of two bump commits: the first is lifted to 1.2.4, so the
// second (1.2.3 → 1.2.5 on the branch) now conflicts with 1.2.4 and goes
// through the strategy, which keeps the branch's 1.2.5 as the higher one.
func TestLiftThenTheStrategyAtASecondBumpCommit(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}, {"app.yaml": appYAML("1.2.5", "1.5.1")}})
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stops) != 2 || len(res.Stops) != 2 || r.Stop != nil || res.Left != nil {
		t.Fatalf("simulated %+v, ran %+v", r.Stops, res.Stops)
	}
	for i, stops := range [][]Stop{r.Stops} {
		if !stops[0].Files[0].Lifted || stops[1].Files[0].Lifted || stops[1].Files[0].Strategy != "owned-line" || !stops[1].Files[0].Resolved {
			t.Fatalf("simulation %d: %+v", i, stops)
		}
	}
	if !res.Stops[0].Files[0].Lifted || res.Stops[1].Files[0].Lifted || !res.Stops[1].Files[0].Resolved {
		t.Fatalf("run: %+v", res.Stops)
	}
	if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.5", "1.5.1") {
		t.Fatalf("app.yaml = %q", got)
	}
	if got := gitIn(t, dir, "show", r.Tree+":app.yaml"); got != strings.TrimRight(appYAML("1.2.5", "1.5.1"), "\n") {
		t.Fatalf("simulated app.yaml = %q", got)
	}
}

// A commit trunk cherry-picked and then changed further in the same hunk: a
// plain rebase drops the commit before it is applied, and so does the run,
// since nothing in it is lifted. Replaying it would stop as a conflict where
// no rebase ever stopped.
func TestACommitTrunkCherryPickedAndChangedIsDroppedAsGitDropsIt(t *testing.T) {
	dir, wt, cfg := liftRepo(t, nil, []map[string]string{{"a.txt": "2\n"}, {"b.txt": "b2\n"}})
	gitIn(t, dir, "cherry-pick", gitIn(t, dir, "rev-parse", "feature~1"))
	gittest.WriteFile(t, filepath.Join(dir, "a.txt"), "3\n")
	gitIn(t, dir, "commit", "-qam", "a to 3 on trunk")
	gitIn(t, dir, "fetch", "-q", "origin")
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 1 || r.Stop != nil || len(r.Stops) != 0 || r.Err != nil {
		t.Fatalf("simulation %+v, want one clean commit", r)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replayed != 1 || len(res.Stops) != 0 || res.Left != nil || res.Restored {
		t.Fatalf("result %+v", res)
	}
	if got := readWT(t, wt, "a.txt"); got != "3\n" {
		t.Fatalf("a.txt = %q, want trunk's 3", got)
	}
	if got := gitIn(t, wt, "log", "--format=%s", "origin/main..HEAD"); got != "branch 2" {
		t.Fatalf("commits %q", got)
	}
}

// A pure bump commit both sides took to a number the rule cannot lift: git
// drops the pick as empty, so a refusal there has no commit for a person's
// fix to ride in. The run fails and restores; the overview says the same,
// as a failure, not as a stop to hand over.
func TestAPureBumpBothSidesTookToANonSemverFailsInBothPaths(t *testing.T) {
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1")}},
		[]map[string]string{{"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1")}, {"b.txt": "b2\n"}})
	const want = `"branch 1" is already on trunk and its version cannot be lifted: app.yaml: max-plus-patch needs an X.Y.Z version on both sides`
	r, err := SimulateRebase(dir, "origin/main", "feature", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil || r.Err == nil || !strings.Contains(r.Err.Error(), want) {
		t.Fatalf("simulation Stop = %+v, Err = %v", r.Stop, r.Err)
	}
	a := Assess(dir, "origin/main", cfg, repo.Worktree{Path: wt, Branch: "feature"}, nil)
	if v, why := Preflight(a); v != RefuseRun || !strings.Contains(why, want) {
		t.Fatalf("preflight %v %q", v, why)
	}
	// The replay reached no stop, which is what clean means, but the run
	// would fail: the row says unknown beside its error line, not clean.
	if a.Class != Unknown || a.Err == nil {
		t.Fatalf("class %s beside error %v, want unknown", a.Class, a.Err)
	}
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err == nil || !strings.Contains(err.Error(), want) || !res.Restored {
		t.Fatalf("run err = %v, result %+v", err, res)
	}
}

// A resume that fails leaves the rest of the list as a plain rebase would
// have it, so that a person's own git rebase --continue neither stops at the
// run's marks nor lands a bump on trunk's number in silence.
func TestAFailedResumeTakesTheMarksOutOfTheList(t *testing.T) {
	// Stop 1 is a person's; the pure bump at 2 is one trunk took patch for
	// patch, to a number that cannot be lifted, so the resume fails there;
	// the pick at 3 changes the yaml too, so the list carries a mark past
	// the failure.
	dir, wt, cfg := liftRepo(t,
		[]map[string]string{{"a.txt": "trunk\n"}, {"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1")}},
		[]map[string]string{{"a.txt": "branch\n"}, {"app.yaml": appYAML("1.2.3-SNAPSHOT", "1.5.1")}, {"app.yaml": appYAML("1.2.4-SNAPSHOT", "1.5.1"), "b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil || res.Left == nil || res.Left.Index != 1 {
		t.Fatalf("Rebase = %+v, %v; want a handover at 1/3", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "--", "a.txt")
	_, err = Resume(dir, cfg, trunkReq(wt, 1), res.OldTip, res.Safety, nil)
	if err == nil || !strings.Contains(err.Error(), "is already on trunk and its version cannot be lifted") || !strings.Contains(err.Error(), "left where it stopped") {
		t.Fatalf("Resume err = %v", err)
	}
	todo := readTodo(t, wt)
	if strings.Contains(todo, "break") || strings.Contains(todo, "edit ") || !strings.Contains(todo, "pick ") {
		t.Fatalf("the list left after the failure:\n%s", todo)
	}
	// And a person's own continue runs the rest without stopping.
	gitIn(t, wt, "-c", "core.editor=true", "rebase", "--continue")
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatalf("still in progress after a continue by hand:\n%s", readTodo(t, wt))
	}
}

func readTodo(t *testing.T, wt string) string {
	t.Helper()
	todo, err := os.ReadFile(filepath.Join(gitIn(t, wt, "rev-parse", "--git-path", "rebase-merge"), "git-rebase-todo"))
	if err != nil {
		t.Fatal(err)
	}
	return string(todo)
}

// A resume interrupted at one of the run's own stops — at the break before
// a pick that may need a lift, or after that pick with the lift not yet
// done — is picked up from there by the next resume, which lifts at the
// pick as if nothing had happened. The interrupt takes the marks out of the
// list; the resume puts them back.
func TestResumeRecoversFromAnInterruptAtTheRunsOwnStops(t *testing.T) {
	for _, atEdit := range []bool{false, true} {
		dir, wt, cfg := liftRepo(t,
			[]map[string]string{{"app.yaml": appYAML("1.2.3", "1.5.1"), "a.txt": "trunk\n"}},
			[]map[string]string{{"a.txt": "branch\n"}, {"app.yaml": appYAML("1.2.3", "1.5.1"), "b.txt": "b2\n"}})
		res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
		if err != nil || res.Left == nil || res.Left.Index != 1 {
			t.Fatalf("Rebase = %+v, %v; want a handover at 1/2", res, err)
		}
		if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("merged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, wt, "add", "--", "a.txt")
		// What a resume does up to the interrupt: the marks back in the
		// list and the continue that reaches the break; for the edit, the
		// break taken as the run takes it, so the pick is applied and the
		// sequencer stopped after it.
		d := &driver{mainRoot: dir, cfg: cfg, req: trunkReq(wt, 1), keep: true}
		if err := d.markTodo(); err != nil {
			t.Fatal(err)
		}
		if _, err := d.git("rebase", "--continue"); err != nil {
			t.Fatal(err)
		}
		if p, _ := RebaseProgress(wt); p.Command != "break" {
			t.Fatalf("atEdit %v: not at the break: %+v", atEdit, p)
		}
		if atEdit {
			if _, err := d.atBreak(); err != nil {
				t.Fatal(err)
			}
			if p, _ := RebaseProgress(wt); p.Command != "edit" || !p.Applied {
				t.Fatalf("not after the pick: %+v", p)
			}
		}
		if err := StripMarks(wt); err != nil {
			t.Fatal(err)
		}
		out, err := Resume(dir, cfg, trunkReq(wt, 1), res.OldTip, res.Safety, nil)
		if err != nil || out.Left != nil || out.Replayed != 2 {
			t.Fatalf("atEdit %v: Resume = %+v, %v", atEdit, out, err)
		}
		if len(out.Stops) != 1 || !out.Stops[0].Files[0].Lifted || out.Stops[0].Index != 2 {
			t.Fatalf("atEdit %v: stops %+v, want the lift at 2/2", atEdit, out.Stops)
		}
		if got := readWT(t, wt, "app.yaml"); got != appYAML("1.2.4", "1.5.1") {
			t.Fatalf("atEdit %v: app.yaml = %q", atEdit, got)
		}
		if got := gitIn(t, wt, "log", "--format=%s", "origin/main..HEAD"); got != "branch 2\nbranch 1" {
			t.Fatalf("atEdit %v: commits %q", atEdit, got)
		}
	}
}
