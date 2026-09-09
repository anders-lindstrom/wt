package wtsync

import "testing"

func TestTakeTrunkReturnsTrunksBlobUnchanged(t *testing.T) {
	c := conflict("lockfileVersion: 9.0\nbase\n", "lockfileVersion: 9.0\ntrunk\n", "lockfileVersion: 9.0\nbranch\n")
	out, err := (TakeTrunk{}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(c.Trunk) {
		t.Errorf("out = %q", out)
	}
}

func TestFromRuleBuildsTakeTrunkAndRejectsTheUnknown(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "take-trunk"}, "", "")
	if err != nil || s.Name() != "take-trunk" {
		t.Errorf("FromRule take-trunk = %v, %v", s, err)
	}
	if _, err := FromRule(Rule{Strategy: "magic"}, "", ""); err == nil {
		t.Error("unknown strategy must be an error")
	}
}
