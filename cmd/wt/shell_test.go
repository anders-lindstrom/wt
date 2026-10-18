package main

import (
	"os"
	"testing"
)

func TestShellPrintsTheLayersVerbatim(t *testing.T) {
	for args, file := range map[string]string{
		"":         "../../shell/wt.sh",
		"--compat": "../../compat/worktree_functions.sh",
	} {
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		argv := []string{"shell"}
		if args != "" {
			argv = append(argv, args)
		}
		out, err := runCmd(t, argv...)
		if err != nil {
			t.Fatalf("wt %v: %v", argv, err)
		}
		if out != string(want) {
			t.Errorf("wt %v does not print %s verbatim", argv, file)
		}
	}
}
