package wtsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedLineLiftDecidesFromTheFourVersions(t *testing.T) {
	s := ownedVersion(t)
	yaml := func(v string) []byte { return []byte(appYAML(v, "1.5.1")) }
	for _, tc := range []struct {
		name                        string
		base, trunk, branch, merged string
		want                        string // "" for untouched
	}{
		{"both bumped to the same number", "1.2.2", "1.2.3", "1.2.3", "1.2.3", "1.2.4"},
		{"the branch is already above trunk", "1.2.2", "1.2.3", "1.3.0", "1.3.0", ""},
		{"the branch did not touch the line", "1.2.2", "1.2.3", "1.2.2", "1.2.3", ""},
		{"trunk did not touch the line", "1.2.2", "1.2.2", "1.2.3", "1.2.3", ""},
		{"a second bump commit above a lifted first", "1.2.3", "1.2.4", "1.2.5", "1.2.5", ""},
	} {
		lift, err := s.Lift("app.yaml", yaml(tc.base), yaml(tc.trunk), yaml(tc.branch), yaml(tc.merged))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.want == "" && lift != nil:
			t.Errorf("%s: lifted to %v, want untouched", tc.name, lift.To)
		case tc.want != "" && (lift == nil || string(lift.Content) != appYAML(tc.want, "1.5.1")):
			t.Errorf("%s: lift = %+v, want %s", tc.name, lift, tc.want)
		}
	}
}

