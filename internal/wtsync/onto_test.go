package wtsync

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// cutoffRepo is a branch whose first commit bumps v.txt against trunk's own
// bump, which no strategy here resolves, and whose second commit is its
// own. The first commit is the cutoff a --upstream names: below it, so a
// rebase onto trunk from there never replays it.
func cutoffRepo(t *testing.T) (dir, cutoff string) {
	t.Helper()
	dir = linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}, {"b.txt": "b2\n"}})
	return dir, gitIn(t, dir, "rev-parse", "feature~1")
}

func TestSimulateRebaseToReplaysOnlyTheCommitsAboveTheCutoff(t *testing.T) {
	t.Parallel()
	dir, cutoff := cutoffRepo(t)
	whole, err := SimulateRebaseTo(dir, Target{Onto: "main"}, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Stop == nil {
		t.Fatalf("without a cutoff the bump below it conflicts: %+v", whole)
	}
	r, err := SimulateRebaseTo(dir, Target{Onto: "main", Upstream: cutoff}, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 1 || r.Stop != nil {
		t.Fatalf("replay above the cutoff = %+v, want one commit and no stop", r)
	}
}

func TestAssessToCountsAndClassifiesOnlyTheCommitsAboveTheCutoff(t *testing.T) {
	t.Parallel()
	dir, cutoff := cutoffRepo(t)
	wt := featureWorktree(t, dir)
	a := AssessTo(dir, Target{Onto: "main", Upstream: cutoff, Name: "main"}, nil, wt, nil, OwnRemote{})
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Class != Clean || a.Ahead != 1 || a.Behind != 1 {
		t.Fatalf("class %v ahead %d behind %d, want conflict-free, 1 ahead, 1 behind", a.Class, a.Ahead, a.Behind)
	}
}

func TestStepsDueWithACutoffChecksOnlyTheCommitsAboveIt(t *testing.T) {
	t.Parallel()
	dir, cutoff := cutoffRepo(t)
	cfg, err := Parse([]byte("each_commit:\n  - run: \"true\"\ndefer:\n  - run: \"true\"\n    paths: [v.txt]\n"))
	if err != nil {
		t.Fatal(err)
	}
	due, err := StepsDue(dir, cfg, cutoff, "feature", "main", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(due.CheckCommits) != 1 || due.CheckCommits[0].Subject != "branch 2" {
		t.Fatalf("checks %+v, want branch 2 alone", due.CheckCommits)
	}
	// v.txt moved on trunk since the cutoff, so a step on it may run.
	if len(due.Deferred) != 1 {
		t.Fatalf("deferred %+v", due.Deferred)
	}
	// What the cutoff leaves out may still change: the rebase drops it.
	due, err = StepsDue(dir, mustParse(t, "defer:\n  - run: \"true\"\n    paths: [b.txt]\n"), cutoff, "feature", "main", cutoff)
	if err != nil || len(due.Deferred) != 1 {
		t.Fatalf("deferred on b.txt %+v %v", due.Deferred, err)
	}
}

func TestPreflightNamesTheTargetWhenItIsNotTrunk(t *testing.T) {
	t.Parallel()
	for class, want := range map[Class]string{Current: "already on integ", Stale: "nothing ahead of integ"} {
		_, why := Preflight(Assessment{Class: class, Target: "integ", Branch: "b"})
		if why != want {
			t.Errorf("%v: %q, want %q", class, why, want)
		}
		_, why = Preflight(Assessment{Class: class, Branch: "b"})
		if !strings.HasSuffix(why, "trunk") {
			t.Errorf("%v without a target: %q", class, why)
		}
	}
}

func mustParse(t *testing.T, yaml string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A—B—C with C the work: onto A above B replays C and drops B. The target
// is an ancestor of the tip, so nothing is behind it, and that is no
// reason to skip.
func TestAssessToAboveACutoffReplaysWhatTheTargetAlreadyUnderlies(t *testing.T) {
	t.Parallel()
	dir := linearRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}, {"a.txt": "a2\n"}})
	wt := featureWorktree(t, dir)
	b := gitIn(t, dir, "rev-parse", "feature~1")
	a := AssessTo(dir, Target{Onto: "main", Upstream: b, Name: "main"}, nil, wt, nil, OwnRemote{})
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Class != Clean || a.Ahead != 1 {
		t.Fatalf("class %v ahead %d, want conflict-free with C to replay", a.Class, a.Ahead)
	}
	// Above the target itself there is nothing to move.
	a = AssessTo(dir, Target{Onto: "main", Upstream: gitIn(t, dir, "rev-parse", "main"), Name: "main"}, nil, wt, nil, OwnRemote{})
	if a.Class != Current {
		t.Fatalf("above the target itself: class %v, want current", a.Class)
	}
}

// A target that already holds the work's tip is nothing ahead of it, with
// or without a cutoff.
func TestAssessToATargetThatHoldsTheTipIsStale(t *testing.T) {
	t.Parallel()
	dir := linearRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}, {"a.txt": "a2\n"}})
	wt := featureWorktree(t, dir)
	gitIn(t, dir, "branch", "landed", "feature")
	gitIn(t, dir, "checkout", "-q", "landed")
	gittest.WriteFile(t, dir+"/l.txt", "l\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "landed")
	gitIn(t, dir, "checkout", "-q", "main")
	for _, up := range []string{"", gitIn(t, dir, "rev-parse", "feature~1")} {
		a := AssessTo(dir, Target{Onto: "landed", Upstream: up, Name: "landed"}, nil, wt, nil, OwnRemote{})
		if a.Class != Stale {
			t.Errorf("upstream %q: class %v, want stale", up, a.Class)
		}
	}
}
