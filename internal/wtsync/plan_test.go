package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func planConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`conflicts:
  - paths: [v.txt]
    strategy: owned-line
    line: '^\d'
    rule: max-plus-patch
  - paths: ["etc/*.json"]
    strategy: openapi
defer:
  - run: ./gradlew generateOpenApi
    paths: ["etc/*.json"]
    commit: "chore(api): regenerate"
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRenderPlanHasEverySection(t *testing.T) {
	dir := repoWith(t, map[string]string{"v.txt": "1.0.0\n", "a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("trunk\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-qam", "feat(pins): trunk moves a.txt")

	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "state_stats", Branch: "feat_wt/state_stats", TrunkRef: "origin/main",
		Base: base, Trunk: "main",
		Landing: Landing{Commits: 90, Scopes: []ScopeCount{{"pins", 6}, {"statepush", 4}}},
		Config:  planConfig(t),
		Handover: Handover{
			Index: 2, Total: 12, Subject: "record every sync run",
			Conflicts: []Conflict{
				{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("2.0.0\n"), Branch: []byte("1.1.0\n")},
				{Path: "a.txt", Base: []byte("a\n"), Trunk: []byte("trunk\nextra\n"), Branch: []byte("branch\n")},
			},
			Files: []FileOutcome{
				{Path: "v.txt", Strategy: "owned-line", Resolved: true},
				{Path: "a.txt", Note: "unclaimed"},
			},
			Staged: map[string]string{"v.txt": "deadbeef"},
			Left:   []string{"a.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# rebase state_stats onto origin/main",
		"90 landed. scopes: pins ×6, statepush ×4",
		"stopped at stop 2/12",
		"## already resolved — do not re-open",
		"owned-line",
		"## yours — 1 file",
		"trunk: feat(pins): trunk moves a.txt",
		"## never hand-merge here",
		"etc/*.json",
		"openapi",
		"the deferred `./gradlew generateOpenApi` owns it",
		"## deferred, runs when the rebase completes",
		"wt sync resume state_stats",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan is missing %q:\n%s", want, out)
		}
	}
}

// A file the strategies own but refused is a person's after all. It must not
// appear under "never hand-merge here", where its own declaration would
// otherwise put it — the plan would then say both "resolve this" and "never
// resolve this".
func TestRenderPlanDoesNotForbidWhatItAsksFor(t *testing.T) {
	dir := repoWith(t, map[string]string{"v.txt": "1.0.0\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Config: planConfig(t),
		Handover: Handover{
			Index: 1, Total: 1,
			Conflicts: []Conflict{{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("x\n"), Branch: []byte("y\n")}},
			Files:     []FileOutcome{{Path: "v.txt", Strategy: "owned-line", Note: "both sides changed a line it does not own"}},
			Left:      []string{"v.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := out[strings.Index(out, "## never hand-merge here"):]
	if strings.Contains(forbidden, "v.txt") {
		t.Fatalf("a refused owned file is listed as never-hand-merge:\n%s", out)
	}
	if !strings.Contains(out, "owned-line refused it") {
		t.Fatalf("the yours entry does not say the strategy refused it:\n%s", out)
	}
}

// Both sides only added lines: the plan says so, because that is a
// five-second read rather than a merge (spec §6).
func TestRenderPlanMarksAnAdditiveConflict(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Config: planConfig(t),
		Handover: Handover{
			Index: 1, Total: 1,
			Conflicts: []Conflict{{Path: "a.txt", Base: []byte("a\n"), Trunk: []byte("a\nt1\nt2\n"), Branch: []byte("a\nb1\n")}},
			Files:     []FileOutcome{{Path: "a.txt", Note: "unclaimed"}},
			Left:      []string{"a.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "additive only (trunk +2, ours +1)") {
		t.Fatalf("plan does not mark the additive conflict:\n%s", out)
	}
}

func TestStateRoundTripsAndIsTheMarker(t *testing.T) {
	dir := t.TempDir()
	if has, err := HasPlan(dir); err != nil || has {
		t.Fatalf("HasPlan on an empty dir = %v, %v", has, err)
	}
	want := State{
		Branch: "feat_wt/w", Work: "w", Trunk: "abc", TrunkRef: "origin/main", Onto: "abc",
		Epoch: 42, Safety: "refs/wt-sync/feat_wt/w/42", OldTip: "def", Stop: 2, Total: 12,
		Resolved: map[string]string{"v.txt": "cafe"}, Strategy: map[string]string{"v.txt": "owned-line"},
		Deleted: []string{"gone.txt"}, Left: []string{"a.txt"}, Lock: LeftLock{PID: 7, Started: 99},
	}
	if err := WriteState(dir, want); err != nil {
		t.Fatal(err)
	}
	has, err := HasPlan(dir)
	if err != nil || !has {
		t.Fatalf("HasPlan after WriteState = %v, %v; the sidecar is the marker", has, err)
	}
	got, ok, err := ReadState(dir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if got.Epoch != want.Epoch || got.Resolved["v.txt"] != "cafe" || got.Lock.PID != 7 || len(got.Deleted) != 1 {
		t.Fatalf("State = %+v, want %+v", got, want)
	}
	if err := RemovePlan(dir); err != nil {
		t.Fatal(err)
	}
	if has, _ := HasPlan(dir); has {
		t.Fatal("RemovePlan left the marker")
	}
}

func TestNeedsYouLine(t *testing.T) {
	got := NeedsYouLine("state_stats", []string{"src/SyncWorker.java", "a", "b", "c"})
	want := "wt: state_stats needs you. 4 left after resolvers: SyncWorker.java +3 · wt sync resume state_stats"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
