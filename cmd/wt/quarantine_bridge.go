package main

import (
	"slices"
	"strings"
)

// BRIDGE(quarantine-spelling): wt purge was wt quarantine purge, and
// --move-to on wt remove and wt sweep was --quarantine; the gittree installed
// today still calls them that. The old spellings are turned into the new
// ones before the line is parsed. Nothing documents or completes them. It
// goes when gittree calls wt purge and --move-to: delete this file and its
// test, the call in Execute, and the bridge's tests in test/purge.bats.

// bridgeQuarantine is args with wt quarantine purge <dir> respelled wt purge
// <dir>, and --quarantine on wt remove and wt sweep respelled --move-to, and
// whether anything was. wt quarantine with no purge after it printed help,
// and is given wt purge's.
func bridgeQuarantine(args []string) ([]string, bool) {
	if len(args) == 0 {
		return args, false
	}
	switch args[0] {
	case "quarantine":
		if len(args) > 1 && args[1] == "purge" {
			return slices.Clone(args[1:]), true
		}
		return []string{"purge", "--help"}, true
	case "remove", "rm", "sweep":
		out, old := slices.Clone(args), false
		for i, a := range out {
			if a == "--" {
				break
			}
			if rest, ok := strings.CutPrefix(a, "--quarantine"); ok && (rest == "" || rest[0] == '=') {
				out[i], old = "--move-to"+rest, true
			}
		}
		return out, old
	}
	return args, false
}
