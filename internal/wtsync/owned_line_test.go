package wtsync

import (
	"regexp"
	"strings"
	"testing"
)

// yamlDoc is the shape of server's openapi: block. (Not "yaml": that is the
// package's import name.)
func yamlDoc(main string, extra string) string {
	return "server:\n  port: 8080\nopenapi:\n  main:\n    name: Backend API\n    # update when the spec changes\n    version: " + main + "\n    public-only: false\n  remote:\n    name: Remote API\n    version: 1.5.1\n" + extra
}

func ownedVersion(t *testing.T) OwnedLine {
	t.Helper()
	rule, err := RuleNamed("max-plus-patch")
	if err != nil {
		t.Fatal(err)
	}
	return OwnedLine{Line: regexp.MustCompile(`^\s*version:`), Rule: rule}
}

func TestOwnedLineLiftsAVersionOnlyConflictAboveTrunk(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.38.3", "")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\n    version: 2.38.6\n") || strings.Contains(string(out), "<<<<<<<") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(string(out), "    version: 1.5.1\n") {
		t.Error("the remote version, untouched by both sides, must survive")
	}
}

func TestOwnedLineKeepsABranchVersionAlreadyAboveTrunk(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.39.0", "")))
	if err != nil || !strings.Contains(string(out), "version: 2.39.0\n") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestOwnedLineResolvesTwoOwnedLinesInSeparateBlocks(t *testing.T) {
	two := func(m, r string) string {
		return "openapi:\n  main:\n    version: " + m + "\n  remote:\n    version: " + r + "\n"
	}
	out, err := ownedVersion(t).Resolve(conflict(two("2.35.0", "1.4.6"), two("2.38.2", "1.5.1"), two("2.36.0", "1.5.0")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != two("2.38.3", "1.5.2") {
		t.Errorf("out = %q", out)
	}
}

func TestOwnedLineKeepsTheRestAsGitMergedIt(t *testing.T) {
	out, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", "  extra: trunk\n"), yamlDoc("2.38.3", "")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "  extra: trunk\n") || !strings.Contains(string(out), "version: 2.38.6\n") {
		t.Errorf("out = %q", out)
	}
}

func TestOwnedLineResolvesABlockGitFoldedAroundTheLine(t *testing.T) {
	withc := func(c, v string) string { return "openapi:\n  main:\n    # " + c + "\n    version: " + v + "\n" }
	// trunk rewrote the comment above the version; the branch bumped the version
	out, err := ownedVersion(t).Resolve(conflict(withc("old", "2.38.0"), withc("new", "2.38.5"), withc("old", "2.38.3")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != withc("new", "2.38.6") {
		t.Errorf("out = %q", out)
	}
	// the branch added a line next to it and trunk only bumped: the branch's line survives
	out, err = ownedVersion(t).Resolve(conflict(
		"k: a\nversion: 1.0.0\n", "k: a\nversion: 1.0.5\n", "k: a\nversion: 1.0.2\nadded: by-branch\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "k: a\nversion: 1.0.6\nadded: by-branch\n" {
		t.Errorf("out = %q", out)
	}
}

func TestCollapseOwnedDistinguishesNoOwnedLineFromBothSidesChangingOtherLines(t *testing.T) {
	line := regexp.MustCompile(`^version:`)
	rule, _ := RuleNamed("max-plus-patch")
	// the branch's side of the block holds no owned line at all
	_, err := collapseOwned(Block{
		Branch: []string{"unrelated"},
		Trunk:  []string{"version: 1.0.5"},
		Base:   []string{"version: 1.0.0"},
	}, line, rule, "f.txt")
	if !IsRefusal(err) || !strings.Contains(err.Error(), "no owned line on one side, or more than one") {
		t.Errorf("err = %v", err)
	}
	// both sides changed a different, non-owned line: a real disagreement,
	// distinct wording from the no-owned-line case above
	_, err = collapseOwned(Block{
		Branch: []string{"version: 1.0.2", "branch-note"},
		Trunk:  []string{"version: 1.0.5", "trunk-note"},
		Base:   []string{"version: 1.0.0", "base-note"},
	}, line, rule, "f.txt")
	if !IsRefusal(err) || !strings.Contains(err.Error(), "the two sides differ by more than the owned line") {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineRefusesWhenBothSidesChangedOtherLines(t *testing.T) {
	withc := func(c, v string) string { return "openapi:\n  main:\n    # " + c + "\n    version: " + v + "\n" }
	_, err := ownedVersion(t).Resolve(conflict(withc("old", "2.38.0"), withc("trunk", "2.38.5"), withc("branch", "2.38.3")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "more than") {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineRefusesAConflictElsewhere(t *testing.T) {
	_, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", "  extra: trunk\n"), yamlDoc("2.38.3", "  extra: branch\n")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineRefusesAVersionThatIsNotSemver(t *testing.T) {
	_, err := ownedVersion(t).Resolve(conflict(yamlDoc("2.38.0", ""), yamlDoc("2.38.5", ""), yamlDoc("2.38.3-SNAPSHOT", "")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestOwnedLineKeepsTheBranchPinAndItsFormatting(t *testing.T) {
	pkg := func(v, react string) string {
		return "{\n  \"name\": \"app\",\n  \"dependencies\": {\n    \"@telcred/spec\": \"" + v + "\",\n    \"react\": \"" + react + "\"\n  }\n}\n"
	}
	rule, _ := RuleNamed("keep-branch")
	s := OwnedLine{Line: regexp.MustCompile(`^\s*"@telcred/spec":`), Rule: rule}
	// trunk also bumped react on the adjacent line: git folds it into the block
	out, err := s.Resolve(conflict(pkg("2.30.2", "19.0.0"), pkg("2.37.1", "19.1.0"), pkg("2.36.0-snapshot.20260831123245", "19.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != pkg("2.36.0-snapshot.20260831123245", "19.1.0") {
		t.Errorf("out = %q", out)
	}
	// both sides changed react: refuse
	_, err = s.Resolve(conflict(pkg("2.30.2", "19.0.0"), pkg("2.37.1", "19.1.0"), pkg("2.36.0", "19.2.0")))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestFromRuleBuildsOwnedLine(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "owned-line", Line: `^\s*version:`, Rule: "keep-trunk"}, "", "")
	if err != nil || s.Name() != "owned-line" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
	if _, err := FromRule(Rule{Strategy: "owned-line", Line: `(`, Rule: "keep-trunk"}, "", ""); err == nil {
		t.Error("a bad regex must be an error")
	}
}
