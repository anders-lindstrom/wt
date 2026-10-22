// Package textwrap breaks prose at spaces for a terminal of a given width.
package textwrap

import (
	"strings"
	"unicode/utf8"
)

// Wrap breaks s at spaces so no line is wider than width, continuing under
// indent. The first line starts at column start, and is returned without
// whatever stands to its left. Width 0 or less leaves s on one line. A word
// wider than the width stays whole.
func Wrap(start int, s string, width int, indent string) string {
	if width <= 0 {
		return s
	}
	var lines []string
	line, col := "", start
	gapBroken := false
	brk := func(word string) {
		lines = append(lines, strings.TrimRight(line, " "))
		line = indent + word
		col = utf8.RuneCountInString(line)
	}
	// Split on single spaces, so a run of them (the gap between a table's
	// columns) survives as it was written; a line never starts with one.
	for _, word := range strings.Split(s, " ") {
		n := utf8.RuneCountInString(word)
		switch {
		case word == "" && line == "":
			continue
		case line == "":
			line, col = word, col+n
		case word == "" && gapBroken:
			// The rest of a gap the line broke inside: nothing to keep.
		case word == "" && col+1 > width:
			gapBroken = true
		case gapBroken:
			gapBroken = false
			brk(word)
		case col+1+n <= width:
			line, col = line+" "+word, col+1+n
		default:
			brk(word)
		}
	}
	return strings.Join(append(lines, strings.TrimRight(line, " ")), "\n")
}
