package wtsync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// yoursSection is the "## yours" block of a rendered plan, up to the next
// heading, so a test can assert what is in it without matching the rest of
// the file.
func yoursSection(t *testing.T, plan string) string {
	t.Helper()
	i := strings.Index(plan, "## yours")
	if i < 0 {
		t.Fatalf("plan has no yours section:\n%s", plan)
	}
	rest := plan[i:]
	if j := strings.Index(rest[1:], "\n## "); j >= 0 {
		rest = rest[:j+1]
	}
	return rest
}

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
		MainRoot: dir, Work: "login-crash", Branch: "feat_wt/login-crash", TrunkRef: "origin/main",
		Base: base, Trunk: "main",
		Landing: Landing{Commits: 90, Scopes: []ScopeCount{{"pins", 6}, {"auth", 4}}},
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
		"# rebase login-crash onto origin/main",
		"90 landed. scopes: pins ×6, auth ×4",
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
		"wt sync resume login-crash",
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

// Left is what the terminal line counts, so it is what the brief lists. If
// the two were derived separately the terminal could say "2 left" while the
// file says "## yours — 1 file", and nothing would fail.
func TestRenderPlanFollowsLeftNotTheUnresolvedFiles(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	cfg, err := Parse([]byte("conflicts:\n  - paths: [a.txt, c.txt]\n    strategy: openapi\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Config: cfg,
		Handover: Handover{
			Index: 1, Total: 1,
			Files: []FileOutcome{
				{Path: "a.txt", Note: "unclaimed"},
				{Path: "b.txt", Strategy: "openapi", Note: "both sides added the same key"},
			},
			// Disagrees with the unresolved set of Files on both sides:
			// a.txt is unresolved but not handed over, c.txt is handed over
			// with no outcome at all.
			Left: []string{"b.txt", "c.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	yours := yoursSection(t, out)
	if !strings.Contains(yours, "## yours — 2 files") {
		t.Fatalf("the count does not follow Left:\n%s", out)
	}
	if !strings.Contains(yours, "b.txt") || !strings.Contains(yours, "openapi refused it") {
		t.Fatalf("a handed-over path lost its outcome detail:\n%s", out)
	}
	if !strings.Contains(yours, "c.txt") {
		t.Fatalf("a handed-over path with no outcome was dropped:\n%s", out)
	}
	if strings.Contains(yours, "a.txt") {
		t.Fatalf("a path Left does not name was listed as a person's:\n%s", out)
	}
	// The suppression rule follows the same set: c.txt is asked for, so its
	// declaration is not also forbidden; a.txt is not asked for, so it is.
	forbidden := out[strings.Index(out, "## never hand-merge here"):]
	if strings.Contains(forbidden, "c.txt") {
		t.Fatalf("a handed-over path is also listed as never-hand-merge:\n%s", out)
	}
	if !strings.Contains(forbidden, "a.txt") {
		t.Fatalf("a declaration nobody was asked about is missing:\n%s", out)
	}
}

// A caller that never sets Left still gets the brief it always got.
func TestRenderPlanFallsBackToFilesWhenLeftIsEmpty(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "v.txt": "1.0.0\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	out, err := RenderPlan(PlanInput{
		MainRoot: dir, Work: "w", Branch: "feat_wt/w", TrunkRef: "origin/main", Base: base, Trunk: "main",
		Handover: Handover{
			Index: 1, Total: 1,
			Files: []FileOutcome{
				{Path: "v.txt", Strategy: "owned-line", Resolved: true},
				{Path: "a.txt", Note: "unclaimed"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	yours := yoursSection(t, out)
	if !strings.Contains(yours, "## yours — 1 file\n") {
		t.Fatalf("the fallback count is not the unresolved set:\n%s", out)
	}
	if !strings.Contains(yours, "a.txt") || strings.Contains(yours, "v.txt") {
		t.Fatalf("the fallback listed the wrong files:\n%s", out)
	}
}

func TestStateRoundTripsAndIsTheMarker(t *testing.T) {
	dir := t.TempDir()
	if has, err := HasPlan(dir); err != nil || has {
		t.Fatalf("HasPlan on an empty dir = %v, %v", has, err)
	}
	want := State{
		Branch: "feat_wt/w", Work: "w", Trunk: "abc", TrunkRef: "origin/main", Onto: "abc",
		Upstream: "origin/feat_wt/parent",
		Epoch:    42, Safety: "refs/wt-sync/feat_wt/w/42", OldTip: "def", Stop: 2, Total: 12,
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
	// The whole struct, not a few fields: four later tasks read this file,
	// and a duplicated or mistyped JSON tag would drop a field silently.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("State = %+v, want %+v", got, want)
	}
	if err := RemovePlan(dir); err != nil {
		t.Fatal(err)
	}
	if has, _ := HasPlan(dir); has {
		t.Fatal("RemovePlan left the marker")
	}
}

// The sidecar is the marker, so it goes first and nothing that will not go
// can keep it in place: a marker left behind reports the worktree as waiting
// on somebody forever.
func TestRemovePlanClearsTheMarkerEvenWhenTheBriefWillNotGo(t *testing.T) {
	dir := t.TempDir()
	if err := WriteState(dir, State{Work: "w", Branch: "feat_wt/w"}); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory is what os.Remove cannot take away on any
	// platform — standing in for the markdown an editor lock or a read-only
	// mount pins in place.
	if err := os.MkdirAll(filepath.Join(PlanPath(dir), "held"), 0o755); err != nil {
		t.Fatal(err)
	}
	leftover := StatePath(dir) + ".tmp"
	if err := os.WriteFile(leftover, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemovePlan(dir); err == nil {
		t.Fatal("RemovePlan swallowed the brief it could not remove")
	}
	has, err := HasPlan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("RemovePlan left the marker because the brief would not go")
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("RemovePlan stopped short of the temp leftover: %v", err)
	}
}

// A sidecar whose maps are null must not hand back nil maps: the first
// consumer to record a resolution into one would panic.
func TestReadStateReturnsWritableMapsAndSlices(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(StatePath(dir), []byte(`{"branch":"feat_wt/w","resolved":null,"deleted":null}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadState(dir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if got.Resolved == nil || got.Strategy == nil || got.Deleted == nil || got.Left == nil {
		t.Fatalf("ReadState handed back nil collections: %+v", got)
	}
	got.Resolved["v.txt"] = "cafe"
	got.Strategy["v.txt"] = "owned-line"
	if got.Resolved["v.txt"] != "cafe" || got.Strategy["v.txt"] != "owned-line" {
		t.Fatalf("the returned maps are not writable: %+v", got)
	}
}

func TestNeedsYouLine(t *testing.T) {
	got := NeedsYouLine("login-crash", []string{"src/LoginHandler.java", "a", "b", "c"})
	want := "wt: login-crash needs you. 4 left after resolvers: LoginHandler.java +3 · wt sync resume login-crash"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