func TestOwnedLineLiftRefusesWithTheStrategysOwnWording(t *testing.T) {
	s := ownedVersion(t)
	yaml := func(v string) []byte { return []byte(appYAML(v, "1.5.1")) }
	_, err := s.Lift("app.yaml", yaml("1.2.2"), yaml("1.2.3-SNAPSHOT"), yaml("1.2.3-SNAPSHOT"), yaml("1.2.3-SNAPSHOT"))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "max-plus-patch needs an X.Y.Z version on both sides") {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnedLineLiftRefusesAFileWhoseShapeChanged(t *testing.T) {
	// The branch added a third owned line: the lines no longer pair up by
	// position across the sides, so nothing is guessed. Refused rather than
	// left alone, as the strategy refuses a block it cannot pair: a lift
	// that may be due is not skipped in silence.
	s := ownedVersion(t)
	three := []byte(appYAML("1.2.3", "1.5.1") + "  third:\n    version: 0.1.0\n")
	lift, err := s.Lift("app.yaml", []byte(appYAML("1.2.2", "1.5.1")), []byte(appYAML("1.2.3", "1.5.1")), three, three)
	if lift != nil || !IsRefusal(err) || err.Error() != "app.yaml: the sides differ in the number of owned lines" {
		t.Fatalf("lift = %+v, %v; want a refusal", lift, err)
	}
	// The reviewers' case: trunk bumped, the branch only added a version
	// line of its own ten lines away and bumped nothing. Refused the same
	// way: nothing here says which line the branch meant.
	far := "\n\n\n\n\n\n\n\n\n\nextra:\n  version: 2.0.0\n"
	lift, err = s.Lift("app.yaml", []byte(appYAML("1.2.2", "1.5.1")), []byte(appYAML("1.2.3", "1.5.1")), []byte(appYAML("1.2.2", "1.5.1")+far), []byte(appYAML("1.2.3", "1.5.1")+far))
	if lift != nil || !IsRefusal(err) || err.Error() != "app.yaml: the sides differ in the number of owned lines" {
		t.Fatalf("added line: lift = %+v, %v; want the shape refusal", lift, err)
	}
	// A line the regex claims that carries no version number, such as a
	// build placeholder, is not paired at all: the branch adding one changes
	// nothing about the shape, and the bump both sides made is still lifted.
	noise := "  build:\n    version: ${git.build.version}\n"
	lift, err = s.Lift("app.yaml", []byte(appYAML("1.2.2", "1.5.1")), []byte(appYAML("1.2.3", "1.5.1")), []byte(appYAML("1.2.3", "1.5.1")+noise), []byte(appYAML("1.2.3", "1.5.1")+noise))
	if err != nil || lift == nil || string(lift.Content) != appYAML("1.2.4", "1.5.1")+noise {
		t.Fatalf("placeholder line: lift = %+v, %v; want 1.2.4 with the placeholder left alone", lift, err)
	}
}

// A side whose info.version cannot be read as one plain literal is refused
// in both directions: an unreadable base must not make an unchanged version
// look like a bump, and an escaped literal in the merged document must not
// let a due lift pass in silence.
func TestOpenAPILiftRefusesASideItCannotRead(t *testing.T) {
	for name, sides := range map[string][4][]byte{
		"base does not parse":       {[]byte("not json"), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3"))},
		"base has no info.version":  {[]byte(`{"info":{"title":"T"}}`), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3"))},
		"merged literal is escaped": {[]byte(specJSON("1.2.2")), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3")), []byte(`{"info":{"version":"1` + "\\" + `u002e2.3"}}`)},
		"trunk does not parse":      {[]byte(specJSON("1.2.2")), []byte("{"), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3"))},
	} {
		lift, err := (OpenAPI{}).Lift("spec.json", sides[0], sides[1], sides[2], sides[3])
		if lift != nil || !IsRefusal(err) || !strings.Contains(err.Error(), "info.version is not one plain string literal on every side") {
			t.Errorf("%s: lift = %+v, %v; want a refusal", name, lift, err)
		}
	}
}

func TestKeepRulesNeverLift(t *testing.T) {
	for _, name := range []string{RuleKeepBranch, RuleKeepTrunk} {
		rule, err := RuleNamed(name)
		if err != nil {
			t.Fatal(err)
		}
		s := OwnedLine{Line: ownedVersion(t).Line, Rule: rule}
		yaml := func(v string) []byte { return []byte(appYAML(v, "1.5.1")) }
		if lift, err := s.Lift("app.yaml", yaml("1.2.2"), yaml("1.2.3"), yaml("1.2.3"), yaml("1.2.3")); err != nil || lift != nil {
			t.Errorf("%s: lift = %+v, %v; a keep rule never moves a number", name, lift, err)
		}
	}
	if lift, err := (OpenAPI{Rule: RuleKeepTrunk}).Lift("spec.json", []byte(specJSON("1.2.2")), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3")), []byte(specJSON("1.2.3"))); err != nil || lift != nil {
		t.Errorf("openapi keep-trunk: lift = %+v, %v", lift, err)
	}
	for _, r := range []Rule{
		{Strategy: StrategyOwnedLine, Rule: RuleKeepTrunk},
		{Strategy: StrategyListUnion},
		{Strategy: StrategyTakeTrunk},
		{Strategy: StrategyScript},
	} {
		if r.lifts() {
			t.Errorf("%+v lifts", r)
		}
	}
	for _, r := range []Rule{
		{Strategy: StrategyOwnedLine, Rule: RuleMaxPlusPatch},
		{Strategy: StrategyOpenAPI},
		{Strategy: StrategyOpenAPI, Rule: RuleMaxPlusPatch},
	} {
		if !r.lifts() {
			t.Errorf("%+v does not lift", r)
		}
	}
}

func TestOpenAPILiftRewritesOnlyInfoVersion(t *testing.T) {
	// The generated document carries other "version" keys, deep in schemas
	// and as a property name; only info.version is the API's.
	doc := func(v string) []byte {
		return []byte(`{
  "openapi" : "3.1.0",
  "info" : {
    "title" : "T",
    "x-version" : { "version" : "9.9.9" },
    "version" : "` + v + `"
  },
  "paths" : { "/v" : { "get" : { "parameters" : [ { "name" : "version", "schema" : { "version" : "1.2.3" } } ] } } },
  "components" : { "schemas" : { "Thing" : { "properties" : { "version" : { "type" : "string", "example" : "1.2.3" } } } } }
}`)
	}
	lift, err := (OpenAPI{}).Lift("spec.json", doc("1.2.2"), doc("1.2.3"), doc("1.2.3"), doc("1.2.3"))
	if err != nil || lift == nil {
		t.Fatalf("lift = %+v, %v", lift, err)
	}
	if string(lift.Content) != string(doc("1.2.4")) {
		t.Fatalf("content:\n%s", lift.Content)
	}
	if lift.note() != "lifted the version to 1.2.4: trunk took 1.2.3" {
		t.Fatalf("note = %q", lift.note())
	}
	if lift, err := (OpenAPI{}).Lift("spec.json", doc("1.2.2"), doc("1.2.3"), doc("1.3.0"), doc("1.3.0")); err != nil || lift != nil {
		t.Fatalf("a branch above trunk: lift = %+v, %v", lift, err)
	}
	_, err = (OpenAPI{}).Lift("spec.json", doc("1.2.2"), doc("1.2.3-SNAPSHOT"), doc("1.2.3-SNAPSHOT"), doc("1.2.3-SNAPSHOT"))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "info.version is not X.Y.Z on both sides") {
		t.Fatalf("err = %v", err)
	}
}

func TestInfoVersionFindsTheLiteral(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want string
		ok   bool
	}{
		{`{"info":{"version":"1.2.3"}}`, "1.2.3", true},
		{`{"a":{"version":"0.0.1"},"info":{"title":"t","version":"1.2.3"},"version":"7.7.7"}`, "1.2.3", true},
		{`{"info":{"title":"t"}}`, "", false},
		{`{"info":"1.2.3"}`, "", false},
		{`{"info":["version","1.2.3"]}`, "", false},
		{`{"info":{"version":7}}`, "", false},
		{`{"info":{"nested":{"version":"9.9.9"},"version":"1.2.3"}}`, "1.2.3", true},
		// The literal is not how Go writes the value, so there is no one
		// span to rewrite.
		{`{"info":{"version":"1\u002e2.3"}}`, "", false},
		{`not json`, "", false},
	} {
		v, at, ok := infoVersion([]byte(tc.doc))
		if ok != tc.ok || v != tc.want {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.doc, v, ok, tc.want, tc.ok)
		}
		if ok && tc.doc[at:at+len(v)+2] != `"`+v+`"` {
			t.Errorf("%s: at %d is %q, not the literal", tc.doc, at, tc.doc[at:])
		}
	}
}

func TestCountPicksCountsCommitsNotLines(t *testing.T) {
	done := "pick aaa one\nbreak\nedit bbb two\n# a comment\n\nexec true\n"
	if n, last := countPicks(done); n != 2 || last != "exec" {
		t.Errorf("done: %d picks, last %q", n, last)
	}
	if n, last := countPicks(""); n != 0 || last != "" {
		t.Errorf("empty: %d picks, last %q", n, last)
	}
	if n, last := countPicks("p aaa\nr bbb\ns ccc\nf ddd\nm eee\nnoop\n"); n != 5 || last != "noop" {
		t.Errorf("abbreviated: %d picks, last %q", n, last)
	}
}

// The editors are run as git runs them, over a list git would write: ids
// abbreviated, matched as prefixes of the full ones; a marked pick gets a
// break and becomes an edit, a dropped pick goes, everything else is left,
// and the plain editor undoes exactly the marks.
func TestSequenceEditorMarksAndDropsTheGivenPicks(t *testing.T) {
	const full1, full2, full3 = "aaa1111111111111111111111111111111111111", "bbb2222222222222222222222222222222222222", "ccc3333333333333333333333333333333333333"
	list := "pick aaa1111 one\npick bbb2222 two\npick ccc3333 three\n# a comment\n\nexec true\n"
	var b strings.Builder
	sequenceEditor(&b, []string{full1, full3}, []string{full2})
	got := runEditor(t, b.String(), list)
	want := "break\nedit aaa1111 one\nbreak\nedit ccc3333 three\n# a comment\n\nexec true\n"
	if got != want {
		t.Errorf("marked list:\n%s\nwant:\n%s", got, want)
	}
	b.Reset()
	plainEditor(&b)
	if got := runEditor(t, b.String(), got); got != "pick aaa1111 one\npick ccc3333 three\n# a comment\n\nexec true\n" {
		t.Errorf("plain list:\n%s", got)
	}
	// A pick with no subject after its id, and an id that is a prefix of
	// another's full id but of a commit not in the list.
	b.Reset()
	sequenceEditor(&b, []string{full1}, nil)
	if got := runEditor(t, b.String(), "pick aaa1111\npick aaa2222 other\n"); got != "break\nedit aaa1111\npick aaa2222 other\n" {
		t.Errorf("bare and near ids:\n%s", got)
	}
}

func runEditor(t *testing.T, script, list string) string {
	t.Helper()
	dir := t.TempDir()
	todo := filepath.Join(dir, "git-rebase-todo")
	if err := os.WriteFile(todo, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := writeScript(script)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if out, err := exec.Command("sh", path, todo).CombinedOutput(); err != nil {
		t.Fatalf("editor: %v\n%s", err, out)
	}
	got, err := os.ReadFile(todo)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestLiftNoteNamesEveryLine(t *testing.T) {
	if got := (Lift{From: []string{"1.2.3"}, To: []string{"1.2.4"}}).note(); got != "lifted the version to 1.2.4: trunk took 1.2.3" {
		t.Errorf("one: %q", got)
	}
	if got := (Lift{From: []string{"1.2.3", "1.5.2"}, To: []string{"1.2.4", "1.5.3"}}).note(); got != "lifted the versions to 1.2.4 and 1.5.3: trunk took 1.2.3 and 1.5.2" {
		t.Errorf("two: %q", got)
	}
	f := FileOutcome{Path: "app.yaml", Strategy: "owned-line", Resolved: true, Lifted: true, Note: "lifted the version to 1.2.4: trunk took 1.2.3"}
	if f.By() != "owned-line lifted the version to 1.2.4: trunk took 1.2.3" {
		t.Errorf("By = %q", f.By())
	}
	if (FileOutcome{Strategy: "openapi", Resolved: true}).By() != "openapi" {
		t.Error("By of a plain resolution is the strategy")
	}
}
