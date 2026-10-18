// Package compat carries the worktree_functions.sh contract inside the
// binary, so `wt shell --compat` can print it however wt was installed.
package compat

import _ "embed"

// Functions is worktree_functions.sh, the functions Herdr's skills source.
//
//go:embed worktree_functions.sh
var Functions string
