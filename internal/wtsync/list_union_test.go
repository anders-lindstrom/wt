package wtsync

import (
	"regexp"
	"strings"
	"testing"
)

func inc(list string) string {
	return "plugins {\n    id 'x' version '1.0.0'\n}\n\nrootProject.name = 'server'\ninclude " + list + "\n"
}

func includes() ListUnion {
	return ListUnion{Line: regexp.MustCompile(`^include `), Delimiter: ","}
}

func includeLine(t *testing.T, out []byte) string {
	t.Helper()
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "include ") {
			return strings.TrimPrefix(l, "include ")
		}
	}
	t.Fatalf("no include line in %q", out)
	return ""
}

func TestListUnionUnionsTrunkOrderFirst(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'pins', 'b', 'c'"), inc("'a', 'b', 'bankid', 'c'")))
	if err != nil {
		t.Fatal(err)
	}
	if got := includeLine(t, out); got != "'a', 'pins', 'b', 'c', 'bankid'" {
		t.Errorf("include = %q", got)
	}
	if strings.Contains(string(out), "<<<<<<<") {
		t.Error("markers left behind")
	}
}

func TestListUnionKeepsTheRestOfTheFileFromGit(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a'"), strings.Replace(inc("'a', 'pins'"), "1.0.0", "2.0.0", 1), inc("'a', 'nbix'")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "version '2.0.0'") {
		t.Errorf("trunk's plugin bump lost: %q", out)
	}
}

func TestListUnionRefusesARemovalOnEitherSide(t *testing.T) {
	_, err := includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'pins', 'b', 'c'"), inc("'a', 'c'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "removes 'b'") {
		t.Errorf("branch removal: err = %v", err)
	}
	_, err = includes().Resolve(conflict(inc("'a', 'b', 'c'"), inc("'a', 'c'"), inc("'a', 'b', 'c', 'd'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "removes 'b'") {
		t.Errorf("trunk removal: err = %v", err)
	}
}

func TestListUnionUnionsIntoAnEmptyBaseList(t *testing.T) {
	out, err := includes().Resolve(conflict(inc(""), inc("'a'"), inc("'b'")))
	if err != nil {
		t.Fatal(err)
	}
	if got := includeLine(t, out); got != "'a', 'b'" {
		t.Errorf("include = %q", got)
	}
}

func TestListUnionRefusesAConflictOffTheListLine(t *testing.T) {
	_, err := includes().Resolve(conflict("rootProject.name = 'a'\ninclude 'x'\n", "rootProject.name = 'b'\ninclude 'x'\n", "rootProject.name = 'c'\ninclude 'x'\n"))
	if !IsRefusal(err) {
		t.Errorf("err = %v", err)
	}
}

func TestListUnionUnionsASecondListLineFoldedIntoTheBlock(t *testing.T) {
	out, err := includes().Resolve(conflict(inc("'a', 'b'"), inc("'a', 'pins', 'b'"), inc("'a', 'b', 'nbix'")+"include 'nbix-devtools'\n"))
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "include ") {
			for _, it := range strings.Split(strings.TrimPrefix(l, "include "), ",") {
				items = append(items, strings.TrimSpace(it))
			}
		}
	}
	if strings.Join(items, " ") != "'a' 'pins' 'b' 'nbix' 'nbix-devtools'" {
		t.Errorf("items = %v", items)
	}
	if strings.Contains(string(out), "<<<<<<<") {
		t.Error("markers left behind")
	}
}

func TestListUnionRefusesAnItemItCannotParse(t *testing.T) {
	_, err := includes().Resolve(conflict(inc("'a', 'b'"), inc("'a', 'pins', 'b'"), inc("'a', 'b', 'nbix'include 'x'")))
	if !IsRefusal(err) || !strings.Contains(err.Error(), "cannot parse") {
		t.Errorf("err = %v", err)
	}
}

func TestFromRuleBuildsListUnion(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "list-union", Line: "^include ", Delimiter: ","}, "")
	if err != nil || s.Name() != "list-union" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}
