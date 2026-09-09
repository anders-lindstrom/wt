package wtsync

import "testing"

func TestMaxPlusPatch(t *testing.T) {
	cases := []struct{ branch, trunk, want string }{
		{"2.39.0", "2.38.5", "2.39.0"}, // branch already above trunk: keep
		{"2.38.3", "2.38.5", "2.38.6"}, // trunk overtook: one patch above trunk
		{"2.38.5", "2.38.5", "2.38.6"}, // equal: the branch still needs its own
		{"1.5.0", "1.5.1", "1.5.2"},
		{"2.38.10", "2.38.9", "2.38.10"}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := MaxPlusPatch(c.branch, c.trunk); got != c.want {
			t.Errorf("MaxPlusPatch(%s, %s) = %s, want %s", c.branch, c.trunk, got, c.want)
		}
	}
}

func TestRuleNamedAppliesToTheVersionInsideALine(t *testing.T) {
	r, err := RuleNamed("max-plus-patch")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Apply("    version: 2.38.3", "    version: 2.38.5")
	if err != nil || got != "    version: 2.38.6" {
		t.Errorf("got %q, %v", got, err)
	}
	// the branch's formatting is kept, only the version changes
	got, err = r.Apply(`    "pkg": "2.38.3",`, `    "pkg": "2.38.5",`)
	if err != nil || got != `    "pkg": "2.38.6",` {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRuleNamedReplacesAtTheMatchedOffsetNotTheFirstOccurrence(t *testing.T) {
	// "1.2.3.4" contains the literal substring "1.2.3": a plain string
	// replace would corrupt it instead of touching the trailing version.
	r, _ := RuleNamed("max-plus-patch")
	got, err := r.Apply("pin 1.2.3.4 to 1.2.3", "pin 1.2.3.4 to 1.2.5")
	if err != nil || got != "pin 1.2.3.4 to 1.2.6" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestRuleNamedRefusesALineWithoutAWholeVersionToken(t *testing.T) {
	r, _ := RuleNamed("max-plus-patch")
	for _, line := range []string{"    version: SNAPSHOT", "    version: 2.38.3-SNAPSHOT", "    version: 2.38.3.1"} {
		if _, err := r.Apply(line, "    version: 2.38.5"); err == nil {
			t.Errorf("expected an error for %q", line)
		}
	}
}

func TestKeepRules(t *testing.T) {
	kb, _ := RuleNamed("keep-branch")
	kt, _ := RuleNamed("keep-trunk")
	if got, _ := kb.Apply("b", "t"); got != "b" {
		t.Errorf("keep-branch = %q", got)
	}
	if got, _ := kt.Apply("b", "t"); got != "t" {
		t.Errorf("keep-trunk = %q", got)
	}
	if _, err := RuleNamed("newest"); err == nil {
		t.Error("unknown rule must be an error")
	}
}
