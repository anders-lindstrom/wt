package main

import "slices"

// BRIDGE(sync-run-spelling): wt sync rebase was wt sync run, and the gittree
// installed today still calls it that. The old spelling is turned into the
// new one before the line is parsed, and the --json result of a line spelled
// that way names its command "sync run", the one value that gittree decodes.
// Nothing documents or completes it. It goes when gittree calls wt sync
// rebase: delete this file and its test, the call in Execute, and make
// rebaseCommand a constant.

// bridgeSyncRun is args with wt sync run <work>... and wt sync [<work>...]
// --run respelled as rebase, and whether anything was. A line that says
// rebase itself is left alone, so --rebase with --run is refused as an
// unknown flag is.
func bridgeSyncRun(args []string) ([]string, bool) {
	if len(args) < 2 || args[0] != "sync" || args[1] == "rebase" || slices.Contains(args, "--rebase") {
		return args, false
	}
	out := slices.Clone(args)
	if out[1] == "run" {
		out[1] = "rebase"
		return out, true
	}
	old := false
	for i, a := range out {
		if a == "--run" {
			out[i], old = "--rebase", true
		}
	}
	return out, old
}
