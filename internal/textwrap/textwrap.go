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
	// Split on single spaces, so a run of them (the gap between a table's
	// columns) survives as it was written; a line never starts with one.
	for _, word := range strings.Split(s, " ") {
		n := utf8.RuneCountInString(word)
		switch {
		case word == "" && line == "":
			continue
		case line == "":
			line, col = word, col+n
		case col+1+n <= width:
			line, col = line+" "+word, col+1+n
		default:
			lines = append(lines, line)
			line = indent + word
			col = utf8.RuneCountInString(line)
		}
	}
	return strings.Join(append(lines, line), "\n")
}
