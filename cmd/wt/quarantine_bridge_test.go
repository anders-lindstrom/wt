package main

import (
	"slices"
	"testing"
)

// BRIDGE(quarantine-spelling): the old spellings of wt purge and --move-to
// become the new ones, and nothing else on any line is touched.
func TestBridgeQuarantineRespellsTheOldCommandAndFlag(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
		old        bool
	}{
		{[]string{"quarantine", "purge", "../trash/lc", "--yes"}, []string{"purge", "../trash/lc", "--yes"}, true},
		{[]string{"quarantine", "purge", "--help"}, []string{"purge", "--help"}, true},
		{[]string{"quarantine"}, []string{"purge", "--help"}, true},
		{[]string{"quarantine", "--help"}, []string{"purge", "--help"}, true},
		{[]string{"remove", "a", "--quarantine", "../t"}, []string{"remove", "a", "--move-to", "../t"}, true},
		{[]string{"rm", "a", "--quarantine=../t", "--yes"}, []string{"rm", "a", "--move-to=../t", "--yes"}, true},
		{[]string{"sweep", "--quarantine", "../t", "--json"}, []string{"sweep", "--move-to", "../t", "--json"}, true},
		{[]string{"remove", "a", "--move-to", "../t"}, []string{"remove", "a", "--move-to", "../t"}, false},
		{[]string{"remove", "--", "--quarantine"}, []string{"remove", "--", "--quarantine"}, false},
		{[]string{"remove", "a", "--quarantined"}, []string{"remove", "a", "--quarantined"}, false},
		{[]string{"purge", "quarantine"}, []string{"purge", "quarantine"}, false},
		{[]string{"exec", "a", "--quarantine"}, []string{"exec", "a", "--quarantine"}, false},
		{[]string{"refs", "sweep", "--quarantine"}, []string{"refs", "sweep", "--quarantine"}, false},
		{nil, nil, false},
	} {
		in := slices.Clone(tc.args)
		got, old := bridgeQuarantine(tc.args)
		if !slices.Equal(got, tc.want) || old != tc.old {
			t.Errorf("%v: %v, %v; want %v, %v", in, got, old, tc.want, tc.old)
		}
		if !slices.Equal(tc.args, in) {
			t.Errorf("%v: the line given was changed to %v", in, tc.args)
		}
	}
}

// BRIDGE(quarantine-spelling): past the bridge the old spellings are not a
// command, a flag or a completion.
func TestTheOldQuarantineSpellingIsNowhereInTheCommands(t *testing.T) {
	root := newRootCmd()
	for _, c := range reachable(root) {
		if c.Name() == "quarantine" || c.HasAlias("quarantine") {
			t.Errorf("%s is a command", c.CommandPath())
		}
		if c.Flags().Lookup("quarantine") != nil {
			t.Errorf("%s declares --quarantine", c.CommandPath())
		}
	}
	t.Chdir(t.TempDir())
	if _, err := runCmd(t, "remove", "a", "--quarantine", "../t"); err == nil || err.Error() != "unknown flag: --quarantine" {
		t.Errorf("--quarantine past the bridge: %v, want an unknown flag", err)
	}
	for _, line := range [][]string{{"q"}, {"remove", "--q"}, {"sweep", "--q"}} {
		words, _ := complete(t, line...)
		for _, w := range words {
			t.Errorf("completing %v offers %q", line, w)
		}
	}
}
