// Package shell carries wt's shell layer inside the binary, so `wt shell`
// can print it however wt was installed.
package shell

import _ "embed"

// Layer is wt.sh, the functions that must run in the calling shell.
//
//go:embed wt.sh
var Layer string
