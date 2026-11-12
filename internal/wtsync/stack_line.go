package wtsync

import "errors"

// LineLayer is one layer of a stack held on one line: a branch whose tip is
// a commit of the line.
type LineLayer struct {
	Branch  string
	Tip     string
	Count   int // commits from the line's base to the tip
	Subject string
	Named   bool // named with --layer rather than found
}

// SkippedBranch is a branch on the line that is not taken as a layer.
type SkippedBranch struct{ Branch, Tip, Why string }

// Line is a stack held on one first-parent line.
type Line struct {
	Branch, Head, Base string
	Commits            []string
	Layers             []LineLayer
	Skipped            []SkippedBranch
}

// ReadLine reads the line of branch.
func ReadLine(dir, branch, trunk string, named, backups []string) (Line, error) {
	return Line{}, errors.New("not yet")
}
