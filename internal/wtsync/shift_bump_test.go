package wtsync

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func shiftRule(t *testing.T) ValueRule {
	t.Helper()
	rule, err := RuleNamed("shift-bump")
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestShiftBumpKeepsTheKindOfEachBump(t *testing.T) {
	t.Parallel()
	rule := shiftRule(t)
	for _, c := range []struct{ base, branch, under, want string }{
		{"2.46.0", "2.47.0", "2.47.0", "2.48.0"}, // a minor on trunk's minor
		{"2.47.0", "2.48.0", "2.48.0", "2.49.0"},
		{"2.48.0", "2.49.0", "2.49.0", "2.50.0"},
		{"2.49.0", "2.49.1", "2.50.0", "2.50.1"}, // a patch keeps the branch's patch
		{"2.49.1", "2.49.2", "2.50.1", "2.50.2"},
		{"2.49.2", "2.49.3", "2.50.2", "2.50.3"},
		{"2.46.0", "3.0.0", "2.47.0", "3.0.0"},   // a major
		{"2.46.0", "2.48.0", "2.47.0", "2.49.0"}, // a minor of two keeps its size
		{"2.46.0", "2.46.3", "2.47.0", "2.47.3"}, // the components above come from under
		{"2.46.0", "2.47.0", "2.46.0", "2.47.0"}, // under == base: the branch's own
		{"2.46.0", "2.46.0", "2.47.0", "2.47.0"}, // the pick did not bump: under's
		{"2.38.9", "2.38.10", "2.38.12", "2.38.13"},
	} {
		got, err := rule.Apply("    version: "+c.base, "    version: "+c.branch, "    version: "+c.under)
		if err != nil || got != "    version: "+c.want {
			t.Errorf("shift-bump %s→%s on %s = %q, %v; want %s", c.base, c.branch, c.under, got, err, c.want)
		}
	}
}

func TestShiftBumpKeepsTheBranchsLine(t *testing.T) {
	t.Parallel()
	got, err := shiftRule(t).Apply(`  "pkg": "1.2.3",`, `    "pkg": "1.3.0",  // ours`, `"pkg": "1.4.0"`)
	if err != nil || got != `    "pkg": "1.5.0",  // ours` {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestShiftBumpRefuses(t *testing.T) {
	t.Parallel()
	rule := shiftRule(t)
	for _, c := range []struct{ base, branch, under, want string }{
		{"version: 2.47.0", "version: 2.46.0", "version: 2.47.0", "the branch lowered the version from 2.47.0 to 2.46.0"},
		{"version: 2.46.0", "version: 2.47.0-SNAPSHOT", "version: 2.47.0", "shift-bump needs an X.Y.Z version on every side"},
		{"version: 2.46.0", "version: 2.47.0", "version: 2.47.0.1", "shift-bump needs an X.Y.Z version on every side"},
		{"", "version: 2.47.0", "version: 2.47.0", "shift-bump needs an X.Y.Z version on every side"},
	} {
		if got, err := rule.Apply(c.base, c.branch, c.under); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Apply(%q, %q, %q) = %q, %v; want an error naming %q", c.base, c.branch, c.under, got, err, c.want)
		}
	}
}

func TestShiftBumpLifts(t *testing.T) {
	t.Parallel()
	for _, r := range []Rule{
		{Strategy: StrategyOwnedLine, Rule: "shift-bump"},
		{Strategy: StrategyOpenAPI, Rule: "shift-bump"},
	} {
		if !r.lifts() {
			t.Errorf("%+v does not lift", r)
		}
	}
}

func TestOwnedLineShiftBumpLiftDecidesFromTheFourVersions(t *testing.T) {
	t.Parallel()
	s := OwnedLine{Line: regexp.MustCompile(`^\s*version:`), Rule: shiftRule(t)}
	yaml := func(v string) []byte { return []byte(appYAML(v, "1.5.1")) }
	for _, tc := range []struct {
		name                        string
		base, trunk, branch, merged string
		want, note                  string // want "" for untouched
	}{
		{"both bumped a minor to the same number", "2.46.0", "2.47.0", "2.47.0", "2.47.0", "2.48.0",
			"shifted the version to 2.48.0: the branch bumped a minor on 2.46.0, trunk took 2.47.0"},
		{"a later minor on the rebased line", "2.47.0", "2.48.0", "2.48.0", "2.48.0", "2.49.0",
			"shifted the version to 2.49.0: the branch bumped a minor on 2.47.0, trunk took 2.48.0"},
		{"a patch", "1.2.2", "1.2.3", "1.2.3", "1.2.3", "1.2.4",
			"shifted the version to 1.2.4: the branch bumped a patch on 1.2.2, trunk took 1.2.3"},
		{"the branch did not touch the line", "2.46.0", "2.47.0", "2.46.0", "2.47.0", "", ""},
		{"trunk did not touch the line", "2.46.0", "2.46.0", "2.47.0", "2.47.0", "", ""},
	} {
		lift, err := s.Lift("app.yaml", yaml(tc.base), yaml(tc.trunk), yaml(tc.branch), yaml(tc.merged))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.want == "" && lift != nil:
			t.Errorf("%s: lifted to %v, want untouched", tc.name, lift.To)
		case tc.want != "" && (lift == nil || string(lift.Content) != appYAML(tc.want, "1.5.1") || lift.note() != tc.note):
			t.Errorf("%s: lift = %+v, want %s noted %q", tc.name, lift, tc.want, tc.note)
		}
	}
}

func TestOwnedLineShiftBumpLiftNotesEveryLine(t *testing.T) {
	t.Parallel()
	s := OwnedLine{Line: regexp.MustCompile(`^\s*version:`), Rule: shiftRule(t)}
	lift, err := s.Lift("app.yaml", []byte(appYAML("2.46.0", "1.5.0")), []byte(appYAML("2.47.0", "1.5.1")),
		[]byte(appYAML("2.47.0", "1.5.1")), []byte(appYAML("2.47.0", "1.5.1")))
	if err != nil || lift == nil {
		t.Fatalf("lift = %+v, %v", lift, err)
	}
	want := "shifted the versions to 2.48.0 and 1.5.2: the branch bumped a minor on 2.46.0 and a patch on 1.5.0, trunk took 2.47.0 and 1.5.1"
	if string(lift.Content) != appYAML("2.48.0", "1.5.2") || lift.note() != want {
		t.Errorf("lift = %q noted %q", lift.Content, lift.note())
	}
}

// The server's stack adds an API of its own, a version line trunk does not
// have, in a pick that bumps the main API while trunk bumped it too. The
// lines no longer pair up by position, and the lift refuses the file as it
// does under max-plus-patch: a person decides that pick.
func TestOwnedLineShiftBumpLiftRefusesAPickThatAddsAVersionLine(t *testing.T) {
	t.Parallel()
	s := OwnedLine{Line: regexp.MustCompile(`^    version:`), Rule: shiftRule(t)}
	mobile := "  mobile:\n    name: Mobile API\n    version: 0.1.0\n"
	lift, err := s.Lift("app.yaml", []byte(appYAML("2.46.0", "1.5.1")), []byte(appYAML("2.47.0", "1.5.1")),
		[]byte(appYAML("2.47.0", "1.5.1")+mobile), []byte(appYAML("2.47.0", "1.5.1")+mobile))
	if lift != nil || !IsRefusal(err) || err.Error() != "app.yaml: the sides differ in the number of owned lines" {
		t.Fatalf("lift = %+v, %v; want the shape refusal", lift, err)
	}
}

func TestOwnedLineShiftBumpResolvesAConflictFromTheBase(t *testing.T) {
	t.Parallel()
	s := OwnedLine{Line: regexp.MustCompile(`^\s*version:`), Rule: shiftRule(t)}
	out, err := s.Resolve(Conflict{Path: "app.yaml",
		Base:   []byte(appYAML("2.49.0", "1.5.1")),
		Trunk:  []byte(appYAML("2.50.0", "1.5.1")),
		Branch: []byte(appYAML("2.49.1", "1.5.1"))})
	if err != nil || string(out) != appYAML("2.50.1", "1.5.1") {
		t.Fatalf("Resolve = %q, %v; want 2.50.1", out, err)
	}
}

func TestOpenAPIShiftBumpResolvesInfoVersionFromTheBase(t *testing.T) {
	t.Parallel()
	c := conflict(
		spec("2.49.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.50.0", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{}`, `[]`),
		spec("2.49.1", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "shift-bump"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if v := decode(t, out)["info"].(map[string]any)["version"]; v != "2.50.1" {
		t.Errorf("version = %v, want 2.50.1", v)
	}
	c = conflict(
		spec("2.47.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.48.0", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{}`, `[]`),
		spec("2.46.0", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	if _, err := (OpenAPI{Rule: "shift-bump"}).Resolve(c); !IsRefusal(err) || !strings.Contains(err.Error(), "the branch lowered the version") {
		t.Errorf("a lowered version: err = %v, want a refusal", err)
	}
}

func TestOpenAPIShiftBumpLiftsInfoVersion(t *testing.T) {
	t.Parallel()
	s := OpenAPI{Rule: "shift-bump"}
	lift, err := s.Lift("spec.json", []byte(specJSON("2.46.0")), []byte(specJSON("2.47.0")), []byte(specJSON("2.47.0")), []byte(specJSON("2.47.0")))
	if err != nil || lift == nil {
		t.Fatalf("lift = %+v, %v", lift, err)
	}
	want := "shifted the version to 2.48.0: the branch bumped a minor on 2.46.0, trunk took 2.47.0"
	if string(lift.Content) != specJSON("2.48.0") || lift.note() != want {
		t.Errorf("lift = %q noted %q", lift.Content, lift.note())
	}
	// Under == base: nothing for the lift, whatever the branch did.
	if lift, err := s.Lift("spec.json", []byte(specJSON("2.46.0")), []byte(specJSON("2.46.0")), []byte(specJSON("2.47.0")), []byte(specJSON("2.47.0"))); lift != nil || err != nil {
		t.Errorf("trunk did not move: lift = %+v, %v", lift, err)
	}
}

// bumpStackRepo is the case of issue #122: trunk bumped the main API 2.46.0 →
// 2.47.0, the number the branch's first bump had, and the branch's later
// commits bump it on to 2.49.3, with other changes between. app.yaml and
// spec.json both carry the version, under rule.
func bumpStackRepo(t *testing.T, rule string) (dir, wt string, cfg *Config) {
	t.Helper()
	bump := func(v string, extra string) map[string]string {
		return map[string]string{"app.yaml": appYAML(v, "1.5.1"), "spec.json": specJSON(v), "b.txt": extra}
	}
	base := map[string]string{"a.txt": "a\n", "b.txt": "b\n", "app.yaml": appYAML("2.46.0", "1.5.1"), "spec.json": specJSON("2.46.0")}
	dir = repoWith(t, base,
		[]map[string]string{{"app.yaml": appYAML("2.47.0", "1.5.1"), "spec.json": specJSON("2.47.0"), "a.txt": "trunk\n"}},
		[]map[string]string{
			bump("2.47.0", "b1\n"),
			{"c.txt": "between\n"},
			bump("2.48.0", "b2\n"),
			bump("2.49.0", "b3\n"),
			{"c.txt": "between again\n"},
			bump("2.49.1", "b4\n"),
			bump("2.49.2", "b5\n"),
			bump("2.49.3", "b6\n"),
		})
	decl := "conflicts:\n  - paths: [app.yaml]\n    strategy: owned-line\n    line: '^    version:'\n    rule: " + rule +
		"\n  - paths: [spec.json]\n    strategy: openapi\n    rule: " + rule + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(decl), 0o644); err != nil {
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
	return dir, featureWorktree(t, dir).Path, cfg
}

// The six bumps of the stack after a rebase, oldest first, each read from
// the commit that made it, with both carriers agreeing.
func bumpStackVersions(t *testing.T, wt string) []string {
	t.Helper()
	var got []string
	// HEAD~7 is the first pick; HEAD~6 and HEAD~3 bump nothing.
	for _, back := range []string{"~7", "~5", "~4", "~2", "~1", ""} {
		yaml := gitIn(t, wt, "show", "HEAD"+back+":app.yaml")
		v := regexp.MustCompile(`main:\n    name: Backend API\n    version: (\S+)`).FindStringSubmatch(yaml)
		if v == nil {
			t.Fatalf("HEAD%s:app.yaml = %q", back, yaml)
		}
		if spec := gitIn(t, wt, "show", "HEAD"+back+":spec.json"); spec != specJSON(v[1]) {
			t.Errorf("HEAD%s: spec.json = %q, want %s as app.yaml", back, spec, v[1])
		}
		got = append(got, v[1])
	}
	return got
}

func TestRebaseShiftsEveryBumpOfAStackByTheSameAmount(t *testing.T) {
	t.Parallel()
	for rule, want := range map[string][]string{
		"shift-bump":     {"2.48.0", "2.49.0", "2.50.0", "2.50.1", "2.50.2", "2.50.3"},
		RuleMaxPlusPatch: {"2.47.1", "2.48.0", "2.49.0", "2.49.1", "2.49.2", "2.49.3"}, // today's answer, unchanged
	} {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			dir, wt, cfg := bumpStackRepo(t, rule)
			sim, err := SimulateRebase(dir, "origin/main", "feature", cfg)
			if err != nil {
				t.Fatal(err)
			}
			if sim.Stop != nil || sim.Err != nil {
				t.Fatalf("simulation stopped: %+v, %v", sim.Stop, sim.Err)
			}
			res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Left != nil || res.Restored || res.Replayed != 8 {
				t.Fatalf("result %+v", res)
			}
			if got := bumpStackVersions(t, wt); !reflect.DeepEqual(got, want) {
				t.Errorf("the bumps after the rebase = %v, want %v", got, want)
			}
			last := want[len(want)-1]
			if got := gitIn(t, dir, "show", sim.Tree+":app.yaml"); got != strings.TrimRight(appYAML(last, "1.5.1"), "\n") {
				t.Errorf("the simulation ends on %q, want %s", got, last)
			}
			if got, want := runStopFiles(res.Stops), stopFiles(sim.Stops); !reflect.DeepEqual(got, want) {
				t.Errorf("the run's stops\n%+v\ndiffer from the simulation's\n%+v", got, want)
			}
		})
	}
}

// stopFiles is what a simulation and a run both say of their stops: where,
// and each file's outcome.
func stopFiles(stops []Stop) map[int][]FileOutcome {
	out := map[int][]FileOutcome{}
	for _, s := range stops {
		out[s.Index] = s.Files
	}
	return out
}

func runStopFiles(stops []StopResult) map[int][]FileOutcome {
	out := map[int][]FileOutcome{}
	for _, s := range stops {
		out[s.Index] = s.Files
	}
	return out
}

// shift-bump shifts from the base, so a block whose base holds no owned
// line, or more than one, gives it nothing to shift from: refused, where
// the rules that ignore the base still decide the block.
func TestCollapseOwnedShiftBumpNeedsOneOwnedLineInTheBase(t *testing.T) {
	t.Parallel()
	line := regexp.MustCompile(`^version:`)
	for name, base := range map[string][]string{
		"two in the base":  {"version: 2.40.0", "version: 2.46.0"},
		"none in the base": nil,
	} {
		b := Block{Base: base, Branch: []string{"version: 2.47.0"}, Trunk: []string{"version: 2.47.0"}}
		if _, err := collapseOwned(b, line, shiftRule(t), "f.txt"); !IsRefusal(err) || !strings.Contains(err.Error(), "no owned line in the base, or more than one") {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
		mpp, _ := RuleNamed("max-plus-patch")
		if out, err := collapseOwned(b, line, mpp, "f.txt"); err != nil || len(out) != 1 || out[0] != "version: 2.47.1" {
			t.Errorf("%s: max-plus-patch = %q, %v; it ignores the base", name, out, err)
		}
	}
}
