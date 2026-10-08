package main

import (
	"slices"
	"strings"
	"testing"
)

// BRIDGE(sync-run-spelling): the old spelling of wt sync rebase becomes the
// new one, and nothing else on any line is touched.
func TestBridgeSyncRunRespellsTheOldVerb(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
		old        bool
	}{
		{[]string{"sync", "run", "a", "b", "--yes"}, []string{"sync", "rebase", "a", "b", "--yes"}, true},
		{[]string{"sync", "run"}, []string{"sync", "rebase"}, true},
		{[]string{"sync", "--run", "--yes"}, []string{"sync", "--rebase", "--yes"}, true},
		{[]string{"sync", "a", "--run", "--if-ready"}, []string{"sync", "a", "--rebase", "--if-ready"}, true},
		{[]string{"sync", "a", "--run", "--undo"}, []string{"sync", "a", "--rebase", "--undo"}, true},
		{[]string{"sync", "rebase", "a"}, []string{"sync", "rebase", "a"}, false},
		{[]string{"sync", "a", "--rebase", "--run"}, []string{"sync", "a", "--rebase", "--run"}, false},
		{[]string{"sync", "keep", "run"}, []string{"sync", "keep", "run"}, false},
		{[]string{"sync"}, []string{"sync"}, false},
		{[]string{"refs", "sweep", "--run-id", "x"}, []string{"refs", "sweep", "--run-id", "x"}, false},
		{[]string{"exec", "a", "--run"}, []string{"exec", "a", "--run"}, false},
		{nil, nil, false},
	} {
		in := slices.Clone(tc.args)
		got, old := bridgeSyncRun(tc.args)
		if !slices.Equal(got, tc.want) || old != tc.old {
			t.Errorf("%v: %v, %v; want %v, %v", in, got, old, tc.want, tc.old)
		}
		if !slices.Equal(tc.args, in) {
			t.Errorf("%v: the line given was changed to %v", in, tc.args)
		}
	}
}

// BRIDGE(sync-run-spelling): past the bridge the old spelling is not a
// command, a flag or a completion, and no help names it.
func TestTheOldSyncRunSpellingIsNowhereInTheCommands(t *testing.T) {
	root := newRootCmd()
	sync, _, err := root.Find([]string{"sync"})
	if err != nil {
		t.Fatal(err)
	}
	if sync.Flags().Lookup("run") != nil {
		t.Error("wt sync declares --run")
	}
	for _, c := range sync.Commands() {
		if c.Name() == "run" || c.HasAlias("run") {
			t.Errorf("wt sync has a subcommand run: %s", c.CommandPath())
		}
	}
	for _, c := range reachable(root) {
		// wt sync keep once keeps its own alias, which is another run.
		if strings.HasPrefix(c.CommandPath(), "wt sync keep") {
			continue
		}
		for _, text := range []string{c.Short, c.Long, c.Example, c.Flags().FlagUsages()} {
			for _, old := range []string{"sync run", "--run "} {
				if strings.Contains(text, old) {
					t.Errorf("%s: help says %q", c.CommandPath(), old)
				}
			}
		}
	}
	t.Chdir(t.TempDir())
	if _, err := runCmd(t, "sync", "a", "--rebase", "--run"); err == nil || err.Error() != "unknown flag: --run" {
		t.Errorf("--rebase with --run: %v, want an unknown flag", err)
	}
	words, _ := complete(t, "sync", "r")
	for _, w := range words {
		if strings.HasPrefix(w, "run") {
			t.Errorf("completion offers %q", w)
		}
	}
}
